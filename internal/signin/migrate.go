// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package signin

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrations is the sign-in schema, versioned in its own table so it
// composes with the other packages' migrators on one database.
var migrations = []struct {
	version int
	sql     string
}{
	{1, `
-- One sealed Latere refresh token per person (048): what the server acts for
-- them with on the Latere platform while a session they started runs. The
-- token is encrypted with a key the database does not hold, and bound to the
-- subject, so a copied row does not open as another person's.
CREATE TABLE platform_signins (
	subject    TEXT PRIMARY KEY,
	sealed     BYTEA NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL
);
`},
}

// migrationLock is this migrator's advisory-lock id, distinct from every
// other migrator's, taken in each migration's transaction so two processes
// of a rolling deploy apply a version once.
const migrationLock = 6

// Migrate applies the sign-in schema. Safe to run on every startup.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS signin_migrations (
				version    INT PRIMARY KEY,
				applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
			)`)
		return err
	}); err != nil {
		return fmt.Errorf("create sign-in migrations table: %w", err)
	}
	for _, m := range migrations {
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
				return err
			}
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM signin_migrations WHERE version=$1)`, m.version).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return nil
			}
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return fmt.Errorf("apply sign-in migration %d: %w", m.version, err)
			}
			_, err := tx.Exec(ctx, `INSERT INTO signin_migrations (version) VALUES ($1)`, m.version)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
