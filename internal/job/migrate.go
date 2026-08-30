// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrations are the schema, versioned and applied in order at startup, so
// `docker compose up` is the whole install. Each is idempotent at the version
// gate: a migration whose version is already recorded is skipped.
var migrations = []struct {
	version int
	sql     string
}{
	{1, `
CREATE TABLE jobs (
	id          TEXT PRIMARY KEY,
	kind        TEXT NOT NULL,
	principal   TEXT NOT NULL,
	project_id  TEXT NOT NULL DEFAULT '',
	ref         TEXT NOT NULL,
	phase       TEXT NOT NULL,
	status      JSONB NOT NULL DEFAULT '{}',
	repo_name   TEXT NOT NULL DEFAULT '',
	error       TEXT NOT NULL DEFAULT '',
	created_at  TIMESTAMPTZ NOT NULL,
	updated_at  TIMESTAMPTZ NOT NULL,
	phase_at    JSONB NOT NULL DEFAULT '{}'
);
-- The claim query hits this partial index: only submitted rows are claimable.
CREATE INDEX jobs_claimable ON jobs (created_at) WHERE phase = 'submitted';
`},
	{2, `
-- A session records the offline paper identity it targets (006g), so the guard
-- can key on it. Empty for an experiment or an unkeyable ref, and then unguarded.
ALTER TABLE jobs ADD COLUMN paper_key TEXT NOT NULL DEFAULT '';
-- The double-trigger guard, structural: at most one non-terminal session per
-- paper — in a project (so two people cannot double-run a shared project) and
-- per principal (so one person cannot double-run the same paper across their
-- own projects). Terminal and unkeyed rows are excluded, so a re-run after the
-- prior finished, and every experiment, are free.
CREATE UNIQUE INDEX jobs_active_paper_project ON jobs (project_id, paper_key)
	WHERE phase NOT IN ('done','failed') AND paper_key <> '';
CREATE UNIQUE INDEX jobs_active_paper_principal ON jobs (principal, paper_key)
	WHERE phase NOT IN ('done','failed') AND paper_key <> '';
`},
	{3, `
-- Whose session quota a run spends (006l). Backfilled to the submitter: before
-- sponsorship, every session was paid for by whoever opened it. NOT NULL,
-- because a null here would silently drop that row from its payer's count.
ALTER TABLE jobs ADD COLUMN charged_to TEXT NOT NULL DEFAULT '';
UPDATE jobs SET charged_to = principal WHERE charged_to = '';
-- The quota counts rows per payer by phase; this is the index it reads.
CREATE INDEX jobs_charged_to_phase ON jobs (charged_to, phase);
`},
	{4, `
-- 011 charges a session to the project it belongs to, out of the project's own
-- credits. Who pays is therefore project_id, and a second column recording a
-- payer is a second answer to a question that now has one — the shape 006l
-- needed only because a quota was keyed on a person.
DROP INDEX IF EXISTS jobs_charged_to_phase;
ALTER TABLE jobs DROP COLUMN charged_to;
`},
	{5, `
-- The price list a session was admitted under (011), snapshotted at enqueue.
-- Settlement prices from this rather than from the admin's current card, so an
-- edit mid-run cannot make the ceiling a run was watched against and the amount
-- it is finally charged come from different numbers.
ALTER TABLE jobs ADD COLUMN rate_card JSONB NOT NULL DEFAULT '{}'::jsonb;
`},
	{6, `
-- The model tier whoever started this session chose (031). Empty means nobody
-- chose, and the engine's default applies — which is why existing rows are left
-- empty rather than backfilled to 'best': a backfill would put a choice on the
-- record that no one made, and the record is the product.
ALTER TABLE jobs ADD COLUMN tier TEXT NOT NULL DEFAULT '';
`},
	{7, `
-- A session the owner threw away (032). Soft, like projects.deleted_at: the row
-- is what ledger_entries.grp points at, and deleting a session must not
-- delete the record of what it cost.
ALTER TABLE jobs ADD COLUMN deleted_at TIMESTAMPTZ;
-- Every read that hides deleted sessions is scoped by project or by principal
-- and now carries "AND deleted_at IS NULL"; these are the indexes those scans
-- read. Partial on the live rows, because the deleted ones are never selected.
CREATE INDEX jobs_live_by_project ON jobs (project_id, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX jobs_live_by_principal ON jobs (principal, created_at DESC) WHERE deleted_at IS NULL;
`},
	{8, `
-- Which process is driving this session, and when it last said so. The queue
-- claims a submitted row, but nothing claimed a row already in flight: every
-- replica reconciles at startup, so a deploy mid-run had two pods rejoin the
-- same backend session, poll it, close it and publish it. One finished and went
-- to record done; the other's close hit the session the first had archived,
-- failed the row, and the winner's write was refused as an illegal transition.
--
-- The lease is what makes driving exclusive. It expires (rather than being
-- held forever) so a run whose driver died is adoptable by a later boot instead
-- of stranded; a live driver keeps it fresh on every observation.
ALTER TABLE jobs ADD COLUMN adopted_by TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN adopted_at TIMESTAMPTZ;
`},
	{9, `
-- Where a session's work happened (045k). Platform is a session this
-- deployment ran and metered; local is one somebody ran on their own machine,
-- with their own model credential, and pushed the record here. The default
-- backfills every existing row correctly, because every existing row is one
-- this deployment ran.
ALTER TABLE jobs ADD COLUMN origin TEXT NOT NULL DEFAULT 'platform';
-- The runtime's own session id for an imported session. With the runtime it is
-- what makes a re-push update the same row rather than create a second, so an
-- interrupted upload is resumed by running it again.
ALTER TABLE jobs ADD COLUMN external_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX jobs_imported_session ON jobs (origin, external_id)
	WHERE external_id <> '';
CREATE INDEX jobs_origin ON jobs (origin);
`},
}

// Migrate applies every migration not yet recorded, each in its own
// transaction, and records the version. It is safe to run on every startup.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// Under the same lock the versions are applied under. IF NOT EXISTS is
	// not atomic against a concurrent creator: two sessions both find the
	// table absent, both run the DDL, and the loser dies on a duplicate key
	// in pg_type — which is the crash this lock exists to prevent, one
	// statement before the lock was taken.
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version    INT PRIMARY KEY,
				applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
			)`)
		return err
	}); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}
	for _, m := range migrations {
		if err := applyMigration(ctx, pool, m.version, m.sql); err != nil {
			return err
		}
	}
	return nil
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
const migrationLock = 1

func applyMigration(ctx context.Context, pool *pgxpool.Pool, version int, sql string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// Serialise against another server starting at the same moment. The
		// version check below cannot do this on its own: a row that does not
		// exist yet locks nothing, so both would read "missing" and both apply.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&exists); err != nil {
			return fmt.Errorf("check migration %d: %w", version, err)
		}
		if exists {
			return nil
		}
		if _, err := tx.Exec(ctx, sql); err != nil {
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		return nil
	})
}
