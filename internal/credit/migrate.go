package credit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrations are the ledger schema (011), versioned and applied at startup
// alongside the job, access, platform, profile and project schemas. It depends
// on none of them: an entry names its holder and its session as opaque strings,
// with no foreign key, because a ledger must outlive the thing it is about.
// Deleting a project must not delete the record of what its sessions cost.
var migrations = []struct {
	version int
	sql     string
}{
	{1, `
CREATE TABLE credit_entries (
	id         TEXT PRIMARY KEY,
	-- "user:<email>" or "project:<id>". One namespaced column rather than two
	-- nullable ones, so a balance is one predicate and a level added later does
	-- not reshape the table.
	holder     TEXT NOT NULL,
	-- Signed micro-USD. Positive adds to the holder, negative takes; a balance
	-- is therefore a sum and never a case analysis over kinds.
	amount     BIGINT NOT NULL,
	kind       TEXT NOT NULL,
	-- The session this entry is about, on hold/release/charge/adjust. No foreign
	-- key: the ledger outlives what it records.
	job_id     TEXT,
	actor      TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL
);

-- Every balance is a fold over one holder, so this is the index that makes the
-- whole model affordable.
CREATE INDEX credit_entries_holder ON credit_entries (holder);

-- Settlement is exactly-once per session: a retried or concurrent terminal
-- transition may not charge twice. A correction is an 'adjust', which carries
-- no such constraint, so reconciling a session that settled from last-observed
-- usage stays possible.
CREATE UNIQUE INDEX credit_entries_one_charge ON credit_entries (job_id) WHERE kind = 'charge';

-- Settlement finds a session's open hold by its job; so does the watchdog.
CREATE INDEX credit_entries_job ON credit_entries (job_id) WHERE job_id IS NOT NULL;
`},
	{2, `
-- The project an allocate or reclaim moved credits to or from, and a snapshot
-- of its name at that moment (024b). Additive over live rows: both nullable, so
-- every entry written before this reads as having no project and falls back to
-- the generic label. No foreign key — the ledger outlives the project, which is
-- the whole point of snapshotting the name rather than joining for it.
ALTER TABLE credit_entries ADD COLUMN project_id TEXT;
ALTER TABLE credit_entries ADD COLUMN project_name TEXT;
`},
	{3, `
-- A payment provider's reference for an entry money entered or left through:
-- the Stripe payment_intent on a purchased top-up, the refund or dispute id on
-- a clawed-back one (022a). It is how a retried webhook is deduped — the insert
-- itself refuses a second row for the same reference, so a balance that is a
-- fold cannot double. Null on every internally-moved entry (allocate, hold,
-- charge…): the partial index ignores them, so the uniqueness binds only where
-- an outside system's id is the idempotency key. The same shape as the
-- exactly-once charge index above.
ALTER TABLE credit_entries ADD COLUMN external_ref TEXT;
CREATE UNIQUE INDEX credit_entries_external_ref ON credit_entries (external_ref) WHERE external_ref IS NOT NULL;
`},
}

// migrationLock is this migrator's advisory-lock id. A rolling deploy runs the
// old and new pods at once, so two processes migrate the same database
// concurrently: both find a version missing, both run its DDL, and the loser
// dies on "relation already exists".
//
// Each namespace has its own id so the migrators only ever serialise against
// another copy of themselves; 1 through 5 are job, access, platform, profile and
// project. A collision here would silently serialise unrelated migrators and
// show up only under a rolling deploy.
const migrationLock = 6

// Migrate applies the ledger schema. It uses its own version namespace table so
// it composes with the other stores' migrations on one database without either
// owning the other's versions.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS credit_migrations (
			version    INT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create credit migrations table: %w", err)
	}
	for _, m := range migrations {
		var exists bool
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			// The check lives inside the transaction, behind the lock: read
			// outside it, two servers starting together both see "missing".
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM credit_migrations WHERE version = $1)`, m.version).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return nil
			}
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO credit_migrations (version) VALUES ($1)`, m.version)
			return err
		}); err != nil {
			return fmt.Errorf("apply credit migration %d: %w", m.version, err)
		}
	}
	return nil
}
