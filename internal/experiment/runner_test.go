// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/managed/legacy"
	managedmemory "github.com/latere-ai/replichai/internal/managed/memory"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/provider/memory"
)

var errBackendDown = errors.New("backend down")

const testDoctrine = "You are repro-runner. Run the command exactly as given; do not modify it. Write result.json to {{OUTPUT_DIR}}."

func newTestRunner(t *testing.T, p *memory.Provider, blobs ArtifactStore) *Runner {
	t.Helper()
	return NewRunner(newManagedController(t, p), blobs, testDoctrine, WithMountToken("ro-token"))
}

type singleRuntimeRouter struct{ runtime managed.Runtime }

func (r singleRuntimeRouter) ForNew(managed.SessionSpec) (managed.Runtime, error) {
	return r.runtime, nil
}
func (r singleRuntimeRouter) ForRef(managed.SessionRef) (managed.Runtime, error) {
	return r.runtime, nil
}

func newManagedController(t *testing.T, p provider.Provider) *managed.Controller {
	t.Helper()
	return managedController(p)
}

func managedController(p provider.Provider) *managed.Controller {
	runtime, err := legacy.New(legacy.Config{Name: "anthropic-managed", ModelProvider: "anthropic", Provider: p})
	if err != nil {
		panic(err)
	}
	return controllerForRuntime(runtime)
}

func controllerForRuntime(runtime managed.Runtime) *managed.Controller {
	policy := managed.DefaultWaitPolicy()
	policy.InitialDelay, policy.MaxDelay = 0, 0
	return managed.NewController(singleRuntimeRouter{runtime: runtime}, managed.WithWaitPolicy(policy))
}

type closeFailProvider struct {
	*memory.Provider
	err error
}

func (p *closeFailProvider) CloseRun(context.Context, provider.RunHandle) error { return p.err }

func mountedInstrument() Instrument {
	inst := runnableInstrument()
	inst.Ref.Repo.URL = "https://github.com/open-hai/a-repro"
	return inst
}

