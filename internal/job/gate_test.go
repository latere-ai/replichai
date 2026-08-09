package job

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
)

// The gate's reserve, fixed for a test. A session may start only where this much
// is available, and starting holds exactly this much.
const testReserve = 2 * credit.Dollar

func fixedReserve(n int64) credit.Reserve {
	return func(context.Context) (int64, error) { return n, nil }
}

// fund puts amount into a project's pot without going through the membership
// checks, which are the ledger's concern and not the gate's.
type funder interface {
	Topup(ctx context.Context, byAdmin access.Principal, email string, amount int64) error
	Allocate(ctx context.Context, by access.Principal, project, projectName string, amount int64) error
}

var banker = access.Principal{Sub: "s-banker", Email: "banker@latere.ai"}

func fund(t *testing.T, f funder, project string, amount int64) {
	t.Helper()
	ctx := context.Background()
	if err := f.Topup(ctx, banker, banker.Email, amount); err != nil {
		t.Fatalf("Topup: %v", err)
	}
	if err := f.Allocate(ctx, banker, project, project, amount); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
}

// runGateContract asserts every credit invariant a Store must hold at enqueue,
// so the in-memory and Postgres implementations cannot diverge. The project "p"
// is funded with four reserves' worth; "poor" with none.
func runGateContract(t *testing.T, s Store, ledger credit.Store) {
	t.Helper()
	ctx := context.Background()

	// A project that cannot commit the reserve cannot start a session. Distinct
	// paper keys throughout, so the double-trigger guard is never what refuses.
	broke := Submission{Principal: "u", Ref: "arxiv:0000.0", Kind: KindAudit, Project: "poor", PaperKey: "arxiv:0000.0"}
	if _, err := s.Enqueue(ctx, broke); !errors.Is(err, ErrNoCredits) {
		t.Fatalf("submitting into an unfunded project: want ErrNoCredits, got %v", err)
	}

	// A funded one can, and starting holds the reserve rather than spending it:
	// the pot still holds what it held, and only what may still be committed
	// moved.
	before, _ := ledger.Balance(ctx, credit.Pot("p"))
	first, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: "arxiv:1111.1", Kind: KindAudit, Project: "p", PaperKey: "arxiv:1111.1"})
	if err != nil {
		t.Fatalf("enqueue into a funded project: %v", err)
	}
	if n, _ := ledger.Balance(ctx, credit.Pot("p")); n != before {
		t.Errorf("balance moved to %d on enqueue; a hold is a commitment, not a spend", n)
	}
	if n, _ := ledger.Available(ctx, credit.Pot("p")); n != before-testReserve {
		t.Errorf("available = %d, want %d — one reserve committed", n, before-testReserve)
	}

	// The hold names the session it is for, so settlement can find it.
	entries, err := ledger.Entries(ctx, credit.Pot("p"), 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	var held bool
	for _, e := range entries {
		if e.Kind == credit.KindHold && e.JobID == first.ID && e.Amount == -testReserve {
			held = true
		}
	}
	if !held {
		t.Errorf("no hold entry for session %s in %+v", first.ID, entries)
	}

	// Each further session holds its own, so a burst cannot be admitted against
	// money the earlier ones already committed. Four reserves funded, one taken.
	for i, ref := range []string{"arxiv:2222.2", "arxiv:3333.3"} {
		if _, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: ref, Kind: KindAudit, Project: "p", PaperKey: ref}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	fourth := Submission{Principal: "u", Ref: "arxiv:4444.4", Kind: KindAudit, Project: "p", PaperKey: "arxiv:4444.4"}
	if _, err := s.Enqueue(ctx, fourth); err != nil {
		t.Fatalf("the fourth reserve is funded and must be admitted: %v", err)
	}
	fifth := Submission{Principal: "u", Ref: "arxiv:5555.5", Kind: KindAudit, Project: "p", PaperKey: "arxiv:5555.5"}
	if _, err := s.Enqueue(ctx, fifth); !errors.Is(err, ErrNoCredits) {
		t.Fatalf("a fifth session with four reserves funded: want ErrNoCredits, got %v", err)
	}

	// The pot is what pays, not the person: a different submitter in the same
	// project meets exactly the same wall.
	other := Submission{Principal: "somebody-else", Ref: "arxiv:6666.6", Kind: KindAudit, Project: "p", PaperKey: "arxiv:6666.6"}
	if _, err := s.Enqueue(ctx, other); !errors.Is(err, ErrNoCredits) {
		t.Fatalf("another member in the same exhausted project: want ErrNoCredits, got %v", err)
	}
	// And a funded project admits them, because who they are was never the
	// question.
	if _, err := s.Enqueue(ctx, Submission{Principal: "somebody-else", Ref: "arxiv:7777.7", Kind: KindAudit, Project: "rich", PaperKey: "arxiv:7777.7"}); err != nil {
		t.Fatalf("a funded project admits any submitter: %v", err)
	}
}

