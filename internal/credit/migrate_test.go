package credit

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// legacySchema is the ledger as it stood before it moved to latere.ai/x/pay:
// migration v1 plus the v2 project columns and the v3 external reference.
const legacySchema = `
CREATE TABLE credit_entries (
	id           TEXT PRIMARY KEY,
	holder       TEXT NOT NULL,
	amount       BIGINT NOT NULL,
	kind         TEXT NOT NULL,
	job_id       TEXT,
	actor        TEXT NOT NULL,
	created_at   TIMESTAMPTZ NOT NULL,
	project_id   TEXT,
	project_name TEXT,
	external_ref TEXT
);
CREATE UNIQUE INDEX credit_entries_one_charge ON credit_entries (job_id) WHERE kind = 'charge';
CREATE UNIQUE INDEX credit_entries_external_ref ON credit_entries (external_ref) WHERE external_ref IS NOT NULL;
`

// seedLegacy writes one row of every historical kind, so the mapping is
// exercised across the whole vocabulary rather than the two kinds a happy path
// touches.
const seedLegacy = `
INSERT INTO credit_entries (id, holder, amount, kind, job_id, actor, created_at, project_id, project_name, external_ref) VALUES
 ('e1', 'user:a@x.test',  20000000, 'topup',    NULL,    'admin@x.test', now(), NULL, NULL, NULL),
 ('e2', 'user:a@x.test',  18700000, 'topup',    NULL,    'stripe:pi_1',  now(), NULL, NULL, 'pi_1'),
 ('e3', 'user:a@x.test', -10000000, 'allocate', NULL,    'a@x.test',     now(), 'p1', 'A study', NULL),
 ('e4', 'project:p1',     10000000, 'allocate', NULL,    'a@x.test',     now(), 'p1', 'A study', NULL),
 ('e5', 'project:p1',     -3000000, 'hold',     'job-1', 'system',       now(), NULL, NULL, NULL),
 ('e6', 'project:p1',      3000000, 'release',  'job-1', 'system',       now(), NULL, NULL, NULL),
 ('e7', 'project:p1',     -1500000, 'charge',   'job-1', 'system',       now(), NULL, NULL, NULL),
 ('e8', 'project:p1',      -250000, 'draft',    NULL,    'a@x.test',     now(), NULL, NULL, 'draft-1'),
 ('e9', 'user:a@x.test',  -1870000, 'adjust',   NULL,    'stripe:refund:re_1', now(), NULL, NULL, 're_1'),
 ('e10','project:p1',      -500000, 'reclaim',  NULL,    'a@x.test',     now(), 'p1', 'A study', NULL),
 ('e11','user:a@x.test',    500000, 'reclaim',  NULL,    'a@x.test',     now(), 'p1', 'A study', NULL);
`

