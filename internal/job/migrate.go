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

func applyMigration(ctx context.Context, pool *pgxpool.Pool, version int, sql string) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// Lock the version row so two starting servers do not both apply it.
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
