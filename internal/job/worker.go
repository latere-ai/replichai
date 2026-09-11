// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"latere.ai/x/pkg/wait"

	"github.com/latere-ai/replichai/internal/managed"
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
	Close(ctx context.Context, ref managed.SessionRef) error
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

// LeaseTTL is how long a job stays leased to a driver that has stopped saying
// it is alive. A live driver heartbeats every job it is driving at a third of
// this, whether or not anything changed, so this bounds how long a *dead* driver
// keeps a run to itself — not how long a run may take, and not how long a run
// may stay quiet.
const LeaseTTL = 5 * time.Minute

// Worker drives claimed jobs through the phases. A small pool of these is the
// whole queue: no external broker, durability from the row.
type Worker struct {
	store  Store
	runner Runner
	poll   time.Duration
	sleep  func(context.Context, time.Duration) error
	budget Budget
	// sweepEvery is how often the pool looks again for stranded runs, and
	// leaseTTL is how long a quiet driver keeps one. The sweep runs at the lease,
	// so it never finds a lease it would have to wait long on.
	sweepEvery time.Duration
	leaseTTL   time.Duration
	// id names this process in the lease it takes on every job it drives. The
	// deployment runs more than one replica, so "the worker" is not one process
	// and a job has to record which one has it.
	id string

	// running maps a session being driven here to the cancel func for its run
	// context, so Cancel can interrupt the agent mid-run (and stop the spend),
	// not just after the next phase boundary. It covers the sessions *this*
	// process drives; the lease is what keeps another replica off them.
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

// WithWorkerID names this process in the leases it takes. Two workers in one
// test process share a hostname, so a test about two replicas has to say which
// is which.
func WithWorkerID(id string) WorkerOption { return func(w *Worker) { w.id = id } }

// defaultWorkerID names this process by its host, falling back to a random id
// when the host has no name to give.
func defaultWorkerID() string {
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	id := newID()
	return id
}

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
	if err := w.fail(id, errors.New(GenericFailure)); err != nil { //nolint:contextcheck // fail must use a fresh context on purpose: the terminal write goes in before interrupt cancels the run, and the pool context is dead on a shutdown
		log.Printf("budget: recording stopped session %s: %v", id, err)
		return
	}
	w.interrupt(id)
}

