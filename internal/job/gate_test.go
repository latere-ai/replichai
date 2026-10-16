// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
)

// testReserve is the gate's reserve, fixed for a test. A session may start
// only where this much is available, and starting holds exactly this much.
const testReserve = 2 * credit.Dollar

func fixedReserve(n int64) credit.Reserve {
	return func(context.Context) (int64, error) { return n, nil }
}

// funder is the part of a ledger that fund uses to put credit into a project's
// pot without going through the membership checks, which are the ledger's
// concern and not the gate's.
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

	// A project that cannot commit the reserve cannot start a session. Paper
	// keys are distinct throughout, so the double-trigger guard never refuses.
	broke := Submission{Principal: "u", Ref: "arxiv:0000.0", Kind: KindAudit, Project: "poor", PaperKey: "arxiv:0000.0"}
	if _, err := s.Enqueue(ctx, broke); !errors.Is(err, ErrNoCredits) {
		t.Fatalf("submitting into an unfunded project: want ErrNoCredits, got %v", err)
	}

	// A funded project can, and starting holds the reserve rather than spending
	// it: the balance is unchanged, and only the available amount moved.
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

	// Each further session holds its own reserve, so a burst cannot be admitted
	// against money the earlier ones already committed. Four reserves are
	// funded and one is taken.
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

	// The pot pays, not the person: a different submitter in the same project
	// is refused in the same way.
	other := Submission{Principal: "somebody-else", Ref: "arxiv:6666.6", Kind: KindAudit, Project: "p", PaperKey: "arxiv:6666.6"}
	if _, err := s.Enqueue(ctx, other); !errors.Is(err, ErrNoCredits) {
		t.Fatalf("another member in the same exhausted project: want ErrNoCredits, got %v", err)
	}
	// A funded project admits them, because the gate does not check the
	// submitter's identity.
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

// runGateConcurrency checks that at the boundary exactly as many sessions as
// the pot can commit are admitted. The in-memory store passes this under its
// mutex. The Postgres store is where the check matters, and is the reason the
// check and the hold run inside the inserting transaction under an advisory
// lock rather than as a read followed by an insert.
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

// TestPGStoreGateContract runs the gate contract against a real database. This
// is the only place the concurrency claim is tested: under READ COMMITTED a
// read followed by a hold admits more sessions than the pot can pay for.
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
	if _, err := pool.Exec(ctx, `TRUNCATE jobs, ledger_entries`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	ledger := credit.NewPGStore(pool, nil, nil)
	fund(t, ledger, "p", 4*testReserve)
	fund(t, ledger, "rich", testReserve)
	runGateContract(t, NewPGStore(pool, WithPGCredits(ledger, fixedReserve(testReserve))), ledger)

	if _, err := pool.Exec(ctx, `TRUNCATE jobs, ledger_entries`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	fund(t, ledger, "race", 3*testReserve)
	runGateConcurrency(t, NewPGStore(pool, WithPGCredits(ledger, fixedReserve(testReserve))), 3, 12)
}

// TestPGStoreEnqueueOnOneConnection checks that starting a project-paid
// session needs only the one connection its transaction holds. Production
// serves through a pooler that holds a single server connection, and the
// reserve is read from the platform settings over the pool. Read inside the
// transaction, that read waited for a connection the transaction itself held,
// so every start hung until the pooler gave up and the whole app stalled behind
// it.
func TestPGStoreEnqueueOnOneConnection(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres gate contract")
	}
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer setup.Close()
	if err := Migrate(ctx, setup); err != nil {
		t.Fatalf("job migrate: %v", err)
	}
	if err := credit.Migrate(ctx, setup); err != nil {
		t.Fatalf("credit migrate: %v", err)
	}
	if _, err := setup.Exec(ctx, `TRUNCATE jobs, ledger_entries`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	fund(t, credit.NewPGStore(setup, nil, nil), "one", testReserve)

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	// The reserve is read over the same pool, as platform.StartReserve reads
	// the settings row.
	reserve := func(ctx context.Context) (int64, error) {
		var n int64
		err := pool.QueryRow(ctx, `SELECT $1::bigint`, testReserve).Scan(&n)
		return n, err
	}
	store := NewPGStore(pool, WithPGCredits(credit.NewPGStore(pool, nil, nil), reserve))

	start, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := store.Enqueue(start, Submission{Principal: "p", Ref: "x", Kind: KindAudit, Project: "one", PaperKey: "k"}); err != nil {
		t.Fatalf("a project-paid start on one connection: %v", err)
	}
}

// TestNoLedgerMeansUnmetered checks that a store with no ledger admits every
// submission, so an offline run and every test that is not about money keep
// working.
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
