// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/provider"
)

// A card with round numbers, so a cost is checkable by hand.
var testCard = credit.Card{Models: []credit.Rates{
	{Model: "cheap", Input: 1 * credit.Dollar, Output: 2 * credit.Dollar, CacheRead: 100_000, CacheWrite5m: 1_250_000, CacheWrite1h: 2 * credit.Dollar},
	{Model: "dear", Input: 10 * credit.Dollar, Output: 20 * credit.Dollar, CacheRead: 1 * credit.Dollar, CacheWrite5m: 12_500_000, CacheWrite1h: 20 * credit.Dollar},
}}

func fixedCard(c credit.Card) credit.CardSnapshot {
	return func(context.Context) (credit.Card, error) { return c, nil }
}

// One million input and half a million output tokens on the cheap model: $1 +
// $1 = $2.
var burned = provider.RunStatus{
	Model: "cheap",
	Usage: provider.Usage{InputTokens: 1_000_000, OutputTokens: 500_000},
}

const burnedCost = 2 * credit.Dollar

// runSettleContract asserts every settlement invariant a Store must hold, so
// the in-memory and Postgres implementations cannot diverge. The project "p" is
// funded with four reserves.
func runSettleContract(t *testing.T, s Store, ledger credit.Store) {
	t.Helper()
	ctx := context.Background()

	start := func(t *testing.T, ref string) Job {
		t.Helper()
		j, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: ref, Kind: KindAudit, Project: "p", PaperKey: ref})
		if err != nil {
			t.Fatalf("enqueue %s: %v", ref, err)
		}
		return j
	}
	drive := func(t *testing.T, id string, to ...Phase) {
		t.Helper()
		for _, p := range to {
			if err := s.Advance(ctx, id, p, nil); err != nil {
				t.Fatalf("advance %s to %s: %v", id, p, err)
			}
		}
	}

	before, _ := ledger.Balance(ctx, credit.Pot("p"))

	// A session that finishes is charged what it burned, and its reserve comes
	// back — the balance moves by the cost, not by the reserve.
	done := start(t, "arxiv:1111.1")
	if err := s.Observe(ctx, done.ID, burned); err != nil {
		t.Fatalf("observe: %v", err)
	}
	drive(t, done.ID, PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone)

	if n, _ := ledger.Balance(ctx, credit.Pot("p")); n != before-burnedCost {
		t.Errorf("balance after a finished session = %d, want %d", n, before-burnedCost)
	}
	// Balance and available agree again: the hold is gone, not merely spent.
	bal, _ := ledger.Balance(ctx, credit.Pot("p"))
	avail, _ := ledger.Available(ctx, credit.Pot("p"))
	if bal != avail {
		t.Errorf("balance %d != available %d — the hold outlived the session", bal, avail)
	}

	// "Failed is refunded" is gone. A session that burned tokens and was then
	// cancelled is charged for them: a refund for a run that cost real money is
	// the platform paying for it.
	stopped := start(t, "arxiv:2222.2")
	if err := s.Observe(ctx, stopped.ID, burned); err != nil {
		t.Fatalf("observe: %v", err)
	}
	mark, _ := ledger.Balance(ctx, credit.Pot("p"))
	if _, err := s.Cancel(ctx, stopped.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if n, _ := ledger.Balance(ctx, credit.Pot("p")); n != mark-burnedCost {
		t.Errorf("balance after a cancelled session = %d, want %d — a run that burned tokens is charged for them", n, mark-burnedCost)
	}

	// A session that never reached the provider releases its hold and is charged
	// nothing: there is no usage to price.
	untouched := start(t, "arxiv:3333.3")
	mark, _ = ledger.Balance(ctx, credit.Pot("p"))
	if _, err := s.Cancel(ctx, untouched.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if n, _ := ledger.Balance(ctx, credit.Pot("p")); n != mark {
		t.Errorf("balance after a session that never ran = %d, want %d unchanged", n, mark)
	}
	if b, _ := ledger.Balance(ctx, credit.Pot("p")); b != mustAvailable(t, ledger, "p") {
		t.Error("an unrun session left its hold open")
	}

	// Settlement is exactly-once. Cancelling again — which is idempotent at the
	// phase level — must not charge or release a second time.
	mark, _ = ledger.Balance(ctx, credit.Pot("p"))
	if _, err := s.Cancel(ctx, stopped.ID); err != nil {
		t.Fatalf("re-cancel: %v", err)
	}
	if n, _ := ledger.Balance(ctx, credit.Pot("p")); n != mark {
		t.Errorf("balance after re-cancelling = %d, want %d — settlement charged twice", n, mark)
	}

	// Exactly one charge exists per session, whatever happened to it.
	entries, err := ledger.Entries(ctx, credit.Pot("p"), 0)
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	charges := map[string]int{}
	for _, e := range entries {
		if e.Kind == credit.KindCharge {
			charges[e.JobID]++
		}
	}
	for _, id := range []string{done.ID, stopped.ID, untouched.ID} {
		if charges[id] != 1 {
			t.Errorf("session %s has %d charges, want exactly 1", id, charges[id])
		}
	}
}

func mustAvailable(t *testing.T, ledger credit.Store, project string) int64 {
	t.Helper()
	n, err := ledger.Available(context.Background(), credit.Pot(project))
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	return n
}

func TestMemStoreSettleContract(t *testing.T) {
	ledger := credit.NewMemStore(nil, nil)
	s := NewMemStore(WithCredits(ledger, fixedReserve(testReserve)), WithRates(fixedCard(testCard)))
	fund(t, ledger, "p", 20*credit.Dollar)
	runSettleContract(t, s, ledger)
}

// The rate card a session is charged at is the one in force when it *started*.
// An admin editing prices mid-run must not make the ceiling a run was watched
// against and the amount it is finally charged come from different numbers.
func TestTheRatesAreSnapshottedAtEnqueue(t *testing.T) {
	ctx := context.Background()
	ledger := credit.NewMemStore(nil, nil)
	card := testCard
	s := NewMemStore(
		WithCredits(ledger, fixedReserve(testReserve)),
		WithRates(func(context.Context) (credit.Card, error) { return card, nil }),
	)
	fund(t, ledger, "p", 40*credit.Dollar)

	j, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: "r", Kind: KindAudit, Project: "p", PaperKey: "r"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// The admin puts the prices up tenfold after the session has started.
	card = credit.Card{Models: []credit.Rates{{
		Model: "cheap", Input: 10 * credit.Dollar, Output: 20 * credit.Dollar,
		CacheRead: 1 * credit.Dollar, CacheWrite5m: 12_500_000, CacheWrite1h: 20 * credit.Dollar,
	}}}

	if err := s.Observe(ctx, j.ID, burned); err != nil {
		t.Fatalf("observe: %v", err)
	}
	before, _ := ledger.Balance(ctx, credit.Pot("p"))
	for _, p := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if err := s.Advance(ctx, j.ID, p, nil); err != nil {
			t.Fatalf("advance: %v", err)
		}
	}
	if n, _ := ledger.Balance(ctx, credit.Pot("p")); n != before-burnedCost {
		t.Errorf("charged %d, want %d — the session was priced at the admin's later rates", before-n, burnedCost)
	}
}

