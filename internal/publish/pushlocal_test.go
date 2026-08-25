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

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
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
	dir := t.TempDir()
	if output, err := exec.Command("git", "init", "--bare", "--initial-branch=main", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, output)
	}
	return dir
}

// An agent-authored repository is a real software project, and all of it
// reaches the remote. Routing it through Classify — the allowlist over a
// harvested output directory — would have stripped the licence, the
// requirements file and the tests.
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

// The gate still refuses third-party content, and refuses before anything
// leaves the machine rather than verifying afterwards.
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

// A workspace with no commits is the 044d outcome, reached before a push
// rather than discovered as an empty repository afterwards.
func TestAWorkspaceWithNoCommitsSaysSo(t *testing.T) {
	dir := workspace(t, nil)
	_, err := LocalPush{Workspace: dir, RemoteURL: "file://" + bareRemote(t)}.Run(context.Background())
	if !errors.Is(err, ErrNothingCommitted) {
		t.Fatalf("Run = %v", err)
	}
}

// The credential never lands anywhere the session directory keeps: not in the
// repository's configuration, not in the reflog, and not in a file left behind.
func TestTheTokenLeavesNothingBehind(t *testing.T) {
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
	// And the askpass helper it was written into is removed.
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "replichai-askpass-*"))
	if len(matches) > 0 {
		t.Errorf("askpass helpers left behind: %v", matches)
	}
}

// The credential's user part may sit in the URL; the credential may not.
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

// The system's own outputs are collected from where the session wrote them,
// so finalize commits the environment record and the judge's verdict beside
// the agent's history rather than inside the agent's reach.
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
			return nil
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
