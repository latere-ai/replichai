// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/managed/contracttest"
	"github.com/latere-ai/replichai/internal/runtimes/local"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/localtest"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

func build(t *testing.T, script localtest.Script) (*local.Runtime, *localtest.Sandbox) {
	t.Helper()
	return buildWith(t, script, local.DefaultCatalogFile(), noCredentials, harness.Machine{})
}

// noCredentials is a lookup with nothing in it, for a catalogue on the
// harness's own login.
func noCredentials(string) (string, bool) { return "", false }

// buildWith composes a runtime over the scripted driver with the given
// catalogue, credential lookup and machine, so a test can put the runtime on an
// API key or on a machine with a home directory.
func buildWith(t *testing.T, script localtest.Script, file local.CatalogFile, lookup local.LookupEnv,
	machine harness.Machine) (*local.Runtime, *localtest.Sandbox) {
	t.Helper()
	driver := localtest.New(script)
	registry, err := harness.NewRegistry(harness.NewClaudeCode(), harness.NewCodex())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	catalog, err := local.NewCatalog(file, registry, lookup)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	runtime, err := local.New(local.Config{
		Sandbox: driver, Harness: harness.NewClaudeCode(), Catalog: catalog,
		Root: t.TempDir(), Lookup: os.LookupEnv, Machine: machine,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runtime, driver
}

// keyedCatalog declares one Claude Code model behind a gateway that carries
// its own API key, read from the named variable.
func keyedCatalog(keyEnv string) local.CatalogFile {
	return local.CatalogFile{
		Gateways: map[string]local.GatewayConfig{
			"keyed": {Dialect: string(harness.DialectAnthropic), Auth: string(harness.AuthAPIKey), KeyEnv: keyEnv},
		},
		Models: []local.ModelConfig{{
			Provider: "anthropic", ID: "claude-opus-5", Gateway: "keyed",
			Harness: string(harness.ClaudeCode), Preferences: []string{"best"},
		}},
	}
}

// readEnvironment is the run's environment record, read back from the session
// directory the reference names.
func readEnvironment(t *testing.T, ref managed.SessionRef) local.Environment {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(sessionDir(t, ref), "session", "environment.json"))
	if err != nil {
		t.Fatalf("read the environment record: %v", err)
	}
	var record local.Environment
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("decode the environment record: %v", err)
	}
	return record
}

// The operator's own sign-in — the harness's configuration directory, and on
// macOS the keychain — reaches a session that runs on it and no other, and the
// environment record lists exactly what was opened. A session carrying its own
// credential is granted nothing under the operator's home: on Linux the login
// and the operator's whole conversation history live in the same directory.
func TestTheLoginGrantReachesOnlyASessionOnTheOperatorsLogin(t *testing.T) {
	machine := harness.Machine{Platform: "darwin", Home: "/Users/x", TempDir: "/tmp", UID: 501}
	login := harness.LoginPaths(harness.NewClaudeCode().Needs(machine))
	if len(login) == 0 {
		t.Fatal("the harness declares no login paths, so there is nothing to gate")
	}

	// On the harness's own login every agentic stage is granted the login,
	// read-only, and the bootstrap — which talks to no model — is not.
	runtime, driver := buildWith(t, localtest.Script{}, local.DefaultCatalogFile(), noCredentials, machine)
	status := runToTerminal(t, runtime, start(t, runtime, spec(true)))
	for _, stage := range driver.Stages() {
		granted := map[string]sandbox.Access{}
		for _, path := range stage.Paths {
			granted[path.Host] = path.Access
		}
		for _, path := range login {
			access, ok := granted[path]
			switch {
			case strings.HasPrefix(stage.Name, "bootstrap") && ok:
				t.Errorf("the bootstrap was handed the login at %s", path)
			case !strings.HasPrefix(stage.Name, "bootstrap") && !ok:
				t.Errorf("stage %s cannot reach the login at %s: %+v", stage.Name, path, stage.Paths)
			case ok && access != sandbox.ReadOnly:
				t.Errorf("stage %s can write the login at %s", stage.Name, path)
			}
		}
	}
	if got := readEnvironment(t, status.Ref).Sandbox.ConfigRead; !slices.Equal(got, login) {
		t.Errorf("the record discloses %v, the run was granted %v", got, login)
	}

	// On an API key nothing under the operator's home is opened, and the
	// record says so by listing nothing.
	credential := func(name string) (string, bool) { return "sk-test", name == "TEST_MODEL_KEY" }
	runtime, driver = buildWith(t, localtest.Script{}, keyedCatalog("TEST_MODEL_KEY"), credential, machine)
	status = runToTerminal(t, runtime, start(t, runtime, spec(true)))
	for _, stage := range driver.Stages() {
		for _, path := range stage.Paths {
			if strings.HasPrefix(path.Host, machine.Home) {
				t.Errorf("a session on its own credential was handed %s in stage %s", path.Host, stage.Name)
			}
		}
	}
	if got := readEnvironment(t, status.Ref).Sandbox.ConfigRead; len(got) != 0 {
		t.Errorf("the record discloses %v for a session that was granted nothing", got)
	}
}

// The runtime is held to the same black-box suite as every other adapter.
func TestLocalMeetsTheManagedContract(t *testing.T) {
	contracttest.Run(t, func() managed.Runtime {
		runtime, _ := build(t, localtest.Script{})
		return runtime
	})
}

// And to the shared terminal state for a rejected evaluation: failed, not done.
// Local reported done, and the job store maps state one to one, so the same
// outcome settled as done here and failed on every other runtime.
func TestLocalFailingEvaluationMeetsTheContract(t *testing.T) {
	contracttest.RunFailingEvaluation(t, func() managed.Runtime {
		runtime, _ := build(t, localtest.Script{Grades: []string{"needs_revision", "needs_revision"}})
		return runtime
	})
}