// An unknown model is charged at the dearest rates on the snapshotted card. A
// vendor shipping a model id the card does not name must not become free
// compute.
func TestAnUnknownModelIsChargedTheDearestRates(t *testing.T) {
	ctx := context.Background()
	ledger := credit.NewMemStore(nil, nil)
	s := NewMemStore(WithCredits(ledger, fixedReserve(testReserve)), WithRates(fixedCard(testCard)))
	fund(t, ledger, "p", 40*credit.Dollar)

	j, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: "r", Kind: KindAudit, Project: "p", PaperKey: "r"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := s.Observe(ctx, j.ID, provider.RunStatus{
		Model: "claude-something-6",
		Usage: provider.Usage{InputTokens: 1_000_000},
	}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	before, _ := ledger.Balance(ctx, credit.Pot("p"))
	for _, p := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if err := s.Advance(ctx, j.ID, p, nil); err != nil {
			t.Fatalf("advance: %v", err)
		}
	}
	after, _ := ledger.Balance(ctx, credit.Pot("p"))
	if before-after != 10*credit.Dollar {
		t.Errorf("charged %d for an unknown model, want the dearest input rate %d", before-after, 10*credit.Dollar)
	}
}

// The Postgres settlement, against a real database. This is the only place the
// exactly-once claim is genuinely tested: the partial unique index is what
// stops a retried or concurrent terminal transition charging twice.
func TestPGStoreSettleContract(t *testing.T) {
	pool := settlePool(t)
	defer pool.Close()
	ledger := credit.NewPGStore(pool, nil, nil)
	s := NewPGStore(pool,
		WithPGCredits(ledger, fixedReserve(testReserve)),
		WithPGRates(fixedCard(testCard)))
	fund(t, ledger, "p", 20*credit.Dollar)
	runSettleContract(t, s, ledger)
}

// Concurrent cancels of one session produce one charge.
//
// What makes this hold is the phase machine, not the ledger: Cancel's UPDATE
// carries `WHERE phase NOT IN ('done','failed')`, so exactly one of the racing
// writers ever reaches settlement and the rest read back the terminal row. The
// ledger's own exactly-once guard is a second line behind that, and is asserted
// where it actually bites — by settling the same session twice directly, in
// internal/credit's store contract. Removing that guard does not fail this
// test, which is why it does not claim to test it.
func TestPGStoreConcurrentCancelsSettleOnce(t *testing.T) {
	pool := settlePool(t)
	defer pool.Close()
	ctx := context.Background()
	ledger := credit.NewPGStore(pool, nil, nil)
	s := NewPGStore(pool,
		WithPGCredits(ledger, fixedReserve(testReserve)),
		WithPGRates(fixedCard(testCard)))
	fund(t, ledger, "p", 20*credit.Dollar)

	j, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: "r", Kind: KindAudit, Project: "p", PaperKey: "r"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := s.Observe(ctx, j.ID, burned); err != nil {
		t.Fatalf("observe: %v", err)
	}
	before, _ := ledger.Balance(ctx, credit.Pot("p"))

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := s.Cancel(ctx, j.ID); err != nil {
				t.Errorf("cancel: %v", err)
			}
		})
	}
	wg.Wait()

	if n, _ := ledger.Balance(ctx, credit.Pot("p")); n != before-burnedCost {
		t.Errorf("balance = %d, want %d — concurrent cancels settled more than once", n, before-burnedCost)
	}
	entries, _ := ledger.Entries(ctx, credit.Pot("p"), 0)
	charges := 0
	for _, e := range entries {
		if e.Kind == credit.KindCharge && e.JobID == j.ID {
			charges++
		}
	}
	if charges != 1 {
		t.Errorf("%d charges for one session, want exactly 1", charges)
	}
}

