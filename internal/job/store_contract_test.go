package job

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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
	j, err := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "arxiv:2302.03714", Kind: KindAudit, Project: "proj"})
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
	obs, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
	if err := s.Observe(ctx, obs.ID, provider.RunStatus{State: provider.StateRunning, Usage: provider.Usage{InputTokens: 7}}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if got, _ := s.Get(ctx, obs.ID); got.Status.Usage.InputTokens != 7 || got.Phase != PhaseSubmitted {
		t.Errorf("observe: status=%+v phase=%s", got.Status, got.Phase)
	}

	// A failure records its cause and lands in failed (submitted -> failed).
	fj, _ := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit})
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

	// ListByPrincipal is scoped to one owner and newest-first. Enqueue two jobs
	// for "owner" and one for a stranger, then assert the stranger's job never
	// appears and the two are ordered newest-first.
	first, _ := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref-a", Kind: KindAudit})
	second, _ := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref-b", Kind: KindAudit})
	_, _ = s.Enqueue(ctx, Submission{Principal: "stranger", Ref: "ref-c", Kind: KindAudit})
	mine, err := s.ListByPrincipal(ctx, "owner")
	if err != nil {
		t.Fatalf("ListByPrincipal: %v", err)
	}
	if len(mine) != 2 {
		t.Fatalf("owner has %d jobs, want 2 (no stranger leak)", len(mine))
	}
	if mine[0].ID != second.ID || mine[1].ID != first.ID {
		t.Errorf("ListByPrincipal not newest-first: %s,%s want %s,%s", mine[0].ID, mine[1].ID, second.ID, first.ID)
	}
	for _, j := range mine {
		if j.Principal != "owner" {
			t.Errorf("ListByPrincipal leaked a %q job", j.Principal)
		}
	}
	// An unknown principal lists nothing, without error.
	if none, err := s.ListByPrincipal(ctx, "nobody"); err != nil || len(none) != 0 {
		t.Errorf("unknown principal: %d jobs, err %v; want 0, nil", len(none), err)
	}
}

// runGuardContract asserts the double-trigger guard both stores must enforce:
// a non-terminal session for a paper blocks a second one (in the project or
// anywhere the principal owns), a deliberate re-run after the prior finished is
// allowed, a different paper is never blocked, and an empty key never guards.
func runGuardContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	const key = "arxiv:2302.03714"

	// First session for the paper opens.
	first, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projA", PaperKey: key})
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}

	// A second, same paper same project, is refused — the accidental double-fire.
	if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projA", PaperKey: key}); !errors.Is(err, ErrActiveSession) {
		t.Errorf("same project double-fire: want ErrActiveSession, got %v", err)
	}
	// Same paper, a different project the same principal owns, is also refused —
	// the per-principal guard that closes the bare-submit hole.
	if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projB", PaperKey: key}); !errors.Is(err, ErrActiveSession) {
		t.Errorf("cross-project double-fire: want ErrActiveSession, got %v", err)
	}
	// A different paper in the same project is never blocked.
	if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projA", PaperKey: "arxiv:9999.99999"}); err != nil {
		t.Errorf("different paper blocked: %v", err)
	}
	// An empty key never guards (an experiment, or an unkeyable ref).
	for i := 0; i < 2; i++ {
		if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "exp", Kind: KindExperiment, Project: "projA"}); err != nil {
			t.Errorf("empty-key enqueue %d blocked: %v", i, err)
		}
	}

	// Finish the first session; a re-run of the same paper is now allowed.
	for _, p := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if err := s.Advance(ctx, first.ID, p, nil); err != nil {
			t.Fatalf("advance first to %s: %v", p, err)
		}
	}
	if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projA", PaperKey: key}); err != nil {
		t.Errorf("re-run after done blocked: %v", err)
	}

	// Cancelling a session frees the guard slot immediately (cancel writes the
	// terminal 'failed'), so the same paper can be stopped and resubmitted — the
	// natural next action. Proven against the partial index, not just the mem check.
	const key2 = "arxiv:1111.11111"
	c1, _ := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projC", PaperKey: key2})
	if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projC", PaperKey: key2}); !errors.Is(err, ErrActiveSession) {
		t.Errorf("pre-cancel double-fire: want ErrActiveSession, got %v", err)
	}
	got, err := s.Cancel(ctx, c1.ID)
	if err != nil || got.Phase != PhaseFailed || got.Err != CancelCause {
		t.Fatalf("Cancel = %+v err=%v; want failed/cancelled", got, err)
	}
	if _, err := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref", Kind: KindAudit, Project: "projC", PaperKey: key2}); err != nil {
		t.Errorf("resubmit after cancel blocked: %v", err)
	}
	// Cancelling an already-terminal session is a no-op, not an error.
	if again, err := s.Cancel(ctx, c1.ID); err != nil || again.Phase != PhaseFailed {
		t.Errorf("idempotent cancel: %+v err=%v", again, err)
	}
}