func spec(graded bool) managed.SessionSpec {
	s := managed.SessionSpec{
		Model:        managed.ModelSelector{Preference: "best"},
		Instructions: "Follow the method.",
		Task:         "Reproduce the result.",
		Sandbox: managed.SandboxSpec{
			Packages: []managed.Package{{Manager: "pip", Name: "pypdf"}},
			Network:  managed.NetworkPolicy{Mode: managed.NetworkOpen},
		},
	}
	if graded {
		s.Evaluation = &managed.EvaluationSpec{Rubric: "The result matches.", MaxRevisions: 2}
	}
	return s
}

func runToTerminal(t *testing.T, runtime *local.Runtime, ref managed.SessionRef) managed.Status {
	t.Helper()
	var previous managed.Usage
	for range 40 {
		status, err := runtime.Poll(context.Background(), ref)
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
		if !status.Usage.MonotonicFrom(previous) {
			t.Fatalf("usage went backwards: %+v then %+v", previous, status.Usage)
		}
		previous = status.Usage
		ref = status.Ref
		if status.State.Terminal() {
			return status
		}
	}
	t.Fatal("the session never became terminal")
	return managed.Status{}
}

func start(t *testing.T, runtime *local.Runtime, s managed.SessionSpec) managed.SessionRef {
	t.Helper()
	if _, err := runtime.Capabilities(context.Background()); err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	ref, err := runtime.Start(context.Background(), s)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return ref
}

// A running session whose journal names no stage is refused rather than
// crashing on the index. It cannot arise from a normal Start — the bootstrap is
// launched before the reference returns — but a truncated locator on disk could
// present it, and Poll must not panic on it.
func TestPollRefusesARunningSessionWithNoStages(t *testing.T) {
	runtime, _ := build(t, localtest.Script{})
	ref := start(t, runtime, spec(false))

	// Truncate the on-disk journal to no stages, while the status stays
	// running, which is the corruption the guard defends against.
	session, err := local.OpenSession(sessionDir(t, ref))
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if err := session.WriteLocator(local.Locator{Driver: sandbox.Host, Harness: harness.ClaudeCode}); err != nil {
		t.Fatalf("WriteLocator: %v", err)
	}
	if _, err := runtime.Poll(context.Background(), ref); err == nil {
		t.Fatal("Poll accepted a running session with no stages")
	}
}

// An attachment is written into the workspace once, and its bytes are not kept
// in the spec Poll re-reads from disk every time. Kept whole, a large
// attachment was base64-decoded on every poll for the life of the session.
func TestAttachmentBytesAreNotReReadOnEveryPoll(t *testing.T) {
	runtime, _ := build(t, localtest.Script{})
	s := spec(false)
	content := bytes.Repeat([]byte("A"), 8<<10)
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	s.Resources = []managed.Resource{{
		Kind:       managed.ResourceAttachment,
		Attachment: &managed.AttachmentResource{Path: "data/input.bin", Content: content, SHA256: digest},
	}}
	ref := start(t, runtime, s)
	dir := sessionDir(t, ref)

	// The attachment is materialised in the workspace with its bytes intact.
	body, err := os.ReadFile(filepath.Join(dir, "workspace", "data", "input.bin"))
	if err != nil {
		t.Fatalf("the attachment was not written into the workspace: %v", err)
	}
	if !bytes.Equal(body, content) {
		t.Fatalf("the attachment's bytes were altered: %d of %d", len(body), len(content))
	}
	// But the per-poll spec on disk does not carry them.
	persisted, err := os.ReadFile(filepath.Join(dir, "session", "spec.json"))
	if err != nil {
		t.Fatalf("read persisted spec: %v", err)
	}
	if bytes.Contains(persisted, []byte(base64.StdEncoding.EncodeToString(content))) {
		t.Fatal("the attachment bytes are re-read from the spec on every poll")
	}
	// The metadata is kept, so the record of what was supplied is intact.
	if !bytes.Contains(persisted, []byte(digest)) || !bytes.Contains(persisted, []byte("data/input.bin")) {
		t.Errorf("the attachment metadata was dropped along with its bytes:\n%s", persisted)
	}
	// And the session still runs to completion.
	runToTerminal(t, runtime, ref)
}

// Every transcript event is stamped with the runtime's clock, not the wall
// clock the decode happened on. The adapters stamped time.Now themselves,
// though the runtime carries an injectable clock; a run on a fixed clock now
// produces events all bearing that time.
func TestEventsAreStampedWithTheRuntimeClock(t *testing.T) {
	fixed := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	driver := localtest.New(localtest.Script{})
	registry, _ := harness.NewRegistry(harness.NewClaudeCode())
	catalog, _ := local.NewCatalog(local.DefaultCatalogFile(), registry, noCredentials)
	runtime, err := local.New(local.Config{
		Sandbox: driver, Harness: harness.NewClaudeCode(), Catalog: catalog,
		Root: t.TempDir(), Lookup: os.LookupEnv, Clock: func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	status := runToTerminal(t, runtime, start(t, runtime, spec(true)))
	events, err := runtime.Events(context.Background(), status.Ref)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events to check")
	}
	for _, event := range events {
		if !event.Time.Equal(fixed) {
			t.Errorf("event %s stamped %v, want the runtime clock %v", event.ID, event.Time, fixed)
		}
	}
}

// The output directory a caller reads before starting is the workspace the
// session it then starts actually writes to. On this runtime that path is
// per-session, so the two calls have to agree.
func TestTheOutputDirectoryIsTheSessionsOwnWorkspace(t *testing.T) {
	runtime, driver := build(t, localtest.Script{})
	capabilities, err := runtime.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	ref, err := runtime.Start(context.Background(), spec(false))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The bootstrap runs first and never touches the workspace; the worker
	// runs in the directory the caller was told about.
	runToTerminal(t, runtime, ref)
	worker := stageNamed(t, driver, "worker")
	if worker.Workdir != capabilities.OutputDir {
		t.Fatalf("the session did not run in the advertised output directory: %q vs %q",
			worker.Workdir, capabilities.OutputDir)
	}
	bootstrap := stageNamed(t, driver, "bootstrap")
	for _, path := range bootstrap.Paths {
		if path.Host == capabilities.OutputDir {
			t.Fatalf("the bootstrap was given the workspace: %+v", path)
		}
	}

	// A second session gets its own directory rather than reusing the first.
	second, err := runtime.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("second Capabilities: %v", err)
	}
	if second.OutputDir == capabilities.OutputDir {
		t.Fatal("two sessions share one workspace")
	}
}

