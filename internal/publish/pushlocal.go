// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Errors the local push returns.
var (
	// ErrNothingCommitted reports a workspace whose repository holds no
	// commit. It is 044d's outcome reached earlier: the run produced
	// nothing, and that is recorded rather than reported as success.
	ErrNothingCommitted = errors.New("the run committed nothing")
	// ErrNoWorkspace reports that there is no repository to push: the
	// directory is gone or was never initialised. It is a failure of the
	// push and not a finding about the run, which is why it is distinct from
	// ErrNothingCommitted.
	ErrNoWorkspace = errors.New("the workspace is not a git repository")
	// ErrRefusedContent reports a committed tree carrying third-party
	// content the redistribution gate will not publish.
	ErrRefusedContent = errors.New("the committed tree carries content that may not be redistributed")
)

// Pushed is what one push put on the remote.
type Pushed struct {
	// Head is the commit the remote now holds.
	Head string
	// Commits is how many commits the branch carries.
	Commits int
	// Branch is what was pushed.
	Branch string
}

// LocalPush is one push of a workspace the agent committed into but never had
// a remote for (045g).
//
// The token reaches git through an askpass helper. It is never passed in
// argv, because process arguments are visible to every user on the machine,
// and never written to the repository's own configuration, where it would
// survive in .git/config and in the reflog after the run. The remote is added,
// pushed to, and removed, so a session directory kept under the retention
// policy carries neither a credential nor a push target.
type LocalPush struct {
	// Workspace is the git repository the agent committed into.
	Workspace string
	// RemoteURL is the push target.
	RemoteURL string
	// Token is the forge credential. It is held for the length of one push
	// and never enters a sandbox.
	Token string
	// Username is the credential's user part, which is not a secret.
	Username string
	// Branch defaults to main.
	Branch string
}

// Verify checks what the agent committed, before anything leaves the machine.
//
// The gate is VerifyTree rather than Classify. Classify is an allowlist over a
// harvested output directory, consisting of the fixed report files plus src/,
// and an agent-authored repository is a real software project: Classify would
// strip a LICENSE, a requirements.txt, tests and configs. VerifyTree is the
// backstop written for this case. It flags the paper, a dataset, model
// weights, an archive or a leaked credential, and presumes everything else is
// the reproduction's own work.
//
// It reads the tree at HEAD, because that is what the push sends. The index is
// not sent: a file staged and never committed does not travel, and a file
// committed and then unstaged does.
func (push LocalPush) Verify(ctx context.Context) ([]string, error) {
	files, err := push.git(ctx, "ls-tree", "-r", "--name-only", "HEAD")
	if err != nil {
		return nil, err
	}
	var paths []string
	for line := range strings.SplitSeq(files, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			paths = append(paths, trimmed)
		}
	}
	return VerifyTree(paths), nil
}

// Run verifies the committed tree and pushes it. It refuses rather than
// pushing where the tree carries third-party content, so the operator can fix
// it and retry instead of owning a repository that may never be made public.
func (push LocalPush) Run(ctx context.Context) (Pushed, error) {
	branch := push.Branch
	if branch == "" {
		branch = "main"
	}
	head, err := push.git(ctx, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		// HEAD cannot be read. Either the repository has no commit yet, in
		// which case the run produced nothing and that is recorded as its
		// result, or there is nothing to read it from: git is missing, the
		// directory is gone, or it was never a repository. Only the first
		// case is ErrNothingCommitted. Reporting the rest as "committed
		// nothing" would record a false outcome over a run that may have
		// committed everything.
		if errors.Is(err, exec.ErrNotFound) {
			return Pushed{}, err
		}
		if _, repoErr := push.git(ctx, "rev-parse", "--is-inside-work-tree"); repoErr != nil {
			return Pushed{}, fmt.Errorf("%w: %s: %w", ErrNoWorkspace, push.Workspace, repoErr)
		}
		return Pushed{}, fmt.Errorf("%w: %s", ErrNothingCommitted, push.Workspace)
	}
	flagged, err := push.Verify(ctx)
	if err != nil {
		return Pushed{}, err
	}
	if len(flagged) > 0 {
		return Pushed{}, fmt.Errorf("%w: %s", ErrRefusedContent, strings.Join(flagged, ", "))
	}
	count, err := push.git(ctx, "rev-list", "--count", "HEAD")
	if err != nil {
		return Pushed{}, err
	}

	askpass, cleanup, err := writeAskpass(push.Token)
	if err != nil {
		return Pushed{}, err
	}
	defer cleanup()

	// The remote is transient. Adding it permanently would leave the push
	// target, and with a credential helper the credential, in a directory
	// the retention policy keeps.
	_, _ = push.git(ctx, "remote", "remove", "replichai-origin")
	if _, err := push.git(ctx, "remote", "add", "replichai-origin", push.remoteWithUser()); err != nil {
		return Pushed{}, err
	}
	defer func() { _, _ = push.git(ctx, "remote", "remove", "replichai-origin") }()

	if _, err := push.gitWithAskpass(ctx, askpass, "push", "replichai-origin", "HEAD:refs/heads/"+branch); err != nil {
		return Pushed{}, fmt.Errorf("push %s: %w", push.RemoteURL, err)
	}
	commits := 0
	_, _ = fmt.Sscanf(strings.TrimSpace(count), "%d", &commits)
	return Pushed{Head: strings.TrimSpace(head), Commits: commits, Branch: branch}, nil
}

