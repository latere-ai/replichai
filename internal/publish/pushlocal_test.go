// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireGit skips a test that needs the git binary on a machine without one,
// so the suite stays hermetic rather than failing on what is not installed.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	requireGit(t)
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

// workspace is a repository the agent committed into and never had a remote
// for, which is what the local path produces.
func workspace(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "--initial-branch=main")
	git(t, dir, "config", "user.name", "replichai")
	git(t, dir, "config", "user.email", "reproduction@replichai.invalid")
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if len(files) > 0 {
		git(t, dir, "add", "-A")
		git(t, dir, "commit", "-m", "the reproduction")
	}
	return dir
}

func bareRemote(t *testing.T) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	if output, err := exec.Command("git", "init", "--bare", "--initial-branch=main", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, output)
	}
	return dir
}

// TestAnAgentAuthoredRepositoryPushesWhole checks that an agent-authored
// repository, which is a real software project, reaches the remote in full.
// Routing it through Classify, the allowlist over a harvested output
// directory, would have stripped the license, the requirements file and the
// tests.
func TestAnAgentAuthoredRepositoryPushesWhole(t *testing.T) {
	dir := workspace(t, map[string]string{
		"README.md":          "# reproduction",
		"REPRODUCIBILITY.md": "verdict",
		"LICENSE":            "MIT",
		"requirements.txt":   "numpy==2.1.0",
		"src/run.py":         "print(1)",
		"tests/test_run.py":  "assert True",
		".gitignore":         "__pycache__/",
		"verdict.json":       `{"verdict":"partial"}`,
	})
	remote := bareRemote(t)
	push := LocalPush{Workspace: dir, RemoteURL: "file://" + remote}
	pushed, err := push.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if pushed.Commits != 1 || pushed.Branch != "main" || pushed.Head == "" {
		t.Fatalf("pushed = %+v", pushed)
	}
	listed := git(t, remote, "ls-tree", "-r", "--name-only", "main")
	for _, want := range []string{"LICENSE", "requirements.txt", "tests/test_run.py", ".gitignore", "src/run.py"} {
		if !strings.Contains(listed, want) {
			t.Errorf("%s did not reach the remote:\n%s", want, listed)
		}
	}
}

// TestThirdPartyContentIsRefusedBeforeThePush checks that the gate refuses
// third-party content before anything leaves the machine rather than verifying
// afterwards.
func TestThirdPartyContentIsRefusedBeforeThePush(t *testing.T) {
	dir := workspace(t, map[string]string{
		"README.md":       "# reproduction",
		"paper.pdf":       "%PDF-1.7",
		"data/corpus.zip": "PK",
	})
	remote := bareRemote(t)
	push := LocalPush{Workspace: dir, RemoteURL: "file://" + remote}
	_, err := push.Run(context.Background())
	if !errors.Is(err, ErrRefusedContent) {
		t.Fatalf("Run = %v", err)
	}
	for _, want := range []string{"paper.pdf", "data/corpus.zip"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q: %v", want, err)
		}
	}
	// Nothing left the machine.
	if branches := git(t, remote, "branch", "--list"); strings.TrimSpace(branches) != "" {
		t.Fatalf("a refused tree was pushed anyway: %q", branches)
	}
}

// TestAWorkspaceWithNoCommitsSaysSo checks that a workspace with no commits
// reports the 044d outcome before a push rather than being discovered as an
// empty repository afterwards.
func TestAWorkspaceWithNoCommitsSaysSo(t *testing.T) {
	dir := workspace(t, nil)
	_, err := LocalPush{Workspace: dir, RemoteURL: "file://" + bareRemote(t)}.Run(context.Background())
	if !errors.Is(err, ErrNothingCommitted) {
		t.Fatalf("Run = %v", err)
	}
}

