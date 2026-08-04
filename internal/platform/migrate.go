package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/credit"
)

// migrations are the platform-policy schema (006k), versioned and applied at
// startup alongside the job, access, and project schemas. It depends on none of
// them.
var migrations = []struct {
	version int
	sql     string
}{
	{1, `
-- One row, pinned. The check constraint makes a second row a constraint
-- violation rather than an ambiguity about which row is the policy.
CREATE TABLE platform_settings (
	id                    SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
	default_session_quota INT NOT NULL,
	updated_by            TEXT NOT NULL,
	updated_at            TIMESTAMPTZ NOT NULL
);
-- Seeded at zero. This interpolated a Go constant when it was written, which
-- 006q had to correct with a second migration; the literal is what that
-- correction settled on, and migration 3 drops the column outright.
INSERT INTO platform_settings (id, default_session_quota, updated_by, updated_at)
VALUES (1, 0, 'bootstrap', now());

-- Keyed by email, not principal, so an admin can hand someone headroom before
-- they have ever signed in — the seed-by-email rule a grant follows (006c).
CREATE TABLE quota_topups (
	email      TEXT PRIMARY KEY,
	extra      INT NOT NULL,
	granted_by TEXT NOT NULL,
	granted_at TIMESTAMPTZ NOT NULL
);
`},
	{2, `
-- 006q drops the default session quota to zero: sign-in is open, so this number
-- is no longer a second lock behind an admin's guest list but the only one.
--
-- Changing the Go constant is not enough. Migration 1 interpolated it *at
-- migration time*, so a deployment that already ran it holds whatever the
-- constant was then, and Settings() returns the row rather than the fallback.
-- Without this statement, "nobody is grandfathered" would be true of a fresh
-- database and false of the one in production.
--
-- Unconditional, and it runs exactly once. Guarding on "only the bootstrapped
-- row" would leave the cutover depending on whether anyone had ever touched the
-- console, so whether the platform actually starts everyone at zero could only
-- be answered by querying the deployment. 006q's rule is one rule for everyone;
-- a cutover that quietly applies to some deployments and not others is not that
-- rule. An admin who wants a different number sets it afterwards, and it sticks:
-- this migration is version-gated and never runs again.
UPDATE platform_settings SET default_session_quota = 0, updated_by = 'bootstrap', updated_at = now()
WHERE id = 1;
`},
	{3, fmt.Sprintf(`
-- 011 replaces the session counter with money. What an admin tunes is no longer
-- how many sessions somebody may run but how much a project must be able to
-- commit before one starts there, in micro-USD.
--
-- The old column is dropped rather than left behind. There is nothing to
-- convert: a session count and a start reserve are different quantities in
-- different units, and a number carried across would be a balance whose origin
-- nobody could read — the reason 006q refused a backfill, for the same reason.
ALTER TABLE platform_settings DROP COLUMN default_session_quota;
ALTER TABLE platform_settings ADD COLUMN start_reserve BIGINT NOT NULL DEFAULT %d;

-- The per-person session headroom an admin granted is now a wallet balance in
-- the ledger (internal/credit), which records who granted it and when. This
-- table cannot be converted into that: it holds a count, and the ledger holds
-- money.
DROP TABLE IF EXISTS quota_topups;
`, DefaultStartReserve)},
	{4, fmt.Sprintf(`
-- What a session's tokens cost (011). Policy rather than a compiled-in
-- constant: a vendor's prices move, and a constant makes every price change a
-- redeploy and a silent mispricing until then.
--
-- Seeded with the published list so a fresh deployment can price a session
-- before an admin has touched anything. An empty card prices nothing, and the
-- gate refuses a session it cannot price, so the seed is what keeps a new
-- deployment usable.
ALTER TABLE platform_settings ADD COLUMN rate_card JSONB NOT NULL DEFAULT '%s'::jsonb;
`, defaultCardJSON())},
	{5, fmt.Sprintf(`
-- How far below zero a project may fall before a run is killed (011). A
-- session runs to completion once started — killing it mid-run converts spend
-- into nothing — so this is a backstop against one runaway, not the routine
-- mechanism. The routine mechanism is the start reserve: a negative project
-- cannot begin anything until it is topped up.
ALTER TABLE platform_settings ADD COLUMN overdraft_cap BIGINT NOT NULL DEFAULT %d;
`, DefaultOverdraftCap)},
}

// defaultCardJSON renders the seeded price list for the migration. It is
// interpolated at migration time on purpose: the row is what a session is
// priced from afterwards, and an admin's later edit must not be overwritten by
// a constant changing in a subsequent release.
func defaultCardJSON() string {
	raw, err := json.Marshal(credit.DefaultCard())
	if err != nil {
		panic("marshal default rate card: " + err.Error())
	}
	// The card holds no apostrophes today; the escape keeps a model id that
	// ever does from ending the SQL literal early.
	return strings.ReplaceAll(string(raw), "'", "''")
}

// migrationLock is this migrator's advisory-lock id. A rolling deploy runs the
// old and new pods at once (maxSurge), so two processes migrate the same
// database concurrently: both find a version missing, both run its DDL, and the
// loser dies on "column already exists". It self-heals on restart, and it
// crash-loops a pod on every schema change until it does.
//
// The lock is taken inside the migration transaction and released with it, so
// the second process waits, then reads the version the first just recorded and
// skips. Each namespace has its own id, so the five migrators only ever
// serialise against another copy of themselves.
const migrationLock = 3

// Migrate applies the platform schema. It uses its own version namespace table
// so it composes with the other stores' migrations on one database without
// either owning the other's versions.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS platform_migrations (
			version    INT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create platform migrations table: %w", err)
	}
	for _, m := range migrations {
		var exists bool
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			// The check moved inside the transaction, behind the lock. Read
			// outside it, two servers starting together both saw "missing".
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM platform_migrations WHERE version = $1)`, m.version).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return nil
			}
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO platform_migrations (version) VALUES ($1)`, m.version)
			return err
		}); err != nil {
			return fmt.Errorf("apply platform migration %d: %w", m.version, err)
		}
	}
	return nil
}
