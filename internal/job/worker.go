package job

import (
	"context"
	"fmt"
	"time"

	"github.com/latere-ai/replichai/internal/provider"
)

// Runner performs a job's actual work, phase by phase. The worker owns the
// lifecycle — claiming, advancing, recording progress and errors — and the
// Runner owns what happens inside each phase. The composition root supplies one
// that dispatches on Job.Kind to audit.Auditor or experiment.Runner, so this
// package imports neither the engine nor a provider adapter.
//
// Each method receives the job by pointer and may set fields on it (the resolved
// reference, the repository name, the latest status); the worker persists those
// with the phase transition that follows.
type Runner interface {
	// Resolve runs the resolving phase: paper.Resolve for an audit, or pinning an
	// instrument for an experiment.
	Resolve(ctx context.Context, j *Job) error
	// Execute runs the audit or experiment, reporting progress through observe.
	Execute(ctx context.Context, j *Job, observe func(provider.RunStatus)) error
	// Publish runs the publishing phase — 006f for an audit, a no-op for an
	// experiment, which has nothing to publish.
	Publish(ctx context.Context, j *Job) error
}

// Worker drives claimed jobs through the phases. A small pool of these is the
// whole queue: no external broker, durability from the row.
type Worker struct {
	store  Store
	runner Runner
	poll   time.Duration
	sleep  func(context.Context, time.Duration) error
}

// WorkerOption configures a Worker.
type WorkerOption func(*Worker)

// WithPoll sets how long a worker waits before looking for work again when the
// queue is empty.
func WithPoll(d time.Duration) WorkerOption { return func(w *Worker) { w.poll = d } }

// withWorkerSleep replaces the idle wait so tests run instantly.
func withWorkerSleep(f func(context.Context, time.Duration) error) WorkerOption {
	return func(w *Worker) { w.sleep = f }
}

// NewWorker builds a worker over a store and a runner.
func NewWorker(store Store, runner Runner, opts ...WorkerOption) *Worker {
	w := &Worker{store: store, runner: runner, poll: time.Second, sleep: sleepCtx}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Process claims one job and drives it to a terminal phase, reporting whether it
// found work. It is the unit the pool loop repeats, and the unit tests drive
// directly.
func (w *Worker) Process(ctx context.Context) (bool, error) {
	j, ok, err := w.store.Claim(ctx)
	if err != nil {
		return false, fmt.Errorf("claim: %w", err)
	}
	if !ok {
		return false, nil
	}
	w.drive(ctx, j)
	return true, nil
}

// Start runs a pool of workers until ctx is canceled. Each loops: process a job,
// or wait and look again when the queue is empty.
func (w *Worker) Start(ctx context.Context, workers int) {
	if workers < 1 {
		workers = 1
	}
	done := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for ctx.Err() == nil {
				did, err := w.Process(ctx)
				if err != nil || !did {
					if w.sleep(ctx, w.poll) != nil {
						return
					}
				}
			}
		}()
	}
	for i := 0; i < workers; i++ {
		<-done
	}
}

// Reap fails jobs a crash left mid-flight, so a restart does not leave them
// stranded. A beta reaps rather than retries; the row was never lost, which is
// the durability the table buys.
func (w *Worker) Reap(ctx context.Context) (int, error) {
	active, err := w.store.Active(ctx)
	if err != nil {
		return 0, fmt.Errorf("list active: %w", err)
	}
	reaped := 0
	for _, j := range active {
		if j.Phase == PhaseSubmitted {
			continue // still claimable; not stranded
		}
		cause := fmt.Sprintf("reaped: interrupted in %s", j.Phase)
		if err := w.store.Advance(ctx, j.ID, PhaseFailed, func(jb *Job) { jb.Err = cause }); err != nil {
			return reaped, err
		}
		reaped++
	}
	return reaped, nil
}

// drive walks one claimed job (already in resolving) through the phases. Any
// error moves it to failed with the cause recorded, never to done.
func (w *Worker) drive(ctx context.Context, j Job) {
	observe := func(s provider.RunStatus) { _ = w.store.Observe(ctx, j.ID, s) }

	if err := w.runner.Resolve(ctx, &j); err != nil {
		w.fail(ctx, j.ID, err)
		return
	}
	if err := w.store.Advance(ctx, j.ID, PhaseRunning, apply(j)); err != nil {
		w.fail(ctx, j.ID, err)
		return
	}
	if err := w.runner.Execute(ctx, &j, observe); err != nil {
		w.fail(ctx, j.ID, err)
		return
	}
	if err := w.store.Advance(ctx, j.ID, PhasePublishing, apply(j)); err != nil {
		w.fail(ctx, j.ID, err)
		return
	}
	if err := w.runner.Publish(ctx, &j); err != nil {
		w.fail(ctx, j.ID, err)
		return
	}
	_ = w.store.Advance(ctx, j.ID, PhaseDone, apply(j))
}

// apply copies the fields a Runner set on its local job onto the stored job at
// the next transition. Status is deliberately not among them: it is owned by the
// Observe path (progress is not a transition), and copying a stale local Status
// here would clobber the latest observation.
func apply(src Job) func(*Job) {
	return func(dst *Job) {
		dst.Ref = src.Ref
		dst.ProjectID = src.ProjectID
		dst.RepoName = src.RepoName
	}
}

// fail records the cause and moves the job to failed, best-effort.
func (w *Worker) fail(ctx context.Context, id string, cause error) {
	_ = w.store.Advance(ctx, id, PhaseFailed, func(j *Job) { j.Err = cause.Error() })
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
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
