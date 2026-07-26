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
	// DefaultMountPath is where a mounted repository lands when the caller does
	// not choose a path.
	DefaultMountPath = "/workspace/repro"
)

// mountProtocol tells the worker how to use a mounted repository: commit the
// reproduction into it and push, keeping third-party downloads out. This
// bounded workspace is what makes a live push safe — third-party data is never
// committed, so the publisher's gate can verify after the fact (006f).
func mountProtocol(mountPath string) string {
	return fmt.Sprintf(`

A git repository is mounted at %[1]s, and its commit history is published as part of the result. Treat it as the home of your reproduction:

- Write your reproduction files (README.md, REPRODUCIBILITY.md, src/, UNVERIFIED.md, SOURCES.md, instrument.json) inside %[1]s.
- Commit your progress with git as you finish each part — small, meaningful commits — and run "git push" so the history is preserved. If git refuses to commit for want of an identity, set one first with "git config user.email" and "git config user.name".
- Keep every download (the paper PDF, datasets, benchmarks, any third-party file) OUTSIDE %[1]s; use /tmp. Never "git add" or commit third-party data: the repository must contain only your own reproduction work.`, mountPath)
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
}

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
	return func(a *Auditor) { a.timeout = d }
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
		sleep:        sleepCtx,
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
func (a *Auditor) Wait(ctx context.Context, h provider.RunHandle) (provider.RunStatus, error) {
	deadline := time.Now().Add(a.timeout)
	var last provider.RunStatus
	for {
		status, err := a.provider.Poll(ctx, h)
		if err != nil {
			return last, fmt.Errorf("poll run %s: %w", h.ID, err)
		}
		last = status
		if a.observe != nil {
			a.observe(status)
		}
		if status.State.Terminal() {
			return status, nil
		}
		if !time.Now().Before(deadline) {
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
			return nil, fmt.Errorf("fetch %s: %w", art.Path, err)
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
//
// The run is released even when the audit fails, and release uses a context
// detached from ctx so a canceled audit still cleans up after itself.
func (a *Auditor) Run(ctx context.Context, paper, dst string) (Result, error) {
	h, err := a.Start(ctx, paper)
	if err != nil {
		return Result{Paper: paper}, err
	}
	result := Result{Paper: paper, Handle: h, ArtifactDir: dst, MethodDigest: a.method.Digest()}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_ = a.provider.CloseRun(releaseCtx, h)
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
	return result, nil
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
