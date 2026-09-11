// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/provider"
)

// fakeRunner records which phase methods ran and can be made to fail at one.
type fakeRunner struct {
	mu          sync.Mutex
	calls       []string
	failAt      string
	repo        string
	resolveRepo string // when set, Resolve sets it on the job (the 006i handoff)
	publishRepo string // the RepoName Publish observed, showing the handoff survived
	publishErr  error
	observed    provider.RunStatus
	closed      []string // run ids Close was called with
	closeErr    error
	onExecute   func() // fires inside Execute; stands in for a signal landing mid-run
	// runExecute, when set, is Execute's whole body after the first observation.
	// A run that holds here is a quiet one: it reports nothing further, which is
	// what a real agent does while it is thinking.
	runExecute func(context.Context) error
	onResume   func() // fires inside Resume; holds a rejoined run open while a test looks
}

func (f *fakeRunner) record(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeRunner) Resolve(_ context.Context, j *Job) error {
	f.record("resolve")
	if f.resolveRepo != "" {
		j.RepoName = f.resolveRepo // set here, must be durable by the time Publish runs
	}
	if f.failAt == "resolve" {
		return errors.New("resolve boom")
	}
	return nil
}

func (f *fakeRunner) Execute(ctx context.Context, j *Job, observe func(provider.RunStatus)) error {
	f.record("execute")
	observe(f.observed)
	if f.onExecute != nil {
		f.onExecute()
	}
	if f.runExecute != nil {
		return f.runExecute(ctx)
	}
	if f.failAt == "execute" {
		return errors.New("execute boom")
	}
	return nil
}

func (f *fakeRunner) Resume(_ context.Context, j *Job, observe func(provider.RunStatus)) error {
	f.record("resume")
	observe(f.observed)
	if f.onResume != nil {
		f.onResume()
	}
	if f.failAt == "resume" {
		return errors.New("resume boom")
	}
	return nil
}

func (f *fakeRunner) Publish(_ context.Context, j *Job) error {
	f.record("publish")
	f.mu.Lock()
	f.publishRepo = j.RepoName // capture what the phase handoff delivered
	f.mu.Unlock()
	if f.repo != "" {
		j.RepoName = f.repo
	}
	if f.failAt == "publish" {
		return errors.New("publish boom")
	}
	return f.publishErr
}

func (f *fakeRunner) Close(_ context.Context, ref managed.SessionRef) error {
	f.mu.Lock()
	f.closed = append(f.closed, ref.ID)
	f.mu.Unlock()
	return f.closeErr
}

func drained(t *testing.T, w *Worker) {
	t.Helper()
	for {
		did, err := w.Process(context.Background())
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if !did {
			return
		}
	}
}

// TestBothKindsWalkTheSameLifecycle checks AC7: both kinds move through the
// same phases via the same transition function, and only the callee differs.
// There is no second state machine.
func TestBothKindsWalkTheSameLifecycle(t *testing.T) {
	for _, k := range []Kind{KindAudit, KindExperiment} {
		t.Run(string(k), func(t *testing.T) {
			s := NewMemStore()
			r := &fakeRunner{repo: "open-hai/x"}
			j, _ := s.Enqueue(context.Background(), Submission{Principal: "sub", Ref: "ref", Kind: k, Project: "proj"})

			drained(t, NewWorker(s, r))

			got, _ := s.Get(context.Background(), j.ID)
			if got.Phase != PhaseDone {
				t.Fatalf("kind %s ended in %s, want done", k, got.Phase)
			}
			if got.RepoName != "open-hai/x" {
				t.Errorf("the runner's repo name was not persisted: %q", got.RepoName)
			}
			// Every phase was recorded, and the work ran in order.
			for _, p := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
				if got.PhaseAt[p].IsZero() {
					t.Errorf("phase %s not recorded for kind %s", p, k)
				}
			}
			if want := []string{"resolve", "execute", "publish"}; !equal(r.calls, want) {
				t.Errorf("work order = %v, want %v", r.calls, want)
			}
		})
	}
}

