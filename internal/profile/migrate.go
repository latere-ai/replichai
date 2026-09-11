// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrations is the profile schema. It has its own version table so it
// composes with job.Migrate, access.Migrate, and project.Migrate on one
// database.
var migrations = []struct {
	version int
	sql     string
}{
	{1, `
-- One self-asserted researcher identity per principal (006n). Every field is
-- optional in the store; the publish wizard requires a contact. There is no
-- foreign key to access_grants, because a profile is display data and grants
-- nothing, so it must not depend on the ACL's shape.
CREATE TABLE profiles (
	principal     TEXT PRIMARY KEY,
	display_name  TEXT NOT NULL DEFAULT '',
	institution   TEXT NOT NULL DEFAULT '',
	contact_email TEXT NOT NULL DEFAULT '',
	orcid         TEXT NOT NULL DEFAULT '',
	scholar_url   TEXT NOT NULL DEFAULT '',
	updated_at    TIMESTAMPTZ NOT NULL
);
`},
	{2, `
-- The public profile (006r): a handle that can go in a URL, and whether the
-- profile has a page at all.
--
-- handle is NULL rather than '' when unset, so the unique index constrains only
-- the people who chose one. Many empty strings would collide; many NULLs do
-- not. public defaults to false, so a profile that was private before the
-- migration does not become a page because the migration ran.
ALTER TABLE profiles ADD COLUMN handle TEXT;
ALTER TABLE profiles ADD COLUMN public BOOLEAN NOT NULL DEFAULT false;
CREATE UNIQUE INDEX profiles_handle_key ON profiles (handle);
`},
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
// serialise against other copies of themselves.
const migrationLock = 4

// Migrate applies the profile schema. Safe to run on every startup.
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
			CREATE TABLE IF NOT EXISTS profile_migrations (
				version    INT PRIMARY KEY,
				applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
			)`)
		return err
	}); err != nil {
		return fmt.Errorf("create profile migrations table: %w", err)
	}
	for _, m := range migrations {
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
				return err
			}
			var exists bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM profile_migrations WHERE version=$1)`, m.version).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return nil
			}
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return fmt.Errorf("apply profile migration %d: %w", m.version, err)
			}
			_, err := tx.Exec(ctx, `INSERT INTO profile_migrations (version) VALUES ($1)`, m.version)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
