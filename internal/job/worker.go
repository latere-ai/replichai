package job

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	// Resume reconnects to a run already in flight (by the id observed onto the
	// job) and drives it to completion — how a restart rejoins a live backend
	// session instead of guessing it failed.
	Resume(ctx context.Context, j *Job, observe func(provider.RunStatus)) error
	// Publish runs the publishing phase — 006f for an audit, a no-op for an
	// experiment, which has nothing to publish.
	Publish(ctx context.Context, j *Job) error
	// Close releases a run on the backend by its id (the managed-agents session),
	// so a run left in flight by a restart is stopped rather than orphaned. Safe
	// to call more than once and on an unknown id.
	Close(ctx context.Context, runID string) error
}

// Budget answers whether a session must be stopped for spend (011). It is
// injected because the answer depends on policy and on the ledger, neither of
// which this package imports; nil means no backstop, which is what an offline
// unmetered run wants.
//
// The string it returns is for the **log and the admin console only**. It names
// the number that fired, and that number is not published: an overdraft
// somebody can read is an allowance they plan around. What reaches the session
// owner is GenericFailure.
type Budget func(ctx context.Context, j Job) (stop bool, why string, err error)

// GenericFailure is what a session records when the real cause must not reach
// its owner. It is true — the session did not complete — and it withholds the
// reason rather than inventing one. The reason is logged, and an admin can read
// it; a project's own members cannot, because it would tell them exactly how
// far past their balance a run is allowed to go.
const GenericFailure = "the session could not be completed"