// TestRepoNameFromResolveReachesPublish checks 006i: a RepoName set in Resolve
// is persisted at the transition into running, because apply copies it, and is
// therefore present by the time Publish runs. The server depends on this
// handoff to create the repository in resolve and finalize it in publish.
func TestRepoNameFromResolveReachesPublish(t *testing.T) {
	s := NewMemStore()
	r := &fakeRunner{resolveRepo: "open-hai/created-in-resolve"} // repo stays "" so Publish does not overwrite it
	j, _ := s.Enqueue(context.Background(), Submission{Principal: "sub", Ref: "ref", Kind: KindAudit, Project: "p"})

	drained(t, NewWorker(s, r))

	r.mu.Lock()
	saw := r.publishRepo
	r.mu.Unlock()
	if saw != "open-hai/created-in-resolve" {
		t.Errorf("Publish saw RepoName %q, want the name Resolve set", saw)
	}
	if got, _ := s.Get(context.Background(), j.ID); got.RepoName != "open-hai/created-in-resolve" {
		t.Errorf("stored RepoName = %q, want it durable from Resolve", got.RepoName)
	}
}

// TestProgressIsObservedOntoTheJob checks AC5: the latest run status is
// recorded on the job as the run observes it, and is readable afterwards. This
// is the store-level half of letting a late reader see where the run is; the
// SSE snapshot is 006a.
func TestProgressIsObservedOntoTheJob(t *testing.T) {
	s := NewMemStore()
	r := &fakeRunner{observed: provider.RunStatus{State: provider.StateRunning, Usage: provider.Usage{InputTokens: 42}}}
	j, _ := s.Enqueue(context.Background(), Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})

	drained(t, NewWorker(s, r))

	got, _ := s.Get(context.Background(), j.ID)
	if got.Status.Usage.InputTokens != 42 {
		t.Errorf("observed usage was not recorded on the job: %+v", got.Status)
	}
}

