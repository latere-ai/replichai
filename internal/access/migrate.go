package access

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrations are the access schema, versioned and applied at startup alongside
// the job schema. The access tables live in the same database the deployment
// owns — grants and the authorisation trail, never a reproduction.
var migrations = []struct {
	version int
	sql     string
}{
	{1, `
CREATE TABLE access_grants (
	email      TEXT PRIMARY KEY,
	principal  TEXT UNIQUE,          -- NULL until bound; UNIQUE binds one principal
	tier       SMALLINT NOT NULL,
	granted_by TEXT NOT NULL,
	granted_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE project_members (
	project    TEXT NOT NULL,
	email      TEXT NOT NULL,
	principal  TEXT,
	role       SMALLINT NOT NULL,
	PRIMARY KEY (project, email),
	UNIQUE (project, principal)
);
CREATE TABLE forge_links (
	principal TEXT NOT NULL,
	forge     TEXT NOT NULL,
	login     TEXT NOT NULL,
	PRIMARY KEY (principal, forge)
);
`},
}

// Migrate applies the access schema. It uses its own version namespace table so
// it composes with job.Migrate on the same database without either owning the
// other's versions.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS access_migrations (
			version    INT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create access migrations table: %w", err)
	}
	for _, m := range migrations {
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			var exists bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM access_migrations WHERE version=$1)`, m.version).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return nil
			}
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return fmt.Errorf("apply access migration %d: %w", m.version, err)
			}
			_, err := tx.Exec(ctx, `INSERT INTO access_migrations (version) VALUES ($1)`, m.version)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
