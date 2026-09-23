// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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

// migrations is the platform-policy schema (006k), versioned and applied at
// startup alongside the job, access, and project schemas. It depends on none
// of them.
var migrations = []struct {
	version int
	sql     string
}{
	{1, `
-- The table holds one row with id 1. The check constraint makes a second row
-- a constraint violation, so there is never any ambiguity about which row is
-- the policy.
CREATE TABLE platform_settings (
	id                    SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
	default_session_quota INT NOT NULL,
	updated_by            TEXT NOT NULL,
	updated_at            TIMESTAMPTZ NOT NULL
);
-- Seeded at zero. When first written this interpolated a Go constant, which
-- 006q had to correct with a second migration. The literal is the value that
-- correction settled on. Migration 3 drops the column.
INSERT INTO platform_settings (id, default_session_quota, updated_by, updated_at)
VALUES (1, 0, 'bootstrap', now());

-- Keyed by email rather than principal, so an admin can grant headroom to
-- someone who has never signed in. This follows the seed-by-email rule for
-- grants (006c).
CREATE TABLE quota_topups (
	email      TEXT PRIMARY KEY,
	extra      INT NOT NULL,
	granted_by TEXT NOT NULL,
	granted_at TIMESTAMPTZ NOT NULL
);
`},
	{2, `
-- 006q sets the default session quota to zero. Sign-in is open, so this number
-- is no longer a second limit behind an admin's guest list but the only limit.
--
-- Changing the Go constant is not enough. Migration 1 interpolated the
-- constant at migration time, so a deployment that already ran it holds
-- whatever the constant was then, and Settings() returns the row rather than
-- the fallback. Without this statement, a fresh database would start everyone
-- at zero and the production database would not.
--
-- The update is unconditional and runs exactly once. Guarding it to run only
-- on the bootstrapped row would make the cutover depend on whether anyone had
-- ever used the console, so whether the platform starts everyone at zero could
-- only be answered by querying the deployment. 006q requires one rule for
-- everyone, and a cutover that applies to some deployments and not others
-- would break it. An admin who wants a different number sets it afterwards and
-- it persists, because this migration is version-gated and never runs again.
UPDATE platform_settings SET default_session_quota = 0, updated_by = 'bootstrap', updated_at = now()
WHERE id = 1;
`},
	{3, fmt.Sprintf(`
-- 011 replaces the session counter with money. The admin-tuned value is no
-- longer how many sessions somebody may run but how much a project must be
-- able to commit before a session starts there, in micro-USD.
--
-- The old column is dropped rather than kept. There is nothing to convert: a
-- session count and a start reserve are different quantities in different
-- units, and a number carried across would be a balance whose origin nobody
-- could trace. 006q refused a backfill for the same reason.
ALTER TABLE platform_settings DROP COLUMN default_session_quota;
ALTER TABLE platform_settings ADD COLUMN start_reserve BIGINT NOT NULL DEFAULT %d;

-- The per-person session headroom an admin granted is now a wallet balance in
-- the ledger (internal/credit), which records who granted it and when. This
-- table cannot be converted into that, because it holds a count and the
-- ledger holds money.
DROP TABLE IF EXISTS quota_topups;
`, DefaultStartReserve)},
	{4, fmt.Sprintf(`
-- The rate card: what a session's tokens cost (011). It is stored as policy
-- rather than compiled in because vendor prices change, and a constant makes
-- every price change a redeploy and misprices sessions until then.
--
-- It is seeded with the published list so a fresh deployment can price a
-- session before an admin has changed anything. An empty card prices nothing,
-- and the gate refuses a session it cannot price, so the seed is what keeps a
-- new deployment usable.
ALTER TABLE platform_settings ADD COLUMN rate_card JSONB NOT NULL DEFAULT '%s'::jsonb;
`, defaultCardJSON())},
	{5, fmt.Sprintf(`
-- The overdraft cap: how far below zero a project may fall before a run is
-- killed (011). A session normally runs to completion once started, because
-- killing it mid-run wastes what it has already spent, so this cap is a
-- backstop against one runaway session rather than the routine mechanism. The
-- routine mechanism is the start reserve: a project with a negative balance
-- cannot start anything until it is topped up.
ALTER TABLE platform_settings ADD COLUMN overdraft_cap BIGINT NOT NULL DEFAULT %d;
`, DefaultOverdraftCap)},
	{6, fmt.Sprintf(`
-- The purchase spread and the minimum top-up (022b). The platform takes its
-- margin at the point of purchase, so the rate card stays at vendor rates and
-- a session's cost still reconciles against them. The spread is a percentage
-- plus a fixed cut, the same shape as a card processor's fee, so a small
-- top-up still covers its cost. The columns are seeded with defaults an admin
-- then tunes.
ALTER TABLE platform_settings ADD COLUMN spread_bps BIGINT NOT NULL DEFAULT %d;
ALTER TABLE platform_settings ADD COLUMN spread_fixed_micro BIGINT NOT NULL DEFAULT %d;
ALTER TABLE platform_settings ADD COLUMN min_topup_micro BIGINT NOT NULL DEFAULT %d;
`, DefaultSpreadBps, DefaultSpreadFixedMicro, DefaultMinTopupMicro)},
	{7, `
-- The drafting model: which model writes a project's description when somebody
-- asks for a draft (039), as a protocol, an endpoint and a model id. The
-- credential is not stored here. It stays in the deployment's environment,
-- because a settings row is read by every admin and is included in every
-- backup.
--
-- The column is empty by default, which means this deployment does not draft.
-- The app renders that state in any case: a description is typed unless a
-- model is configured.
ALTER TABLE platform_settings ADD COLUMN draft JSONB NOT NULL DEFAULT '{}'::jsonb;
`},
}

// defaultCardJSON renders the seeded price list for the migration. It is
// interpolated at migration time deliberately. The row is what a session is
// priced from afterwards, and an admin's later edit must not be overwritten
// when the constant changes in a later release.
func defaultCardJSON() string {
	raw, err := json.Marshal(credit.DefaultCard())
	if err != nil {
		panic("marshal default rate card: " + err.Error())
	}
	// The card holds no apostrophes today. The escape keeps a model id that
	// contains one from ending the SQL literal early.
	return strings.ReplaceAll(string(raw), "'", "''")
}

// migrationLock is this migrator's advisory-lock id. A rolling deploy runs
// the old and new pods at once (maxSurge), so two processes migrate the same
// database concurrently. Without the lock, both find a version missing, both
// run its DDL, and the second fails with "column already exists". That
// failure heals on restart, but it crash-loops a pod on every schema change
// until it does.
//
// The lock is taken inside the migration transaction and released with it,
// so the second process waits, then reads the version the first recorded and
// skips it. Each migrator has its own lock id, so the five migrators only
// serialize against other copies of themselves.
const migrationLock = 3

// Migrate applies the platform schema. It uses its own version table so it
// composes with the other stores' migrations on one database without either
// owning the other's versions.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// The version table is created under the same lock the versions are
	// applied under. CREATE TABLE IF NOT EXISTS is not atomic against a
	// concurrent creator: two sessions both find the table absent, both run
	// the DDL, and the second fails on a duplicate key in pg_type. Without
	// the lock here, that is the same crash the lock prevents, one statement
	// earlier.
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS platform_migrations (
				version    INT PRIMARY KEY,
				applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
			)`)
		return err
	}); err != nil {
		return fmt.Errorf("create platform migrations table: %w", err)
	}
	for _, m := range migrations {
		var exists bool
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			// The version check runs inside the transaction, behind the lock.
			// When it ran outside, two servers starting together both saw the
			// version as missing.
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
