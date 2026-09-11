// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package audit drives one reproducibility audit from a paper reference to a
// graded set of artifacts.
//
// It knows the shape of the work, never the backend: every call it makes goes
// through provider.Provider. It returns values and errors and never writes to
// stdout, so an HTTP handler can wrap it unchanged.
package audit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"latere.ai/x/pkg/wait"

	"github.com/latere-ai/replichai/internal/instrument"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/provider"
)

// Errors returned by this package.
var (
	// ErrTimedOut reports that a run outlived its deadline.
	ErrTimedOut = errors.New("run did not finish in time")
	// ErrUnsafePath reports an artifact path that would escape its destination.
	ErrUnsafePath = errors.New("artifact path escapes the destination directory")
)

// Defaults applied when an Option does not override them.
const (
	DefaultWorkerName      = "repro-auditor"
	DefaultModelPreference = "best"
	DefaultMaxRevisions    = 3
	DefaultPollInterval    = 20 * time.Second
	DefaultTimeout         = time.Hour
	// DefaultPollFailures is how many *consecutive* failed observations are
	// tolerated before the wait gives up. A single 5xx from the backend is not a
	// failed session, and an unbounded wait makes hundreds of observations, so
	// one transient error must not fail a healthy run. At DefaultPollInterval
	// this rides out roughly seven minutes of a backend that will not answer —
	// long enough for an API degradation four hours into a run, short enough
	// that a genuinely unreachable session does not poll forever.
	DefaultPollFailures = 20
	// DefaultMountPath is where a mounted repository lands when the caller does
	// not choose a path.
	DefaultMountPath = "/workspace/repro"
)

// mountProtocol tells the worker how to use a mounted repository: commit the
// reproduction into it and push, keeping third-party downloads out. This
// bounded workspace is what makes a live push safe — third-party data is never
// committed, so the publisher's gate can verify after the fact (006f).
//
// The push is its own step, and says what happens without it (044a). Asking for
// it inside the commit sentence produced a run that committed five times and
// pushed nothing: every local commit died with the sandbox, and the grader —
// which reads files, not remotes — passed it.
func mountProtocol(mountPath string) string {
	return fmt.Sprintf(`

A git repository is mounted at %[1]s, and its commit history is published as part of the result. Treat it as the home of your reproduction:

- Write your reproduction files (README.md, REPRODUCIBILITY.md, src/, UNVERIFIED.md, SOURCES.md, instrument.json) inside %[1]s.
- Commit your progress with git as you finish each part — small, meaningful commits. If git refuses to commit for want of an identity, set one first with "git config user.email" and "git config user.name".
- Run "git push" immediately after every commit. A commit that is not pushed is discarded when this session ends: the sandbox is destroyed and only what reached the remote survives. Committing is not publishing.
- Before you finish, run "git status -sb" and confirm the branch is not ahead of origin/main. If it is, push. Record that output — criterion 9 is graded on it.
- Keep every download (the paper PDF, datasets, benchmarks, any third-party file) OUTSIDE %[1]s; use /tmp. Never "git add" or commit third-party data: the repository must contain only your own reproduction work.`, mountPath)
}

// workspaceProtocol tells the worker how to use a workspace that is a git
// repository with no remote (045g). It replaces mountProtocol rather than
// editing it: the two describe different arrangements, and a run that has no
// remote must not be told to push to one.
//
// The push sentence is gone because there is nothing to push to, and inventing
// a remote so the instruction could stay would put a forge credential back
// inside the sandbox to satisfy a sentence. What replaces it is a check the
// agent can genuinely fail — uncommitted work is dropped — and a system check
// it cannot influence: the host verifies the committed tree and publishes it
// after the session ends.
//
// It says "no remote" outright, because that is the condition rubric criterion
// 9 keys on. The two texts are read together by the grader, so they have to
// agree about what is present.
func workspaceProtocol(workspace string) string {
	return fmt.Sprintf(`

Your reproduction lives at %[1]s, which is a git repository on the machine running this session. There is no remote, and %[1]s is not destroyed when this session ends.

- Write your reproduction files (README.md, REPRODUCIBILITY.md, src/, UNVERIFIED.md, SOURCES.md, verdict.json, instrument.json) inside %[1]s.
- Commit your progress with git as you finish each part — small, meaningful commits. If git refuses to commit for want of an identity, set one first with "git config user.email" and "git config user.name".
- There is nothing to push. The system publishes what you committed, after this session ends, and verifies it independently. Criterion 9 does not apply to this run.
- Before you finish, run "git status" and confirm nothing is uncommitted, then record "git log --oneline -5". Uncommitted work is not part of the history that gets published.
- A Python environment is already prepared for you at "$VIRTUAL_ENV", with this run's declared packages installed. Use it — "$VIRTUAL_ENV/bin/python" and "$VIRTUAL_ENV/bin/pip" — and do not create a virtualenv inside %[1]s: it is not your work, it is enormous, and a tree carrying one is refused at publication along with everything else in it.
- This session reaches a fixed list of domains. A fetch refused with EGRESS_BLOCKED is a limit of this run, not a fact about the paper: record it as "not fetched — the sandbox refused <domain>", never as "no artifact found" or "not released". Saying an author released nothing when you were not allowed to look is the worst error this audit can make.
- Keep every download (the paper PDF, datasets, benchmarks, any third-party file) OUTSIDE %[1]s. Put them in "$TMPDIR", which is writable and is discarded with the session. Never "git add" or commit third-party data: the repository must contain only your own reproduction work, and a tree carrying a paper or a dataset is refused at publication.`, workspace)
}