// AC3 at the worker: a failure in any phase ends the job failed, never done.
func TestWorkerFailurePhases(t *testing.T) {
	for _, at := range []string{"resolve", "execute", "publish"} {
		t.Run(at, func(t *testing.T) {
			s := NewMemStore()
			j, _ := s.Enqueue(context.Background(), Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
			drained(t, NewWorker(s, &fakeRunner{failAt: at}))

			got, _ := s.Get(context.Background(), j.ID)
			if got.Phase != PhaseFailed {
				t.Fatalf("failure at %s left phase %s, want failed", at, got.Phase)
			}
			if got.Err == "" {
				t.Error("a failure must record its cause")
			}
		})
	}
}

// TestReconcileFailsUnrecoverable checks that a job left mid-run with no live
// backend session to rejoin, because its run id was never observed, cannot be
// synchronised and is failed by reconcile. A fresh submitted job is left
// claimable.
func TestReconcileFailsUnrecoverable(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	stranded, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	if _, ok, _ := s.Claim(ctx, "test-worker"); !ok { // -> resolving, no run id
		t.Fatal("claim should have taken the job")
	}
	fresh, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref2", Kind: KindAudit})

	resumed, failed, err := NewWorker(s, &fakeRunner{}, WithWorkerID("test-worker")).Reconcile(ctx)
	if err != nil || resumed != 0 || failed != 1 {
		t.Fatalf("Reconcile = resumed %d failed %d err %v; want 0/1/nil", resumed, failed, err)
	}
	if got, _ := s.Get(ctx, stranded.ID); got.Phase != PhaseFailed || got.Err == "" {
		t.Errorf("unrecoverable job should be failed with a cause, got %s/%q", got.Phase, got.Err)
	}
	if got, _ := s.Get(ctx, fresh.ID); got.Phase != PhaseSubmitted {
		t.Errorf("a fresh submitted job must not be touched, got %s", got.Phase)
	}
}

// TestReconcileLeavesARunAnotherWorkerIsDriving checks that exactly one process
// may drive a run. A deploy overlaps a pod coming up with one still working, and
// both reconcile. Without a lease each rejoined the same backend session and
// the two drivers collided: one closed the session the other was still using,
// and the failure it recorded overwrote the success the other had just
// published.
func TestReconcileLeavesARunAnotherWorkerIsDriving(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	_, _, _ = s.Claim(ctx, "pod-a")
	_ = s.Advance(ctx, j.ID, PhaseRunning, nil)
	_ = s.Observe(ctx, j.ID, provider.RunStatus{RunID: "sesn_live_01"})

	held := make(chan struct{})
	defer close(held)
	first := &fakeRunner{onResume: func() { <-held }} // pod-a stays inside the run
	if resumed, _, err := NewWorker(s, first, WithWorkerID("pod-a")).Reconcile(ctx); err != nil || resumed != 1 {
		t.Fatalf("pod-a Reconcile = %d/%v, want it to adopt the run", resumed, err)
	}
	waitForCall(t, first, "resume")

	second := &fakeRunner{}
	resumed, failed, err := NewWorker(s, second, WithWorkerID("pod-b")).Reconcile(ctx)
	if err != nil || resumed != 0 || failed != 0 {
		t.Fatalf("pod-b Reconcile = %d/%d/%v, want 0/0/nil — the run is not its to take", resumed, failed, err)
	}
	second.mu.Lock()
	defer second.mu.Unlock()
	if len(second.calls) != 0 {
		t.Errorf("pod-b drove a run pod-a holds: %v", second.calls)
	}
}

// TestThePoolSweepsForStrandedRuns checks that the pool looks again for
// stranded runs. Reconciling only at startup was enough while every replica
// took every stranded run. Now that a lease stops the incoming pod taking one
// the outgoing pod still holds, something has to look again once that lease
// expires; otherwise a deploy mid-run leaves the session in running until the
// next deploy.
func TestThePoolSweepsForStrandedRuns(t *testing.T) {
	s := NewMemStore()
	ctx := t.Context()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	_, _, _ = s.Claim(ctx, "pod-gone") // the pod that had it is not coming back
	_ = s.Advance(ctx, j.ID, PhaseRunning, nil)
	_ = s.Observe(ctx, j.ID, provider.RunStatus{RunID: "sesn_stranded_01"})

	r := &fakeRunner{}
	// A zero lease expires the departed pod's hold immediately, which is the
	// state a sweep finds it in five minutes later in the deployment.
	w := NewWorker(s, r, WithWorkerID("pod-here"), WithPoll(time.Millisecond),
		withSweepEvery(time.Millisecond), withLeaseTTL(0))
	go w.Start(ctx, 1)

	waitForPhase(t, s, j.ID, PhaseDone)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !slices.Contains(r.calls, "resume") {
		t.Errorf("the sweep should have rejoined the stranded run, calls=%v", r.calls)
	}
}

// TestASweepDoesNotFailASessionItCannotRejoin checks that a sweep leaves alone
// what it cannot positively rejoin. Resolving is quiet: the paper fetch and the
// repository creation report no status, so a slow one lets its lease expire
// while it is working. A sweep runs beside a live pool and cannot distinguish
// that from a dead driver. The startup pass, when nothing is in progress, is
// the only place a session is judged unrecoverable.
func TestASweepDoesNotFailASessionItCannotRejoin(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	_, _, _ = s.Claim(ctx, "pod-a") // -> resolving on the other replica, no run id yet

	// Lease expired: the other pod has been resolving for longer than the ttl.
	w := NewWorker(s, &fakeRunner{}, WithWorkerID("pod-b"), withLeaseTTL(0))
	resumed, failed, err := w.reconcile(ctx, false)
	if err != nil || resumed != 0 || failed != 0 {
		t.Fatalf("sweep = %d/%d/%v, want 0/0/nil", resumed, failed, err)
	}
	if got, _ := s.Get(ctx, j.ID); got.Phase != PhaseResolving {
		t.Errorf("phase = %s, want resolving — a sweep failed a session that was working", got.Phase)
	}
	// It did not take the lease either: it must not hold a row it will not drive.
	if won, _ := s.Adopt(ctx, j.ID, "pod-a", time.Minute); !won {
		t.Error("the sweep took the lease on a row it left alone")
	}
}

// TestReconcileLeavesAFreshlyClaimedSessionAlone checks that the claim leases
// too, so a session another pod took seconds ago is not read as stranded.
// Nothing has been observed onto it yet, so it has no run id, which is exactly
// the shape reconciliation fails as unrecoverable, and failing it would stop a
// session that is resolving correctly on the pod that claimed it.
func TestReconcileLeavesAFreshlyClaimedSessionAlone(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	if _, ok, _ := s.Claim(ctx, "pod-a"); !ok { // -> resolving, no run id yet
		t.Fatal("claim should have taken the job")
	}

	resumed, failed, err := NewWorker(s, &fakeRunner{}, WithWorkerID("pod-b")).Reconcile(ctx)
	if err != nil || resumed != 0 || failed != 0 {
		t.Fatalf("pod-b Reconcile = %d/%d/%v, want 0/0/nil", resumed, failed, err)
	}
	if got, _ := s.Get(ctx, j.ID); got.Phase != PhaseResolving {
		t.Errorf("phase = %s, want resolving — pod-b failed a session pod-a is driving", got.Phase)
	}
}

// waitForCall blocks until the runner has recorded a call, so a test can look at
// the store while a driver is inside one.
func waitForCall(t *testing.T, r *fakeRunner, call string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		got := slices.Contains(r.calls, call)
		r.mu.Unlock()
		if got {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("runner never reached %s", call)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestReconcileRejoinsLiveRun checks that a run whose backend session id was
// observed is rejoined on restart, not failed: reconcile resumes it and drives
// it to done, reflecting the live session rather than assuming it failed while
// it kept running.
func TestReconcileRejoinsLiveRun(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	_, _, _ = s.Claim(ctx, "test-worker")                                                   // -> resolving
	_ = s.Advance(ctx, j.ID, PhaseRunning, func(jb *Job) { jb.RepoName = "open-hai/live" }) // running, repo created pre-crash
	_ = s.Observe(ctx, j.ID, provider.RunStatus{RunID: "sesn_live_01"})                     // id observed before the "crash"

	r := &fakeRunner{} // repo stays "" so the pre-crash RepoName survives to Publish
	resumed, failed, err := NewWorker(s, r, WithWorkerID("test-worker")).Reconcile(ctx)
	if err != nil || resumed != 1 || failed != 0 {
		t.Fatalf("Reconcile = resumed %d failed %d err %v; want 1/0/nil", resumed, failed, err)
	}
	// The resume goroutine drives it to done; wait for it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got, _ := s.Get(ctx, j.ID); got.Phase == PhaseDone {
			break
		}
		if time.Now().After(deadline) {
			got, _ := s.Get(ctx, j.ID)
			t.Fatalf("rejoined run did not reach done, phase %s", got.Phase)
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 || r.calls[0] != "resume" {
		t.Errorf("reconcile should have resumed (not restarted) the run, calls=%v", r.calls)
	}
	if len(r.closed) != 0 {
		t.Errorf("a live run should be rejoined, not closed; closed=%v", r.closed)
	}
	// The repository created before the crash reaches Publish on resume, so a
	// restart finalizes to it rather than silently losing the publish.
	if r.publishRepo != "open-hai/live" {
		t.Errorf("resumed Publish saw RepoName %q, want the pre-crash name", r.publishRepo)
	}
}

// TestReconcileRepublishesAnInterruptedPublish checks that a restart after the
// run settled but before finalizing finished re-publishes rather than fails:
// the session succeeded, publishing is idempotent, and failing the row would
// strand a reproduction that is already committed.
func TestReconcileRepublishesAnInterruptedPublish(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	_, _, _ = s.Claim(ctx, "test-worker") // -> resolving
	_ = s.Advance(ctx, j.ID, PhaseRunning, func(jb *Job) { jb.RepoName = "open-hai/live" })
	_ = s.Observe(ctx, j.ID, provider.RunStatus{RunID: "sesn_live_01"})
	_ = s.Advance(ctx, j.ID, PhasePublishing, nil) // crashed mid-publish

	r := &fakeRunner{}
	resumed, failed, err := NewWorker(s, r, WithWorkerID("test-worker")).Reconcile(ctx)
	if err != nil || resumed != 1 || failed != 0 {
		t.Fatalf("Reconcile = resumed %d failed %d err %v; want 1/0/nil", resumed, failed, err)
	}
	waitForPhase(t, s, j.ID, PhaseDone)

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) != 1 || r.calls[0] != "publish" {
		t.Errorf("reconcile should have re-published only, calls=%v", r.calls)
	}
	if r.publishRepo != "open-hai/live" {
		t.Errorf("re-published against RepoName %q, want the pre-crash name", r.publishRepo)
	}
}

// TestShutdownLeavesTheJobForReconcile checks that a shutdown is not a failed
// session. When the pool context is cancelled, which happens on every deploy
// and node drain, the run's error must not write the terminal failed phase,
// because Reconcile cannot recover a failed row: it would be stuck while the
// backend session kept working. Leaving it in its phase lets the next boot
// rejoin it.
func TestShutdownLeavesTheJobForReconcile(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})

	pool, stopPool := context.WithCancel(ctx)
	r := &fakeRunner{failAt: "execute", onExecute: stopPool} // SIGTERM lands mid-run
	if _, err := NewWorker(s, r).Process(pool); err != nil {
		t.Fatalf("Process: %v", err)
	}

	got, _ := s.Get(ctx, j.ID)
	if got.Phase != PhaseRunning {
		t.Fatalf("phase = %s, want running — a shutdown must leave the job rejoinable", got.Phase)
	}
	if got.Err != "" {
		t.Errorf("a shutdown recorded a failure cause: %q", got.Err)
	}
}

