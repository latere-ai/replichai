// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/batch"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/publish"
)

// TestMain keeps every test of this command off the network. A study fetches
// its papers before any run starts, and here they come from a stand-in that
// hands every paper a copy of its own.
func TestMain(m *testing.M) {
	fetchStudyPaper = func(_ context.Context, reference string) (paper.Copy, error) {
		return paper.CopyOf([]byte("%PDF-1.7 "+reference), "https://papers.example/"+reference), nil
	}
	fetchStudyArtifact = func(_ context.Context, source string) (paper.Artifact, error) {
		return paper.SnapshotOf(source, paper.ArtifactFile, "", "artifact.zip", []byte("snapshot of "+source)), nil
	}
	os.Exit(m.Run())
}

// withPaperSource replaces the stand-in for one test.
func withPaperSource(t *testing.T, source batch.FetchPaper) {
	t.Helper()
	previous := fetchStudyPaper
	fetchStudyPaper = source
	t.Cleanup(func() { fetchStudyPaper = previous })
}

// Every run of a study is handed the one copy of its paper, under the profile
// its project names, and its manifest says which copy and which profile.
func TestAStudysManifestsNameTheProfileAndThePaperCopy(t *testing.T) {
	const ref = "10.1145/3654777.3676417"
	server, writes := fakeForgeRecording(t, nil)
	runner := testProjectRunner(t, &fakeRepositories{forge: server})
	copied := paper.CopyOf([]byte("%PDF-1.7 the paper"), "https://papers.example/paper.pdf")
	snapshot := paper.SnapshotOf("https://github.com/authors/system", paper.ArtifactGit, "c0ffee", "system.bundle", []byte("bundle"))
	runner.inputs = map[string]batch.StudyInputs{ref: {Paper: copied, Artifacts: []paper.Artifact{snapshot}}}
	runner.profiles = map[int]audit.NetworkProfile{0: audit.ProfileArtifacts, 1: audit.ProfileOpen}

	state, err := batch.Run(t.Context(), batch.File{Papers: []batch.Entry{{Paper: ref, Runs: 2}}}, runner,
		batch.Options{Parallel: 1, StatePath: filepath.Join(t.TempDir(), "batch.json"), Publishing: true})
	if err != nil {
		t.Fatalf("Run: %v: %+v", err, state.Entries)
	}
	for index, want := range []audit.NetworkProfile{audit.ProfileArtifacts, audit.ProfileOpen} {
		var manifest publish.Manifest
		if err := json.Unmarshal(writes.file(state.Entries[index].Repo, publish.ManifestFile), &manifest); err != nil {
			t.Fatalf("run %d: no manifest: %v", index+1, err)
		}
		if manifest.Run == nil || manifest.Run.PaperDigest != copied.Digest() || manifest.Run.NetworkProfile != string(want) {
			t.Errorf("run %d's record = %+v, want the copy %s under %s", index+1, manifest.Run, copied.Digest(), want)
			continue
		}
		artifacts := manifest.Run.Artifacts
		if len(artifacts) != 1 || artifacts[0].Source != snapshot.Source || artifacts[0].Commit != "c0ffee" || artifacts[0].Digest != snapshot.Digest() {
			t.Errorf("run %d's artifacts = %+v, want the snapshot %s", index+1, artifacts, snapshot.Digest())
		}
	}
}