// driving reports whether this process is already driving a session.
func (w *Worker) driving(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.running[id]
	return ok
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

// withSweepEvery shortens the stranded-run sweep so a test can watch one happen.
func withSweepEvery(d time.Duration) WorkerOption {
	return func(w *Worker) { w.sweepEvery = d }
}

// withLeaseTTL shortens the lease, so a test can put a departed driver's hold in
// the state a sweep finds it in minutes later.
func withLeaseTTL(d time.Duration) WorkerOption {
	return func(w *Worker) { w.leaseTTL = d }
}

// withWorkerSleep replaces the idle wait so tests run instantly.
func withWorkerSleep(f func(context.Context, time.Duration) error) WorkerOption {
	return func(w *Worker) { w.sleep = f }
}

// NewWorker builds a worker over a store and a runner. Its identity defaults to
// the host it runs on, which in the deployment is the pod name: a lease is read
// by a person looking at a stuck session, and "replichai-6c786cc9c6-c577z" tells
// them which pod to pull logs from where a random id would not.
func NewWorker(store Store, runner Runner, opts ...WorkerOption) *Worker {
	w := &Worker{store: store, runner: runner, poll: time.Second, sleep: wait.Sleep,
		sweepEvery: LeaseTTL, leaseTTL: LeaseTTL, id: defaultWorkerID(), running: map[string]context.CancelFunc{}}
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
	if ref, ok, refErr := j.ManagedSessionRef(); refErr != nil {
		return fmt.Errorf("read backend session reference: %w", refErr)
	} else if ok {
		if err := w.call("close", func() error { return w.runner.Close(ctx, ref) }); err != nil {
			return fmt.Errorf("stop backend run %s: %w", ref.ID, err)
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
	j, ok, err := w.store.Claim(ctx, w.id)
	if err != nil {
		return false, fmt.Errorf("claim: %w", err)
	}
	if !ok {
		return false, nil
	}
	return true, w.drive(ctx, j)
}

// Start runs a pool of workers until ctx is canceled. Each loops: process a job,
// or wait and look again when the queue is empty. Alongside them it sweeps for
// stranded runs.
func (w *Worker) Start(ctx context.Context, workers int) {
	if workers < 1 {
		workers = 1
	}
	go w.sweep(ctx)
	go w.heartbeat(ctx)
	done := make(chan struct{})
	for range workers {
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
	for range workers {
		<-done
	}
}

// sweep re-runs reconciliation for as long as the pool lives, so a run whose
// driver died is picked up by whoever is still standing.
//
// Reconciling only at startup was enough while every replica adopted every
// stranded run: the pod that came up during a deploy took over the outgoing
// pod's work — which is exactly the double-drive the lease now prevents. With
// adoption exclusive, the incoming pod correctly declines a run the outgoing one
// still holds, and if nothing looked again the run would sit in `running` until
// the next deploy. This is that second look. The interval is the lease, so a
// sweep never finds a lease it must wait on for long.
func (w *Worker) sweep(ctx context.Context) {
	tick := time.NewTicker(w.sweepEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		resumed, _, err := w.reconcile(ctx, false)
		if err != nil {
			log.Printf("worker: sweep: %v", err)
		}
		if resumed > 0 {
			log.Printf("worker: sweep adopted %d stranded run(s)", resumed)
		}
	}
}

// heartbeat keeps the lease fresh on every job this process is driving, and
// stops driving one it has lost.
//
// The lease was refreshed only by an observation, and the managed controller
// emits one only when the observed status *changed*. A run that is thinking
// reports nothing for minutes at a time, so a healthy driver looked exactly like
// a dead one, and a replica beside it adopted a live session — two drivers on
// one backend session, both finalizing it, the loser recording a failure over
// the winner's published success (044c). A heartbeat separates *alive* from
// *changed*, which is what the lease meant all along.
//
// It runs here rather than in the controller on purpose: w.running already names
// every job this process drives, so no hook has to be threaded through the
// session contract, and every phase is covered — including the quiet resolve
// that reconcile's own comment worries about and that no observation has ever
// reached.
func (w *Worker) heartbeat(ctx context.Context) {
	every := w.leaseTTL / 3
	if every <= 0 {
		every = time.Second
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		w.beat(ctx)
	}
}

// beat refreshes one round of leases. A job this worker no longer holds is one
// another replica has adopted, so this one yields it: interrupting its own run
// is what keeps a wrongful adoption from becoming two drivers racing to publish.
// A store error is left alone — the next tick is soon, and a transient database
// blip must not cancel a healthy run.
//
// A row that went terminal reports the same held=false and means something
// else entirely: a run that just finished, in the moment between its own final
// transition and the driver deregistering. That is every successful run, so it
// is passed over silently — logging it would put "yielding session X" in the log
// after each success, and the one time that line matters is during an incident.
func (w *Worker) beat(ctx context.Context) {
	for _, id := range w.drivingIDs() {
		held, err := w.store.Touch(ctx, id, w.id)
		if err != nil {
			log.Printf("worker: heartbeat %s: %v", id, err)
			continue
		}
		if held {
			continue
		}
		if j, err := w.store.Get(ctx, id); err == nil && j.Phase.Terminal() {
			continue // finished under us; drive is already unwinding
		}
		log.Printf("worker: yielding session %s: its lease is held elsewhere", id)
		w.interrupt(id)
	}
}

// drivingIDs snapshots the sessions this process is driving, so the heartbeat
// touches the store without holding the lock that Cancel and interrupt need.
func (w *Worker) drivingIDs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	ids := make([]string, 0, len(w.running))
	for id := range w.running {
		ids = append(ids, id)
	}
	return ids
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
	return w.reconcile(ctx, true)
}

// reconcile is Reconcile with one thing made explicit: whether a job with no run
// to rejoin may be failed.
//
// It may at startup, where "nothing in this process is mid-anything" is true by
// construction. It may not on a sweep, which runs beside a live pool: a session
// this process is not driving may still be resolving on the other replica, and
// resolving is quiet — the paper fetch and the repository creation report no
// status, so a slow one lets its lease go cold while it is working perfectly
// well. Failing it there would kill a healthy session, which is the failure the
// lease exists to prevent, arriving by another door. A sweep therefore only ever
// picks up what it can positively identify as rejoinable: a run with a backend
// session id behind it.
func (w *Worker) reconcile(ctx context.Context, strand bool) (resumed, failed int, err error) {
	active, err := w.store.Active(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("list active: %w", err)
	}
	for _, j := range active {
		if j.Phase == PhaseSubmitted {
			continue // still claimable; not stranded
		}
		if w.driving(j.ID) {
			continue // this process is already on it; a sweep must not re-drive it
		}
		rejoinable := j.Status.RunID != "" && (j.Phase == PhaseRunning || j.Phase == PhasePublishing)
		if !rejoinable && !strand {
			continue // not this sweep's to judge — and not even its lease to take
		}
		// Take the row before touching it. Every replica reconciles at startup,
		// and a rolling deploy overlaps a starting pod with one still driving, so
		// without this each of them rejoins the same backend session: two drivers
		// poll it, close it and publish it, and the one that loses the race
		// records a failure over the other's success.
		mine, err := w.store.Adopt(ctx, j.ID, w.id, w.leaseTTL)
		if err != nil {
			return resumed, failed, fmt.Errorf("adopt session %s: %w", j.ID, err)
		}
		if !mine {
			continue // another worker has it
		}
		if j.Phase == PhaseRunning && j.Status.RunID != "" {
			w.spawn(j.ID, func() error { return w.driveResume(ctx, j) }) // rejoin the live backend session
			resumed++
			continue
		}
		if j.Phase == PhasePublishing && j.Status.RunID != "" {
			w.spawn(j.ID, func() error { return w.drivePublish(ctx, j) }) // the run settled; finish finalizing it
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

// call runs one step of a Runner and turns a panic into an error. The runner is
// the composition root's code driving a third-party engine, and a panic there
// is one job's failure, not the process's: an unrecovered panic in a worker
// goroutine takes the whole server down, and if the job that caused it is
// still in an active phase the next boot picks it up and panics again. Every
// call into the runner goes through here, so no phase can be the exception.
func (w *Worker) call(step string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: runner panicked: %v\n%s", step, r, debug.Stack())
		}
	}()
	return fn()
}

// spawn starts background work that must not be able to end the process. The
// reconciler's rejoins run here: a panic in one is logged and the job it was
// driving is failed, and the pool keeps serving.
func (w *Worker) spawn(id string, fn func() error) {
	go func() {
		err := w.call("background drive", fn)
		if err != nil {
			log.Printf("worker: session %s: %v", id, err)
		}
	}()
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

	if err := w.call("resolve", func() error { return w.runner.Resolve(ctx, &j) }); err != nil {
		return w.stop(pool, j.ID, err)
	}
	if err := w.store.Advance(ctx, j.ID, PhaseRunning, apply(j)); err != nil {
		return w.stop(pool, j.ID, err)
	}
	runErr := w.call("execute", func() error { return w.runner.Execute(ctx, &j, observe) })
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
	runErr := w.call("resume", func() error { return w.runner.Resume(ctx, &j, observe) })
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

	if err := w.call("publish", func() error { return w.runner.Publish(ctx, &j) }); err != nil {
		if errors.Is(err, ErrRetryablePublish) {
			return err
		}
		return w.stop(pool, j.ID, err)
	}
	return w.complete(ctx, j)
}

// publish moves a job whose run has settled through publishing to done.
func (w *Worker) publish(pool, ctx context.Context, j Job) error {
	if err := w.store.Advance(ctx, j.ID, PhasePublishing, apply(j)); err != nil {
		return w.stop(pool, j.ID, err)
	}
	if err := w.call("publish", func() error { return w.runner.Publish(ctx, &j) }); err != nil {
		if errors.Is(err, ErrRetryablePublish) {
			return err
		}
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
		return nil //nolint:nilerr // a shutdown is not a failed session: leaving the row in phase is what lets the next boot rejoin it
	}
	if err := w.fail(id, cause); err != nil { //nolint:contextcheck // fail must use a fresh context on purpose: pool can die between the guard above and the write, and a cancelled job's context is already dead, yet both must still land terminal
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
