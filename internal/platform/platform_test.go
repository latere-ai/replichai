package platform

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/draft"
)

var ctx = context.Background()

var (
	admin = access.Principal{Sub: "admin-sub", Email: "admin@example.com"}
	user  = access.Principal{Sub: "user-sub", Email: "user@example.com"}
)

// newAuthority builds an access store with one admin and one plain user bound,
// so the platform store's tier re-check has something real to resolve against.
func newAuthority(t *testing.T) *access.MemStore {
	t.Helper()
	acc := access.NewMemStore()
	if err := acc.SeedAdmin(ctx, admin.Email); err != nil {
		t.Fatalf("SeedAdmin: %v", err)
	}
	if err := acc.SeedGrant(ctx, user.Email, access.TierUser); err != nil {
		t.Fatalf("SeedGrant: %v", err)
	}
	for _, p := range []access.Principal{admin, user} {
		if _, ok, _ := acc.Resolve(ctx, p); !ok {
			t.Fatalf("%s should resolve granted", p.Email)
		}
	}
	return acc
}

// contract is every invariant a Store must satisfy, run against both
// implementations so the two cannot drift.
func contract(t *testing.T, s Store) {
	t.Helper()

	// The seeded value is the compiled-in default, and it is one number rather
	// than two that could drift.
	got, err := s.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.StartReserve != DefaultStartReserve {
		t.Errorf("seeded reserve = %d, want %d", got.StartReserve, DefaultStartReserve)
	}
	// The purchase spread and floor are seeded with their defaults too, so a
	// fresh deployment can price a top-up before an admin touches anything.
	if got.SpreadBps != DefaultSpreadBps || got.SpreadFixedMicro != DefaultSpreadFixedMicro || got.MinTopupMicro != DefaultMinTopupMicro {
		t.Errorf("seeded spread = {bps:%d fixed:%d min:%d}, want the defaults", got.SpreadBps, got.SpreadFixedMicro, got.MinTopupMicro)
	}

	// A fresh deployment drafts nothing: no model is configured, which is the
	// state that leaves a description something people type (039).
	if got.Draft.Configured() {
		t.Errorf("a fresh deployment came with a drafting model: %+v", got.Draft)
	}

	// Policy is admin-only, re-checked in the store rather than trusted to the
	// route guard.
	if err := s.SetSettings(ctx, user, Settings{StartReserve: 999}); !errors.Is(err, ErrNotAdmin) {
		t.Errorf("SetSettings by a user: want ErrNotAdmin, got %v", err)
	}

	// An admin's number sticks, and is recorded against them.
	if err := s.SetSettings(ctx, admin, Settings{StartReserve: 7 * credit.Dollar}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	got, err = s.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.StartReserve != 7*credit.Dollar {
		t.Errorf("reserve = %d, want %d", got.StartReserve, 7*credit.Dollar)
	}
	// The drafting model an admin configures is durable, and the row carries no
	// credential to be durable *with*: the key is the deployment's, from its
	// environment, and this struct has nowhere to put one.
	if err := s.SetSettings(ctx, admin, Settings{
		StartReserve: 7 * credit.Dollar,
		Draft:        draft.Config{Provider: "openai", BaseURL: "https://models.example/v1", Model: "small-1"},
	}); err != nil {
		t.Fatalf("SetSettings with a drafting model: %v", err)
	}
	if got, err = s.Settings(ctx); err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.Draft.Model != "small-1" || got.Draft.BaseURL != "https://models.example/v1" || !got.Draft.Configured() {
		t.Errorf("drafting config = %+v, did not survive the store", got.Draft)
	}

	if got.UpdatedBy != admin.Email || got.UpdatedAt.IsZero() {
		t.Errorf("settings = %+v, want the admin and a time recorded", got)
	}

	// A negative reserve cannot mean anything.
	if err := s.SetSettings(ctx, admin, Settings{StartReserve: -1}); !errors.Is(err, ErrNegative) {
		t.Errorf("negative reserve: want ErrNegative, got %v", err)
	}

	// The purchase spread and floor round-trip, and a negative one is refused.
	if err := s.SetSettings(ctx, admin, Settings{
		StartReserve: DefaultStartReserve, SpreadBps: 400, SpreadFixedMicro: 250_000, MinTopupMicro: 3 * credit.Dollar,
	}); err != nil {
		t.Fatalf("SetSettings spread: %v", err)
	}
	got, err = s.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.SpreadBps != 400 || got.SpreadFixedMicro != 250_000 || got.MinTopupMicro != 3*credit.Dollar {
		t.Errorf("spread round-trip = {bps:%d fixed:%d min:%d}, want {400 250000 %d}", got.SpreadBps, got.SpreadFixedMicro, got.MinTopupMicro, 3*credit.Dollar)
	}
	if err := s.SetSettings(ctx, admin, Settings{SpreadBps: -1}); !errors.Is(err, ErrNegative) {
		t.Errorf("negative spread: want ErrNegative, got %v", err)
	}

	// Restore, so a shared database is left as the next test expects it.
	if err := s.SetSettings(ctx, admin, Settings{
		StartReserve: DefaultStartReserve, SpreadBps: DefaultSpreadBps, SpreadFixedMicro: DefaultSpreadFixedMicro, MinTopupMicro: DefaultMinTopupMicro,
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

// Credited applies the purchase spread — the percentage cut and the fixed cut —
// and never returns less than zero.
func TestCreditedAppliesTheSpread(t *testing.T) {
	// $20.00 gross, 5% + $0.30 → $1.00 + $0.30 off → $18.70 credited.
	s := Settings{SpreadBps: 500, SpreadFixedMicro: 300_000}
	if got, want := s.Credited(20*credit.Dollar), int64(20*credit.Dollar-1*credit.Dollar-300_000); got != want {
		t.Errorf("Credited($20) = %d, want %d", got, want)
	}
	// A gross the fixed cut exceeds credits zero, never a negative wallet from a purchase.
	if got := (Settings{SpreadFixedMicro: 1 * credit.Dollar}).Credited(100_000); got != 0 {
		t.Errorf("Credited below the fixed cut = %d, want 0", got)
	}
	// No spread credits the whole gross.
	if got := (Settings{}).Credited(20 * credit.Dollar); got != 20*credit.Dollar {
		t.Errorf("Credited with no spread = %d, want the gross", got)
	}
}

func TestMemStoreSatisfiesTheContract(t *testing.T) {
	contract(t, NewMemStore(newAuthority(t)))
}

// The reserve fails **high**, and this is the direction that matters. A reserve
// resolving to zero when the settings read fails would let any project start a
// session on an empty pot — the unbounded spend 011 exists to prevent — so the
// seeded value and the fallback are one constant, and neither is zero.
func TestTheReserveFailsHigh(t *testing.T) {
	if DefaultStartReserve <= 0 {
		t.Fatalf("DefaultStartReserve = %d; a zero reserve admits a session on an empty pot", DefaultStartReserve)
	}
	got, err := NewMemStore(nil).Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.StartReserve != DefaultStartReserve {
		t.Errorf("fresh store reserve = %d, want the compiled-in %d", got.StartReserve, DefaultStartReserve)
	}
	// And the adapter the enqueue gate reads through resolves the same way when
	// the store itself is broken: the gate never sees "no reserve".
	n, err := StartReserve(brokenStore{})(ctx)
	if err != nil {
		t.Fatalf("StartReserve on a failing store returned an error the gate would have to interpret: %v", err)
	}
	if n != DefaultStartReserve {
		t.Errorf("reserve on a failing store = %d, want %d", n, DefaultStartReserve)
	}
}

// brokenStore stands in for a database that is not answering.
type brokenStore struct{}

func (brokenStore) Settings(context.Context) (Settings, error) {
	return Settings{}, errors.New("postgres is not answering")
}
func (brokenStore) SetSettings(context.Context, access.Principal, Settings) error {
	return errors.New("postgres is not answering")
}

// The contract against a real Postgres, including the single-row constraint.
func TestPGStoreSatisfiesTheContract(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	contract(t, NewPGStore(pool, newAuthority(t)))

	// A second settings row is a constraint violation rather than an ambiguity
	// about which row is the policy.
	if _, err := pool.Exec(ctx,
		`INSERT INTO platform_settings (id, start_reserve, updated_by, updated_at) VALUES (2, 1, 'x', now())`,
	); err == nil {
		t.Error("a second platform_settings row was accepted; the policy must be one row")
	}
}

// The read is fail-closed at the store, not just at the constant: a missing row
// yields the conservative default rather than nothing.
func TestAMissingRowStillYieldsAReserve(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DELETE FROM platform_settings`); err != nil {
		t.Fatalf("delete row: %v", err)
	}
	got, err := NewPGStore(pool, newAuthority(t)).Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.StartReserve != DefaultStartReserve {
		t.Errorf("missing row = %d, want the conservative default %d", got.StartReserve, DefaultStartReserve)
	}
}

// The cutover to money is a schema change, not a conversion. 011 keeps no
// session counter and no quota top-up table: the two hold counts, and what
// replaces them holds money, so there is nothing to carry across — the rule
// 006q set when it refused to grandfather anybody.
func TestTheMigrationLeavesNoCounterBehind(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()

	for _, tc := range []struct{ query, name string }{
		{`SELECT 1 FROM information_schema.columns WHERE table_name='platform_settings' AND column_name='default_session_quota'`, "platform_settings.default_session_quota"},
		{`SELECT 1 FROM information_schema.tables WHERE table_name='quota_topups'`, "quota_topups"},
	} {
		var found int
		if err := pool.QueryRow(ctx, tc.query).Scan(&found); err == nil {
			t.Errorf("%s still exists; 011 removes the counter rather than deprecating it", tc.name)
		}
	}

	var reserve int64
	if err := pool.QueryRow(ctx, `SELECT start_reserve FROM platform_settings WHERE id=1`).Scan(&reserve); err != nil {
		t.Fatalf("read start_reserve: %v", err)
	}
	if reserve != DefaultStartReserve {
		t.Errorf("migrated reserve = %d, want the compiled-in %d", reserve, DefaultStartReserve)
	}
}

// platformTestDB is this package's own database: its tests delete and rewrite
// the single settings row, which cannot be shared with a package running in
// parallel against the same server.
const platformTestDB = "replichai_platform_test"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres platform tests")
	}
	adminPool, err := pgxpool.New(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	_, _ = adminPool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid<>pg_backend_pid()`, platformTestDB)
	if _, err := adminPool.Exec(ctx, `DROP DATABASE IF EXISTS `+platformTestDB); err != nil {
		adminPool.Close()
		t.Fatalf("drop db: %v", err)
	}
	if _, err := adminPool.Exec(ctx, `CREATE DATABASE `+platformTestDB); err != nil {
		adminPool.Close()
		t.Fatalf("create db: %v", err)
	}
	adminPool.Close()

	pool, err := pgxpool.New(ctx, swapDBName(t, base, platformTestDB))
	if err != nil {
		t.Fatalf("connect to %s: %v", platformTestDB, err)
	}
	if err := access.Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("access migrate: %v", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		t.Fatalf("platform migrate: %v", err)
	}
	return pool
}

func swapDBName(t *testing.T, raw, name string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}