// Worker drives claimed jobs through the phases. A small pool of these is the
// whole queue: no external broker, durability from the row.
type Worker struct {
	store  Store
	runner Runner
	poll   time.Duration
	sleep  func(context.Context, time.Duration) error
	budget Budget

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

// WithBudget installs the spend backstop (011). Without it a run is never
// stopped for money — which is right for an offline run and wrong for a
// deployment holding a real credential.
func WithBudget(b Budget) WorkerOption { return func(w *Worker) { w.budget = b } }

// watch is the backstop, run on every observation because that is the only
// moment fresh usage exists. A session runs to completion in the ordinary case
// — killing one mid-run turns spend into nothing — so this fires only for a
// runaway, and its job is to bound the loss rather than to meter the session.
//
// The order matters. The terminal write goes first, so the outcome is durable
// even if the process dies immediately after; interrupting the run then stops
// the agent, and the failure write that interruption provokes is refused by the
// state machine and discarded — the same rule Cancel already relies on.
func (w *Worker) watch(pool context.Context, id string) {
	if w.budget == nil {
		return
	}
	j, err := w.store.Get(pool, id)
	if err != nil {
		return
	}
	stop, why, err := w.budget(pool, j)
	if err != nil {
		log.Printf("budget: checking session %s: %v", id, err)
		return
	}
	if !stop {
		return
	}
	// Logged here and nowhere a member can read: the row records only that the
	// session did not complete.
	log.Printf("budget: stopping session %s in project %s: %s", j.ID, j.ProjectID, why)
	if err := w.fail(id, errors.New(GenericFailure)); err != nil {
		log.Printf("budget: recording stopped session %s: %v", id, err)
		return
	}
	w.interrupt(id)
}

// interrupt cancels the run context of a session this worker is driving, so the
// agent stops rather than running on after the row went terminal.
func (w *Worker) interrupt(id string) {
	w.mu.Lock()
	cancel := w.running[id]
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

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
	j, err := w.store.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("read session before cancel: %w", err)
	}
	if j.Phase.Terminal() {
		return nil
	}
	// Stop the durable backend handle before making the row terminal. If this
	// fails, the active row is the retry handle; cancelling it locally first
	// would report success while making the still-live remote run unreachable.
	if j.Status.RunID != "" {
		if err := w.runner.Close(ctx, j.Status.RunID); err != nil {
			return fmt.Errorf("stop backend run %s: %w", j.Status.RunID, err)
		}
	}
	// Persist the terminal cancellation before interrupting the local waiter, so
	// the waiter's context error cannot win the race and replace "cancelled".
	if _, err = w.store.Cancel(ctx, id); err != nil {
		return err
	}
	w.interrupt(id)
	return nil
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
	return true, w.drive(ctx, j)
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
				if err != nil {
					log.Printf("worker: process: %v", err)
				}
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

// Reconcile brings jobs left in flight by a restart back in line with the
// backend, rather than guessing they failed. A run whose backend session id was
// observed is *rejoined* — resumed and driven to completion, reflecting the
// backend's real outcome — so a session still running on the backend keeps being
// tracked instead of being marked failed while it burns on. One interrupted
// mid-publish is re-published: the session already settled, and finalizing is
// idempotent, so re-running it is cheaper and truer than failing a reproduction
// that succeeded. A job with no run to rejoin (it never reached the backend, or
// its id was never recorded) cannot be synced and is failed. It returns how many
// were resumed and how many failed.
func (w *Worker) Reconcile(ctx context.Context) (resumed, failed int, err error) {
	active, err := w.store.Active(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("list active: %w", err)
	}
	for _, j := range active {
		if j.Phase == PhaseSubmitted {
			continue // still claimable; not stranded
		}
		if j.Phase == PhaseRunning && j.Status.RunID != "" {
			go func(j Job) {
				if err := w.driveResume(ctx, j); err != nil {
					log.Printf("worker: resume session %s: %v", j.ID, err)
				}
			}(j) // rejoin the live backend session
			resumed++
			continue
		}
		if j.Phase == PhasePublishing && j.Status.RunID != "" {
			go func(j Job) {
				if err := w.drivePublish(ctx, j); err != nil {
					log.Printf("worker: republish session %s: %v", j.ID, err)
				}
			}(j) // the run settled; finish finalizing it
			resumed++
			continue
		}
		cause := fmt.Sprintf("interrupted by a restart in %s; no live run to rejoin", j.Phase)
		if aErr := w.store.Advance(ctx, j.ID, PhaseFailed, func(jb *Job) { jb.Err = cause }); aErr != nil {
			return resumed, failed, aErr
		}
		failed++
	}
	return resumed, failed, nil
}

// track registers a job as being driven here under a per-job context Cancel can
// interrupt, and returns that context with the deregistration.
func (w *Worker) track(pool context.Context, id string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(pool)
	w.mu.Lock()
	w.running[id] = cancel
	w.mu.Unlock()
	return ctx, func() {
		w.mu.Lock()
		delete(w.running, id)
		w.mu.Unlock()
		cancel()
	}
}

// drive walks one claimed job (already in resolving) through the phases under a
// per-job context Cancel can interrupt. Any error moves it to failed with the
// cause recorded, never to done — except a shutdown, which leaves the job where
// it is for the next boot to reconcile.
func (w *Worker) drive(pool context.Context, j Job) error {
	ctx, done := w.track(pool, j.ID)
	defer done()

	var observeMu sync.Mutex
	var observeErr error
	observe := func(s provider.RunStatus) {
		if err := w.store.Observe(ctx, j.ID, s); err != nil {
			observeMu.Lock()
			if observeErr == nil {
				observeErr = fmt.Errorf("record run status: %w", err)
			}
			observeMu.Unlock()
			w.interrupt(j.ID)
			return
		}
		w.watch(pool, j.ID)
	}

	if err := w.runner.Resolve(ctx, &j); err != nil {
		return w.stop(pool, j.ID, err)
	}
	if err := w.store.Advance(ctx, j.ID, PhaseRunning, apply(j)); err != nil {
		return w.stop(pool, j.ID, err)
	}
	runErr := w.runner.Execute(ctx, &j, observe)
	observeMu.Lock()
	statusErr := observeErr
	observeMu.Unlock()
	if statusErr != nil {
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			statusErr = errors.Join(statusErr, runErr)
		}
		return w.stop(pool, j.ID, statusErr)
	}
	if runErr != nil {
		return w.stop(pool, j.ID, runErr)
	}
	return w.publish(pool, ctx, j)
}

