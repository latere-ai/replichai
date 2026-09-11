// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

func newSession(t *testing.T) *Session {
	t.Helper()
	session, err := CreateSession(t.TempDir(), "7f3a91c2", os.LookupEnv)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return session
}

// A session id whose directory already exists is refused rather than adopted.
// The id is four random bytes, so two sessions can collide, and MkdirAll would
// have re-initialised the first session's workspace as if it were the
// second's, losing the first's evidence to a run that believed it started
// clean.
func TestCreateSessionRefusesAnExistingID(t *testing.T) {
	root := t.TempDir()
	first, err := CreateSession(root, "collision", os.LookupEnv)
	if err != nil {
		t.Fatalf("first CreateSession: %v", err)
	}
	// Leave a mark in the first session's workspace, so a re-initialisation
	// that adopted the directory would be detectable.
	marker := filepath.Join(first.Workspace(), "evidence.txt")
	if err := os.WriteFile(marker, []byte("the first session's work"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if _, err := CreateSession(root, "collision", os.LookupEnv); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("a colliding id was not refused: %v", err)
	}
	// The first session's directory is untouched: nothing re-initialised it.
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the colliding create disturbed the first session: %v", err)
	}
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

// The evidence path is never the same as any path a stage may write. This is
// the reason the directory is split as it is.
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

// An event whose Raw carries a whole enormous stream line is bounded when it is
// written, not left to break the read. The line on disk stays under the cap,
// the event still round-trips, and its Raw is replaced by a marker that names
// the raw log where the whole value still is.
func TestAppendBoundsAnOversizeEventLine(t *testing.T) {
	session := newSession(t)
	payload := strings.Repeat("a", maxEventBytes+1<<10)
	raw, err := json.Marshal(payload) // Raw must be valid JSON
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event := managed.Event{ID: "worker-0", Role: managed.RoleAgent, Kind: managed.KindMessage, Raw: raw}
	if err := session.AppendEvents([]managed.Event{event}); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(session.Dir(), eventsFile))
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(body)), "\n") {
		if len(line) > maxEventBytes {
			t.Fatalf("an event line of %d bytes was written past the %d-byte bound", len(line), maxEventBytes)
		}
	}
	events, err := session.Events()
	if err != nil || len(events) != 1 {
		t.Fatalf("Events = %+v, %v", events, err)
	}
	if !strings.Contains(string(events[0].Raw), "elided") || !strings.Contains(string(events[0].Raw), "raw log") {
		t.Fatalf("the oversize Raw was not marked: %s", events[0].Raw)
	}
}