// DefaultPackages are installed into every sandbox: enough to pull a paper
// down and read it, with anything domain-specific installed per run.
func DefaultPackages() map[string][]string {
	return map[string][]string{"pip": {"pypdf", "requests"}}
}

// Result is everything one audit produced.
type Result struct {
	Paper       string
	Handle      provider.RunHandle
	Status      provider.RunStatus
	Artifacts   []provider.Artifact
	ArtifactDir string
	// MethodDigest pins which version of the method produced this run, so a
	// published reproduction records what it is comparable against.
	MethodDigest string
	// InstrumentPresent reports whether the run wrote instrument.json (007a).
	InstrumentPresent bool
	// InstrumentViolations is the finalize-time schema check of instrument.json:
	// empty when it validates. This is a report to the caller, never folded into
	// the verdict or the manifest — rubric criterion 7 is what drives revision.
	InstrumentViolations []instrument.Violation
}

// InstrumentOK reports whether the run wrote a schema-valid instrument.json.
func (r Result) InstrumentOK() bool {
	return r.InstrumentPresent && len(r.InstrumentViolations) == 0
}

// InstrumentFile is the audit's declared study protocol (007a).
const InstrumentFile = "instrument.json"

// Passed reports whether the run met the rubric.
func (r Result) Passed() bool { return r.Status.Passed() }

// Grade reports the verdict's grade, or GradeUnknown when none was recorded.
func (r Result) Grade() provider.Grade {
	if r.Status.Verdict == nil {
		return provider.GradeUnknown
	}
	return r.Status.Verdict.Grade
}

// Auditor runs audits against a provider.
type Auditor struct {
	provider provider.Provider
	method   method.Method

	workerName   string
	modelPref    string
	packages     map[string][]string
	maxRevisions int
	pollInterval time.Duration
	timeout      time.Duration
	unbounded    bool
	pollFailures int
	mount        provider.RepoMount
	observe      func(provider.RunStatus)
	sleep        func(context.Context, time.Duration) error
}

// Option configures an Auditor.
type Option func(*Auditor)

// WithWorkerName overrides the name given to the worker.
func WithWorkerName(name string) Option {
	return func(a *Auditor) { a.workerName = name }
}

// WithModelPreference overrides the model tier requested.
func WithModelPreference(pref string) Option {
	return func(a *Auditor) { a.modelPref = pref }
}

// WithMaxRevisions overrides how many times the worker may revise.
func WithMaxRevisions(n int) Option {
	return func(a *Auditor) {
		if n > 0 {
			a.maxRevisions = n
		}
	}
}

// WithPollInterval overrides the gap between status checks.
func WithPollInterval(d time.Duration) Option {
	return func(a *Auditor) { a.pollInterval = d }
}

// WithTimeout overrides how long a run may take before it is abandoned.
func WithTimeout(d time.Duration) Option {
	return func(a *Auditor) { a.timeout = d; a.unbounded = false }
}

// WithoutTimeout removes the wall-clock deadline: the wait ends when the backend
// session ends, and nothing else.
//
// The backend session is the source of truth for whether the work succeeded, and
// a deadline is a second, weaker opinion about it — one that reports a session
// still grading, or one that finished a minute late, as a failure while the
// agent's work sits complete in the mounted repository. A run that genuinely
// hangs is bounded by the backend's own session lifetime, by the operator
// cancelling the session, and by the consecutive-failure budget below; none of
// those need a clock here.
func WithoutTimeout() Option {
	return func(a *Auditor) { a.unbounded = true }
}

// WithPollFailures overrides how many consecutive failed observations are
// tolerated before the wait gives up.
func WithPollFailures(n int) Option {
	return func(a *Auditor) {
		if n > 0 {
			a.pollFailures = n
		}
	}
}