func TestMemStoreGateContract(t *testing.T) {
	ledger := credit.NewMemStore(nil, nil)
	s := NewMemStore(WithCredits(ledger, fixedReserve(testReserve)))
	fund(t, ledger, "p", 4*testReserve)
	fund(t, ledger, "rich", testReserve)
	runGateContract(t, s, ledger)
}

// At the boundary, exactly what the pot can commit gets through. The in-memory
// store passes this under its mutex; the Postgres one is where it has teeth,
// and is the reason the check and the hold run inside the inserting transaction
// under an advisory lock rather than as a read-then-insert.
func runGateConcurrency(t *testing.T, s Store, room int, attempts int) {
	t.Helper()
	ctx := context.Background()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ok, refused, other int
	for i := range attempts {
		wg.Go(func() {
			ref := "arxiv:9000." + string(rune('a'+i))
			_, err := s.Enqueue(ctx, Submission{Principal: "racer", Ref: ref, Kind: KindAudit, Project: "race", PaperKey: ref})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrNoCredits):
				refused++
			default:
				other++
				t.Errorf("unexpected enqueue error: %v", err)
			}
		})
	}
	wg.Wait()
	if ok != room || refused != attempts-room || other != 0 {
		t.Errorf("concurrent enqueues: %d admitted, %d refused, %d errored; want exactly %d admitted", ok, refused, other, room)
	}
}

func TestMemStoreGateIsExactUnderConcurrency(t *testing.T) {
	ledger := credit.NewMemStore(nil, nil)
	s := NewMemStore(WithCredits(ledger, fixedReserve(testReserve)))
	fund(t, ledger, "race", 3*testReserve)
	runGateConcurrency(t, s, 3, 12)
}

// The Postgres gate, against a real database. This is the only place the
// concurrency claim is genuinely tested: under READ COMMITTED a read-then-hold
// check admits more sessions than the pot can pay for.
func TestPGStoreGateContract(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres gate contract")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("job migrate: %v", err)
	}
	if err := credit.Migrate(ctx, pool); err != nil {
		t.Fatalf("credit migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE jobs, credit_entries`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	ledger := credit.NewPGStore(pool, nil, nil)
	fund(t, ledger, "p", 4*testReserve)
	fund(t, ledger, "rich", testReserve)
	runGateContract(t, NewPGStore(pool, WithPGCredits(ledger, fixedReserve(testReserve))), ledger)

	if _, err := pool.Exec(ctx, `TRUNCATE jobs, credit_entries`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	fund(t, ledger, "race", 3*testReserve)
	runGateConcurrency(t, NewPGStore(pool, WithPGCredits(ledger, fixedReserve(testReserve))), 3, 12)
}

// An unmetered store (no ledger wired) admits everything — an offline run and
// every test that is not about money keep working.
func TestNoLedgerMeansUnmetered(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	for i := range 20 {
		ref := "arxiv:7000." + string(rune('a'+i))
		if _, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: ref, Kind: KindAudit, Project: "p", PaperKey: ref}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
}