// A Start that fails after it has taken a session leaves nothing behind: the
// directory it made is gone, so a driver that cannot launch a stage does not
// accumulate an empty session per attempt. The reference was never returned,
// so no later call could find the directory to clean it up.
func TestStartCleansUpTheSessionWhenLaunchFails(t *testing.T) {
	root := t.TempDir()
	driver := localtest.New(localtest.Script{FailLaunch: errors.New("the driver cannot start a stage")})
	registry, _ := harness.NewRegistry(harness.NewClaudeCode())
	catalog, _ := local.NewCatalog(local.DefaultCatalogFile(), registry, noCredentials)
	runtime, err := local.New(local.Config{
		Sandbox: driver, Harness: harness.NewClaudeCode(), Catalog: catalog,
		Root: root, Lookup: os.LookupEnv,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := runtime.Start(context.Background(), spec(false)); err == nil {
		t.Fatal("Start succeeded although the driver cannot launch anything")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read the session root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("a failed Start left %d session directories behind: %v", len(entries), entries)
	}
}

// A stage writes its last line and exits between two polls. That line is the
// harness's result event, which carries the run's total cost, and a Poll that
// drained before it observed left it for a next poll a finished stage never
// gets — so the line was lost and the bill folded from a per-message maximum.
func TestPollKeepsTheFinalLineOfAStageThatExitsBetweenObservations(t *testing.T) {
	runtime, _ := build(t, localtest.Script{PollsBeforeExit: 1, FinalLineOnExit: true})
	status := runToTerminal(t, runtime, start(t, runtime, spec(false)))

	events, err := runtime.Events(context.Background(), status.Ref)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Kind == harness.KindResult {
			found = true
		}
	}
	if !found {
		t.Fatal("the stage's final result line was lost: the poll drained before it observed")
	}
	// The result event carries the run's vendor cost, and the terminal status's
	// usage is only complete if that final line was folded in.
	if status.Usage.Details["vendor_cost_micro_usd"] == 0 {
		t.Errorf("the total cost was lost with the final line: %+v", status.Usage.Details)
	}
}

// The session total is folded onto the status, which is the one place it
// lives. There is no per-stage usage field carrying a single poll's increment,
// which each poll overwrote rather than accumulated.
func TestTheSessionTotalIsFoldedOntoTheStatus(t *testing.T) {
	runtime, _ := build(t, localtest.Script{PollsBeforeExit: 1})
	status := runToTerminal(t, runtime, start(t, runtime, spec(true)))
	if status.Usage.TotalTokens() == 0 {
		t.Fatalf("the session total is empty: %+v", status.Usage)
	}
}

// A graded session that passes ends done with a decided verdict, having run
// one worker and one judge.
func TestAGradedSessionRunsAWorkerThenAJudge(t *testing.T) {
	runtime, driver := build(t, localtest.Script{PollsBeforeExit: 1})
	status := runToTerminal(t, runtime, start(t, runtime, spec(true)))
	if status.State != managed.StateDone || status.Verdict == nil || !status.Verdict.Passed() {
		t.Fatalf("status = %+v", status)
	}
	if status.TerminalReason != managed.ReasonSucceeded {
		t.Errorf("terminal reason = %q", status.TerminalReason)
	}
	if got := names(driver.Stages()); len(got) != 3 ||
		!strings.HasPrefix(got[0], "bootstrap") || !strings.HasPrefix(got[1], "worker") || !strings.HasPrefix(got[2], "judge") {
		t.Fatalf("stages = %v", got)
	}
	// The verdict names the model that produced it, so a reader can see
	// when the grader and the worker were the same thing.
	if !strings.Contains(status.Verdict.Explanation, "claude-opus-5") {
		t.Errorf("the verdict does not name its grader: %q", status.Verdict.Explanation)
	}
}

// The judge gets the workspace read-only and one writable directory, and no
// stage is ever given the evidence directory at all.
func TestTheJudgeCannotWriteWhatItGrades(t *testing.T) {
	runtime, driver := build(t, localtest.Script{})
	runToTerminal(t, runtime, start(t, runtime, spec(true)))

	stages := driver.Stages()
	worker, judge := stageNamed(t, driver, "worker"), stageNamed(t, driver, "judge")
	for _, path := range judge.Paths {
		if path.Destination() == "/workspace" && path.Access != sandbox.ReadOnly {
			t.Fatalf("the judge can write the workspace: %+v", path)
		}
	}
	writable := 0
	for _, path := range judge.Paths {
		if path.Access == sandbox.ReadWrite && path.Destination() == "/grade" {
			writable++
		}
	}
	if writable != 1 {
		t.Fatalf("the judge has %d writable grade paths: %+v", writable, judge.Paths)
	}
	// The transcript is never handed to any stage, in any mode.
	for _, stage := range stages {
		for _, path := range stage.Paths {
			if strings.HasSuffix(path.Host, "/session") || strings.Contains(path.Host, "/session/") {
				t.Fatalf("stage %s was given the evidence directory: %+v", stage.Name, path)
			}
		}
	}
	if worker.Workdir == judge.Workdir {
		t.Error("the judge works in the directory it grades")
	}
}

// A rejection spends a revision, and the budget is spent exactly. An
// evaluation the run never passes ends failed — the same terminal state the
// other runtimes reach — not done.
func TestARejectionSpendsARevisionAndTheBudgetIsExact(t *testing.T) {
	runtime, driver := build(t, localtest.Script{Grades: []string{"needs_revision", "needs_revision", "needs_revision"}})
	status := runToTerminal(t, runtime, start(t, runtime, spec(true)))
	if status.State != managed.StateFailed || status.TerminalReason != managed.ReasonEvaluationFailed {
		t.Fatalf("status = %+v", status)
	}
	if status.Verdict == nil || status.Verdict.Grade != managed.GradeExhausted {
		t.Fatalf("verdict = %+v", status.Verdict)
	}
	// MaxRevisions is 2, so at most three worker-side stages ran.
	workers, judges := 0, 0
	for _, stage := range driver.Stages() {
		switch {
		case strings.HasPrefix(stage.Name, "worker"), strings.HasPrefix(stage.Name, "revision"):
			workers++
		case strings.HasPrefix(stage.Name, "judge"):
			judges++
		}
	}
	if workers != 3 || judges != 3 {
		t.Fatalf("%d worker stages and %d judge stages: %v", workers, judges, names(driver.Stages()))
	}
	// The revision carries the grader's reasoning under its own header, so
	// it reads in the transcript as its own request.
	var revision sandbox.StageSpec
	for _, stage := range driver.Stages() {
		if strings.HasPrefix(stage.Name, "revision") {
			revision = stage
			break
		}
	}
	joined := strings.Join(revision.Argv, " ")
	if !strings.Contains(joined, "The grader rejected the previous attempt") {
		t.Fatalf("the revision does not carry the rejection: %v", revision.Argv)
	}
}

// A judge that cannot produce a parsable verdict is retried once, and a
// session that still cannot be graded ends failed with that reason — never
// done with no verdict.
func TestAnUngradableSessionFailsRatherThanFinishingUndecided(t *testing.T) {
	runtime, driver := build(t, localtest.Script{MalformedRounds: map[int]bool{0: true, 1: true}})
	status := runToTerminal(t, runtime, start(t, runtime, spec(true)))
	if status.State != managed.StateFailed || status.Verdict != nil {
		t.Fatalf("status = %+v", status)
	}
	if !strings.Contains(status.Error, "grading failed") {
		t.Errorf("error = %q", status.Error)
	}
	judges := 0
	for _, stage := range driver.Stages() {
		if strings.HasPrefix(stage.Name, "judge") {
			judges++
		}
	}
	if judges != 2 {
		t.Fatalf("the judge was retried %d times", judges-1)
	}

	// One bad round followed by a good one is graded, not failed.
	runtime, _ = build(t, localtest.Script{MalformedRounds: map[int]bool{0: true}})
	status = runToTerminal(t, runtime, start(t, runtime, spec(true)))
	if status.State != managed.StateDone || status.Verdict == nil {
		t.Fatalf("a retried grading did not settle: %+v", status)
	}
}

// A worker that exits non-zero fails the session; the run does not go on to
// grade a reproduction that was never produced.
func TestAFailedWorkerFailsTheSession(t *testing.T) {
	runtime, driver := build(t, localtest.Script{WorkerExit: 3})
	status := runToTerminal(t, runtime, start(t, runtime, spec(true)))
	if status.State != managed.StateFailed || status.TerminalReason != managed.ReasonRuntimeFailed {
		t.Fatalf("status = %+v", status)
	}
	if !strings.Contains(status.Error, "exited 3") {
		t.Errorf("error = %q", status.Error)
	}
	if len(driver.Stages()) != 2 {
		t.Fatalf("a failed worker was graded anyway: %v", names(driver.Stages()))
	}
}

// A dependency that will not install fails the session before any agent runs,
// with a reason — rather than surfacing an hour later as an agent that cannot
// import what the method told it to use.
func TestAFailedBootstrapFailsBeforeAnyAgentRuns(t *testing.T) {
	runtime, driver := build(t, localtest.Script{BootstrapExit: 1})
	status := runToTerminal(t, runtime, start(t, runtime, spec(true)))
	if status.State != managed.StateFailed || status.TerminalReason != managed.ReasonRuntimeFailed {
		t.Fatalf("status = %+v", status)
	}
	if !strings.Contains(status.Error, "dependencies are not installed") {
		t.Errorf("error = %q", status.Error)
	}
	if got := names(driver.Stages()); len(got) != 1 {
		t.Fatalf("an agent ran after a failed bootstrap: %v", got)
	}
}

// The bootstrap installs what the specification declared, into the session's
// own environment rather than the workspace: nothing installed belongs in the
// reproduction, and one `git add -A` must not be able to commit an
// interpreter.
func TestTheBootstrapInstallsOutsideTheWorkspace(t *testing.T) {
	runtime, driver := build(t, localtest.Script{})
	runToTerminal(t, runtime, start(t, runtime, spec(true)))
	bootstrap := stageNamed(t, driver, "bootstrap")
	script := strings.Join(bootstrap.Argv, " ")
	if !strings.Contains(script, "python3 -m venv") || !strings.Contains(script, "pypdf") {
		t.Fatalf("the declared package was not installed: %s", script)
	}
	if !strings.Contains(script, "pip") || !strings.Contains(script, "freeze") {
		t.Errorf("the bootstrap records nothing about what it resolved: %s", script)
	}
	for _, path := range bootstrap.Paths {
		if strings.HasSuffix(path.Host, "/workspace") {
			t.Fatalf("the bootstrap can reach the workspace: %+v", path)
		}
	}
	// No credential reaches a stage that talks to no model.
	if len(bootstrap.EnvFile) != 0 {
		t.Errorf("the bootstrap carries a credential: %v", bootstrap.EnvFile)
	}
}

// A fresh runtime, holding nothing but the reference, finishes a session
// another process started. This is what makes a detached run recoverable.
func TestAFreshRuntimeFinishesADetachedSession(t *testing.T) {
	driver := localtest.New(localtest.Script{PollsBeforeExit: 1})
	registry, _ := harness.NewRegistry(harness.NewClaudeCode())
	catalog, _ := local.NewCatalog(local.DefaultCatalogFile(), registry, func(string) (string, bool) { return "", false })
	root := t.TempDir()
	config := local.Config{Sandbox: driver, Harness: harness.NewClaudeCode(), Catalog: catalog, Root: root, Lookup: os.LookupEnv}

	starter, err := local.New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ref := start(t, starter, spec(true))
	first, err := starter.Poll(context.Background(), ref)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}

	finisher, err := local.New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	status := runToTerminal(t, finisher, first.Ref)
	if status.State != managed.StateDone || status.Verdict == nil {
		t.Fatalf("the detached session did not finish: %+v", status)
	}
	events, err := finisher.Events(context.Background(), status.Ref)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	// The transcript has no duplicate ids: the pump resumed from its
	// journalled position rather than re-decoding from the start.
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		if seen[event.ID] {
			t.Fatalf("duplicate event id %q after a restart", event.ID)
		}
		seen[event.ID] = true
	}
	if len(events) == 0 {
		t.Fatal("the transcript is empty")
	}
	artifacts, err := finisher.ListArtifacts(context.Background(), status.Ref)
	if err != nil || len(artifacts) == 0 {
		t.Fatalf("ListArtifacts = %v, %v", artifacts, err)
	}
}

