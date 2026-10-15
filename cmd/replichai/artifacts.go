// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/batch"
	"github.com/latere-ai/replichai/internal/paper"
)

// artifactTimeout bounds taking one snapshot, a repository's clone included.
const artifactTimeout = 10 * time.Minute

// fetchStudyArtifact snapshots one artifact a project lists. It is a variable
// so the tests of this command run with no network.
var fetchStudyArtifact batch.FetchArtifact = snapshotter(httpFetcher{timeout: paperFetchTimeout, maxBytes: paperMaxBytes})

// codeHosts are the hosts whose addresses name a repository by owner and name
// in the first two path segments, so an address into a repository's tree or a
// file in it still names the repository.
var codeHosts = map[string]bool{"github.com": true, "bitbucket.org": true, "codeberg.org": true}

// snapshotter takes a snapshot of one artifact: a repository as a git bundle,
// anything else as the file its address serves, fetched through fetcher.
func snapshotter(fetcher paper.Fetcher) batch.FetchArtifact {
	return func(ctx context.Context, source string) (paper.Artifact, error) {
		if repository, ok := gitRepository(source); ok {
			return snapshotRepository(ctx, source, repository)
		}
		return snapshotFile(ctx, fetcher, source)
	}
}

// gitRepository reports whether source names a git repository, and the address
// to clone it from. A code host's address is cut to its repository; GitLab's
// at the "/-/" that begins a path inside one, since a GitLab project may sit
// in nested groups. An address ending in .git, or a local file:// one, is
// cloned as written.
func gitRepository(source string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(source))
	if err != nil || parsed.Scheme == "" {
		return "", false
	}
	switch {
	case parsed.Scheme == "file":
		return parsed.String(), true
	case codeHosts[strings.ToLower(parsed.Hostname())]:
		segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(segments) < 2 {
			return "", false
		}
		return fmt.Sprintf("%s://%s/%s/%s", parsed.Scheme, parsed.Host, segments[0], strings.TrimSuffix(segments[1], ".git")), true
	case strings.EqualFold(parsed.Hostname(), "gitlab.com"):
		project, _, _ := strings.Cut(strings.Trim(parsed.Path, "/"), "/-/")
		if !strings.Contains(project, "/") {
			return "", false
		}
		return fmt.Sprintf("%s://%s/%s", parsed.Scheme, parsed.Host, strings.TrimSuffix(project, ".git")), true
	case strings.HasSuffix(parsed.Path, ".git"):
		return parsed.String(), true
	}
	return "", false
}

// snapshotRepository clones a repository bare and bundles every branch and tag,
// so a run can clone the snapshot with no network and land on the commit the
// default branch pointed at. A bare clone takes the branches and tags and not
// a code host's pull-request refs, which a mirror would carry into the bundle.
func snapshotRepository(ctx context.Context, source, repository string) (paper.Artifact, error) {
	ctx, cancel := context.WithTimeout(ctx, artifactTimeout)
	defer cancel()
	dir, err := os.MkdirTemp("", "replichai-artifact-")
	if err != nil {
		return paper.Artifact{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	clone, bundle := filepath.Join(dir, "repository.git"), filepath.Join(dir, "snapshot.bundle")
	if _, err := runGit(ctx, "clone", "--bare", "--quiet", "--", repository, clone); err != nil {
		return paper.Artifact{}, err
	}
	commit, err := runGit(ctx, "-C", clone, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return paper.Artifact{}, fmt.Errorf("the repository has no commit to snapshot: %w", err)
	}
	if _, err := runGit(ctx, "-C", clone, "bundle", "create", bundle, "--all"); err != nil {
		return paper.Artifact{}, err
	}
	//nolint:gosec // a file this function created in its own temporary directory
	body, err := os.ReadFile(bundle)
	if err != nil {
		return paper.Artifact{}, err
	}
	if int64(len(body)) > paperMaxBytes {
		return paper.Artifact{}, fmt.Errorf("the repository is %d MB as a bundle, over the %d MB a run can be handed",
			len(body)>>20, paperMaxBytes>>20)
	}
	return paper.SnapshotOf(source, paper.ArtifactGit, commit, safeName(strings.TrimSuffix(path.Base(repository), ".git"), "repository")+".bundle", body), nil
}

// runGit runs git with no terminal to prompt on, so a private or mistyped
// repository fails instead of waiting for a password nobody will type, and
// returns its trimmed output.
func runGit(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...) //nolint:gosec // fixed git verbs; the repository comes from the operator's own project file, after "--"
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", gitVerb(args), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(output)), nil
}

// gitVerb is the subcommand an argument list runs, past a leading -C and its
// directory, so an error names what failed rather than an option.
func gitVerb(args []string) string {
	for index := 0; index < len(args); index++ {
		if args[index] == "-C" {
			index++
			continue
		}
		return args[index]
	}
	return "git"
}

// snapshotFile downloads the file an address serves. A page is not a file:
// the landing pages of data repositories are the usual mistake, and a page
// handed to every run would misstate what each was given.
func snapshotFile(ctx context.Context, fetcher paper.Fetcher, source string) (paper.Artifact, error) {
	body, err := fetcher.Fetch(ctx, source)
	if err != nil {
		return paper.Artifact{}, err
	}
	if looksLikePage(body) {
		return paper.Artifact{}, fmt.Errorf("%s serves a web page, not a file; name the direct download address", source)
	}
	name := "artifact"
	if parsed, err := url.Parse(source); err == nil {
		name = safeName(path.Base(parsed.Path), name)
	}
	return paper.SnapshotOf(source, paper.ArtifactFile, "", name, body), nil
}

// looksLikePage reports whether a body is an HTML page.
func looksLikePage(body []byte) bool {
	head := strings.ToLower(strings.TrimSpace(string(body[:min(len(body), 512)])))
	return strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html")
}

// unsafeNameRe matches what a snapshot's file name may not hold.
var unsafeNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// safeName is name reduced to characters a file name can carry anywhere, or
// fallback when nothing is left of it.
func safeName(name, fallback string) string {
	name = strings.Trim(unsafeNameRe.ReplaceAllString(name, "-"), "-.")
	if name == "" {
		return fallback
	}
	return name
}
