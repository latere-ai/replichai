package job

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/provider"
)

// runStoreContract asserts the invariants every Store must hold, so the
// in-memory and Postgres implementations cannot diverge. The store is assumed
// empty.
func runStoreContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	// Enqueue and read back.
	j, err := s.Enqueue(ctx, "sub", "arxiv:2302.03714", KindAudit, "proj")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got, _ := s.Get(ctx, j.ID); got.Phase != PhaseSubmitted || got.Ref != "arxiv:2302.03714" || got.ProjectID != "proj" {
		t.Fatalf("read-back = %+v", got)
	}

	// An illegal transition is refused and does not move the job.
	if err := s.Advance(ctx, j.ID, PhaseRunning, nil); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("illegal transition: want ErrIllegalTransition, got %v", err)
	}

	// The full lifecycle walks through the transition function, recording each.
	for _, p := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if err := s.Advance(ctx, j.ID, p, func(jb *Job) {
			if p == PhasePublishing {
				jb.RepoName = "open-hai/x"
			}
		}); err != nil {
			t.Fatalf("advance to %s: %v", p, err)
		}
	}
	done, _ := s.Get(ctx, j.ID)
	if done.Phase != PhaseDone || done.RepoName != "open-hai/x" {
		t.Fatalf("final = %+v", done)
	}
	for _, p := range []Phase{PhaseSubmitted, PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if done.PhaseAt[p].IsZero() {
			t.Errorf("phase %s has no timestamp", p)
		}
	}

	// Observe records status in place, without a phase change.
	obs, _ := s.Enqueue(ctx, "sub", "ref", KindAudit, "")
	if err := s.Observe(ctx, obs.ID, provider.RunStatus{State: provider.StateRunning, Usage: provider.Usage{InputTokens: 7}}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if got, _ := s.Get(ctx, obs.ID); got.Status.Usage.InputTokens != 7 || got.Phase != PhaseSubmitted {
		t.Errorf("observe: status=%+v phase=%s", got.Status, got.Phase)
	}

	// A failure records its cause and lands in failed (submitted -> failed).
	fj, _ := s.Enqueue(ctx, "sub", "ref", KindAudit, "")
	_ = s.Advance(ctx, fj.ID, PhaseFailed, func(jb *Job) { jb.Err = "boom" })
	if got, _ := s.Get(ctx, fj.ID); got.Phase != PhaseFailed || got.Err != "boom" {
		t.Errorf("failure: phase=%s err=%q", got.Phase, got.Err)
	}

	// Active excludes terminal jobs.
	active, err := s.Active(ctx)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	for _, a := range active {
		if a.Phase.Terminal() {
			t.Errorf("Active returned a terminal job: %s", a.Phase)
		}
	}
}

// runClaimContention asserts concurrent claims are disjoint — the assertion that
// only really means something against Postgres's SKIP LOCKED.
func runClaimContention(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	const n = 30
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
	for id, c := range seen {
		if c != 1 {
			t.Errorf("job %s claimed %d times", id, c)
		}
	}
}

func TestMemStoreSatisfiesTheContract(t *testing.T) {
	runStoreContract(t, NewMemStore())
	runClaimContention(t, NewMemStore())
}

// TestPGStoreSatisfiesTheContract runs the same contract against a real
// Postgres, skipping when TEST_DATABASE_URL is not set. `docker compose` sets
// it, so the SKIP LOCKED path is exercised for real.
func TestPGStoreSatisfiesTheContract(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres store contract")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	runStoreContract(t, NewPGStore(pool))
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	runClaimContention(t, NewPGStore(pool))
}
