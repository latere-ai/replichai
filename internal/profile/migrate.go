package profile

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrations are the profile schema, in their own version namespace so they
// compose with job.Migrate, access.Migrate, and project.Migrate on one database.
var migrations = []struct {
	version int
	sql     string
}{
	{1, `
-- One self-asserted researcher identity per principal (006n). Every field is
-- optional at rest; the publish wizard is where a contact becomes required. No
-- foreign key to access_grants: a profile is display data and grants nothing, so
-- it must not depend on the ACL's shape.
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
-- The public profile (006r): a handle that can go in a URL, and the decision to
-- have a page at all.
--
-- handle is NULL rather than '' when unset, so the unique index constrains only
-- the people who chose one — many empty strings would collide, many NULLs do
-- not. public defaults to false: a profile that was private yesterday must not
-- become a page because a migration ran.
ALTER TABLE profiles ADD COLUMN handle TEXT;
ALTER TABLE profiles ADD COLUMN public BOOLEAN NOT NULL DEFAULT false;
CREATE UNIQUE INDEX profiles_handle_key ON profiles (handle);
`},
}

// Migrate applies the profile schema. Safe to run on every startup.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS profile_migrations (
			version    INT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create profile migrations table: %w", err)
	}
	for _, m := range migrations {
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
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
