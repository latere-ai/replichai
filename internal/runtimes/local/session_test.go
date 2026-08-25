package local

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

func newSession(t *testing.T) *Session {
	t.Helper()
	session, err := CreateSession(t.TempDir(), "7f3a91c2", os.Environ())
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return session
}

// The session directory is private, and the workspace is a git repository with
// an identity and no remote: an identity because a run must not stall on git
// refusing to commit, and no remote because the host pushes.
func TestTheWorkspaceIsARepositoryWithNoRemote(t *testing.T) {
	session := newSession(t)
	info, err := os.Stat(session.Dir())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("session directory mode = %o", perm)
	}
	if _, err := os.Stat(filepath.Join(session.Workspace(), ".git")); err != nil {
		t.Fatalf("the workspace is not a repository: %v", err)
	}
	remotes, err := exec.Command("git", "-C", session.Workspace(), "remote").Output()
	if err != nil {
		t.Fatalf("git remote: %v", err)
	}
	if strings.TrimSpace(string(remotes)) != "" {
		t.Fatalf("the workspace has a remote: %q", remotes)
	}
	email, err := exec.Command("git", "-C", session.Workspace(), "config", "user.email").Output()
	if err != nil || strings.TrimSpace(string(email)) == "" {
		t.Fatalf("no committer identity: %q, %v", email, err)
	}
	// A commit succeeds with no further configuration, which is the point of
	// setting the identity up front.
	if err := os.WriteFile(filepath.Join(session.Workspace(), "README.md"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-m", "first"}} {
		command := exec.Command("git", append([]string{"-C", session.Workspace()}, args...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
}

// The evidence path is never the same as anything a stage may write. This is
// the whole reason the directory is split the way it is.
func TestEvidenceIsNotUnderTheWorkspace(t *testing.T) {
	session := newSession(t)
	for _, writable := range []string{session.Workspace(), session.Tmp(), session.Venv(), session.Grade(), session.Home()} {
		if strings.HasPrefix(session.Evidence(), writable+string(filepath.Separator)) || session.Evidence() == writable {
			t.Fatalf("evidence %q is inside writable %q", session.Evidence(), writable)
		}
	}
	// The venv and the downloads are outside the repository, so one
	// `git add -A` cannot commit either.
	for _, outside := range []string{session.Venv(), session.Tmp()} {
		if strings.HasPrefix(outside, session.Workspace()+string(filepath.Separator)) {
			t.Fatalf("%q is inside the workspace", outside)
		}
	}
}

// The transcript is append-only and survives a truncated final line: a process
// killed mid-write loses that line, not the evidence before it.
func TestTranscriptAppendsAndSurvivesATruncatedLine(t *testing.T) {
	session := newSession(t)
	first := []managed.Event{
		{ID: "worker-0", Role: managed.RoleSystem, Kind: managed.EventKind("system"), Time: time.Now().UTC()},
		{ID: "worker-1", Role: managed.RoleAgent, Kind: managed.KindMessage, Content: "hello"},
	}
	if err := session.AppendEvents(first); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	if err := session.AppendEvents([]managed.Event{{ID: "worker-2", Kind: managed.KindToolUse, Tool: "Bash"}}); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	events, err := session.Events()
	if err != nil || len(events) != 3 || events[2].Tool != "Bash" {
		t.Fatalf("Events = %+v, %v", events, err)
	}

	path := filepath.Join(session.Dir(), eventsFile)
	body, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(body, []byte(`{"id":"worker-3","kin`)...), 0o600); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	events, err = session.Events()
	if err != nil {
		t.Fatalf("a torn final line failed the read: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("a torn final line changed the transcript: %d events", len(events))
	}
}

// Artifacts are the run's own work. The repository's metadata and build
// residue are not results and never reach a reproduction.
func TestArtifactsExcludeRepositoryMetadataAndResidue(t *testing.T) {
	session := newSession(t)
	files := map[string]string{
		"README.md":             "r",
		"src/run.py":            "p",
		"src/__pycache__/a.pyc": "junk",
		"src/stale.pyc":         "junk",
		".git/config":           "junk",
	}
	for name, body := range files {
		path := filepath.Join(session.Workspace(), filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	artifacts, err := session.Artifacts()
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	var paths []string
	for _, artifact := range artifacts {
		paths = append(paths, artifact.Path)
	}
	want := []string{"README.md", "src/run.py"}
	if len(paths) != len(want) {
		t.Fatalf("artifacts = %v, want %v", paths, want)
	}
	for index := range want {
		if paths[index] != want[index] {
			t.Fatalf("artifacts = %v, want %v", paths, want)
		}
	}
	body, err := session.Fetch(managed.Artifact{ID: "src/run.py", Path: "src/run.py"})
	if err != nil || string(body) != "p" {
		t.Fatalf("Fetch = %q, %v", body, err)
	}
	if _, err := session.Fetch(managed.Artifact{Path: "../../etc/passwd"}); err == nil {
		t.Fatal("a path escaping the workspace was fetched")
	}
	if _, err := session.Fetch(managed.Artifact{Path: "absent.md"}); err == nil {
		t.Fatal("a missing artifact was fetched")
	}
}

// A fresh process finds a detached run by its directory alone: the locator,
// the status and the transcript are all on disk.
func TestAFreshProcessReopensASession(t *testing.T) {
	session := newSession(t)
	locator := Locator{
		Driver: sandbox.Host, Harness: harness.ClaudeCode,
		Stages: []StageRecord{{
			Role: harness.StageWorker, Name: "worker-0",
			Handle: sandbox.StageHandle{Driver: sandbox.Host, ID: "pid:48213@1724570400"},
			Offset: 4096, Lines: 12,
		}},
	}
	if err := session.WriteLocator(locator); err != nil {
		t.Fatalf("WriteLocator: %v", err)
	}
	status := managed.Status{State: managed.StateRunning, Model: managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5"}}
	if err := session.WriteStatus(status); err != nil {
		t.Fatalf("WriteStatus: %v", err)
	}

	reopened, err := OpenSession(session.Dir())
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	back, err := reopened.ReadLocator()
	if err != nil {
		t.Fatalf("ReadLocator: %v", err)
	}
	if back.Dir != session.Dir() || back.Version != LocatorVersion || len(back.Stages) != 1 {
		t.Fatalf("locator = %+v", back)
	}
	if back.Stages[0].Offset != 4096 || back.Stages[0].Lines != 12 {
		t.Fatalf("the pump's position was lost: %+v", back.Stages[0])
	}
	if got, err := reopened.ReadStatus(); err != nil || got.State != managed.StateRunning {
		t.Fatalf("ReadStatus = %+v, %v", got, err)
	}
	if _, err := OpenSession(filepath.Join(session.Dir(), "absent")); err == nil {
		t.Fatal("a missing session opened")
	}
}

// The locator travels through storage and logs untouched, so the contract
// refuses one carrying a secret-bearing key. These field names were chosen to
// pass that check rather than to be excused from it.
func TestTheLocatorSurvivesTheContractsSecretCheck(t *testing.T) {
	session := newSession(t)
	locator := Locator{
		Driver: sandbox.Container, Harness: harness.Codex,
		Stages: []StageRecord{{
			Role:   harness.StageJudge,
			Name:   "judge-0",
			Handle: sandbox.StageHandle{Driver: sandbox.Container, ID: "replichai-7f3a91c2-j0"},
		}},
	}
	if err := session.WriteLocator(locator); err != nil {
		t.Fatalf("WriteLocator: %v", err)
	}
	encoded, err := json.Marshal(locator)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ref := managed.SessionRef{
		Runtime: Name, ID: session.ID(), Revision: 1,
		LocatorVersion: LocatorVersion, Locator: encoded,
	}
	if err := ref.Validate(managed.DefaultMaxLocatorBytes); err != nil {
		t.Fatalf("the locator is refused by the contract: %v", err)
	}
}

// The environment record carries no credential, only which kind was used.
func TestTheEnvironmentRecordNamesTheAuthKindNotTheCredential(t *testing.T) {
	session := newSession(t)
	record := NewEnvironment(sandbox.Host, harness.ClaudeCode,
		managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5"},
		harness.Gateway{Name: "lux", Auth: harness.AuthAPIKey, Key: "sk-secret"},
		sandbox.NetworkOpen)
	record.Toolchain = map[string]string{"python": "3.13.2"}
	if err := session.WriteEnvironment(record); err != nil {
		t.Fatalf("WriteEnvironment: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(session.Dir(), environmentFile))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(body), "sk-secret") {
		t.Fatalf("the credential reached the environment record:\n%s", body)
	}
	back, err := session.ReadEnvironment()
	if err != nil || back.Harness.Auth != harness.AuthAPIKey || back.Host.OS == "" {
		t.Fatalf("environment = %+v, %v", back, err)
	}
	if back.Toolchain["python"] != "3.13.2" {
		t.Errorf("toolchain = %v", back.Toolchain)
	}
}