// TestCancelStillFailsTheJob checks the other path, a person cancelling their
// session: only that job's context is cancelled, not the pool's, so the row
// still ends terminal.
func TestCancelStillFailsTheJob(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})

	r := &fakeRunner{failAt: "execute"}
	if _, err := NewWorker(s, r).Process(ctx); err != nil {
		t.Fatalf("Process: %v", err)
	}
	got, _ := s.Get(ctx, j.ID)
	if got.Phase != PhaseFailed || got.Err == "" {
		t.Errorf("phase = %s err = %q, want failed with a cause", got.Phase, got.Err)
	}
}

// waitForPhase blocks until a job reaches phase, or the test times out.
func waitForPhase(t *testing.T, s Store, id string, phase Phase) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got, _ := s.Get(context.Background(), id)
		if got.Phase == phase {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s stopped at phase %s, want %s", id, got.Phase, phase)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestCancelClosesAnOrphanRun checks that cancelling an orphaned running
// session, one no worker is driving, still closes it on the backend via its
// persisted run id.
func TestCancelClosesAnOrphanRun(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit, Project: "p", PaperKey: "arxiv:1"})
	_, _, _ = s.Claim(ctx, "test-worker")
	_ = s.Observe(ctx, j.ID, provider.RunStatus{State: provider.StateRunning, RunID: "sesn_live_02"})

	r := &fakeRunner{}
	if err := NewWorker(s, r).Cancel(ctx, j.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(r.closed) != 1 || r.closed[0] != "sesn_live_02" {
		t.Errorf("cancel should close the backend session, closed=%v", r.closed)
	}
	if got, _ := s.Get(ctx, j.ID); got.Phase != PhaseFailed || got.Err != CancelCause {
		t.Errorf("cancelled job = %s/%q, want failed/cancelled", got.Phase, got.Err)
	}
}

