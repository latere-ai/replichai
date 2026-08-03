package platform

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrations are the platform-policy schema (006k), versioned and applied at
// startup alongside the job, access, and project schemas. It depends on none of
// them.
var migrations = []struct {
	version int
	sql     string
}{
	{1, fmt.Sprintf(`
-- One row, pinned. The check constraint makes a second row a constraint
-- violation rather than an ambiguity about which row is the policy.
CREATE TABLE platform_settings (
	id                    SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
	default_session_quota INT NOT NULL,
	updated_by            TEXT NOT NULL,
	updated_at            TIMESTAMPTZ NOT NULL
);
INSERT INTO platform_settings (id, default_session_quota, updated_by, updated_at)
VALUES (1, %d, 'bootstrap', now());

-- Keyed by email, not principal, so an admin can hand someone headroom before
-- they have ever signed in — the seed-by-email rule a grant follows (006c).
CREATE TABLE quota_topups (
	email      TEXT PRIMARY KEY,
	extra      INT NOT NULL,
	granted_by TEXT NOT NULL,
	granted_at TIMESTAMPTZ NOT NULL
);
`, DefaultSessionQuota)},
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
}

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
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM platform_migrations WHERE version = $1)`, m.version).Scan(&exists); err != nil {
			return fmt.Errorf("check platform migration %d: %w", m.version, err)
		}
		if exists {
			continue
		}
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
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