// The migration's whole job is that no balance changes. A ledger that loses a
// row is not recoverable, so this asserts the fold rather than the row count.
func TestTheBackfillPreservesEveryBalance(t *testing.T) {
	pool := legacyPool(t)
	ctx := context.Background()

	// What the legacy ledger folds to, before anything moves.
	var wantWallet, wantPot, wantTotal int64
	q := `SELECT COALESCE(SUM(amount),0) FROM credit_entries WHERE holder=$1 AND kind NOT IN ('hold','release')`
	if err := pool.QueryRow(ctx, q, "user:a@x.test").Scan(&wantWallet); err != nil {
		t.Fatalf("fold wallet: %v", err)
	}
	if err := pool.QueryRow(ctx, q, "project:p1").Scan(&wantPot); err != nil {
		t.Fatalf("fold pot: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount),0) FROM credit_entries WHERE kind NOT IN ('hold','release')`).Scan(&wantTotal); err != nil {
		t.Fatalf("fold total: %v", err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewPGStore(pool, nil, nil)

	if got, _ := s.Balance(ctx, Wallet("a@x.test")); got != wantWallet {
		t.Errorf("wallet balance = %d, want %d", got, wantWallet)
	}
	if got, _ := s.Balance(ctx, Pot("p1")); got != wantPot {
		t.Errorf("pot balance = %d, want %d", got, wantPot)
	}
	if got, _ := s.TotalOutstanding(ctx); got != wantTotal {
		t.Errorf("outstanding = %d, want %d", got, wantTotal)
	}
	// And the reconciliation an operator runs agrees.
	legacy, carried, err := Reconcile(ctx, pool)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if legacy != carried {
		t.Errorf("reconcile: legacy %d, carried %d", legacy, carried)
	}

	// The hold and its release came across as a matched pair, so an available
	// balance is not off by a stranded commitment.
	avail, _ := s.Available(ctx, Pot("p1"))
	if bal, _ := s.Balance(ctx, Pot("p1")); avail != bal {
		t.Errorf("available %d != balance %d: a hold was stranded by the copy", avail, bal)
	}
}

// A statement rendered from the carried rows must read exactly as it did
// before: the historical kinds are what the browser knows.
func TestTheBackfillPreservesTheStatementVocabulary(t *testing.T) {
	pool := legacyPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewPGStore(pool, nil, nil)

	rows, err := s.Entries(ctx, Wallet("a@x.test"), 50)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	seen := map[Kind]int{}
	for _, r := range rows {
		seen[r.Kind]++
	}
	for _, k := range []Kind{KindTopup, KindAllocate, KindReclaim, KindAdjust} {
		if seen[k] == 0 {
			t.Errorf("no %s entry in the carried statement", k)
		}
	}
	// The purchased top-up keeps its processor reference; the admin grant has
	// none, exactly as before.
	var paid, granted int
	for _, r := range rows {
		if r.Kind != KindTopup {
			continue
		}
		if r.ExternalRef == "pi_1" {
			paid++
		}
		if r.ExternalRef == "" {
			granted++
		}
	}
	if paid != 1 || granted != 1 {
		t.Errorf("top-ups carried: %d paid, %d granted; want one of each", paid, granted)
	}
	// The snapshotted project name survived as a label.
	var named bool
	for _, r := range rows {
		if r.Kind == KindAllocate && r.ProjectName == "A study" && r.ProjectID == "p1" {
			named = true
		}
	}
	if !named {
		t.Error("the allocate entry lost its snapshotted project")
	}
}

// A rolling deploy runs the migration from two pods at once, and it runs again
// on every restart. Neither may double the balances.
func TestTheBackfillIsIdempotent(t *testing.T) {
	pool := legacyPool(t)
	ctx := context.Background()
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := NewPGStore(pool, nil, nil)
	first, _ := s.TotalOutstanding(ctx)
	for i := 0; i < 3; i++ {
		if err := Migrate(ctx, pool); err != nil {
			t.Fatalf("migrate again: %v", err)
		}
	}
	again, _ := s.TotalOutstanding(ctx)
	if first != again {
		t.Errorf("outstanding %d then %d: the copy ran twice", first, again)
	}
}

// A fresh database has no legacy table, and the migration must not trip over
// its absence.
func TestMigrateOnAFreshDatabase(t *testing.T) {
	pool := legacyPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP TABLE credit_entries`); err != nil {
		t.Fatalf("drop legacy: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate on a fresh database: %v", err)
	}
	s := NewPGStore(pool, nil, nil)
	if got, _ := s.TotalOutstanding(ctx); got != 0 {
		t.Errorf("outstanding on a fresh database = %d, want 0", got)
	}
}

// legacyPool returns a clean database holding the pre-migration ledger.
func legacyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()
	// testPool has already applied the new schema; start from the old one.
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS ledger_entries, ledger_migrations`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := pool.Exec(ctx, legacySchema); err != nil {
		t.Fatalf("legacy schema: %v", err)
	}
	if _, err := pool.Exec(ctx, seedLegacy); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return pool
}
