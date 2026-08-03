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
}

// Migrate applies every migration not yet recorded, each in its own
// transaction, and records the version. It is safe to run on every startup.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
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