// A transcript that cannot be read to the end fails the read rather than
// returning the prefix that fitted as if it were the whole of it. The write
// side can no longer produce a line this long; one written directly stands in
// for a transcript damaged some other way.
func TestEventsSurfacesAScannerErrorRatherThanTruncating(t *testing.T) {
	session := newSession(t)
	if err := session.AppendEvents([]managed.Event{
		{ID: "worker-0", Role: managed.RoleAgent, Kind: managed.KindMessage, Content: "ok"},
	}); err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	path := filepath.Join(session.Dir(), eventsFile)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	// A single line longer than the read buffer, which bufio cannot scan.
	if _, err := file.WriteString(strings.Repeat("x", 9<<20) + "\n"); err != nil {
		_ = file.Close()
		t.Fatalf("write oversize line: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := session.Events(); err == nil {
		t.Fatal("a transcript that could not be read to the end returned no error")
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

// A symlink is not an artifact. uv unpacks its wheel cache into directories
// and links to them, and WalkDir reports a link as a non-directory whatever it
// points at: listing one and then reading it failed the whole harvest, and
// with it every publication of a run that had installed a dependency.
func TestArtifactsSkipLinks(t *testing.T) {
	session := newSession(t)
	workspace := session.Workspace()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("r"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cache := filepath.Join(workspace, "cache", "wheels", "attrs-26.1.0-py3-none-any")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cache, "attrs.py"), []byte("w"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(cache, filepath.Join(workspace, "attrs-26.1.0-py3-none-any")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	artifacts, err := session.Artifacts()
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	for _, artifact := range artifacts {
		if artifact.Path == "attrs-26.1.0-py3-none-any" {
			t.Fatalf("a link to a directory was listed as an artifact: %v", artifacts)
		}
		if _, err := session.Fetch(artifact); err != nil {
			t.Fatalf("listed %s but could not read it: %v", artifact.Path, err)
		}
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

// A configured credential is removed from an event's content and its raw
// payload before it is written, so a harness that echoes its own environment
// into a tool result does not put a live credential into the published
// evidence. An empty credential is ignored rather than replacing everything.
func TestRedactEventRemovesConfiguredCredentials(t *testing.T) {
	event := managed.Event{
		Content: "export ANTHROPIC_API_KEY=sk-secret-123 && run",
		Raw:     json.RawMessage(`"the key sk-secret-123 leaked here"`),
	}
	redactEvent(&event, []string{"", "sk-secret-123"})
	if strings.Contains(event.Content, "sk-secret-123") {
		t.Errorf("the credential survived in the content: %q", event.Content)
	}
	if strings.Contains(string(event.Raw), "sk-secret-123") {
		t.Errorf("the credential survived in the raw payload: %s", event.Raw)
	}
	if !strings.Contains(event.Content, "[redacted]") {
		t.Errorf("the redaction left no marker: %q", event.Content)
	}
	// An event holding no credential is untouched.
	clean := managed.Event{Content: "nothing secret here"}
	redactEvent(&clean, []string{"sk-secret-123"})
	if clean.Content != "nothing secret here" {
		t.Errorf("a clean event was altered: %q", clean.Content)
	}
}

// The environment record carries no credential, only which kind was used.
func TestTheEnvironmentRecordNamesTheAuthKindNotTheCredential(t *testing.T) {
	session := newSession(t)
	record := NewEnvironment(context.Background(), sandbox.Host, harness.ClaudeCode,
		managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5"},
		harness.Gateway{Name: "lux", Auth: harness.AuthAPIKey, Key: "sk-secret"},
		sandbox.NetworkOpen)
	record.Sandbox.Version = "python 3.13.2"
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
	if back.Sandbox.Version != "python 3.13.2" {
		t.Errorf("sandbox version = %q", back.Sandbox.Version)
	}
}

// A local run builds in the directory that becomes the repository, so the
// dependency tree it installs sits in the repository root. Keeping it out of a
// commit is the repository's own rule, not something the agent has to
// remember, and an agent that writes its own .gitignore must not be able to
// undo it.
// The same tree must not be harvested either. A run of this shape produced
// 8200 artifacts, 24 of which were the reproduction; the rest was the
// virtualenv, copied file by file into the output directory.
func TestAnInstalledDependencyTreeIsNotHarvested(t *testing.T) {
	session := newSession(t)
	workspace := session.Workspace()

	// A virtualenv under an unpredictable name, recognised by the marker
	// Python itself writes, and one recognised by name.
	for _, dir := range []string{"pyenv-3.13/lib/python3.13/site-packages/numpy", "node_modules/left-pad"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	for path, body := range map[string]string{
		"pyenv-3.13/pyvenv.cfg": "home = /usr/bin",
		"pyenv-3.13/lib/python3.13/site-packages/numpy/__init__.py": "x",
		"node_modules/left-pad/index.js":                            "x",
		"README.md":                                                 "the reproduction",
		"src/main.py":                                               "the reproduction",
		"outputs/summary.json":                                      "{}",
	} {
		full := filepath.Join(workspace, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
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
	slices.Sort(paths)
	want := []string{"README.md", "outputs/summary.json", "src/main.py"}
	if !slices.Equal(paths, want) {
		t.Errorf("harvested %v, want %v", paths, want)
	}
}

func TestAnInstalledDependencyTreeCannotBeCommitted(t *testing.T) {
	session := newSession(t)
	workspace := session.Workspace()

	packages := filepath.Join(workspace, "venv", "lib", "python3.13", "site-packages", "numpy")
	if err := os.MkdirAll(packages, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for path, body := range map[string]string{
		filepath.Join(packages, "__init__.py"):       "x",
		filepath.Join(workspace, "node_modules.txt"): "not a tree",
		filepath.Join(workspace, "src.py"):           "the reproduction",
		// The agent authors its own ignore file, replacing anything seeded
		// there. The rule must survive that.
		filepath.Join(workspace, ".gitignore"): "*.log\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	command := exec.Command("git", "-C", workspace, "add", "-A")
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}
	staged, err := exec.Command("git", "-C", workspace, "diff", "--cached", "--name-only").Output()
	if err != nil {
		t.Fatalf("git diff: %v", err)
	}
	listed := strings.Fields(string(staged))
	for _, path := range listed {
		if strings.HasPrefix(path, "venv/") {
			t.Errorf("the dependency tree was staged: %v", listed)
			break
		}
	}
	if !slices.Contains(listed, "src.py") || !slices.Contains(listed, "node_modules.txt") {
		t.Errorf("the reproduction's own files were not staged: %v", listed)
	}
}