// TestOnlyAnUnbornBranchIsNothingCommitted checks that only an unborn branch
// reports ErrNothingCommitted. That error is a finding about the run, and the
// CLI records it as the run's outcome. Every other reason HEAD cannot be read,
// such as the directory being gone, never having been a repository, or git
// being missing, is a failure of the push. Reporting it as the run's outcome
// would record "produced nothing" over a workspace that may hold the whole
// reproduction.
func TestOnlyAnUnbornBranchIsNothingCommitted(t *testing.T) {
	requireGit(t)
	remote := "file://" + bareRemote(t)

	t.Run("the workspace directory is gone", func(t *testing.T) {
		_, err := LocalPush{Workspace: filepath.Join(t.TempDir(), "gone"), RemoteURL: remote}.Run(context.Background())
		if errors.Is(err, ErrNothingCommitted) {
			t.Fatalf("a missing workspace was reported as a run that committed nothing: %v", err)
		}
		if !errors.Is(err, ErrNoWorkspace) {
			t.Fatalf("Run = %v, want ErrNoWorkspace", err)
		}
	})

	t.Run("the directory is not a repository", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# not committed anywhere"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		_, err := LocalPush{Workspace: dir, RemoteURL: remote}.Run(context.Background())
		if errors.Is(err, ErrNothingCommitted) {
			t.Fatalf("a plain directory was reported as a run that committed nothing: %v", err)
		}
		if !errors.Is(err, ErrNoWorkspace) {
			t.Fatalf("Run = %v, want ErrNoWorkspace", err)
		}
	})

	t.Run("git is not installed", func(t *testing.T) {
		dir := workspace(t, map[string]string{"README.md": "# reproduction"})
		t.Setenv("PATH", t.TempDir())
		_, err := LocalPush{Workspace: dir, RemoteURL: remote}.Run(context.Background())
		if errors.Is(err, ErrNothingCommitted) || errors.Is(err, ErrNoWorkspace) {
			t.Fatalf("a missing git binary was blamed on the workspace: %v", err)
		}
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("Run = %v, want the missing executable", err)
		}
	})
}

// TestVerifyReadsTheCommittedTreeNotTheIndex checks that the gate reads the
// tree about to be pushed rather than the index. A file staged but never
// committed does not travel, so it cannot block the push; a file committed and
// then unstaged does travel, so it must.
func TestVerifyReadsTheCommittedTreeNotTheIndex(t *testing.T) {
	dir := workspace(t, map[string]string{"README.md": "# reproduction"})
	remote := bareRemote(t)

	// Staged, never committed: not part of what is pushed.
	if err := os.WriteFile(filepath.Join(dir, "paper.pdf"), []byte("%PDF-1.7"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	git(t, dir, "add", "paper.pdf")
	push := LocalPush{Workspace: dir, RemoteURL: "file://" + remote}
	if flagged, err := push.Verify(context.Background()); err != nil || len(flagged) != 0 {
		t.Fatalf("a staged, uncommitted file blocked the push: flagged %v, err %v", flagged, err)
	}
	if _, err := push.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Committed, then removed from the index: it is in the tree being pushed.
	git(t, dir, "commit", "-m", "the paper, by mistake")
	git(t, dir, "rm", "--cached", "paper.pdf")
	flagged, err := push.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(flagged) != 1 || flagged[0] != "paper.pdf" {
		t.Fatalf("a committed third-party file hidden from the index was not flagged: %v", flagged)
	}
	if _, err := push.Run(context.Background()); !errors.Is(err, ErrRefusedContent) {
		t.Fatalf("Run = %v, want the committed paper refused", err)
	}
}

// TestTheTokenLeavesNothingBehind checks that the credential is not written
// anywhere the session directory keeps: not in the repository's configuration,
// not in the reflog, and not in a file left behind.
func TestTheTokenLeavesNothingBehind(t *testing.T) {
	// The helper is written under TMPDIR, so the assertion below has to look
	// at a directory only this test writes to. Against the machine's own
	// TMPDIR it read every replichai that ever ran here, and a push killed
	// before its cleanup, for example by the out-of-memory killer, failed this
	// test from a package that had nothing to do with it.
	t.Setenv("TMPDIR", t.TempDir())
	dir := workspace(t, map[string]string{"README.md": "# reproduction"})
	remote := bareRemote(t)
	push := LocalPush{Workspace: dir, RemoteURL: "file://" + remote, Token: "ghp-supersecret"}
	if _, err := push.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	found := grepTree(t, dir, "ghp-supersecret")
	if len(found) > 0 {
		t.Fatalf("the token survives in %v", found)
	}
	// The remote is gone, so a kept session directory carries no push
	// target either.
	if remotes := git(t, dir, "remote"); strings.TrimSpace(remotes) != "" {
		t.Fatalf("the remote survived the push: %q", remotes)
	}
	// The askpass helper it was written into is removed.
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "replichai-askpass-*"))
	if len(matches) > 0 {
		t.Errorf("askpass helpers left behind: %v", matches)
	}
}

