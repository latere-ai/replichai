package project

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrations are the project schema, in their own version namespace so they
// compose with job.Migrate and access.Migrate on one database. This migration
// runs after job.Migrate: its backfill reads the jobs table.
var migrations = []struct {
	version int
	sql     string
}{
	{1, `
CREATE TABLE projects (
	id          TEXT PRIMARY KEY,
	owner       TEXT NOT NULL,
	title       TEXT NOT NULL DEFAULT '',
	visibility  TEXT NOT NULL DEFAULT 'private',
	created_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX projects_by_owner ON projects (owner, created_at DESC);

-- Backfill the entity layer from the sessions already in jobs (006g): every
-- distinct (principal, project_id) becomes a project, reusing the existing
-- project_id slug as the project id so each session's project_id already points
-- at a real project — no repoint. The title is the slug for now (SQL only, no
-- network); the real paper titles are a one-off data fix after deploy.
INSERT INTO projects (id, owner, title, visibility, created_at)
SELECT project_id, principal, project_id, 'private', MIN(created_at)
FROM jobs
WHERE project_id <> ''
GROUP BY project_id, principal
ON CONFLICT (id) DO NOTHING;

-- The session -> project foreign key, added NOT VALID: every new session must
-- reference a real project (Enqueue always assigns one), while any pre-existing
-- unkeyed row is grandfathered rather than blocking the migration.
ALTER TABLE jobs ADD CONSTRAINT jobs_project_fk
	FOREIGN KEY (project_id) REFERENCES projects (id) NOT VALID;
`},
	{2, `
-- The public-flip provenance (006f): the recorded responsibility sign-off.
ALTER TABLE projects ADD COLUMN signed_off_by       TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN signed_off_at       TIMESTAMPTZ;
ALTER TABLE projects ADD COLUMN attestation_version TEXT NOT NULL DEFAULT '';
`},
	{3, `
-- The owner's standing consent to cover their members' sessions (006l). Off by
-- default: sponsorship is always something the owner turned on.
ALTER TABLE projects ADD COLUMN sponsored BOOLEAN NOT NULL DEFAULT false;
`},
}

// Migrate applies the project schema and the one-time backfill. Safe to run on
// every startup, and it must run after job.Migrate.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS project_migrations (
			version    INT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create project migrations table: %w", err)
	}
	for _, m := range migrations {
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			var exists bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM project_migrations WHERE version=$1)`, m.version).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return nil
			}
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return fmt.Errorf("apply project migration %d: %w", m.version, err)
			}
			_, err := tx.Exec(ctx, `INSERT INTO project_migrations (version) VALUES ($1)`, m.version)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
