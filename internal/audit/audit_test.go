package audit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/provider/memory"
)

var testMethod = method.Method{
	Instructions: "Audit papers. Never fabricate. Write to {{OUTPUT_DIR}}.",
	Rubric:       "1. Verdict\n2. Boundary",
	TaskTemplate: "Audit {{PAPER}}.",
}

// newTestAuditor builds an auditor that never actually waits.
func newTestAuditor(t *testing.T, p provider.Provider, opts ...Option) *Auditor {
	t.Helper()
	base := make([]Option, 0, 1+len(opts))
	base = append(base, withSleep(func(context.Context, time.Duration) error { return nil }))
	return New(p, testMethod, append(base, opts...)...)
}

func TestStartPassesTheMethodToTheProvider(t *testing.T) {
	p := memory.New()
	if _, err := newTestAuditor(t, p).Start(context.Background(), "https://arxiv.org/abs/2603.01795"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	runs := p.Runs()
	if len(runs) != 1 {
		t.Fatalf("want 1 run started, got %d", len(runs))
	}
	if runs[0].Rubric != testMethod.Rubric {
		t.Errorf("rubric not passed through: %q", runs[0].Rubric)
	}
	if !strings.Contains(runs[0].Task, "2603.01795") {
		t.Errorf("paper not substituted into task: %q", runs[0].Task)
	}
	if runs[0].MaxRevisions != DefaultMaxRevisions {
		t.Errorf("MaxRevisions = %d, want %d", runs[0].MaxRevisions, DefaultMaxRevisions)
	}
}

func TestStartResolvesOutputDirFromTheProvider(t *testing.T) {
	// The method must never hard-code one backend's filesystem layout.
	p := memory.New()
	if _, err := newTestAuditor(t, p).Start(context.Background(), "paper"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	got := p.Workers()[0].Instructions
	if strings.Contains(got, method.OutputDirPlaceholder) {
		t.Error("placeholder was left unsubstituted in the instructions")
	}
	if !strings.Contains(got, p.OutputDir()) {
		t.Errorf("provider output dir %q missing from instructions", p.OutputDir())
	}
}

func TestStartMountsARepository(t *testing.T) {
	p := memory.New()
	mount := provider.RepoMount{URL: "https://github.com/open-hai/x-a3f9", Token: "mount-token"}
	if _, err := newTestAuditor(t, p, WithMount(mount)).Start(context.Background(), "paper"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The run carries the mount, with the default path filled in.
	run := p.Runs()[0]
	if !run.Mount.Set() {
		t.Fatal("the run should carry the mount")
	}
	if run.Mount.MountPath != DefaultMountPath {
		t.Errorf("mount path = %q, want default %q", run.Mount.MountPath, DefaultMountPath)
	}

	// The worker is told the bounded-workspace protocol, rooted at the mount and
	// not the backend output directory.
	instr := p.Workers()[0].Instructions
	for _, want := range []string{DefaultMountPath, "git push", "Never"} {
		if !strings.Contains(instr, want) {
			t.Errorf("instructions missing %q:\n%s", want, instr)
		}
	}
	if strings.Contains(instr, p.OutputDir()) {
		t.Error("with a mount, output must route to the mount, not the backend directory")
	}
}

func TestStartRejectsAnEmptyPaperReference(t *testing.T) {
	if _, err := newTestAuditor(t, memory.New()).Start(context.Background(), "  "); !errors.Is(err, method.ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestWaitPollsUntilTerminal(t *testing.T) {
	p := memory.New()
	p.PollsBeforeDone = 3
	var seen []provider.RunState
	a := newTestAuditor(t, p, WithObserver(func(s provider.RunStatus) {
		seen = append(seen, s.State)
	}))

	h, err := a.Start(context.Background(), "paper")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := a.Wait(context.Background(), h); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	want := []provider.RunState{provider.StateRunning, provider.StateRunning, provider.StateDone}
	if len(seen) != len(want) {
		t.Fatalf("observed %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("observed %v, want %v", seen, want)
		}
	}
}

func TestWaitTimesOut(t *testing.T) {
	p := memory.New()
	p.PollsBeforeDone = 99
	a := newTestAuditor(t, p, WithTimeout(-time.Second))

	h, err := a.Start(context.Background(), "paper")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := a.Wait(context.Background(), h); !errors.Is(err, ErrTimedOut) {
		t.Fatalf("want ErrTimedOut, got %v", err)
	}
}

// flakyProvider fails its first n Poll calls, then defers to the real one. It
// stands in for a backend that momentarily will not answer while the session
// behind it keeps working.
type flakyProvider struct {
	*memory.Provider
	failures int
	seen     int
}

func (f *flakyProvider) Poll(ctx context.Context, h provider.RunHandle) (provider.RunStatus, error) {
	f.seen++
	if f.seen <= f.failures {
		return provider.RunStatus{}, errors.New("backend unavailable")
	}
	return f.Provider.Poll(ctx, h)
}

func TestWaitWithoutTimeoutOutlivesTheDeadline(t *testing.T) {
	// The backend session is the source of truth: a run that takes longer than
	// any wall clock still finishes when the session does.
	p := memory.New()
	p.PollsBeforeDone = 50
	a := newTestAuditor(t, p, WithTimeout(-time.Second), WithoutTimeout())

	h, err := a.Start(context.Background(), "paper")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	status, err := a.Wait(context.Background(), h)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if status.State != provider.StateDone {
		t.Errorf("State = %s, want done", status.State)
	}
}

func TestWaitWithoutTimeoutStillFailsOnAFailedSession(t *testing.T) {
	// Removing the clock removes only the clock: a session the backend reports
	// as failed still ends the wait, with that state reported.
	p := memory.New()
	p.Grade = provider.GradeFailed
	a := newTestAuditor(t, p, WithoutTimeout())

	h, err := a.Start(context.Background(), "paper")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	status, err := a.Wait(context.Background(), h)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if status.State != provider.StateFailed {
		t.Errorf("State = %s, want failed", status.State)
	}
}

func TestWaitToleratesTransientPollFailures(t *testing.T) {
	p := memory.New()
	p.PollsBeforeDone = 1
	f := &flakyProvider{Provider: p, failures: DefaultPollFailures - 1}
	a := newTestAuditor(t, f, WithoutTimeout())

	h, err := a.Start(context.Background(), "paper")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	status, err := a.Wait(context.Background(), h)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if status.State != provider.StateDone {
		t.Errorf("State = %s, want done", status.State)
	}
}

func TestWaitGivesUpOnPersistentPollFailures(t *testing.T) {
	p := memory.New()
	f := &flakyProvider{Provider: p, failures: 99}
	a := newTestAuditor(t, f, WithoutTimeout(), WithPollFailures(3))

	h, err := a.Start(context.Background(), "paper")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := a.Wait(context.Background(), h); err == nil {
		t.Fatal("a backend that never answers must end the wait")
	}
	if f.seen != 3 {
		t.Errorf("polled %d times, want 3", f.seen)
	}
}

func TestWaitHonorsContextCancellation(t *testing.T) {
	p := memory.New()
	p.PollsBeforeDone = 99
	// Use the real sleep so cancellation, not the stub, ends the wait.
	a := New(p, testMethod, WithPollInterval(time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	h, err := a.Start(ctx, "paper")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan error, 1)
	go func() { _, err := a.Wait(ctx, h); done <- err }()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait ignored cancellation")
	}
}

func TestCollectWritesArtifacts(t *testing.T) {
	p := memory.New()
	p.Files = map[string][]byte{
		"README.md":   []byte("# repo"),
		"src/core.py": []byte("print(1)"),
	}
	a := newTestAuditor(t, p)
	dst := t.TempDir()

	h, err := a.Start(context.Background(), "paper")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	got, err := a.Collect(context.Background(), h, dst)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("collected %d artifacts, want 2", len(got))
	}
	for path, want := range p.Files {
		body, err := os.ReadFile(filepath.Join(dst, path))
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if string(body) != string(want) {
			t.Errorf("%s = %q, want %q", path, body, want)
		}
	}
}

func TestCollectSkipsCompiledResidue(t *testing.T) {
	p := memory.New()
	p.Files = map[string][]byte{
		"core.py":                          []byte("source"),
		"__pycache__/core.cpython-311.pyc": []byte("compiled"),
		"core.pyo":                         []byte("compiled"),
	}
	a := newTestAuditor(t, p)
	dst := t.TempDir()

	h, _ := a.Start(context.Background(), "paper")
	got, err := a.Collect(context.Background(), h, dst)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 || got[0].Path != "core.py" {
		t.Fatalf("collected %v, want only core.py", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "__pycache__")); !os.IsNotExist(err) {
		t.Error("__pycache__ was written to the destination")
	}
}

func TestCollectRefusesPathsThatEscapeTheDestination(t *testing.T) {
	// Artifact paths come from a sandbox that just executed code found on the
	// internet, so they are untrusted input.
	for _, hostile := range []string{
		"../escaped.md",
		"nested/../../escaped.md",
		"/etc/passwd",
		"..",
	} {
		t.Run(hostile, func(t *testing.T) {
			p := memory.New()
			p.Files = map[string][]byte{hostile: []byte("payload")}
			a := newTestAuditor(t, p)
			root := t.TempDir()
			dst := filepath.Join(root, "out")

			h, _ := a.Start(context.Background(), "paper")
			if _, err := a.Collect(context.Background(), h, dst); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("want ErrUnsafePath, got %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "escaped.md")); !os.IsNotExist(err) {
				t.Error("a file was written outside the destination")
			}
		})
	}
}

func TestRunEndToEnd(t *testing.T) {
	p := memory.New()
	p.PollsBeforeDone = 2
	p.Files = map[string][]byte{"REPRODUCIBILITY.md": []byte("40%")}
	a := newTestAuditor(t, p)
	dst := t.TempDir()

	result, err := a.Run(context.Background(), "https://arxiv.org/abs/2603.01795", dst)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Passed() {
		t.Error("run should have passed")
	}
	if result.Grade() != provider.GradePassed {
		t.Errorf("grade = %s, want passed", result.Grade())
	}
	if len(result.Artifacts) != 1 {
		t.Errorf("collected %d artifacts, want 1", len(result.Artifacts))
	}
	if closed := p.Closed(); len(closed) != 1 || closed[0] != result.Handle.ID {
		t.Errorf("run was not released: %v", closed)
	}
}

type closeFailProvider struct {
	*memory.Provider
	err error
}

func (p *closeFailProvider) CloseRun(context.Context, provider.RunHandle) error { return p.err }

func TestRunReturnsCleanupErrors(t *testing.T) {
	closeErr := errors.New("close failed")
	p := &closeFailProvider{Provider: memory.New(), err: closeErr}
	p.Files = map[string][]byte{"REPRODUCIBILITY.md": []byte("40%")}

	if _, err := newTestAuditor(t, p).Run(context.Background(), "paper", t.TempDir()); !errors.Is(err, closeErr) {
		t.Fatalf("Run error = %v, want cleanup error", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newTestAuditor(t, p).Resume(cancelled, provider.RunHandle{ID: "run-existing"}, "paper", t.TempDir()); !errors.Is(err, closeErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want joined run and cleanup errors", err)
	}
}

// A well-formed instrument.json the memory provider can hand back as an
// artifact, so Run's finalize-time check has something to validate.
const validInstrumentJSON = `{
  "protocol": {
    "design": "2x2 within",
    "trials": "120 trials",
    "measures": [{"name": "rt", "scale": "ms", "level": "per_trial"}],
    "plan": ["LMM"],
    "reported": [{"metric": "RT", "value": "p=.003", "citation": "Table 2"}]
  },
  "analysis": {
    "command": "python src/analyze.py {{INPUT}}",
    "input": {"columns": [{"name": "rt", "type": "number", "required": true}]},
    "outputs": ["results.csv"]
  },
  "servability": {
    "components": [{"component": "task", "servable": true}],
    "all_servable": true
  }
}`

func TestRunValidatesTheInstrument(t *testing.T) {
	t.Run("valid instrument", func(t *testing.T) {
		p := memory.New()
		p.Files = map[string][]byte{InstrumentFile: []byte(validInstrumentJSON)}
		result, err := newTestAuditor(t, p).Run(context.Background(), "paper", t.TempDir())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !result.InstrumentPresent {
			t.Error("instrument should be present")
		}
		if !result.InstrumentOK() {
			t.Errorf("instrument should validate, got %v", result.InstrumentViolations)
		}
	})

	t.Run("invalid instrument is reported, not fatal", func(t *testing.T) {
		p := memory.New()
		// A null protocol without a reason — a schema violation.
		p.Files = map[string][]byte{InstrumentFile: []byte(
			`{"protocol": null, "analysis": null, "servability": {"components": [], "all_servable": true}}`)}
		result, err := newTestAuditor(t, p).Run(context.Background(), "paper", t.TempDir())
		if err != nil {
			t.Fatalf("Run should not fail over a bad instrument: %v", err)
		}
		if result.InstrumentOK() {
			t.Error("an unstated null should not validate")
		}
		if len(result.InstrumentViolations) < 2 {
			t.Errorf("both null fields should be flagged, got %v", result.InstrumentViolations)
		}
	})

	t.Run("non-JSON is one violation, not fatal", func(t *testing.T) {
		p := memory.New()
		p.Files = map[string][]byte{InstrumentFile: []byte("not json")}
		result, err := newTestAuditor(t, p).Run(context.Background(), "paper", t.TempDir())
		if err != nil {
			t.Fatalf("Run should not fail over non-JSON: %v", err)
		}
		if result.InstrumentOK() || len(result.InstrumentViolations) != 1 {
			t.Errorf("non-JSON should be one violation, got %v", result.InstrumentViolations)
		}
	})

	t.Run("absent instrument", func(t *testing.T) {
		p := memory.New()
		p.Files = map[string][]byte{"REPRODUCIBILITY.md": []byte("verdict")}
		result, err := newTestAuditor(t, p).Run(context.Background(), "paper", t.TempDir())
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if result.InstrumentPresent {
			t.Error("no instrument was written; present should be false")
		}
		if result.InstrumentOK() {
			t.Error("an absent instrument is not OK")
		}
	})
}

func TestRunReleasesTheRunEvenWhenItFails(t *testing.T) {
	p := memory.New()
	p.PollsBeforeDone = 99
	a := newTestAuditor(t, p, WithTimeout(-time.Second))

	if _, err := a.Run(context.Background(), "paper", t.TempDir()); err == nil {
		t.Fatal("expected an error")
	}
	if len(p.Closed()) == 0 {
		t.Error("a failed run must still be released")
	}
}

func TestRunReleasesTheRunEvenWhenCanceled(t *testing.T) {
	// Release uses a context detached from the caller's, so a canceled audit
	// still cleans up after itself.
	p := memory.New()
	p.PollsBeforeDone = 99
	a := New(p, testMethod, WithPollInterval(time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = a.Run(ctx, "paper", t.TempDir())
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run ignored cancellation")
	}
	if len(p.Closed()) == 0 {
		t.Error("a canceled run must still be released")
	}
}

func TestAFailingGradeIsNotAPass(t *testing.T) {
	p := memory.New()
	p.Grade = provider.GradeNeedsRevision
	a := newTestAuditor(t, p)

	result, err := a.Run(context.Background(), "paper", t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Passed() {
		t.Error("needs_revision must not count as a pass")
	}
}

func TestResultWithoutAVerdictIsUnknownAndNotPassed(t *testing.T) {
	var r Result
	if r.Grade() != provider.GradeUnknown {
		t.Errorf("grade = %s, want unknown", r.Grade())
	}
	if r.Passed() {
		t.Error("a result with no verdict must not pass")
	}
}

func TestStartSurfacesProviderFailures(t *testing.T) {
	p := memory.New()
	p.Err = errors.New("backend down")
	if _, err := newTestAuditor(t, p).Start(context.Background(), "paper"); err == nil {
		t.Fatal("expected the provider error to surface")
	}
}

func TestOptionsOverrideDefaults(t *testing.T) {
	a := New(memory.New(), testMethod,
		WithWorkerName("custom"),
		WithModelPreference("fast"),
		WithMaxRevisions(7),
		WithPollInterval(time.Second),
		WithTimeout(2*time.Minute),
	)
	if a.workerName != "custom" || a.modelPref != "fast" || a.maxRevisions != 7 {
		t.Errorf("options not applied: %+v", a)
	}
	// A non-positive revision budget is ignored rather than accepted.
	b := New(memory.New(), testMethod, WithMaxRevisions(0))
	if b.maxRevisions != DefaultMaxRevisions {
		t.Errorf("maxRevisions = %d, want the default", b.maxRevisions)
	}
}

func TestCollectSurfacesProviderFailures(t *testing.T) {
	t.Run("listing fails", func(t *testing.T) {
		p := memory.New()
		a := newTestAuditor(t, p)
		h, _ := a.Start(context.Background(), "paper")
		p.Err = errors.New("listing unavailable")
		if _, err := a.Collect(context.Background(), h, t.TempDir()); err == nil {
			t.Fatal("a failed listing must surface")
		}
	})

	t.Run("destination cannot be created", func(t *testing.T) {
		p := memory.New()
		a := newTestAuditor(t, p)
		h, _ := a.Start(context.Background(), "paper")
		// A regular file cannot also be a directory.
		blocked := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if _, err := a.Collect(context.Background(), h, blocked); err == nil {
			t.Fatal("an uncreatable destination must surface")
		}
	})
}

func TestRunSurfacesACollectFailure(t *testing.T) {
	p := memory.New()
	p.Files = map[string][]byte{"../escaped.md": []byte("payload")}
	a := newTestAuditor(t, p)

	if _, err := a.Run(context.Background(), "paper", t.TempDir()); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("want ErrUnsafePath from Run, got %v", err)
	}
	if len(p.Closed()) == 0 {
		t.Error("the run must still be released after a collect failure")
	}
}

func TestWaitSurfacesAPollFailure(t *testing.T) {
	p := memory.New()
	a := newTestAuditor(t, p)
	h, _ := a.Start(context.Background(), "paper")
	p.Err = errors.New("backend down")

	if _, err := a.Wait(context.Background(), h); err == nil {
		t.Fatal("a failed poll must surface")
	}
}

func TestSleepCtxHonorsBothPathsAndCancellation(t *testing.T) {
	ctx := context.Background()
	if err := sleepCtx(ctx, 0); err != nil {
		t.Errorf("a zero delay should return immediately: %v", err)
	}
	if err := sleepCtx(ctx, time.Millisecond); err != nil {
		t.Errorf("a short delay should elapse cleanly: %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(canceled, 0); !errors.Is(err, context.Canceled) {
		t.Errorf("zero delay ignored cancellation: %v", err)
	}
	if err := sleepCtx(canceled, time.Hour); !errors.Is(err, context.Canceled) {
		t.Errorf("long delay ignored cancellation: %v", err)
	}
}

func TestDefaultPackagesAreIndependentCopies(t *testing.T) {
	// Callers must not be able to mutate the shared default.
	first := DefaultPackages()
	first["pip"] = append(first["pip"], "mutated")
	if second := DefaultPackages(); len(second["pip"]) != 2 {
		t.Errorf("DefaultPackages leaked mutable state: %v", second)
	}
}

// TestMountProtocolDemandsAPush pins the push as its own instruction with its
// own consequence (044a). A run once read this protocol, committed five times
// into the mount and pushed nothing, and every commit died with the sandbox —
// because the push was a subordinate clause of the commit sentence. It must not
// be edited back down into one.
func TestMountProtocolDemandsAPush(t *testing.T) {
	protocol := mountProtocol(DefaultMountPath)
	for _, want := range []string{
		`Run "git push" immediately after every commit`,
		"discarded when this session ends",
		"Committing is not publishing",
		`"git status -sb"`,
		"ahead of origin/main",
	} {
		if !strings.Contains(protocol, want) {
			t.Errorf("mount protocol missing %q:\n%s", want, protocol)
		}
	}
	// The push must not be buried in the sentence that asks for the commit.
	for line := range strings.SplitSeq(protocol, "\n") {
		if strings.Contains(line, "small, meaningful commits") && strings.Contains(line, "git push") {
			t.Errorf("the push is back inside the commit instruction:\n%s", line)
		}
	}
}