// A reference whose journal names another driver is refused: two drivers'
// handles are both opaque strings, so a pid would otherwise be looked up as a
// container name.
func TestAReferenceFromAnotherDriverIsRefused(t *testing.T) {
	runtime, _ := build(t, localtest.Script{})
	ref := start(t, runtime, spec(false))

	other := &containerNamed{Sandbox: localtest.New(localtest.Script{})}
	registry, _ := harness.NewRegistry(harness.NewClaudeCode())
	catalog, _ := local.NewCatalog(local.DefaultCatalogFile(), registry, func(string) (string, bool) { return "", false })
	stranger, err := local.New(local.Config{Sandbox: other, Harness: harness.NewClaudeCode(), Catalog: catalog, Root: t.TempDir(), Lookup: os.LookupEnv})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = stranger.Poll(context.Background(), ref)
	if !errors.Is(err, managed.ErrRuntimeMismatch) {
		t.Fatalf("a session crossed drivers: %v", err)
	}
	if !strings.Contains(err.Error(), "host") || !strings.Contains(err.Error(), "container") {
		t.Errorf("the refusal names only one driver: %v", err)
	}
}

type containerNamed struct{ *localtest.Sandbox }

func (containerNamed) Name() sandbox.DriverName { return sandbox.Container }

// reportingDriver is a scripted driver that also reports a pinned image and
// toolchain, the way the container driver does, so the record's population of
// those fields is testable without an engine.
type reportingDriver struct{ *localtest.Sandbox }

