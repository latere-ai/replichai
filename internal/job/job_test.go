package job

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// AC3 is the invariant the whole state machine exists to hold: a failure is
// reachable from every active phase, done is not, and no illegal jump is ever
// accepted. Written first, because a transition function that cannot refuse is a
// mutable column, not a state machine.
func TestTransitionsAreConstrained(t *testing.T) {
	// Failure from every active phase; nothing from a terminal one.
	for _, p := range []Phase{PhaseSubmitted, PhaseResolving, PhaseRunning, PhasePublishing} {
		if !p.Active() {
			t.Errorf("%s should be active", p)
		}
		if !CanTransition(p, PhaseFailed) {
			t.Errorf("a failure must be reachable from %s", p)
		}
	}
	for _, p := range []Phase{PhaseDone, PhaseFailed} {
		if !p.Terminal() {
			t.Errorf("%s should be terminal", p)
		}
		if CanTransition(p, PhaseRunning) || CanTransition(p, PhaseFailed) {
			t.Errorf("nothing should leave the terminal phase %s", p)
		}
	}

	// Done is reachable only from publishing.
	if CanTransition(PhaseRunning, PhaseDone) || CanTransition(PhaseSubmitted, PhaseDone) {
		t.Error("done must not be reachable by skipping publishing")
	}
	if !CanTransition(PhasePublishing, PhaseDone) {
		t.Error("done should follow publishing")
	}
	// No phase-skipping.
	if CanTransition(PhaseSubmitted, PhaseRunning) {
		t.Error("submitted must not jump straight to running")
	}
}

func TestAdvanceRefusesIllegalTransition(t *testing.T) {
	s := NewMemStore()
	j, _ := s.Enqueue(context.Background(), "sub-1", "arxiv:1", KindAudit, "")

	// submitted -> running skips resolving and must be refused.
	if err := s.Advance(context.Background(), j.ID, PhaseRunning, nil); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("an illegal transition should be refused, got %v", err)
	}
	// The job did not move.
	got, _ := s.Get(context.Background(), j.ID)
	if got.Phase != PhaseSubmitted {
		t.Errorf("a refused transition must not move the job, phase is %s", got.Phase)
	}
}

// AC3: a failure in any active phase moves to failed with the error kept, never
// to done.
func TestFailureFromAnyActivePhase(t *testing.T) {
	ctx := context.Background()
	for _, start := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing} {
		s := NewMemStore()
		j, _ := s.Enqueue(ctx, "sub", "ref", KindAudit, "")
		// Walk to the phase under test.
		for _, p := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing} {
			if err := s.Advance(ctx, j.ID, p, nil); err != nil {
				t.Fatalf("walk to %s: %v", p, err)
			}
			if p == start {
				break
			}
		}
		if err := s.Advance(ctx, j.ID, PhaseFailed, func(jb *Job) { jb.Err = "boom" }); err != nil {
			t.Fatalf("failure from %s: %v", start, err)
		}
		got, _ := s.Get(ctx, j.ID)
		if got.Phase != PhaseFailed || got.Err != "boom" {
			t.Errorf("from %s: phase=%s err=%q, want failed/boom", start, got.Phase, got.Err)
		}
	}
}

// AC2: every phase change goes through the one transition function and is
// durable; the timeline is recorded.
func TestLifecycleWalkRecordsEveryPhase(t *testing.T) {
	tick := make(chan time.Time, 16)
	base := time.Unix(1000, 0)
	for i := 0; i < 16; i++ {
		tick <- base.Add(time.Duration(i) * time.Second)
	}
	s := NewMemStore(WithClock(func() time.Time { return <-tick }))
	ctx := context.Background()

	j, _ := s.Enqueue(ctx, "sub", "arxiv:2302.03714", KindAudit, "proj-1")
	for _, p := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if err := s.Advance(ctx, j.ID, p, nil); err != nil {
			t.Fatalf("advance to %s: %v", p, err)
		}
	}
	got, _ := s.Get(ctx, j.ID)
	if got.Phase != PhaseDone {
		t.Fatalf("final phase = %s, want done", got.Phase)
	}
	for _, p := range []Phase{PhaseSubmitted, PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if got.PhaseAt[p].IsZero() {
			t.Errorf("phase %s has no recorded timestamp", p)
		}
	}
	// Timestamps are monotonic across the walk.
	if !got.PhaseAt[PhaseDone].After(got.PhaseAt[PhaseSubmitted]) {
		t.Error("the timeline should advance")
	}
}

func TestEnqueueRejectsEmptyRef(t *testing.T) {
	if _, err := NewMemStore().Enqueue(context.Background(), "sub", "  ", KindAudit, ""); !errors.Is(err, ErrEmptyRef) {
		t.Fatalf("an empty reference should be refused, got %v", err)
	}
}

func TestGetMissingIsNotFound(t *testing.T) {
	if _, err := NewMemStore().Get(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestStoreCopiesOnBoundary(t *testing.T) {
	s := NewMemStore()
	j, _ := s.Enqueue(context.Background(), "sub", "ref", KindAudit, "")
	j.PhaseAt[PhaseSubmitted] = time.Unix(0, 0) // mutate the returned copy
	got, _ := s.Get(context.Background(), j.ID)
	if got.PhaseAt[PhaseSubmitted].Equal(time.Unix(0, 0)) {
		t.Error("the store returned an aliased map")
	}
}

// AC4 (contract): concurrent claims hand out disjoint jobs and never the same
// one twice. Run against the in-memory store now; the Postgres store runs the
// same contract with SELECT … FOR UPDATE SKIP LOCKED (that is where the real
// assertion lands, since a lock-guarded map gives disjointness trivially).
func TestConcurrentClaimsAreDisjoint(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	const n = 50
	for i := 0; i < n; i++ {
		if _, err := s.Enqueue(ctx, "sub", "ref", KindAudit, ""); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if j, ok, err := s.Claim(ctx); err == nil && ok {
				mu.Lock()
				seen[j.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != n {
		t.Errorf("claimed %d distinct jobs, want %d", len(seen), n)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("job %s was claimed %d times, want exactly once", id, count)
		}
	}
	// Every claimed job left the submitted phase.
	if _, ok, _ := s.Claim(ctx); ok {
		t.Error("no submitted job should remain after all were claimed")
	}
}
