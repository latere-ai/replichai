package job

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fixedAllowance meters everyone at n, except the subs listed as unlimited.
func fixedAllowance(n int, unlimited ...string) Allowance {
	free := map[string]bool{}
	for _, s := range unlimited {
		free[s] = true
	}
	return func(_ context.Context, payer string) (int, bool, error) {
		return n, free[payer], nil
	}
}

// runQuotaContract asserts every quota invariant a Store must hold, so the
// in-memory and Postgres implementations cannot diverge. The store is metered
// at 2 for everyone but "admin-sub", and is assumed empty.
func runQuotaContract(t *testing.T, s Store) {
	t.Helper()
	ctx := context.Background()

	// AC1: the limit is a limit. Distinct paper keys, so the double-trigger
	// guard is not what refuses the third.
	for i, ref := range []string{"arxiv:1111.1", "arxiv:2222.2"} {
		if _, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: ref, Kind: KindAudit, Project: "p", PaperKey: ref}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	third := Submission{Principal: "u", Ref: "arxiv:3333.3", Kind: KindAudit, Project: "p", PaperKey: "arxiv:3333.3"}
	if _, err := s.Enqueue(ctx, third); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("third enqueue: want ErrQuotaExceeded, got %v", err)
	}

	// AC4: in-flight sessions count. Both of the above are still `submitted`,
	// and they are what refused the third — a burst cannot outrun the counter.

	// AC3: a failed session releases its hold; a done one does not.
	held, err := s.ListByPrincipal(ctx, "u")
	if err != nil || len(held) != 2 {
		t.Fatalf("list = %d (err %v), want 2", len(held), err)
	}
	if _, err := s.Cancel(ctx, held[0].ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	refunded, err := s.Enqueue(ctx, third)
	if err != nil {
		t.Fatalf("after a cancel the payer should have room again: %v", err)
	}
	// Drive the refunded one to done: a completed session stays spent.
	for _, to := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if err := s.Advance(ctx, refunded.ID, to, nil); err != nil {
			t.Fatalf("advance to %s: %v", to, err)
		}
	}
	fourth := Submission{Principal: "u", Ref: "arxiv:4444.4", Kind: KindAudit, Project: "p", PaperKey: "arxiv:4444.4"}
	if _, err := s.Enqueue(ctx, fourth); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("a done session must stay spent: want ErrQuotaExceeded, got %v", err)
	}

	// AC5: the payer, not the submitter, is what is metered — a sponsored
	// session charges someone else and leaves the submitter's own quota alone.
	sponsored := Submission{
		Principal: "u", Ref: "arxiv:5555.5", Kind: KindAudit, Project: "p",
		PaperKey: "arxiv:5555.5", ChargedTo: "sponsor-sub",
	}
	got, err := s.Enqueue(ctx, sponsored)
	if err != nil {
		t.Fatalf("a sponsored submit should draw the sponsor's quota: %v", err)
	}
	if got.ChargedTo != "sponsor-sub" || got.Principal != "u" {
		t.Errorf("job = charged_to %q / principal %q, want the sponsor charged and the submitter recorded", got.ChargedTo, got.Principal)
	}

	// AC7: a platform admin is never refused and never metered.
	for i, ref := range []string{"arxiv:6.1", "arxiv:6.2", "arxiv:6.3", "arxiv:6.4"} {
		j, err := s.Enqueue(ctx, Submission{Principal: "admin-sub", Ref: ref, Kind: KindAudit, Project: "a", PaperKey: ref})
		if err != nil {
			t.Fatalf("admin enqueue %d past the limit: %v", i, err)
		}
		if j.ChargedTo != "admin-sub" {
			t.Errorf("an admin's session should still record charged_to, got %q", j.ChargedTo)
		}
	}
}

func TestMemStoreQuotaContract(t *testing.T) {
	runQuotaContract(t, NewMemStore(WithAllowance(fixedAllowance(2, "admin-sub"))))
}

// AC2: at the boundary, exactly the remaining number get through. The
// in-memory store passes this trivially under its mutex; the Postgres one is
// where it has teeth, and is the reason the count runs inside the inserting
// transaction under an advisory lock rather than as a read-then-insert.
func runQuotaConcurrency(t *testing.T, s Store, remaining int, attempts int) {
	t.Helper()
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ok, refused, other int
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ref := "arxiv:9000." + string(rune('a'+i))
			_, err := s.Enqueue(ctx, Submission{Principal: "racer", Ref: ref, Kind: KindAudit, Project: "p", PaperKey: ref})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrQuotaExceeded):
				refused++
			default:
				other++
				t.Errorf("unexpected enqueue error: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != remaining || refused != attempts-remaining || other != 0 {
		t.Errorf("concurrent enqueues: %d admitted, %d refused, %d errored; want exactly %d admitted", ok, refused, other, remaining)
	}
}

func TestMemStoreQuotaIsExactUnderConcurrency(t *testing.T) {
	runQuotaConcurrency(t, NewMemStore(WithAllowance(fixedAllowance(3))), 3, 12)
}

// The Postgres quota, against a real database. This is the only place AC2 is
// genuinely tested: under READ COMMITTED a read-then-insert check would admit
// more than the remaining number here.
func TestPGStoreQuotaContract(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres quota contract")
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
	runQuotaContract(t, NewPGStore(pool, WithPGAllowance(fixedAllowance(2, "admin-sub"))))

	if _, err := pool.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	runQuotaConcurrency(t, NewPGStore(pool, WithPGAllowance(fixedAllowance(3))), 3, 12)
}

// An unmetered store (no Allowance wired) is unlimited — an offline run and
// every test that predates the quota keep working.
func TestNoAllowanceMeansUnlimited(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	for i := range 20 {
		ref := "arxiv:7000." + string(rune('a'+i))
		if _, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: ref, Kind: KindAudit, Project: "p", PaperKey: ref}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
}