// TestCancelKeepsTheJobRetryableWhenBackendCloseFails checks that a backend
// stop failure is not turned into a terminal local cancellation, which would
// make the still-running remote session impossible to stop by retry.
func TestCancelKeepsTheJobRetryableWhenBackendCloseFails(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit, Project: "p", PaperKey: "arxiv:1"})
	_, _, _ = s.Claim(ctx, "test-worker")
	_ = s.Observe(ctx, j.ID, provider.RunStatus{State: provider.StateRunning, RunID: "sesn_live_03"})

	r := &fakeRunner{closeErr: errors.New("backend down")}
	if err := NewWorker(s, r).Cancel(ctx, j.ID); err == nil {
		t.Fatal("Cancel succeeded despite a failed backend close")
	}
	if got, _ := s.Get(ctx, j.ID); got.Phase.Terminal() {
		t.Errorf("failed backend close made job terminal: %s", got.Phase)
	}
}

// TestStartProcessesThePool checks that the pool loop processes enqueued jobs
// and stops on cancellation.
func TestStartProcessesThePool(t *testing.T) {
	s := NewMemStore()
	ctx, cancel := context.WithCancel(context.Background())
	for range 5 {
		_, _ = s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	}

	w := NewWorker(s, &fakeRunner{}, WithPoll(time.Millisecond),
		withWorkerSleep(func(c context.Context, d time.Duration) error {
			select {
			case <-c.Done():
				return c.Err()
			case <-time.After(time.Millisecond):
				return nil
			}
		}))
	go w.Start(ctx, 3)

	deadline := time.Now().Add(2 * time.Second)
	for {
		active, _ := s.Active(ctx)
		if len(active) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pool did not drain, %d jobs left", len(active))
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
}

// failClaimStore errors on Claim, so the worker's claim-error path is covered.
type failClaimStore struct{ Store }

func (failClaimStore) Claim(context.Context, string) (Job, bool, error) {
	return Job{}, false, errors.New("db down")
}

func TestProcessSurfacesClaimError(t *testing.T) {
	w := NewWorker(failClaimStore{NewMemStore()}, &fakeRunner{})
	if _, err := w.Process(context.Background()); err == nil {
		t.Fatal("a claim failure should surface from Process")
	}
}

func TestReconcileSurfacesActiveError(t *testing.T) {
	if _, _, err := NewWorker(failActiveStore{NewMemStore()}, &fakeRunner{}).Reconcile(context.Background()); err == nil {
		t.Fatal("an Active failure should surface from Reconcile")
	}
}

type failActiveStore struct{ Store }

func (failActiveStore) Active(context.Context) ([]Job, error) { return nil, errors.New("db down") }

type observeFailureStore struct {
	Store
	err error
}

func (s observeFailureStore) Observe(context.Context, string, provider.RunStatus) error { return s.err }

type advanceFailureStore struct {
	Store
	phase     Phase
	remaining int
	err       error
}

func (s *advanceFailureStore) Advance(ctx context.Context, id string, to Phase, mutate func(*Job)) error {
	if to == s.phase && s.remaining != 0 {
		if s.remaining > 0 {
			s.remaining--
		}
		return s.err
	}
	return s.Store.Advance(ctx, id, to, mutate)
}

type waitAfterObserveRunner struct{ *fakeRunner }

func (r *waitAfterObserveRunner) Execute(ctx context.Context, _ *Job, observe func(provider.RunStatus)) error {
	r.record("execute")
	observe(provider.RunStatus{State: provider.StateRunning, RunID: "sesn_lost"})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

func TestObserveFailureInterruptsAndPreventsPublish(t *testing.T) {
	base := NewMemStore()
	j, _ := base.Enqueue(context.Background(), Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	storeErr := errors.New("observe write failed")
	r := &waitAfterObserveRunner{fakeRunner: &fakeRunner{}}

	if _, err := NewWorker(observeFailureStore{Store: base, err: storeErr}, r).Process(context.Background()); err != nil {
		t.Fatalf("Process recorded the observation failure and should not surface it: %v", err)
	}
	got, _ := base.Get(context.Background(), j.ID)
	if got.Phase != PhaseFailed || !strings.Contains(got.Err, storeErr.Error()) {
		t.Fatalf("job = %s/%q, want failed with observation error", got.Phase, got.Err)
	}
	if slices.Contains(r.calls, "publish") {
		t.Errorf("publish ran after observation failed: %v", r.calls)
	}
}

func TestDoneTransitionRetriesAWriteFailure(t *testing.T) {
	base := NewMemStore()
	j, _ := base.Enqueue(context.Background(), Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	s := &advanceFailureStore{Store: base, phase: PhaseDone, remaining: 1, err: errors.New("transient write failure")}

	if _, err := NewWorker(s, &fakeRunner{}).Process(context.Background()); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got, _ := base.Get(context.Background(), j.ID); got.Phase != PhaseDone {
		t.Fatalf("phase = %s, want done after retry", got.Phase)
	}
}

func TestPersistentDoneTransitionFailureSurfaces(t *testing.T) {
	base := NewMemStore()
	j, _ := base.Enqueue(context.Background(), Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	writeErr := errors.New("persistent write failure")
	s := &advanceFailureStore{Store: base, phase: PhaseDone, remaining: -1, err: writeErr}

	if _, err := NewWorker(s, &fakeRunner{}).Process(context.Background()); !errors.Is(err, writeErr) {
		t.Fatalf("Process error = %v, want terminal write failure", err)
	}
	if got, _ := base.Get(context.Background(), j.ID); got.Phase != PhasePublishing {
		t.Fatalf("phase = %s, want publishing for reconciliation", got.Phase)
	}
}

func TestRetryablePublishFailureStaysPublishing(t *testing.T) {
	base := NewMemStore()
	j, _ := base.Enqueue(context.Background(), Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	dependencyErr := errors.New("transcript backend unavailable")
	r := &fakeRunner{
		publishErr: RetryablePublish(dependencyErr),
		observed:   provider.RunStatus{RunID: "sesn_retry_publish"},
	}

	if _, err := NewWorker(base, r).Process(context.Background()); !errors.Is(err, dependencyErr) {
		t.Fatalf("Process error = %v, want retryable publish cause", err)
	}
	if got, _ := base.Get(context.Background(), j.ID); got.Phase != PhasePublishing {
		t.Fatalf("phase = %s, want publishing for reconciliation", got.Phase)
	}

	// Once the dependency recovers, restart reconciliation replays the
	// idempotent publish and records success.
	r.publishErr = nil
	resumed, failed, err := NewWorker(base, r).Reconcile(context.Background())
	if err != nil || resumed != 1 || failed != 0 {
		t.Fatalf("Reconcile = %d/%d/%v, want 1/0/nil", resumed, failed, err)
	}
	waitForPhase(t, base, j.ID, PhaseDone)
}

func TestFailureTransitionFailureSurfaces(t *testing.T) {
	base := NewMemStore()
	_, _ = base.Enqueue(context.Background(), Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	writeErr := errors.New("cannot persist failure")
	s := &advanceFailureStore{Store: base, phase: PhaseFailed, remaining: -1, err: writeErr}

	if _, err := NewWorker(s, &fakeRunner{failAt: "resolve"}).Process(context.Background()); !errors.Is(err, writeErr) || !strings.Contains(err.Error(), "resolve boom") {
		t.Fatalf("Process error = %v, want joined work and failure-write errors", err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestAQuietRunKeepsItsLease checks that a run whose model is thinking reports
// nothing and still keeps its lease. This is the incident of 044: one pod
// claimed a session at 07:39 and drove it correctly; five quiet minutes later
// the pod beside it logged "sweep adopted 1 stranded run(s)" and took a live
// session, and the two drivers' finalizes raced until the loser recorded a
// failure over the winner's published success.
func TestAQuietRunKeepsItsLease(t *testing.T) {
	s := NewMemStore()
	ctx := t.Context()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})

	const ttl = 60 * time.Millisecond
	held := make(chan struct{})
	r := &fakeRunner{observed: provider.RunStatus{RunID: "sesn_quiet_01"}}
	r.runExecute = func(ctx context.Context) error {
		close(held)
		<-ctx.Done() // report nothing further, exactly like an agent that is thinking
		return ctx.Err()
	}
	driver := NewWorker(s, r, WithWorkerID("pod-a"), WithPoll(time.Millisecond), withLeaseTTL(ttl))
	go driver.Start(ctx, 1)
	<-held

	// Well past the lease, with no observation since the run began.
	time.Sleep(4 * ttl)

	if won, err := s.Adopt(ctx, j.ID, "pod-b", ttl); err != nil || won {
		t.Fatalf("Adopt by a second replica = %v/%v, want it refused — pod-a is alive and driving", won, err)
	}
	if got, _ := s.Get(ctx, j.ID); got.Phase != PhaseRunning {
		t.Errorf("phase = %s, want running", got.Phase)
	}
}

// TestADriverYieldsASessionItHasLost checks that a driver that finds its lease
// held elsewhere stops its own run rather than driving on to a second finalize
// of the same backend session. The heartbeat makes a wrongful adoption rare;
// yielding makes one harmless.
func TestADriverYieldsASessionItHasLost(t *testing.T) {
	s := NewMemStore()
	ctx := t.Context()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})

	const ttl = 60 * time.Millisecond
	held, yielded := make(chan struct{}), make(chan struct{})
	r := &fakeRunner{observed: provider.RunStatus{RunID: "sesn_lost_01"}}
	r.runExecute = func(ctx context.Context) error {
		close(held)
		<-ctx.Done()
		close(yielded)
		return ctx.Err()
	}
	driver := NewWorker(s, r, WithWorkerID("pod-a"), WithPoll(time.Millisecond), withLeaseTTL(ttl))
	go driver.Start(ctx, 1)
	<-held

	// Another replica takes the row. However that happened, this driver no
	// longer owns it.
	if won, err := s.Adopt(ctx, j.ID, "pod-b", 0); err != nil || !won {
		t.Fatalf("Adopt = %v/%v, want pod-b to win", won, err)
	}
	select {
	case <-yielded:
	case <-time.After(2 * time.Second):
		t.Fatal("the driver kept running a session whose lease it had lost")
	}
}

// TestAFinishedRunIsNotAnnouncedAsAYield checks that a run that finished is
// not logged as a yield. It reports the same lost lease a taken one does,
// because the row went terminal between its last transition and the driver
// deregistering, which happens on every successful run. The yield log line
// matters only during an incident.
func TestAFinishedRunIsNotAnnouncedAsAYield(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	s := NewMemStore()
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	_, _, _ = s.Claim(ctx, "pod-a")
	_ = s.Advance(ctx, j.ID, PhaseRunning, nil)
	_ = s.Advance(ctx, j.ID, PhaseFailed, func(jb *Job) { jb.Err = "over" })

	w := NewWorker(s, &fakeRunner{}, WithWorkerID("pod-a"))
	// The job is still registered as driven here: the phase landed, the deferred
	// deregistration has not run yet, and a tick falls in between.
	_, done := w.track(ctx, j.ID)
	defer done()
	w.beat(ctx)

	if strings.Contains(buf.String(), "yielding session") {
		t.Errorf("a finished run was logged as a yield: %q", buf.String())
	}
}