// remoteWithUser puts the credential's user part in the URL and never the
// credential itself: a token in a URL lands in .git/config and in the reflog.
func (push LocalPush) remoteWithUser() string {
	user := push.Username
	if user == "" {
		user = "x-access-token"
	}
	if push.Token == "" || !strings.HasPrefix(push.RemoteURL, "https://") {
		return push.RemoteURL
	}
	return "https://" + user + "@" + strings.TrimPrefix(push.RemoteURL, "https://")
}

func (push LocalPush) git(ctx context.Context, args ...string) (string, error) {
	return push.gitWithAskpass(ctx, "", args...)
}

func (push LocalPush) gitWithAskpass(ctx context.Context, askpass string, args ...string) (string, error) {
	//nolint:gosec // a fixed git verb over this package's own arguments
	command := exec.CommandContext(ctx, "git", append([]string{"-C", push.Workspace}, args...)...)
	command.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null")
	if askpass != "" {
		command.Env = append(command.Env, "GIT_ASKPASS="+askpass, "SSH_ASKPASS="+askpass)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

// writeAskpass writes the helper git asks for a password. It is mode 0700,
// lives outside the workspace so it cannot be committed, and is removed as
// soon as the push is over.
func writeAskpass(token string) (string, func(), error) {
	if token == "" {
		return "", func() {}, nil
	}
	file, err := os.CreateTemp("", "replichai-askpass-*.sh")
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	script := "#!/bin/sh\ncase \"$1\" in\n*[Uu]sername*) printf %s \"${GIT_USERNAME:-x-access-token}\" ;;\n*) printf %s " +
		shellQuote(token) + " ;;\nesac\n"
	if _, err := file.WriteString(script); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	// 0o700, not 0o600: git execs this helper, so it needs the execute bit.
	// It is owner-only and removed as soon as the push is over.
	//nolint:gosec // an askpass helper must be executable
	if err := os.Chmod(path, 0o700); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// WorkspaceOutputs reads the files a finalize step commits beside the agent's
// own history: the environment record and the judge's verdict, both authored
// by the system rather than by the subject of the audit.
func WorkspaceOutputs(sessionDir string) (map[string][]byte, error) {
	outputs := make(map[string][]byte)
	for _, name := range []string{"environment.json"} {
		//nolint:gosec // the path is this program's own session directory
		body, err := os.ReadFile(filepath.Join(sessionDir, "session", name))
		if err != nil {
			continue
		}
		outputs[name] = body
	}
	entries, err := os.ReadDir(filepath.Join(sessionDir, "grade"))
	if err != nil {
		// A session that was never graded has no verdict to commit, which is
		// a fact about the run rather than a failure to read it.
		//nolint:nilerr // an ungraded session contributes no verdict file
		return outputs, nil
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "verdict-") {
			continue
		}
		//nolint:gosec // the path is this program's own session directory
		body, err := os.ReadFile(filepath.Join(sessionDir, "grade", entry.Name()))
		if err != nil {
			continue
		}
		outputs["grade/"+entry.Name()] = body
	}
	return outputs, nil
}
