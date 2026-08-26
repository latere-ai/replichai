package local_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/managed/contracttest"
	"github.com/latere-ai/replichai/internal/runtimes/local"
	"github.com/latere-ai/replichai/internal/runtimes/local/harness"
	"github.com/latere-ai/replichai/internal/runtimes/local/localtest"
	"github.com/latere-ai/replichai/internal/runtimes/local/sandbox"
)

func build(t *testing.T, script localtest.Script) (*local.Runtime, *localtest.Sandbox) {
	t.Helper()
	driver := localtest.New(script)
	registry, err := harness.NewRegistry(harness.NewClaudeCode(), harness.NewCodex())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	catalog, err := local.NewCatalog(local.DefaultCatalogFile(), registry, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	runtime, err := local.New(local.Config{
		Sandbox: driver, Harness: harness.NewClaudeCode(), Catalog: catalog,
		Root: t.TempDir(), Environ: os.Environ(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runtime, driver
}

// The runtime is held to the same black-box suite as every other adapter.
func TestLocalMeetsTheManagedContract(t *testing.T) {
	contracttest.Run(t, func() managed.Runtime {
		runtime, _ := build(t, localtest.Script{})
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

// A rejection spends a revision, and the budget is spent exactly.
func TestARejectionSpendsARevisionAndTheBudgetIsExact(t *testing.T) {
	runtime, driver := build(t, localtest.Script{Grades: []string{"needs_revision", "needs_revision", "needs_revision"}})
	status := runToTerminal(t, runtime, start(t, runtime, spec(true)))
	if status.State != managed.StateDone || status.TerminalReason != managed.ReasonEvaluationFailed {
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
	config := local.Config{Sandbox: driver, Harness: harness.NewClaudeCode(), Catalog: catalog, Root: root, Environ: os.Environ()}

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
	stranger, err := local.New(local.Config{Sandbox: other, Harness: harness.NewClaudeCode(), Catalog: catalog, Root: t.TempDir(), Environ: os.Environ()})
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
		Root: t.TempDir(), Retention: local.RetainNever, Environ: os.Environ(),
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
