// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/wirejson"
)

// Defaults for a runner. The instrument is mounted read-only, the dataset lands
// outside it, and the worker writes its report to the provider's output dir.
const (
	// DefaultInstrumentMount is where the instrument repository is mounted
	// read-only; an experiment reads it and must never write to it.
	DefaultInstrumentMount = "/workspace/instrument"
	// DefaultInputPath is where the dataset is attached. It is outside the
	// instrument mount so it cannot be committed into the reproduction (AC4).
	DefaultInputPath = "/workspace/input/dataset"
	// ResultFile is the report the worker writes and the runner reads back.
	ResultFile = "result.json"

	defaultRunnerModel   = "best"
	defaultRunnerTimeout = 30 * time.Minute
	outputDirPlaceholder = "{{OUTPUT_DIR}}"
)

// Runner executes one experiment in the provider sandbox: the instrument
// mounted read-only, the dataset attached outside it, the declared command run
// once, its real output captured. It does not grade and does not repair: there
// is no rubric and no revision budget, and the worker's instructions forbid
// changing the command.
//
// It uses the same normalized controller as audits, with Evaluation left nil.
type Runner struct {
	controller   *managed.Controller
	blobs        ArtifactStore
	instructions string // the experiment doctrine (method/experiment.md)

	modelPref  string
	mountToken string // read-only-scoped; never the org token (AC3)
	mountPath  string
	inputPath  string
	packages   map[string][]string
	timeout    time.Duration
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithMountToken sets the read-only-scoped credential the instrument is mounted
// with. It must never be the organization token, because an experiment only
// reads.
func WithMountToken(token string) RunnerOption {
	return func(r *Runner) { r.mountToken = token }
}

// WithRunnerTimeout overrides how long a run may take.
func WithRunnerTimeout(d time.Duration) RunnerOption {
	return func(r *Runner) { r.timeout = d }
}

// NewRunner builds a Runner. instructions is the experiment doctrine loaded from
// method/experiment.md; blobs is where result artifacts are persisted.
func NewRunner(controller *managed.Controller, blobs ArtifactStore, instructions string, opts ...RunnerOption) *Runner {
	r := &Runner{
		controller:   controller,
		blobs:        blobs,
		instructions: instructions,
		modelPref:    defaultRunnerModel,
		mountPath:    DefaultInstrumentMount,
		inputPath:    DefaultInputPath,
		packages:     map[string][]string{"pip": {"pandas", "numpy"}},
		timeout:      defaultRunnerTimeout,
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
// the bytes to analyze, the researcher's dataset for an uploaded source or the
// paper's released data for a released one, and is attached outside the mount.
// Run resolves the instrument's command onto the input path, runs it once, and
// reads the worker's report back. A command that failed to run is a complete
// outcome, not an error from Run.
func (r *Runner) Run(ctx context.Context, e *Experiment, inst Instrument, input []byte) (out Outcome, err error) {
	if inst.Decl.Analysis == nil {
		return Outcome{}, ErrNoAnalysis
	}
	command, err := inst.Decl.Analysis.CommandFor(r.inputPath)
	if err != nil {
		return Outcome{}, err
	}

	selector := managed.ModelSelector{Preference: r.modelPref}
	base := managed.SessionSpec{Model: selector}
	capabilities, err := r.controller.Capabilities(ctx, base)
	if err != nil {
		return Outcome{}, fmt.Errorf("read runtime capabilities: %w", err)
	}
	instructions := strings.ReplaceAll(r.instructions, outputDirPlaceholder, capabilities.OutputDir)
	digest := sha256.Sum256(input)
	repository := managed.RepositoryResource{
		URL: inst.Ref.Repo.URL, Destination: strings.TrimPrefix(r.mountPath, "/workspace/"), Access: managed.AccessReadOnly,
	}
	if r.mountToken != "" {
		repository.CredentialRef = "experiment-repository"
		ctx = managed.WithSecrets(ctx, map[string]string{repository.CredentialRef: r.mountToken})
	}
	spec := managed.SessionSpec{
		Model: selector, Instructions: instructions, Task: r.taskFor(command),
		Sandbox: managed.SandboxSpec{
			Packages: packageSpecs(r.packages), Network: managed.NetworkPolicy{Mode: managed.NetworkOpen},
		},
		Resources: []managed.Resource{
			{Kind: managed.ResourceRepository, Repository: &repository},
			{Kind: managed.ResourceAttachment, Attachment: &managed.AttachmentResource{
				Path: strings.TrimPrefix(r.inputPath, "/workspace/"), Content: input, SHA256: fmt.Sprintf("%x", digest),
			}},
		},
		Evaluation: nil,
	}
	runCtx := ctx
	cancel := func() {}
	if r.timeout != 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.timeout)
	}
	defer cancel()
	hooks := managed.Hooks{Retain: func(retainCtx context.Context, evidence managed.EvidenceReader) error {
		out, err = r.collect(retainCtx, e, evidence, command)
		return err
	}}
	_, err = r.controller.Execute(runCtx, spec, hooks)
	return out, err
}

func packageSpecs(packages map[string][]string) []managed.Package {
	var result []managed.Package
	managers := make([]string, 0, len(packages))
	for manager := range packages {
		managers = append(managers, manager)
	}
	slices.Sort(managers)
	for _, manager := range managers {
		names := packages[manager]
		for _, name := range names {
			result = append(result, managed.Package{Manager: manager, Name: name})
		}
	}
	return result
}

// taskFor builds the per-run task: the exact command, where its input and the
// instrument are, and the report to write. The rule against repair is in the
// worker's standing instructions; this only names the command and the paths.
func (r *Runner) taskFor(command string) string {
	return fmt.Sprintf(`Run this analysis exactly as written and report what happened:

    %s

The input data is at %s. The instrument repository is mounted read-only at %s — read from it, never write to it. Run the command once, capture its real stdout, stderr, exit code, and output files, and write %s to your output directory as your doctrine describes. Do not modify the command; if it fails, report ran=false with the real error.`,
		command, r.inputPath, r.mountPath, ResultFile)
}

// collect reads the worker's report and persists the output artifacts. A missing
// or unreadable report is itself an outcome: a run that produced nothing
// reportable did not run, and that is recorded rather than raised as an error.
func (r *Runner) collect(ctx context.Context, e *Experiment, evidence managed.EvidenceReader, command string) (Outcome, error) {
	byPath := make(map[string]managed.Artifact, len(evidence.Artifacts))
	for _, a := range evidence.Artifacts {
		byPath[a.Path] = a
	}

	out := Outcome{Command: command}
	reportArt, ok := byPath[ResultFile]
	if !ok {
		out.Stderr = "the run produced no " + ResultFile
		return out, nil
	}
	raw, err := evidence.Fetch(ctx, reportArt)
	if err != nil {
		return Outcome{}, fmt.Errorf("fetch %s: %w", ResultFile, err)
	}
	var rep runReport
	if err := json.Unmarshal(raw, &rep, wirejson.Foreign); err != nil {
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
			continue // the worker named an output it did not produce
		}
		body, err := evidence.Fetch(ctx, art)
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
