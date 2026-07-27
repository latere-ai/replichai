package job

import (
	"context"
	"fmt"
	"sync"
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
	// Close releases a run on the backend by its id (the managed-agents session),
	// so a run left in flight by a restart is stopped rather than orphaned. Safe
	// to call more than once and on an unknown id.
	Close(ctx context.Context, runID string) error
}

// Worker drives claimed jobs through the phases. A small pool of these is the
// whole queue: no external broker, durability from the row.
type Worker struct {
	store  Store
	runner Runner
	poll   time.Duration
	sleep  func(context.Context, time.Duration) error

	// running maps a session being driven here to the cancel func for its run
	// context, so Cancel can interrupt the agent mid-run (and stop the spend),
	// not just after the next phase boundary. One process, one replica.
	mu      sync.Mutex
	running map[string]context.CancelFunc
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
	w := &Worker{store: store, runner: runner, poll: time.Second, sleep: sleepCtx, running: map[string]context.CancelFunc{}}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Cancel stops a session: it interrupts the run if it is being driven here (so
// the agent stops and its spend with it), and marks the session cancelled in the
// store regardless — so a queued session, or one on no worker, still stops. The
// store write wins; the interrupted run's own failure write is refused by the
// state machine and discarded.
func (w *Worker) Cancel(ctx context.Context, id string) error {
	w.mu.Lock()
	cancel := w.running[id]
	w.mu.Unlock()
	if cancel != nil {
		cancel() // a run we drive: its deferred CloseRun archives the session
	}
	// Also close the backend run directly, in case this is an orphan we are not
	// driving (a run left in flight by a prior restart). Idempotent.
	if j, err := w.store.Get(ctx, id); err == nil && j.Status.RunID != "" {
		_ = w.runner.Close(ctx, j.Status.RunID)
	}
	_, err := w.store.Cancel(ctx, id)
	return err
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
		// Close the backend run this restart orphaned, so it stops rather than
		// keeps running (and spending) unreachable. Only possible if its id was
		// observed and persisted before the crash.
		cause := fmt.Sprintf("interrupted by a restart in %s", j.Phase)
		if j.Status.RunID != "" {
			_ = w.runner.Close(ctx, j.Status.RunID)
			cause = fmt.Sprintf("stopped: interrupted by a restart in %s; the agent session was closed", j.Phase)
		}
		if err := w.store.Advance(ctx, j.ID, PhaseFailed, func(jb *Job) { jb.Err = cause }); err != nil {
			return reaped, err
		}
		reaped++
	}
	return reaped, nil
}

// drive walks one claimed job (already in resolving) through the phases under a
// per-job context Cancel can interrupt. Any error moves it to failed with the
// cause recorded, never to done.
func (w *Worker) drive(pool context.Context, j Job) {
	ctx, cancel := context.WithCancel(pool)
	w.mu.Lock()
	w.running[j.ID] = cancel
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.running, j.ID)
		w.mu.Unlock()
		cancel()
	}()

	observe := func(s provider.RunStatus) { _ = w.store.Observe(ctx, j.ID, s) }

	if err := w.runner.Resolve(ctx, &j); err != nil {
		w.fail(j.ID, err)
		return
	}
	if err := w.store.Advance(ctx, j.ID, PhaseRunning, apply(j)); err != nil {
		w.fail(j.ID, err)
		return
	}
	if err := w.runner.Execute(ctx, &j, observe); err != nil {
		w.fail(j.ID, err)
		return
	}
	if err := w.store.Advance(ctx, j.ID, PhasePublishing, apply(j)); err != nil {
		w.fail(j.ID, err)
		return
	}
	if err := w.runner.Publish(ctx, &j); err != nil {
		w.fail(j.ID, err)
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

// fail records the cause and moves the job to failed, best-effort. It uses a
// fresh context so it can still write the terminal state after the job's own
// context was cancelled by a stop — otherwise a cancelled run would be left
// non-terminal. When the store already cancelled the job, this transition is
// refused by the state machine and harmlessly discarded.
func (w *Worker) fail(id string, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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