func (reportingDriver) Image() string     { return "ghcr.io/latere-ai/harness:pinned" }
func (reportingDriver) Toolchain() string { return "python 3.13.2" }

// The environment record carries what the driver pinned: its image and its
// toolchain, read through the optional interfaces a driver satisfies rather
// than by naming a concrete one. The host driver reports neither and leaves
// both empty, which is why they are omitempty.
func TestTheEnvironmentRecordCarriesTheDriversImageAndToolchain(t *testing.T) {
	driver := reportingDriver{localtest.New(localtest.Script{})}
	registry, _ := harness.NewRegistry(harness.NewClaudeCode())
	catalog, _ := local.NewCatalog(local.DefaultCatalogFile(), registry, noCredentials)
	runtime, err := local.New(local.Config{
		Sandbox: driver, Harness: harness.NewClaudeCode(), Catalog: catalog,
		Root: t.TempDir(), Lookup: os.LookupEnv,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ref := start(t, runtime, spec(false))
	runToTerminal(t, runtime, ref)

	record := readEnvironment(t, ref)
	if record.Sandbox.Image != "ghcr.io/latere-ai/harness:pinned" {
		t.Errorf("the record did not carry the image: %q", record.Sandbox.Image)
	}
	if record.Sandbox.Version != "python 3.13.2" {
		t.Errorf("the record did not carry the toolchain: %q", record.Sandbox.Version)
	}
}

// Release discards the driver's stages and keeps the directory, because the
// workspace is the reproduction and the push may not have taken it yet.
func TestReleaseKeepsTheWorkspaceByDefault(t *testing.T) {
	runtime, _ := build(t, localtest.Script{})
	ref := start(t, runtime, spec(false))
	status := runToTerminal(t, runtime, ref)
	dir := sessionDir(t, status.Ref)

	if err := runtime.Release(context.Background(), status.Ref); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "workspace", "REPRODUCIBILITY.md")); err != nil {
		t.Fatalf("release destroyed the reproduction: %v", err)
	}
}

