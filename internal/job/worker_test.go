package job

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/provider"
)

// fakeRunner records which phase methods ran and can be made to fail at one.
type fakeRunner struct {
	mu          sync.Mutex
	calls       []string
	failAt      string
	repo        string
	resolveRepo string // when set, Resolve stamps it onto the job (the 006i handoff)
	publishRepo string // the RepoName Publish observed — proves the handoff survived
	observed    provider.RunStatus
	closed      []string // run ids Close was called with
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

func (f *fakeRunner) Execute(_ context.Context, j *Job, observe func(provider.RunStatus)) error {
	f.record("execute")
	observe(f.observed)
	if f.failAt == "execute" {
		return errors.New("execute boom")
	}
	return nil
}

func (f *fakeRunner) Resume(_ context.Context, j *Job, observe func(provider.RunStatus)) error {
	f.record("resume")
	observe(f.observed)
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
	return nil
}

func (f *fakeRunner) Close(_ context.Context, runID string) error {
	f.mu.Lock()
	f.closed = append(f.closed, runID)
	f.mu.Unlock()
	return nil
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

// AC7: both kinds walk the same phases through the same transition function;
// only the callee differs. No second state machine.
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

// 006i: a RepoName set in Resolve is persisted at the transition into running
// (apply copies it) and is therefore present by the time Publish runs — the
// handoff the server's create-repo-in-resolve / finalize-in-publish depends on.
func TestRepoNameFromResolveReachesPublish(t *testing.T) {
	s := NewMemStore()
	r := &fakeRunner{resolveRepo: "open-hai/created-in-resolve"} // repo stays "" so Publish won't clobber
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

// AC5: the latest run status is recorded on the job as the run observes it, and
// is readable afterwards — the store-level half of "a late reader sees where it
// is" (the SSE snapshot is 006a).
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

// A job left mid-run with no live backend session to rejoin (its run id was
// never observed) cannot be synced, so reconcile fails it. A fresh submitted job
// is left claimable.
func TestReconcileFailsUnrecoverable(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	stranded, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	if _, ok, _ := s.Claim(ctx); !ok { // -> resolving, no run id
		t.Fatal("claim should have taken the job")
	}
	fresh, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref2", Kind: KindAudit})

	resumed, failed, err := NewWorker(s, &fakeRunner{}).Reconcile(ctx)
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

// A run whose backend session id was observed is REJOINED on restart, not
// failed: reconcile resumes it and drives it to done, reflecting the live
// session rather than guessing it failed while it kept running.
func TestReconcileRejoinsLiveRun(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	_, _, _ = s.Claim(ctx)                                                                  // -> resolving
	_ = s.Advance(ctx, j.ID, PhaseRunning, func(jb *Job) { jb.RepoName = "open-hai/live" }) // running, repo created pre-crash
	_ = s.Observe(ctx, j.ID, provider.RunStatus{RunID: "sesn_live_01"})                     // id observed before the "crash"

	r := &fakeRunner{} // repo stays "" so the pre-crash RepoName survives to Publish
	resumed, failed, err := NewWorker(s, r).Reconcile(ctx)
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

// Cancelling an orphaned running session (one no worker is driving) still closes
// it on the backend, via its persisted run id.
func TestCancelClosesAnOrphanRun(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	j, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit, Project: "p", PaperKey: "arxiv:1"})
	_, _, _ = s.Claim(ctx)
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

// The pool loop processes enqueued jobs and stops on cancellation.
func TestStartProcessesThePool(t *testing.T) {
	s := NewMemStore()
	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < 5; i++ {
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

func (failClaimStore) Claim(context.Context) (Job, bool, error) {
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