// `replichai run` fetches each paper once, before any run, and keeps the copy
// beside the project file.
func TestRunFetchesEachPaperOnceBeforeAnyRun(t *testing.T) {
	fetched := map[string]int{}
	withPaperSource(t, func(_ context.Context, reference string) (paper.Copy, error) {
		fetched[reference]++
		return paper.CopyOf([]byte("%PDF-1.7 "+reference), "https://papers.example/"+reference), nil
	})
	dir := t.TempDir()
	project := filepath.Join(dir, "papers.yaml")
	body := "network: open\npapers:\n  - paper: 10.1145/3654777.3676417\n    runs: 3\n    artifacts:\n      - https://osf.io/abcd/download\n"
	if err := os.WriteFile(project, []byte(body), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	if _, stderr, err := capture(t, "run", project, "--runtime", "memory", "--method", writeMethod(t),
		"--out", filepath.Join(dir, "runs"), "--interval", "0"); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr)
	}
	if fetched["10.1145/3654777.3676417"] != 1 {
		t.Fatalf("fetches = %v, want the paper fetched once for its three runs", fetched)
	}
	raw, err := os.ReadFile(filepath.Join(dir, batch.PapersFile))
	if err != nil || !strings.Contains(string(raw), "https://osf.io/abcd/download") {
		t.Fatalf("the study kept no record of its copies and snapshots: %s, %v", raw, err)
	}
}