func TestRunnerRunsAndReports(t *testing.T) {
	p := memory.New()
	p.Files = map[string][]byte{
		ResultFile:           []byte(`{"ran":true,"exit_code":0,"stdout":"done","recomputed":[{"metric":"effect_size","value":"0.41"}],"outputs":["results/table2.csv"]}`),
		"results/table2.csv": []byte("a,b\n1,2\n"),
	}
	blobs := NewMemArtifactStore()
	e := &Experiment{ID: "exp-1", ProjectID: "proj-1", Source: SourceUploaded}

	out, err := newTestRunner(t, p, blobs).Run(context.Background(), e, mountedInstrument(), []byte("rt,id\n1,a\n"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !out.Ran || out.Stdout != "done" {
		t.Errorf("outcome = %+v, want a clean run", out)
	}
	if len(out.Recomputed) != 1 || out.Recomputed[0].Metric != "effect_size" {
		t.Errorf("recomputed = %v, want the worker's statistic", out.Recomputed)
	}
	if !strings.Contains(out.Command, DefaultInputPath) {
		t.Errorf("command %q should have the input path substituted", out.Command)
	}
	// The output artifact was persisted, checksummed.
	if len(out.Artifacts) != 1 || out.Artifacts[0].Name != "results/table2.csv" {
		t.Fatalf("artifacts = %v, want the stored output", out.Artifacts)
	}
	body, ok, _ := blobs.Get(context.Background(), ArtifactKey("exp-1", "results/table2.csv"))
	if !ok || string(body) != "a,b\n1,2\n" {
		t.Errorf("output not persisted to the artifact store: %q ok=%v", body, ok)
	}
	if out.Artifacts[0].Checksum != Checksum([]byte("a,b\n1,2\n")) {
		t.Errorf("checksum mismatch: %s", out.Artifacts[0].Checksum)
	}
}

func TestRunnerUsesOneUngradedManagedSession(t *testing.T) {
	runtime := managedmemory.NewWithStore(nil, managedmemory.Config{Files: map[string][]byte{
		ResultFile: []byte(`{"ran":true,"stdout":"managed"}`),
	}})
	runner := NewRunner(controllerForRuntime(runtime), NewMemArtifactStore(), testDoctrine, WithMountToken("scoped"))
	out, err := runner.Run(context.Background(), &Experiment{ID: "managed"}, mountedInstrument(), []byte("data"))
	if err != nil || !out.Ran || out.Stdout != "managed" {
		t.Fatalf("Run = %+v, %v", out, err)
	}
	specs := runtime.Specs()
	if len(specs) != 1 || specs[0].Evaluation != nil || len(specs[0].Resources) != 2 {
		t.Fatalf("managed specs = %+v", specs)
	}
}

func TestRunnerReturnsCleanupError(t *testing.T) {
	closeErr := errors.New("close failed")
	p := &closeFailProvider{Provider: memory.New(), err: closeErr}
	p.Files = map[string][]byte{ResultFile: []byte(`{"ran":true}`)}
	r := NewRunner(newManagedController(t, p), NewMemArtifactStore(), testDoctrine, WithMountToken("ro-token"))

	if _, err := r.Run(context.Background(), &Experiment{ID: "exp-1"}, mountedInstrument(), []byte("data")); !errors.Is(err, closeErr) {
		t.Fatalf("Run error = %v, want cleanup error", err)
	}
}

// AC3/AC4: the instrument is mounted read-only with a non-org token, and the
// dataset lands outside the mount so it cannot be committed.
func TestRunnerMountsReadOnlyAndAttachesOutside(t *testing.T) {
	p := memory.New()
	p.Files = map[string][]byte{ResultFile: []byte(`{"ran":true}`)}
	e := &Experiment{ID: "exp-1", Source: SourceUploaded}

	if _, err := newTestRunner(t, p, NewMemArtifactStore()).Run(context.Background(), e, mountedInstrument(), []byte("data")); err != nil {
		t.Fatalf("Run: %v", err)
	}

	runs := p.Runs()
	if len(runs) != 1 {
		t.Fatalf("recorded %d runs, want 1 (no retry)", len(runs))
	}
	spec := runs[0]
	if !spec.Mount.ReadOnly {
		t.Error("the instrument mount must be read-only")
	}
	if spec.Mount.Token == "" || spec.Mount.Token != "ro-token" {
		t.Errorf("mount token = %q, want the read-only-scoped token", spec.Mount.Token)
	}
	if spec.Rubric != "" || spec.MaxRevisions != 0 {
		t.Errorf("an experiment has no rubric and no revisions, got rubric=%q rev=%d", spec.Rubric, spec.MaxRevisions)
	}
	if len(spec.Attachments) != 1 {
		t.Fatalf("want the dataset attached, got %d attachments", len(spec.Attachments))
	}
	if strings.HasPrefix(spec.Attachments[0].Path, spec.Mount.MountPath) {
		t.Errorf("the dataset (%s) must land outside the mount (%s)", spec.Attachments[0].Path, spec.Mount.MountPath)
	}
	if string(spec.Attachments[0].Content) != "data" {
		t.Errorf("attached content = %q, want the dataset bytes", spec.Attachments[0].Content)
	}
}

// AC6: a failure is a complete outcome, and the run is not retried.
func TestRunnerFailedRunIsAComplete(t *testing.T) {
	p := memory.New()
	p.Files = map[string][]byte{ResultFile: []byte(`{"ran":false,"exit_code":1,"stderr":"KeyError: 'rt'"}`)}
	e := &Experiment{ID: "exp-1", Source: SourceUploaded}

	out, err := newTestRunner(t, p, NewMemArtifactStore()).Run(context.Background(), e, mountedInstrument(), []byte("data"))
	if err != nil {
		t.Fatalf("a failed analysis must not be an error from Run: %v", err)
	}
	if out.Ran || out.ExitCode != 1 || !strings.Contains(out.Stderr, "KeyError") {
		t.Errorf("outcome = %+v, want a recorded failure with the real error", out)
	}
	if len(p.Runs()) != 1 {
		t.Errorf("a failure must not be retried, got %d runs", len(p.Runs()))
	}
}

func TestRunnerMissingReportIsFailedToRun(t *testing.T) {
	p := memory.New()
	p.Files = map[string][]byte{"stray.txt": []byte("no report")}
	out, err := newTestRunner(t, p, NewMemArtifactStore()).Run(
		context.Background(), &Experiment{ID: "exp-1"}, mountedInstrument(), []byte("data"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Ran || !strings.Contains(out.Stderr, ResultFile) {
		t.Errorf("a run with no report should be a failed-to-run outcome, got %+v", out)
	}
}

// The worker may name an output it did not produce; the runner skips it rather
// than failing, since a missing file is a fault in the worker's report and not
// a runner error.
func TestRunnerSkipsNamedButAbsentOutput(t *testing.T) {
	p := memory.New()
	p.Files = map[string][]byte{
		ResultFile:    []byte(`{"ran":true,"outputs":["present.csv","ghost.csv"]}`),
		"present.csv": []byte("x"),
	}
	blobs := NewMemArtifactStore()
	out, err := newTestRunner(t, p, blobs).Run(
		context.Background(), &Experiment{ID: "exp-1"}, mountedInstrument(), []byte("d"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Name != "present.csv" {
		t.Errorf("only the produced output should be stored, got %v", out.Artifacts)
	}
}

func TestRunnerRefusesNullAnalysis(t *testing.T) {
	inst := mountedInstrument()
	inst.Decl.Analysis = nil
	_, err := newTestRunner(t, memory.New(), NewMemArtifactStore()).Run(
		context.Background(), &Experiment{ID: "x"}, inst, nil)
	if err == nil {
		t.Fatal("a null-analysis instrument should not run")
	}
}

func TestRunnerRefusesUnparameterisedCommand(t *testing.T) {
	inst := mountedInstrument()
	inst.Decl.Analysis.Command = "python run.py data/original.csv" // no {{INPUT}}
	_, err := newTestRunner(t, memory.New(), NewMemArtifactStore()).Run(
		context.Background(), &Experiment{ID: "x"}, inst, []byte("d"))
	if err == nil {
		t.Fatal("a command that cannot take the input path should not run")
	}
}

func TestRunnerSurfacesProviderFailure(t *testing.T) {
	p := memory.New()
	p.Err = errBackendDown
	_, err := newTestRunner(t, p, NewMemArtifactStore()).Run(
		context.Background(), &Experiment{ID: "x"}, mountedInstrument(), []byte("d"))
	if err == nil {
		t.Fatal("a backend failure should surface from Run")
	}
}

// failAt wraps the memory provider and fails one named method, so the runner's
// error wrapping at each backend stage is exercised.
type failAt struct {
	*memory.Provider
	stage string
}

func (f failAt) EnsureWorkspace(ctx context.Context, s provider.WorkspaceSpec) (provider.WorkspaceRef, error) {
	if f.stage == "workspace" {
		return provider.WorkspaceRef{}, errBackendDown
	}
	return f.Provider.EnsureWorkspace(ctx, s)
}
func (f failAt) EnsureWorker(ctx context.Context, s provider.WorkerSpec) (provider.WorkerRef, error) {
	if f.stage == "worker" {
		return provider.WorkerRef{}, errBackendDown
	}
	return f.Provider.EnsureWorker(ctx, s)
}
func (f failAt) StartRun(ctx context.Context, w provider.WorkerRef, ws provider.WorkspaceRef, s provider.RunSpec) (provider.RunHandle, error) {
	if f.stage == "start" {
		return provider.RunHandle{}, errBackendDown
	}
	return f.Provider.StartRun(ctx, w, ws, s)
}
func (f failAt) ListArtifacts(ctx context.Context, h provider.RunHandle) ([]provider.Artifact, error) {
	if f.stage == "list" {
		return nil, errBackendDown
	}
	return f.Provider.ListArtifacts(ctx, h)
}
func (f failAt) FetchArtifact(ctx context.Context, h provider.RunHandle, a provider.Artifact) ([]byte, error) {
	if f.stage == "fetch" {
		return nil, errBackendDown
	}
	return f.Provider.FetchArtifact(ctx, h, a)
}

// failBlobs errors on Put, so the runner's store-error path is covered.
type failBlobs struct{ ArtifactStore }

func (failBlobs) Put(context.Context, string, []byte) (string, error) { return "", errBackendDown }

func TestRunnerSurfacesArtifactStoreFailure(t *testing.T) {
	p := memory.New()
	p.Files = map[string][]byte{
		ResultFile: []byte(`{"ran":true,"outputs":["out.csv"]}`),
		"out.csv":  []byte("x"),
	}
	r := NewRunner(newManagedController(t, p), failBlobs{NewMemArtifactStore()}, testDoctrine)
	if _, err := r.Run(context.Background(), &Experiment{ID: "x"}, mountedInstrument(), []byte("d")); err == nil {
		t.Fatal("a store failure while persisting an output should surface")
	}
}

func TestRunnerWrapsEachStageFailure(t *testing.T) {
	for _, stage := range []string{"workspace", "worker", "start", "list", "fetch"} {
		t.Run(stage, func(t *testing.T) {
			mem := memory.New()
			mem.Files = map[string][]byte{ResultFile: []byte(`{"ran":true}`)}
			r := NewRunner(newManagedController(t, failAt{Provider: mem, stage: stage}), NewMemArtifactStore(), testDoctrine)
			if _, err := r.Run(context.Background(), &Experiment{ID: "x"}, mountedInstrument(), []byte("d")); err == nil {
				t.Fatalf("a failure at %s should surface from Run", stage)
			}
		})
	}
}

func TestRunnerTimesOut(t *testing.T) {
	p := memory.New()
	p.PollsBeforeDone = 99 // never finishes in time
	r := NewRunner(newManagedController(t, p), NewMemArtifactStore(), testDoctrine,
		WithRunnerTimeout(-time.Second),
	)
	if _, err := r.Run(context.Background(), &Experiment{ID: "x"}, mountedInstrument(), []byte("d")); err == nil {
		t.Fatal("a run that never finishes should time out")
	}
}

// AC6: the instructions the worker is given grant no permission to repair.
// Asserted on the real method/experiment.md, since that is what is sent live.
func TestExperimentDoctrineForbidsRepair(t *testing.T) {
	doctrine, err := method.LoadExperiment("")
	if err != nil {
		t.Fatalf("LoadExperiment: %v", err)
	}
	lower := strings.ToLower(doctrine)
	for _, must := range []string{"exactly as given", "do not modify", "did not run"} {
		if !strings.Contains(lower, must) {
			t.Errorf("doctrine is missing the no-repair rule %q", must)
		}
	}
	// Instructions that permitted repair would allow the command to be
	// changed. They must never phrase modification as allowed.
	for _, license := range []string{"you may modify", "feel free to", "if needed, adjust", "make it work"} {
		if strings.Contains(lower, license) {
			t.Errorf("doctrine appears to license repair: %q", license)
		}
	}
}