// settlePool returns a clean database with the job and credit schemas applied.
func settlePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres settlement contract")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("job migrate: %v", err)
	}
	if err := credit.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("credit migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE jobs, ledger_entries`); err != nil {
		pool.Close()
		t.Fatalf("truncate: %v", err)
	}
	return pool
}

// A metered deployment that cannot price a session refuses it at the door.
//
// The alternative is worse than it looks: an empty card makes Cost() zero, so
// the session settles at nothing *and* is invisible to the backstop's accrual —
// it would run for free and count against no floor. Tokens nobody can price are
// not free tokens, so the refusal is at enqueue, where nothing has been spent.
func TestAnUnpriceableSessionIsRefusedAtTheDoor(t *testing.T) {
	ctx := context.Background()
	ledger := credit.NewMemStore(nil, nil)
	s := NewMemStore(
		WithCredits(ledger, fixedReserve(testReserve)),
		WithRates(fixedCard(credit.Card{})),
	)
	fund(t, ledger, "p", 10*credit.Dollar)

	_, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: "r", Kind: KindAudit, Project: "p", PaperKey: "r"})
	if !errors.Is(err, ErrUnpriceable) {
		t.Fatalf("enqueue with an empty card: want ErrUnpriceable, got %v", err)
	}
	// And nothing was committed on the way to the refusal.
	bal, _ := ledger.Balance(ctx, credit.Pot("p"))
	avail, _ := ledger.Available(ctx, credit.Pot("p"))
	if bal != avail || bal != 10*credit.Dollar {
		t.Errorf("balance %d, available %d; want %d and no hold taken", bal, avail, 10*credit.Dollar)
	}
}

// A rate card that cannot be *read* is a fault, not an unpriced deployment. It
// resolves to the compiled-in list, because falling back to nothing would make
// every session free for as long as the fault lasted.
func TestABrokenRateCardStillPrices(t *testing.T) {
	ctx := context.Background()
	ledger := credit.NewMemStore(nil, nil)
	s := NewMemStore(
		WithCredits(ledger, fixedReserve(testReserve)),
		WithRates(func(context.Context) (credit.Card, error) {
			return credit.Card{}, errors.New("postgres is not answering")
		}),
	)
	fund(t, ledger, "p", 40*credit.Dollar)

	j, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: "r", Kind: KindAudit, Project: "p", PaperKey: "r"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if len(j.Rates.Models) == 0 {
		t.Fatal("a session was admitted with no way to price it")
	}
	if err := s.Observe(ctx, j.ID, provider.RunStatus{
		Model: "claude-opus-5", Usage: provider.Usage{InputTokens: 1_000_000},
	}); err != nil {
		t.Fatalf("observe: %v", err)
	}
	before, _ := ledger.Balance(ctx, credit.Pot("p"))
	for _, p := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if err := s.Advance(ctx, j.ID, p, nil); err != nil {
			t.Fatalf("advance: %v", err)
		}
	}
	after, _ := ledger.Balance(ctx, credit.Pot("p"))
	if before-after != 5*credit.Dollar {
		t.Errorf("charged %d, want the compiled-in input rate %d", before-after, 5*credit.Dollar)
	}
}

// An in-flight session with no priced rates would be invisible to the floor.
// Accrued must see what every running session has cost, which is only true if a
// session can never be admitted unpriced in the first place.
func TestAccruedSeesEveryInFlightSession(t *testing.T) {
	ctx := context.Background()
	ledger := credit.NewMemStore(nil, nil)
	s := NewMemStore(WithCredits(ledger, fixedReserve(testReserve)), WithRates(fixedCard(testCard)))
	fund(t, ledger, "p", 20*credit.Dollar)

	for _, ref := range []string{"a", "b"} {
		j, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: ref, Kind: KindAudit, Project: "p", PaperKey: ref})
		if err != nil {
			t.Fatalf("enqueue %s: %v", ref, err)
		}
		if err := s.Observe(ctx, j.ID, burned); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}
	n, err := s.Accrued(ctx, "p")
	if err != nil {
		t.Fatalf("Accrued: %v", err)
	}
	if n != 2*burnedCost {
		t.Errorf("accrued = %d, want %d — a running session was priced at nothing", n, 2*burnedCost)
	}
}
