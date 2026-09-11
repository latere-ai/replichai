// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"latere.ai/x/pay/ledger/pgledger"
)

// Migrate applies the shared ledger's schema and then copies this product's
// existing rows into it.
//
// The ledger moved to latere.ai/x/pay, and its table is `ledger_entries`, with
// a vocabulary split in two: Kind is mechanical, recording whether an entry
// commits and what sign it has, and Reason is descriptive. `credit_entries`
// holds live balances, so the rows have to be copied, and a balance that is a
// sum is wrong if a single row is lost.
//
// Migrate is forward-only and idempotent. `credit_entries` is left in place: it
// is the only way back if the sums do not reconcile, and dropping it in the
// same release as the copy would remove the evidence at exactly the moment it
// is needed. A later release drops it, once the balances have been read from
// the new table in production for long enough to trust them.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if err := pgledger.Migrate(ctx, pool); err != nil {
		return err
	}
	return backfill(ctx, pool)
}

// backfillLock serialises the copy the way the schema migrations serialise
// their DDL: a rolling deploy runs two pods at once and both would otherwise
// copy the same rows.
const backfillLock int64 = 0x1ED6E5

// backfill copies credit_entries into ledger_entries, once.
//
// The work is the kind mapping. Every historical kind becomes a mechanical
// Kind plus the Reason that names it, so a statement rendered from the new
// table reads exactly as it did from the old one.
func backfill(ctx context.Context, pool *pgxpool.Pool) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, backfillLock); err != nil {
			return err
		}
		var legacy bool
		if err := tx.QueryRow(ctx, `SELECT to_regclass('credit_entries') IS NOT NULL`).Scan(&legacy); err != nil {
			return fmt.Errorf("look for the legacy ledger: %w", err)
		}
		if !legacy {
			return nil // a fresh database has nothing to copy
		}
		var done bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM ledger_entries WHERE actor = $1)`, backfillMarker).Scan(&done); err != nil {
			return err
		}
		if done {
			return nil
		}
		if _, err := tx.Exec(ctx, backfillSQL); err != nil {
			return fmt.Errorf("carry the ledger across: %w", err)
		}
		// A zero-amount marker row records that the copy happened. It affects no
		// sum: the balance of its holder is unchanged, and its holder namespace
		// is used by no account.
		_, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (id, holder, amount, kind, reason, actor, created_at)
			VALUES ($1, 'system:migration', 0, 'adjust', 'backfill', $2, now())`,
			backfillMarker+"-marker", backfillMarker)
		return err
	})
}

// backfillMarker is the actor on the row that records the copy.
const backfillMarker = "credit-to-ledger-backfill"

// backfillSQL copies every legacy row across.
//
// The id is preserved, so the copy is idempotent under its primary key and an
// operator can match a row in one table to its copy in the other.
// `external_ref` becomes `ref` and `job_id` becomes `grp`; the two project
// columns become labels, which the shared ledger stores and never interprets.
const backfillSQL = `
INSERT INTO ledger_entries (id, holder, amount, kind, reason, ref, grp, actor, labels, created_at)
SELECT
	c.id,
	c.holder,
	c.amount,
	CASE c.kind
		WHEN 'topup'    THEN 'credit'
		WHEN 'allocate' THEN 'transfer'
		WHEN 'reclaim'  THEN 'transfer'
		WHEN 'hold'     THEN 'hold'
		WHEN 'release'  THEN 'release'
		WHEN 'charge'   THEN 'debit'
		WHEN 'draft'    THEN 'debit'
		WHEN 'adjust'   THEN 'adjust'
		ELSE 'adjust'
	END,
	c.kind,
	NULLIF(c.external_ref, ''),
	NULLIF(c.job_id, ''),
	c.actor,
	CASE
		WHEN COALESCE(c.project_id, '') = '' AND COALESCE(c.project_name, '') = '' THEN NULL
		ELSE jsonb_strip_nulls(jsonb_build_object(
			'project_id',   NULLIF(c.project_id, ''),
			'project_name', NULLIF(c.project_name, '')))
	END,
	c.created_at
FROM credit_entries c
ON CONFLICT (id) DO NOTHING`

// Reconcile returns the outstanding total of the legacy ledger and of the
// copied ledger, so an operator can check that they agree.
//
// It exists because the claim that the balances were copied correctly can only
// be verified by comparing the two totals. An operator runs it after deploying
// and before trusting the new table.
func Reconcile(ctx context.Context, pool *pgxpool.Pool) (legacy, carried int64, err error) {
	const settled = `kind NOT IN ('hold','release')`
	if err = pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM credit_entries WHERE `+settled).Scan(&legacy); err != nil {
		return 0, 0, fmt.Errorf("fold the legacy ledger: %w", err)
	}
	if err = pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM ledger_entries WHERE `+settled).Scan(&carried); err != nil {
		return 0, 0, fmt.Errorf("fold the carried ledger: %w", err)
	}
	return legacy, carried, nil
}
