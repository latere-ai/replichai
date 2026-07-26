package experiment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/provider"
)

// Defaults for a runner. The instrument is mounted read-only, the dataset lands
// outside it, and the worker writes its report to the provider's output dir.
const (
	// DefaultInstrumentMount is where the instrument repository is mounted,
	// read-only — an experiment reads it and must never write to it.
	DefaultInstrumentMount = "/workspace/instrument"
	// DefaultInputPath is where the dataset is attached, deliberately OUTSIDE the
	// instrument mount so it cannot be committed into the reproduction (AC4).
	DefaultInputPath = "/workspace/input/dataset"
	// ResultFile is the report the worker writes and the runner reads back.
	ResultFile = "result.json"

	defaultRunnerModel   = "best"
	defaultRunnerWorker  = "repro-runner"
	defaultPollInterval  = 20 * time.Second
	defaultRunnerTimeout = 30 * time.Minute
	outputDirPlaceholder = "{{OUTPUT_DIR}}"
)

// Runner executes one experiment in the provider sandbox: the instrument
// mounted read-only, the dataset attached outside it, the declared command run
// once, its real output captured. It does not grade and it does not repair —
// there is no rubric and no revision budget, and the worker's doctrine forbids
// changing the command.
//
// It reuses provider.Provider unchanged: an experiment is a run with an empty
// rubric and no revisions, not a second execution substrate.
type Runner struct {
	provider     provider.Provider
	blobs        ArtifactStore
	instructions string // the experiment doctrine (method/experiment.md)

	modelPref    string
	workerName   string
	mountToken   string // read-only-scoped; never the org token (AC3)
	mountPath    string
	inputPath    string
	packages     map[string][]string
	pollInterval time.Duration
	timeout      time.Duration
	sleep        func(context.Context, time.Duration) error
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithRunnerModel overrides the model preference.
func WithRunnerModel(pref string) RunnerOption {
	return func(r *Runner) { r.modelPref = pref }
}

// WithMountToken sets the read-only-scoped credential the instrument is mounted
// with. It must never be the organisation token — an experiment only reads.
func WithMountToken(token string) RunnerOption {
	return func(r *Runner) { r.mountToken = token }
}

// WithRunnerTimeout overrides how long a run may take.
func WithRunnerTimeout(d time.Duration) RunnerOption {
	return func(r *Runner) { r.timeout = d }
}

// WithRunnerPollInterval overrides the gap between status checks.
func WithRunnerPollInterval(d time.Duration) RunnerOption {
	return func(r *Runner) { r.pollInterval = d }
}

// withRunnerSleep replaces the wait between polls so tests run instantly.
func withRunnerSleep(f func(context.Context, time.Duration) error) RunnerOption {
	return func(r *Runner) { r.sleep = f }
}

// NewRunner builds a Runner. instructions is the experiment doctrine loaded from
// method/experiment.md; blobs is where result artifacts are persisted.
func NewRunner(p provider.Provider, blobs ArtifactStore, instructions string, opts ...RunnerOption) *Runner {
	r := &Runner{
		provider:     p,
		blobs:        blobs,
		instructions: instructions,
		modelPref:    defaultRunnerModel,
		workerName:   defaultRunnerWorker,
		mountPath:    DefaultInstrumentMount,
		inputPath:    DefaultInputPath,
		packages:     map[string][]string{"pip": {"pandas", "numpy"}},
		pollInterval: defaultPollInterval,
		timeout:      defaultRunnerTimeout,
		sleep:        sleepCtx,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// runReport is the worker's structured account of the run, read back from
// ResultFile. The worker reports; it never judges.
type runReport struct {
	Ran        bool        `json:"ran"`
	ExitCode   int         `json:"exit_code"`
	Stdout     string      `json:"stdout"`
	Stderr     string      `json:"stderr"`
	Recomputed []Statistic `json:"recomputed"`
	Outputs    []string    `json:"outputs"`
}

// Run executes the experiment against input and returns its outcome. input is
// the bytes to analyse — the researcher's dataset for an uploaded source, the
// paper's released data for a released one — attached outside the mount. It
// resolves the instrument's command onto the input path, runs it once, and
// reads the worker's report back. A command that failed to run is a complete
// outcome, not an error from Run.
func (r *Runner) Run(ctx context.Context, e *Experiment, inst Instrument, input []byte) (Outcome, error) {
	if inst.Decl.Analysis == nil {
		return Outcome{}, ErrNoAnalysis
	}
	command, err := inst.Decl.Analysis.CommandFor(r.inputPath)
	if err != nil {
		return Outcome{}, err
	}

	model, err := r.provider.ResolveModel(ctx, r.modelPref)
	if err != nil {
		return Outcome{}, fmt.Errorf("resolve model: %w", err)
	}
	ws, err := r.provider.EnsureWorkspace(ctx, provider.WorkspaceSpec{
		Name:     r.workerName + "-workspace",
		Packages: r.packages,
		Network:  provider.NetworkOpen,
	})
	if err != nil {
		return Outcome{}, fmt.Errorf("ensure workspace: %w", err)
	}
	instructions := strings.ReplaceAll(r.instructions, outputDirPlaceholder, r.provider.OutputDir())
	worker, err := r.provider.EnsureWorker(ctx, provider.WorkerSpec{
		Name:         r.workerName,
		Instructions: instructions,
		Model:        model,
		Description:  "Runs a declared analysis on given data and reports what happened.",
	})
	if err != nil {
		return Outcome{}, fmt.Errorf("ensure worker: %w", err)
	}

	h, err := r.provider.StartRun(ctx, worker, ws, provider.RunSpec{
		Task: r.taskFor(command),
		// No rubric and no revisions: an experiment is run and reported, never graded.
		Mount: provider.RepoMount{
			URL:       inst.Ref.Repo.URL,
			Token:     r.mountToken,
			MountPath: r.mountPath,
			ReadOnly:  true,
		},
		Attachments: []provider.Attachment{{Path: r.inputPath, Content: input}},
	})
	if err != nil {
		return Outcome{}, fmt.Errorf("start run: %w", err)
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = r.provider.CloseRun(releaseCtx, h)
	}()

	if err := r.wait(ctx, h); err != nil {
		return Outcome{}, err
	}
	return r.collect(ctx, e, h, command)
}

// taskFor builds the per-run task: the exact command, where its input and the
// instrument are, and the report to write. The doctrine (no repair) is the
// worker's standing instruction; this only names the command and the paths.
func (r *Runner) taskFor(command string) string {
	return fmt.Sprintf(`Run this analysis exactly as written and report what happened:

    %s

The input data is at %s. The instrument repository is mounted read-only at %s — read from it, never write to it. Run the command once, capture its real stdout, stderr, exit code, and output files, and write %s to your output directory as your doctrine describes. Do not modify the command; if it fails, report ran=false with the real error.`,
		command, r.inputPath, r.mountPath, ResultFile)
}

// wait polls until the run reaches a terminal state, the deadline passes, or the
// context is canceled.
func (r *Runner) wait(ctx context.Context, h provider.RunHandle) error {
	deadline := time.Now().Add(r.timeout)
	for {
		status, err := r.provider.Poll(ctx, h)
		if err != nil {
			return fmt.Errorf("poll run %s: %w", h.ID, err)
		}
		if status.State.Terminal() {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("experiment run %s did not finish within %s", h.ID, r.timeout)
		}
		if err := r.sleep(ctx, r.pollInterval); err != nil {
			return err
		}
	}
}

// collect reads the worker's report and persists the output artifacts. A missing
// or unreadable report is itself an outcome — a run that produced nothing
// reportable did not run, honestly recorded rather than raised as an error.
func (r *Runner) collect(ctx context.Context, e *Experiment, h provider.RunHandle, command string) (Outcome, error) {
	arts, err := r.provider.ListArtifacts(ctx, h)
	if err != nil {
		return Outcome{}, fmt.Errorf("list artifacts: %w", err)
	}
	byPath := make(map[string]provider.Artifact, len(arts))
	for _, a := range arts {
		byPath[a.Path] = a
	}

	out := Outcome{Command: command}
	reportArt, ok := byPath[ResultFile]
	if !ok {
		out.Stderr = "the run produced no " + ResultFile
		return out, nil
	}
	raw, err := r.provider.FetchArtifact(ctx, h, reportArt)
	if err != nil {
		return Outcome{}, fmt.Errorf("fetch %s: %w", ResultFile, err)
	}
	var rep runReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		out.Stderr = fmt.Sprintf("%s was not valid JSON: %v", ResultFile, err)
		return out, nil
	}

	out.Ran = rep.Ran
	out.ExitCode = rep.ExitCode
	out.Stdout = rep.Stdout
	out.Stderr = rep.Stderr
	out.Recomputed = rep.Recomputed

	for _, name := range rep.Outputs {
		art, ok := byPath[name]
		if !ok {
			continue // the worker named an output it did not actually produce; skip it
		}
		body, err := r.provider.FetchArtifact(ctx, h, art)
		if err != nil {
			return Outcome{}, fmt.Errorf("fetch output %s: %w", name, err)
		}
		sum, err := r.blobs.Put(ctx, ArtifactKey(e.ID, name), body)
		if err != nil {
			return Outcome{}, fmt.Errorf("store output %s: %w", name, err)
		}
		out.Artifacts = append(out.Artifacts, Artifact{Name: name, Checksum: sum})
	}
	return out, nil
}

// sleepCtx waits for d, or returns early if ctx is canceled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