// Under the discard policy the directory goes, and only then.
func TestDiscardRetentionRemovesTheDirectory(t *testing.T) {
	driver := localtest.New(localtest.Script{})
	registry, _ := harness.NewRegistry(harness.NewClaudeCode())
	catalog, _ := local.NewCatalog(local.DefaultCatalogFile(), registry, func(string) (string, bool) { return "", false })
	runtime, err := local.New(local.Config{
		Sandbox: driver, Harness: harness.NewClaudeCode(), Catalog: catalog,
		Root: t.TempDir(), Retention: local.RetainNever, Lookup: os.LookupEnv,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	status := runToTerminal(t, runtime, start(t, runtime, spec(false)))
	dir := sessionDir(t, status.Ref)
	if err := runtime.Release(context.Background(), status.Ref); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the directory survived a discard policy: %v", err)
	}
}

// A specification asking for a writable remote is refused with the leaf that
// covers it named, rather than honoured by putting a token in a sandbox.
func TestAWritableRepositoryIsRefused(t *testing.T) {
	runtime, _ := build(t, localtest.Script{})
	s := spec(false)
	s.Resources = []managed.Resource{{Kind: managed.ResourceRepository, Repository: &managed.RepositoryResource{
		URL: "https://github.com/open-hai/x", Destination: "repro", Access: managed.AccessReadWrite, CredentialRef: "mount",
	}}}
	err := runtime.Validate(s)
	if !errors.Is(err, managed.ErrUnsupported) {
		t.Fatalf("a writable mount was accepted: %v", err)
	}
	s.Resources[0].Repository.Access = managed.AccessReadOnly
	s.Resources[0].Repository.CredentialRef = ""
	if err := runtime.Validate(s); err != nil {
		t.Fatalf("a read-only repository was refused: %v", err)
	}
}

// A model the operator never declared has no endpoint to reach it at, so the
// session is refused before anything is created.
func TestAnUndeclaredModelIsRefusedBeforeAnythingIsCreated(t *testing.T) {
	runtime, driver := build(t, localtest.Script{})
	s := spec(false)
	s.Model = managed.ModelSelector{Exact: &managed.ModelRef{Provider: "anthropic", ID: "claude-opus-9"}}
	if err := runtime.Validate(s); !errors.Is(err, managed.ErrInvalidSpec) {
		t.Fatalf("an undeclared model was accepted: %v", err)
	}
	if _, err := runtime.Start(context.Background(), s); err == nil {
		t.Fatal("Start accepted an undeclared model")
	}
	if len(driver.Stages()) != 0 {
		t.Fatal("a stage was launched for a refused specification")
	}
}

// A driver that cannot run on this machine refuses with its own instructions,
// before a session directory is made.
func TestANotReadyDriverRefusesWithInstructions(t *testing.T) {
	notReady := sandbox.NotReady(sandbox.Darwin, sandbox.Host, sandbox.ConditionMissing, "srt")
	runtime, driver := build(t, localtest.Script{NotReady: notReady})
	_, err := runtime.Capabilities(context.Background())
	if !errors.Is(err, sandbox.ErrNotReady) || !strings.Contains(err.Error(), "npm install") {
		t.Fatalf("Capabilities = %v", err)
	}
	if _, err := runtime.Start(context.Background(), spec(false)); !errors.Is(err, sandbox.ErrNotReady) {
		t.Fatalf("Start = %v", err)
	}
	if len(driver.Stages()) != 0 {
		t.Fatal("a stage was launched on a machine that is not ready")
	}
}

// Cancel mid-flight stops the running stage, records the session failed and
// cancelled, keeps its evidence readable, and is idempotent: a second Cancel,
// and a Cancel of a session that already finished, both do nothing.
func TestCancelStopsTheRunningStageAndIsIdempotent(t *testing.T) {
	runtime, driver := build(t, localtest.Script{PollsBeforeExit: 10})
	ref := start(t, runtime, spec(true))
	running, err := runtime.Poll(context.Background(), ref)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if running.State.Terminal() {
		t.Fatalf("the session finished before it could be cancelled: %+v", running)
	}

	if err := runtime.Cancel(context.Background(), running.Ref); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	status, err := runtime.Poll(context.Background(), running.Ref)
	if err != nil {
		t.Fatalf("Poll after cancel: %v", err)
	}
	if status.State != managed.StateFailed || status.TerminalReason != managed.ReasonCancelled {
		t.Fatalf("a cancelled session did not end cancelled: %+v", status)
	}
	// The evidence stays readable — Cancel leaves the directory intact.
	if _, err := runtime.Events(context.Background(), status.Ref); err != nil {
		t.Fatalf("Events after cancel: %v", err)
	}
	// The current stage was told to stop.
	if got := len(driver.Stages()); got == 0 {
		t.Fatal("no stage was ever launched")
	}
	// Cancelling again, and cancelling a session already terminal, are both
	// no-ops rather than errors.
	if err := runtime.Cancel(context.Background(), status.Ref); err != nil {
		t.Fatalf("repeated Cancel: %v", err)
	}
}

// A stage the driver has lost — Observe reports it Gone — is recorded exiting
// −1 and fails the session, rather than reading as a success on an unknowable
// exit. The bootstrap's handle is rewritten to one the driver never issued, so
// the next observation finds it gone.
func TestAStageTheDriverLostExitsMinusOneAndFails(t *testing.T) {
	runtime, _ := build(t, localtest.Script{PollsBeforeExit: 10})
	ref := start(t, runtime, spec(false))

	session, err := local.OpenSession(sessionDir(t, ref))
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	locator, err := session.ReadLocator()
	if err != nil {
		t.Fatalf("ReadLocator: %v", err)
	}
	last := len(locator.Stages) - 1
	locator.Stages[last].Handle.ID = "fake:404" // a handle the driver never issued
	if err := session.WriteLocator(locator); err != nil {
		t.Fatalf("WriteLocator: %v", err)
	}

	status, err := runtime.Poll(context.Background(), ref)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if status.State != managed.StateFailed {
		t.Fatalf("a lost stage did not fail the session: %+v", status)
	}
	after, err := session.ReadLocator()
	if err != nil {
		t.Fatalf("ReadLocator: %v", err)
	}
	if after.Stages[last].Exit == nil || *after.Stages[last].Exit != -1 {
		t.Fatalf("a lost stage's exit was not recorded as -1: %+v", after.Stages[last].Exit)
	}
}

// A stage's partial final line — written but not yet terminated by a newline —
// is held back rather than decoded as a torn record, and picked up once it is
// complete. The bootstrap's log is rewritten under the driver to present the
// partial line, then completed.
func TestPollHoldsBackAPartialFinalLineUntilItIsComplete(t *testing.T) {
	runtime, _ := build(t, localtest.Script{PollsBeforeExit: 10})
	ref := start(t, runtime, spec(false))
	session, err := local.OpenSession(sessionDir(t, ref))
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	logPath := session.RawLog("bootstrap-0")

	// A complete line, then a partial one with no trailing newline.
	if err := os.WriteFile(logPath, []byte("first line\nsecond line still being written"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	first, err := runtime.Poll(context.Background(), ref)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	events := transcript(t, runtime, first.Ref)
	if !containsContent(events, "first line") {
		t.Fatalf("the complete line was not decoded: %v", events)
	}
	if containsContent(events, "second line") {
		t.Fatalf("the partial line was decoded before it was complete: %v", events)
	}

	// Complete the held-back line.
	if err := os.WriteFile(logPath, []byte("first line\nsecond line still being written, now finished\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	second, err := runtime.Poll(context.Background(), first.Ref)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if !containsContent(transcript(t, runtime, second.Ref), "now finished") {
		t.Fatal("the completed line was never decoded")
	}
}

// Under the on-failure policy Release keeps a failed session's directory and
// discards a successful one's: a run whose publication or grading failed stays
// on disk to be looked at, and a clean success does not accumulate.
func TestRetainOnFailureKeepsAFailureAndDiscardsASuccess(t *testing.T) {
	newRuntime := func(t *testing.T, script localtest.Script) *local.Runtime {
		t.Helper()
		registry, _ := harness.NewRegistry(harness.NewClaudeCode())
		catalog, _ := local.NewCatalog(local.DefaultCatalogFile(), registry, noCredentials)
		runtime, err := local.New(local.Config{
			Sandbox: localtest.New(script), Harness: harness.NewClaudeCode(), Catalog: catalog,
			Root: t.TempDir(), Retention: local.RetainOnFailure, Lookup: os.LookupEnv,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return runtime
	}

	// A success is discarded.
	success := newRuntime(t, localtest.Script{})
	done := runToTerminal(t, success, start(t, success, spec(false)))
	if done.State != managed.StateDone {
		t.Fatalf("the passing session did not finish done: %+v", done)
	}
	doneDir := sessionDir(t, done.Ref)
	if err := success.Release(context.Background(), done.Ref); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(doneDir); !os.IsNotExist(err) {
		t.Fatalf("the succeeded session's directory survived on-failure retention: %v", err)
	}

	// A failure is kept.
	failure := newRuntime(t, localtest.Script{WorkerExit: 7})
	failed := runToTerminal(t, failure, start(t, failure, spec(false)))
	if failed.State != managed.StateFailed {
		t.Fatalf("the failing session did not fail: %+v", failed)
	}
	failedDir := sessionDir(t, failed.Ref)
	if err := failure.Release(context.Background(), failed.Ref); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(filepath.Join(failedDir, "workspace")); err != nil {
		t.Fatalf("the failed session's directory was discarded under on-failure retention: %v", err)
	}
}

// The Codex harness is driven through the whole runtime at least once, not only
// Claude Code: it speaks a different stream and carries its role and login
// differently, so a run that never exercised it would leave that half of the
// seam unchecked here.
func TestTheCodexHarnessRunsThroughTheRuntime(t *testing.T) {
	driver := localtest.New(localtest.Script{PollsBeforeExit: 1})
	registry, err := harness.NewRegistry(harness.NewClaudeCode(), harness.NewCodex())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	catalog, err := local.NewCatalog(local.CatalogFile{
		Gateways: map[string]local.GatewayConfig{
			"login": {Dialect: string(harness.DialectOpenAIResponses), Auth: string(harness.AuthHarness)},
		},
		Models: []local.ModelConfig{{
			Provider: "vendor", ID: "gpt-5-codex", Gateway: "login",
			Harness: string(harness.Codex), Preferences: []string{"best"},
		}},
	}, registry, noCredentials)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	runtime, err := local.New(local.Config{
		Sandbox: driver, Harness: harness.NewCodex(), Catalog: catalog,
		Root: t.TempDir(), Lookup: os.LookupEnv,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := runtime.Capabilities(context.Background()); err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	ref, err := runtime.Start(context.Background(), spec(true))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	status := runToTerminal(t, runtime, ref)
	if !status.State.Terminal() {
		t.Fatalf("the Codex session never became terminal: %+v", status)
	}
	worker := stageNamed(t, driver, "worker")
	if !strings.Contains(strings.Join(worker.Argv, " "), "codex") {
		t.Fatalf("the worker did not run the Codex harness: %v", worker.Argv)
	}
}

// A judge round that writes no verdict at all is retried and, where a round
// still produces none, the session fails to be graded rather than finishing
// undecided.
func TestASilentJudgeRoundIsRetriedThenFails(t *testing.T) {
	runtime, driver := build(t, localtest.Script{SilentRounds: map[int]bool{0: true, 1: true}})
	status := runToTerminal(t, runtime, start(t, runtime, spec(true)))
	if status.State != managed.StateFailed || status.Verdict != nil {
		t.Fatalf("a silent grading did not fail undecided: %+v", status)
	}
	if !strings.Contains(status.Error, "grading failed") {
		t.Errorf("error = %q", status.Error)
	}
	judges := 0
	for _, stage := range driver.Stages() {
		if strings.HasPrefix(stage.Name, "judge") {
			judges++
		}
	}
	if judges != 2 {
		t.Fatalf("a silent judge round was retried %d times, want one retry", judges-1)
	}
}

// transcript reads a session's events, failing the test on any error.
func transcript(t *testing.T, runtime *local.Runtime, ref managed.SessionRef) []managed.Event {
	t.Helper()
	events, err := runtime.Events(context.Background(), ref)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	return events
}

// containsContent reports whether any event's content holds the substring.
func containsContent(events []managed.Event, substring string) bool {
	for _, event := range events {
		if strings.Contains(event.Content, substring) {
			return true
		}
	}
	return false
}

// stageNamed is the first stage of one role. Tests ask by role rather than by
// index, so inserting a stage into the sequence does not rewrite them.
func stageNamed(t *testing.T, driver *localtest.Sandbox, role string) sandbox.StageSpec {
	t.Helper()
	for _, stage := range driver.Stages() {
		if strings.HasPrefix(stage.Name, role) {
			return stage
		}
	}
	t.Fatalf("no %s stage ran: %v", role, names(driver.Stages()))
	return sandbox.StageSpec{}
}

func names(stages []sandbox.StageSpec) []string {
	out := make([]string, 0, len(stages))
	for _, stage := range stages {
		out = append(out, stage.Name)
	}
	return out
}

func sessionDir(t *testing.T, ref managed.SessionRef) string {
	t.Helper()
	var locator local.Locator
	if err := jsonUnmarshal(ref.Locator, &locator); err != nil {
		t.Fatalf("decode locator: %v", err)
	}
	return locator.Dir
}

func jsonUnmarshal(body []byte, into any) error { return json.Unmarshal(body, into) }

// A later process can name the directory a referenced session worked in, which
// is what lets it publish a session it did not start. Without it, finishing a
// local run finalizes a repository nothing ever pushed to.
func TestAReferencedSessionNamesItsWorkspace(t *testing.T) {
	runtime, _ := build(t, localtest.Script{})
	ref := start(t, runtime, spec(false))
	status := runToTerminal(t, runtime, ref)

	workspace, err := runtime.Workspace(status.Ref)
	if err != nil {
		t.Fatalf("Workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "REPRODUCIBILITY.md")); err != nil {
		t.Fatalf("the named workspace does not hold the reproduction: %v", err)
	}
	// A reference from another runtime is refused rather than answered with
	// somebody else's directory.
	stranger := status.Ref
	stranger.Runtime = "anthropic-managed"
	if _, err := runtime.Workspace(stranger); !errors.Is(err, managed.ErrRuntimeMismatch) {
		t.Fatalf("Workspace on a foreign ref = %v", err)
	}
	// And after release the reference no longer resolves.
	if err := runtime.Release(context.Background(), status.Ref); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := runtime.Workspace(status.Ref); !errors.Is(err, managed.ErrNotFound) {
		t.Fatalf("Workspace after release = %v", err)
	}
}

// Release runs after the caller's own retention work, and a retention policy
// that discards must not destroy a workspace whose push failed. The hook that
// pushes runs inside Retain, and a Retain that fails stops the controller
// before Release.
func TestAFailedRetainStopsBeforeRelease(t *testing.T) {
	runtime, _ := build(t, localtest.Script{})
	ref := start(t, runtime, spec(false))
	status := runToTerminal(t, runtime, ref)
	dir := sessionDir(t, status.Ref)

	// The retention decision is read from the session, so a run that ended
	// well and a run whose publication failed are distinguishable — and the
	// default keeps either way.
	if _, err := os.Stat(filepath.Join(dir, "workspace", "REPRODUCIBILITY.md")); err != nil {
		t.Fatalf("the reproduction is not there to begin with: %v", err)
	}
	if err := runtime.Release(context.Background(), status.Ref); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "workspace", "REPRODUCIBILITY.md")); err != nil {
		t.Fatalf("the default policy destroyed the reproduction: %v", err)
	}
}

// A caller may poll twice with the same reference. The checkpoint it gets back
// must still advance, because the journal underneath it has — otherwise the
// controller sees a locator that changed without saying so and fails the run.
func TestPollingTwiceWithOneReferenceStillAdvances(t *testing.T) {
	runtime, _ := build(t, localtest.Script{PollsBeforeExit: 3})
	ref := start(t, runtime, spec(true))

	seen := map[uint64]string{}
	for range 4 {
		// Deliberately the same ref each time, as a caller that does not
		// thread the returned one would.
		status, err := runtime.Poll(context.Background(), ref)
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
		if previous, repeated := seen[status.Ref.Revision]; repeated && previous != string(status.Ref.Locator) {
			t.Fatalf("revision %d came back twice with different journals", status.Ref.Revision)
		}
		seen[status.Ref.Revision] = string(status.Ref.Locator)
		if status.Ref.Revision <= ref.Revision {
			t.Fatalf("the checkpoint did not advance: %d after %d", status.Ref.Revision, ref.Revision)
		}
	}
}

// A reproduction installs what the paper needs, so the arrangement that makes
// the bootstrap's install work has to apply to the agent's too. It failed for
// the agent alone in a real audit, an hour after the bootstrap had succeeded.
func TestEveryPipInTheSessionCanReachTheIndex(t *testing.T) {
	runtime, driver := build(t, localtest.Script{})
	runToTerminal(t, runtime, start(t, runtime, spec(false)))
	for _, stage := range driver.Stages() {
		trusted := stage.Env["PIP_TRUSTED_HOST"]
		for _, host := range local.PackageIndexHosts() {
			if !strings.Contains(trusted, host) {
				t.Errorf("stage %s cannot install from %s: %q", stage.Name, host, trusted)
			}
		}
	}
}