// driveResume rejoins a run already in flight (in the running phase) and drives
// it the rest of the way — resume, then publish, then done — under a per-job
// context Cancel can interrupt, exactly like a fresh drive minus the phases
// already passed.
func (w *Worker) driveResume(pool context.Context, j Job) error {
	ctx, done := w.track(pool, j.ID)
	defer done()

	var observeMu sync.Mutex
	var observeErr error
	observe := func(s provider.RunStatus) {
		if err := w.store.Observe(ctx, j.ID, s); err != nil {
			observeMu.Lock()
			if observeErr == nil {
				observeErr = fmt.Errorf("record resumed run status: %w", err)
			}
			observeMu.Unlock()
			w.interrupt(j.ID)
			return
		}
		w.watch(pool, j.ID)
	}
	runErr := w.runner.Resume(ctx, &j, observe)
	observeMu.Lock()
	statusErr := observeErr
	observeMu.Unlock()
	if statusErr != nil {
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			statusErr = errors.Join(statusErr, runErr)
		}
		return w.stop(pool, j.ID, statusErr)
	}
	if runErr != nil {
		return w.stop(pool, j.ID, runErr)
	}
	return w.publish(pool, ctx, j)
}

// drivePublish re-runs the publishing phase for a job a restart interrupted
// after its run had already settled. Finalizing is idempotent — an unchanged
// manifest adds no commit — so re-running it costs a few reads and recovers a
// reproduction that succeeded, where failing the row would have stranded it.
func (w *Worker) drivePublish(pool context.Context, j Job) error {
	ctx, done := w.track(pool, j.ID)
	defer done()

	if err := w.runner.Publish(ctx, &j); err != nil {
		return w.stop(pool, j.ID, err)
	}
	return w.complete(ctx, j)
}

// publish moves a job whose run has settled through publishing to done.
func (w *Worker) publish(pool, ctx context.Context, j Job) error {
	if err := w.store.Advance(ctx, j.ID, PhasePublishing, apply(j)); err != nil {
		return w.stop(pool, j.ID, err)
	}
	if err := w.runner.Publish(ctx, &j); err != nil {
		return w.stop(pool, j.ID, err)
	}
	return w.complete(ctx, j)
}

// complete records the terminal success after publishing. Publishing is
// already complete and idempotent, so retry only the durable transition; if all
// attempts fail, leave the row in publishing for restart reconciliation.
func (w *Worker) complete(ctx context.Context, j Job) error {
	var err error
	for range 3 {
		if err = w.store.Advance(ctx, j.ID, PhaseDone, apply(j)); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return fmt.Errorf("record session %s done: %w", j.ID, err)
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

// stop ends the drive of one job: it fails the row, unless the pool itself is
// shutting down.
//
// A shutdown is not a failed session. The pool context is cancelled on SIGTERM,
// which surfaces inside the run as an ordinary error; writing failed for it
// would mark a live backend session dead on every deploy and node drain — and
// because failed is terminal, Reconcile could never rescue it. Leaving the row
// in its phase is what lets the next boot rejoin the session and reflect its
// real outcome. A person cancelling a session takes the other path: only that
// job's own context is cancelled, the pool's is not, so the row is still failed
// here — and the store's cancel write, made first, wins the state machine.
func (w *Worker) stop(pool context.Context, id string, cause error) error {
	if pool.Err() != nil {
		return nil
	}
	if err := w.fail(id, cause); err != nil {
		return errors.Join(cause, err)
	}
	return nil
}

// fail records the cause and moves the job to failed, best-effort. It uses a
// fresh context so it can still write the terminal state after the job's own
// context was cancelled by a stop — otherwise a cancelled run would be left
// non-terminal. When the store already cancelled the job, this transition is
// refused by the state machine and harmlessly discarded.
func (w *Worker) fail(id string, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := w.store.Advance(ctx, id, PhaseFailed, func(j *Job) { j.Err = cause.Error() })
	if errors.Is(err, ErrIllegalTransition) {
		if j, getErr := w.store.Get(ctx, id); getErr == nil && j.Phase.Terminal() {
			return nil // cancellation/backstop won the terminal transition race
		}
	}
	if err != nil {
		return fmt.Errorf("record session %s failed: %w", id, err)
	}
	return nil
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