// runProjectJoinContract asserts the two project-join reads both stores must
// hold: sessions scoped to one project, newest-first, and per-project counts
// for one owner in a single call.
func runProjectJoinContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	_, _ = s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref-1", Kind: KindAudit, Project: "pX"})
	_, _ = s.Enqueue(ctx, Submission{Principal: "stranger", Ref: "ref-4", Kind: KindAudit, Project: "pX"})
	_, _ = s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref-3", Kind: KindAudit, Project: "pY"})
	two, _ := s.Enqueue(ctx, Submission{Principal: "owner", Ref: "ref-2", Kind: KindAudit, Project: "pX"}) // newest in pX

	inX, err := s.ListByProject(ctx, "pX")
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	// pX holds both owner sessions and the stranger's, newest-first.
	if len(inX) != 3 || inX[0].ID != two.ID {
		t.Fatalf("pX sessions = %d, first %s; want 3 newest-first %s", len(inX), first3(inX), two.ID)
	}

	status, err := s.StatusByProjects(ctx, []string{"pX", "pY"})
	if err != nil {
		t.Fatalf("StatusByProjects: %v", err)
	}
	// Every session in the project, whoever submitted it — the stranger's run in
	// pX is pX's (018). All four are still submitted, so all four are running.
	if status["pX"].Total != 3 || status["pY"].Total != 1 {
		t.Errorf("status = %v; want pX total 3 (including the stranger), pY total 1", status)
	}
	if status["pX"].Running != 3 || status["pX"].Latest != PhaseSubmitted {
		t.Errorf("pX status = %+v; want running 3, latest submitted", status["pX"])
	}
	if status["pX"].LatestAt.IsZero() {
		t.Error("pX LatestAt is zero; want the newest session's update time")
	}
	// A project nobody asked about is not summarised, so a list pays for the page
	// it is showing and not for the table.
	if _, ok := status["pZ"]; ok {
		t.Error("pZ summarised; want only the projects asked for")
	}

	runSpendWindowContract(t, s)
}

// runSpendWindowContract asserts the window both stores must agree on (021e).
// The edges are the whole point: the window is on when a session last changed,
// so a long run that settled inside it counts and a short one that settled
// before it does not.
func runSpendWindowContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	// Every session enqueued above updated just now, so a window opening in the
	// past holds all of them and one opening in the future holds none. That is
	// the assertion that catches a store filtering on created_at, or on the
	// wrong side of the comparison.
	all, err := s.SpendSince(ctx, []string{"pX", "pY"}, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("SpendSince: %v", err)
	}
	none, err := s.SpendSince(ctx, []string{"pX", "pY"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("SpendSince: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("a window that has not opened yet reported spend: %v", none)
	}
	// Nothing has run, so nothing has been spent — but the projects inside the
	// window must be summable without erroring, and must not appear with a
	// fabricated non-zero.
	for _, id := range []string{"pX", "pY"} {
		if all[id] != 0 {
			t.Errorf("%s spent %d before any session ran", id, all[id])
		}
	}

	// A project nobody asked about is never summed.
	if _, ok := all["pZ"]; ok {
		t.Error("pZ summed; want only the projects asked for")
	}
	if got, err := s.SpendSince(ctx, nil, time.Now().Add(-24*time.Hour)); err != nil || len(got) != 0 {
		t.Errorf("SpendSince(nil) = %v, %v; want an empty map and no error", got, err)
	}
}

// first3 renders up to three ids, for a failure message.
func first3(js []Job) string {
	var ids []string
	for i, j := range js {
		if i == 3 {
			break
		}
		ids = append(ids, j.ID)
	}
	return strings.Join(ids, ",")
}

// runClaimContention asserts concurrent claims are disjoint — the assertion that
// only really means something against Postgres's SKIP LOCKED.
func runClaimContention(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()
	const n = 30
	for i := 0; i < n; i++ {
		if _, err := s.Enqueue(ctx, Submission{Principal: "sub", Ref: "ref", Kind: KindAudit}); err != nil {
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
	runGuardContract(t, NewMemStore())
	runProjectJoinContract(t, NewMemStore())
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
	runGuardContract(t, NewPGStore(pool))
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	runProjectJoinContract(t, NewPGStore(pool))
	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	runClaimContention(t, NewPGStore(pool))
}