// WithObserver installs a callback invoked on every status observation.
func WithObserver(f func(provider.RunStatus)) Option {
	return func(a *Auditor) { a.observe = f }
}

// WithMount mounts a repository into the run so the worker commits its
// reproduction directly, with the history published as part of the result. The
// worker is told to keep third-party downloads out of the repository (006f).
func WithMount(m provider.RepoMount) Option {
	return func(a *Auditor) { a.mount = m }
}

// withSleep replaces the wait between polls. Tests use it to run instantly.
func withSleep(f func(context.Context, time.Duration) error) Option {
	return func(a *Auditor) { a.sleep = f }
}

// Unbounded reports whether this auditor waits without a wall-clock deadline —
// letting the composition root assert how it wired the wait.
func (a *Auditor) Unbounded() bool { return a.unbounded }

// ModelPreference reports the tier this auditor will resolve a model from, for
// the same reason Unbounded exists: the composition root decides it per session
// (031) and has to be able to prove it wired the job's choice through, on the
// resumed path as well as the fresh one.
func (a *Auditor) ModelPreference() string { return a.modelPref }

// New builds an Auditor.
func New(p provider.Provider, m method.Method, opts ...Option) *Auditor {
	a := &Auditor{
		provider:     p,
		method:       m,
		workerName:   DefaultWorkerName,
		modelPref:    DefaultModelPreference,
		packages:     DefaultPackages(),
		maxRevisions: DefaultMaxRevisions,
		pollInterval: DefaultPollInterval,
		timeout:      DefaultTimeout,
		pollFailures: DefaultPollFailures,
		sleep:        wait.Sleep,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Start begins an audit of paper and returns without waiting for it.
func (a *Auditor) Start(ctx context.Context, paper string) (provider.RunHandle, error) {
	task, err := a.method.TaskFor(paper)
	if err != nil {
		return provider.RunHandle{}, err
	}
	model, err := a.provider.ResolveModel(ctx, a.modelPref)
	if err != nil {
		return provider.RunHandle{}, fmt.Errorf("resolve model: %w", err)
	}
	ws, err := a.provider.EnsureWorkspace(ctx, provider.WorkspaceSpec{
		Name:     a.workerName + "-workspace",
		Packages: a.packages,
		Network:  provider.NetworkOpen,
	})
	if err != nil {
		return provider.RunHandle{}, fmt.Errorf("ensure workspace: %w", err)
	}
	// With a repository mounted, the worker writes and commits into it; without
	// one, it writes to the backend's output directory as before.
	mount := a.mount
	outputDir := a.provider.OutputDir()
	if mount.Set() {
		if mount.MountPath == "" {
			mount.MountPath = DefaultMountPath
		}
		outputDir = mount.MountPath
	}
	instructions := a.method.InstructionsFor(outputDir)
	if mount.Set() {
		instructions += mountProtocol(mount.MountPath)
	}

	worker, err := a.provider.EnsureWorker(ctx, provider.WorkerSpec{
		Name:         a.workerName,
		Instructions: instructions,
		Model:        model,
		Description:  "Audits the reproducibility of a research paper.",
	})
	if err != nil {
		return provider.RunHandle{}, fmt.Errorf("ensure worker: %w", err)
	}
	h, err := a.provider.StartRun(ctx, worker, ws, provider.RunSpec{
		Task:         task,
		Rubric:       a.method.Rubric,
		MaxRevisions: a.maxRevisions,
		Mount:        mount,
	})
	if err != nil {
		return provider.RunHandle{}, fmt.Errorf("start run: %w", err)
	}
	return h, nil
}

// Wait polls until the run reaches a terminal state, the deadline passes, or
// the context is canceled. It always returns the last status it observed.
//
// Under WithoutTimeout there is no deadline: only the backend session settling,
// the caller cancelling, or the backend refusing to answer for pollFailures
// observations in a row ends the wait.
func (a *Auditor) Wait(ctx context.Context, h provider.RunHandle) (provider.RunStatus, error) {
	var deadline time.Time
	if !a.unbounded {
		deadline = time.Now().Add(a.timeout)
	}
	var last provider.RunStatus
	var failures int
	for {
		status, err := a.provider.Poll(ctx, h)
		if err != nil {
			// A backend that will not answer is not a session that failed. Retry
			// until the budget runs out, so one bad response does not fail a run
			// whose agent is working away on the other side.
			failures++
			if failures >= a.pollFailures || ctx.Err() != nil {
				return last, fmt.Errorf("poll run %s (%d consecutive failures): %w", h.ID, failures, err)
			}
			if err := a.sleep(ctx, a.pollInterval); err != nil {
				return last, err
			}
			continue
		}
		failures = 0
		last = status
		if a.observe != nil {
			a.observe(status)
		}
		if status.State.Terminal() {
			return status, nil
		}
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return status, fmt.Errorf("%w: run %s after %s (last state: %s)",
				ErrTimedOut, h.ID, a.timeout, status.State)
		}
		if err := a.sleep(ctx, a.pollInterval); err != nil {
			return status, err
		}
	}
}

// Collect downloads a finished run's artifacts into dst.
//
// Compiled residue is skipped: it is machine-specific, and a reproduction
// repository should hold sources only.
func (a *Auditor) Collect(ctx context.Context, h provider.RunHandle, dst string) ([]provider.Artifact, error) {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, fmt.Errorf("create destination: %w", err)
	}
	listed, err := a.provider.ListArtifacts(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	collected := make([]provider.Artifact, 0, len(listed))
	for _, art := range listed {
		if art.IsCompiled() {
			continue
		}
		target, err := safeJoin(dst, art.Path)
		if err != nil {
			return nil, err
		}
		body, err := a.provider.FetchArtifact(ctx, h, art)
		if err != nil {
			return nil, fmt.Errorf("read artifact %s: %w", art.Path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", target, err)
		}
		collected = append(collected, art)
	}
	return collected, nil
}

// Run performs one audit end to end: start, wait, collect, release.
func (a *Auditor) Run(ctx context.Context, paper, dst string) (Result, error) {
	h, err := a.Start(ctx, paper)
	if err != nil {
		return Result{Paper: paper}, err
	}
	return a.finish(ctx, h, paper, dst)
}

// Resume reconnects to an already-started run by its handle and drives it the
// rest of the way — wait, collect, release. It is how the worker reconciles a
// run left in flight by a restart: rather than guess the run failed, it rejoins
// the live backend session and reflects its real outcome (006b reconciliation).
func (a *Auditor) Resume(ctx context.Context, h provider.RunHandle, paper, dst string) (Result, error) {
	return a.finish(ctx, h, paper, dst)
}

// finish drives a started run to completion: it waits for a terminal state,
// collects the artifacts, and releases the run even on failure — release on a
// context detached from ctx so a cancelled audit still cleans up after itself.
func (a *Auditor) finish(ctx context.Context, h provider.RunHandle, paper, dst string) (result Result, err error) {
	result = Result{Paper: paper, Handle: h, ArtifactDir: dst, MethodDigest: a.method.Digest()}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if closeErr := a.provider.CloseRun(releaseCtx, h); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close run %s: %w", h.ID, closeErr))
		}
	}()

	status, waitErr := a.Wait(ctx, h)
	result.Status = status
	if waitErr != nil {
		return result, waitErr
	}
	artifacts, err := a.Collect(ctx, h, dst)
	if err != nil {
		return result, err
	}
	result.Artifacts = artifacts

	present, violations, err := validateInstrument(dst)
	if err != nil {
		return result, err
	}
	result.InstrumentPresent = present
	result.InstrumentViolations = violations
	return result, nil
}