// A study with a paper the system cannot fetch does not start, and says how to
// supply one; nothing runs and nothing is spent.
func TestAStudyThatCannotFetchAPaperDoesNotStart(t *testing.T) {
	withPaperSource(t, func(_ context.Context, reference string) (paper.Copy, error) {
		return paper.Copy{}, errors.New("403 Forbidden")
	})
	dir := t.TempDir()
	project := filepath.Join(dir, "papers.txt")
	if err := os.WriteFile(project, []byte("10.1145/2702123.2702520\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	_, _, err := capture(t, "run", project, "--runtime", "memory", "--method", writeMethod(t), "--interval", "0")
	if !errors.Is(err, batch.ErrMissingInputs) || !strings.Contains(err.Error(), "file: path/to/paper.pdf") {
		t.Fatalf("run = %v, want the paper named and how to supply it", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "batch.json")); !os.IsNotExist(err) {
		t.Fatalf("a study that could not start left a state file: %v", err)
	}
}

// A profile nobody defined fails the study before anything runs, whether the
// project or the flag names it.
func TestAnUnknownNetworkProfileFailsTheStudy(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "papers.yaml")
	if err := os.WriteFile(project, []byte("network: closed\npapers:\n  - 10.1145/3654777.3676417\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	if _, _, err := capture(t, "run", project, "--runtime", "memory", "--method", writeMethod(t)); err == nil ||
		!strings.Contains(err.Error(), `"closed"`) {
		t.Fatalf("run = %v, want the unknown profile named", err)
	}
	listed := filepath.Join(dir, "papers.txt")
	if err := os.WriteFile(listed, []byte("10.1145/3654777.3676417\n"), 0o600); err != nil {
		t.Fatalf("write project: %v", err)
	}
	if _, _, err := capture(t, "run", listed, "--runtime", "memory", "--method", writeMethod(t), "--network", "wide"); err == nil ||
		!strings.Contains(err.Error(), `"wide"`) {
		t.Fatalf("run --network wide = %v", err)
	}
}

// The production fetcher reads a paper's full text over HTTP, up to the
// paper's size cap rather than the metadata one.
func TestTheStudyFetcherReadsAPaperOverHTTP(t *testing.T) {
	large := append([]byte("%PDF-1.7 "), make([]byte, 9<<20)...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(large)
	}))
	t.Cleanup(server.Close)
	fetch := studyFetcher(httpFetcher{timeout: 10 * time.Second, maxBytes: paperMaxBytes, private: true})
	copied, err := fetch(t.Context(), server.URL+"/paper.pdf")
	if err != nil || len(copied.Content) != len(large) || copied.MediaType != "application/pdf" {
		t.Fatalf("copy of %d bytes, %v; want the whole %d-byte PDF", len(copied.Content), err, len(large))
	}
}

// gitRepo makes a repository with one commit at dir, as a code host would
// serve it, and returns that commit.
func gitRepo(t *testing.T, dir string) string {
	t.Helper()
	for _, args := range [][]string{
		{"init", "--quiet", "--initial-branch=main", dir},
		{"-C", dir, "-c", "user.name=Author", "-c", "user.email=author@example.org", "commit", "--quiet", "--allow-empty", "-m", "the released code"},
	} {
		if _, err := runGit(t.Context(), args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	commit, err := runGit(t.Context(), "-C", dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return commit
}

// A repository is snapshotted as a bundle a run can clone with no network,
// landing on the commit the default branch pointed at.
func TestARepositoryIsSnapshottedAsABundle(t *testing.T) {
	origin := filepath.Join(t.TempDir(), "system.git")
	commit := gitRepo(t, origin)
	snapshot, err := snapshotter(httpFetcher{})(t.Context(), "file://"+origin)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Kind != paper.ArtifactGit || snapshot.Commit != commit || snapshot.Name != "system.bundle" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	bundle := filepath.Join(t.TempDir(), snapshot.Name)
	if err := os.WriteFile(bundle, snapshot.Content, 0o600); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "clone")
	if _, err := runGit(t.Context(), "clone", "--quiet", bundle, clone); err != nil {
		t.Fatalf("clone the bundle: %v", err)
	}
	if got, err := runGit(t.Context(), "-C", clone, "rev-parse", "HEAD"); err != nil || got != commit {
		t.Fatalf("the clone is at %s, %v; want %s", got, err, commit)
	}
	if _, err := snapshotter(httpFetcher{})(t.Context(), "file://"+filepath.Join(t.TempDir(), "missing.git")); err == nil {
		t.Error("a repository that does not exist was snapshotted")
	}
	empty := filepath.Join(t.TempDir(), "empty.git")
	if _, err := runGit(t.Context(), "init", "--quiet", "--bare", empty); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotter(httpFetcher{})(t.Context(), "file://"+empty); err == nil || !strings.Contains(err.Error(), "no commit") {
		t.Errorf("an empty repository = %v", err)
	}
}

// A file is snapshotted as the bytes its address serves, and a web page in
// its place is refused with what to name instead.
func TestAFileIsSnapshottedAndAPageIsRefused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/record/42" {
			_, _ = w.Write([]byte("  <!DOCTYPE html><html><body>Download</body></html>"))
			return
		}
		_, _ = w.Write([]byte("PK\x03\x04 the data"))
	}))
	t.Cleanup(server.Close)
	fetch := snapshotter(httpFetcher{timeout: 10 * time.Second, maxBytes: paperMaxBytes, private: true})
	snapshot, err := fetch(t.Context(), server.URL+"/files/data%20set.zip?download=1")
	if err != nil || snapshot.Kind != paper.ArtifactFile || snapshot.Name != "data-set.zip" || !strings.HasPrefix(string(snapshot.Content), "PK") {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
	if _, err := fetch(t.Context(), server.URL+"/record/42"); err == nil || !strings.Contains(err.Error(), "direct download address") {
		t.Fatalf("a page = %v", err)
	}
	if _, err := fetch(t.Context(), "http://127.0.0.1:1/gone.zip"); err == nil {
		t.Fatal("an address that does not answer was snapshotted")
	}
}

func TestWhichAddressesNameARepository(t *testing.T) {
	for source, want := range map[string]string{
		"https://github.com/authors/system":               "https://github.com/authors/system",
		"https://github.com/authors/system.git":           "https://github.com/authors/system",
		"https://github.com/authors/system/tree/main/src": "https://github.com/authors/system",
		"https://gitlab.com/group/sub/system/-/tree/main": "https://gitlab.com/group/sub/system",
		"https://gitlab.com/group/system.git":             "https://gitlab.com/group/system",
		"https://git.example.org/lab/system.git":          "https://git.example.org/lab/system.git",
		"https://codeberg.org/authors/system":             "https://codeberg.org/authors/system",
		"https://osf.io/abcd/download":                    "",
		"https://github.com/authors":                      "",
		"https://gitlab.com/system":                       "",
		"not an address":                                  "",
	} {
		got, ok := gitRepository(source)
		if (want != "") != ok || got != want {
			t.Errorf("gitRepository(%q) = %q, %v; want %q", source, got, ok, want)
		}
	}
	if got := safeName("..", "artifact"); got != "artifact" {
		t.Errorf("safeName(..) = %q", got)
	}
}
