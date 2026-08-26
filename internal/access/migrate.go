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
	{2, `
-- The "trusted" tier (value 2) was removed in the two-tier rework: publishing is
-- authorised by project ownership plus a sign-off (006f), not a tier. Fold any
-- stray trusted grant down to a plain user so no orphan tier lingers in the ACL.
UPDATE access_grants SET tier = 1 WHERE tier = 2;
`},
	{3, `
-- Sign-in is open (006q): a first login grants itself TierUser. That turns a
-- deleted grant into a logout, because the very next sign-in would re-create
-- the row an admin just removed. Revocation therefore becomes a recorded state
-- rather than a DELETE, and the self-grant refuses a revoked address.
--
-- Every existing grant is un-revoked, which is what it already meant.
ALTER TABLE access_grants ADD COLUMN revoked_at TIMESTAMPTZ;
ALTER TABLE access_grants ADD COLUMN revoked_by TEXT NOT NULL DEFAULT '';
`},
	{4, `
-- An admin's decision to let a negative-wallet account keep working despite its
-- debt (022d), beside revocation because it is the same shape: recorded state an
-- admin sets, keyed by email. Unlike revocation it is retired — cleared_at set
-- when the wallet recovers — so a later chargeback re-freezes as a fresh
-- decision. It is not in the credit ledger because the ledger is append-only and
-- this is a fact that gets taken back.
CREATE TABLE freeze_overrides (
	email      TEXT PRIMARY KEY,
	granted_by TEXT NOT NULL,
	granted_at TIMESTAMPTZ NOT NULL,
	cleared_at TIMESTAMPTZ            -- NULL while the override is in force
);
`},
	{5, `
-- The forge link never had a caller. 006c designed it as the proof that a
-- principal owns a GitHub login, so 006f could invite that login; 006f instead
-- takes the login from the request and invites it, and ownership is granted by
-- the organisation's invitation rather than by anything this table asserted.
-- Nothing read it, nothing wrote it, and a table nobody writes cannot be
-- evidence of anything -- so it goes rather than waiting for a consumer that
-- two specs failed to produce.
DROP TABLE IF EXISTS forge_links;
`},
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
const migrationLock = 2

// Migrate applies the access schema. It uses its own version namespace table so
// it composes with job.Migrate on the same database without either owning the
// other's versions.
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
			CREATE TABLE IF NOT EXISTS access_migrations (
				version    INT PRIMARY KEY,
				applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
			)`)
		return err
	}); err != nil {
		return fmt.Errorf("create access migrations table: %w", err)
	}
	for _, m := range migrations {
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
				return err
			}
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