// validateInstrument reads the collected instrument.json and checks it against
// the 007a schema. A missing file is reported as absence, not an error — that
// is criterion 7's job to fail in the grade-and-revise loop, and this is only a
// finalize-time report. Content that is not JSON at all is reported as one
// violation rather than failing the whole audit over a malformed file.
func validateInstrument(dir string) (present bool, violations []instrument.Violation, err error) {
	raw, err := os.ReadFile(filepath.Join(dir, InstrumentFile)) //nolint:gosec // G304: InstrumentFile is a package constant, and dir is the audit's own artifact destination (workDir/<job id> or the CLI's -out), never a name carried in a request
	if errors.Is(err, os.ErrNotExist) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("read %s: %w", InstrumentFile, err)
	}
	vs, verr := instrument.Validate(raw)
	if verr != nil {
		return true, []instrument.Violation{{Field: InstrumentFile, Message: verr.Error()}}, nil //nolint:nilerr // a malformed instrument is one violation to report, not a failed check
	}
	return true, vs, nil
}

// safeJoin resolves rel under root, refusing to escape it.
//
// Artifact paths come from a sandbox that has just executed code found on the
// internet, so they are untrusted: an absolute path or one containing ".."
// must not write outside root.
func safeJoin(root, rel string) (string, error) {
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, rel)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnsafePath, err)
	}
	target := filepath.Join(absRoot, rel)
	if target == absRoot || !strings.HasPrefix(target, absRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, rel)
	}
	return target, nil
}