// TestOnlyTheUserPartReachesTheURL checks that the credential's user part may
// appear in the URL and the credential itself may not.
func TestOnlyTheUserPartReachesTheURL(t *testing.T) {
	push := LocalPush{RemoteURL: "https://github.com/open-hai/x", Token: "ghp-supersecret"}
	url := push.remoteWithUser()
	if strings.Contains(url, "ghp-supersecret") {
		t.Fatalf("the token is in the remote URL: %s", url)
	}
	if !strings.HasPrefix(url, "https://x-access-token@github.com/") {
		t.Fatalf("remote = %s", url)
	}
	// With no token there is nothing to authenticate and the URL is left
	// exactly as given, which is what a file:// remote in a test needs.
	plain := LocalPush{RemoteURL: "file:///tmp/x"}.remoteWithUser()
	if plain != "file:///tmp/x" {
		t.Fatalf("remote = %s", plain)
	}
}

// TestWorkspaceOutputsAreTheSystemsOwn checks that the system's own outputs
// are collected from where the session wrote them, so finalize commits the
// environment record and the judge's verdict beside the agent's history rather
// than within the agent's reach.
func TestWorkspaceOutputsAreTheSystemsOwn(t *testing.T) {
	dir := t.TempDir()
	for path, body := range map[string]string{
		"session/environment.json": `{"sandbox":{"driver":"host"}}`,
		"grade/verdict-0.json":     `{"grade":"passed"}`,
		"grade/verdict-1.json":     `{"grade":"failed"}`,
		"grade/notes.txt":          "ignored",
	} {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	outputs, err := WorkspaceOutputs(dir)
	if err != nil {
		t.Fatalf("WorkspaceOutputs: %v", err)
	}
	for _, want := range []string{"environment.json", "grade/verdict-0.json", "grade/verdict-1.json"} {
		if _, ok := outputs[want]; !ok {
			t.Errorf("%s was not collected: %v", want, outputNames(outputs))
		}
	}
	if _, ok := outputs["grade/notes.txt"]; ok {
		t.Error("an unrelated file in the grade directory was collected")
	}
	// Both survive the gate they are committed through: neither is a fixed
	// report file, and an allowlist would have dropped them.
	for name := range outputs {
		if len(VerifyTree([]string{name})) > 0 {
			t.Errorf("%s is refused by the redistribution gate", name)
		}
	}
}

func outputNames(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}

// grepTree reports every file under dir whose bytes contain want.
func grepTree(t *testing.T, dir, want string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // an unreadable entry cannot hold the token; keep walking
		}
		body, readErr := os.ReadFile(path)
		if readErr == nil && strings.Contains(string(body), want) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return found
}

// TestAPushNeedsARemoteTheWorkspaceDescendsFrom checks that a local run's
// remote has to be empty when the host pushes. The workspace is a fresh
// repository with no remote (045g), so its history descends from nothing. A
// commit already on the remote, such as an identity file written there before
// the run, makes every push a non-fast-forward, and the whole reproduction is
// refused after the agent has already done the work.
//
// This is the constraint the ordering in cmd/replichai depends on: whatever
// the system writes into the repository through the forge API, it writes after
// this push and never before it.
func TestAPushNeedsARemoteTheWorkspaceDescendsFrom(t *testing.T) {
	remote := bareRemote(t)
	seed := workspace(t, map[string]string{".replichai/paper.json": `{"key":"doi:10/x"}`})
	git(t, seed, "push", "file://"+remote, "HEAD:refs/heads/main")

	dir := workspace(t, map[string]string{"README.md": "# reproduction"})
	_, err := LocalPush{Workspace: dir, RemoteURL: "file://" + remote}.Run(context.Background())
	if err == nil {
		t.Fatal("a push onto an unrelated history was accepted; it cannot be")
	}
	if !strings.Contains(err.Error(), "rejected") && !strings.Contains(err.Error(), "fetch first") {
		t.Fatalf("the refusal does not say the remote had other work: %v", err)
	}
}
