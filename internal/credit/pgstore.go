package credit

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/access"
)

// PGStore is the Postgres ledger, against the same contract the in-memory one
// satisfies.
type PGStore struct {
	pool  *pgxpool.Pool
	clock func() time.Time

	auth    Authority
	members Members
}

// NewPGStore builds a ledger over a pgx pool.
func NewPGStore(pool *pgxpool.Pool, auth Authority, members Members) *PGStore {
	return &PGStore{pool: pool, clock: time.Now, auth: auth, members: members}
}

// balanceSQL sums a holder's entries. The two balances differ by one predicate:
// an available balance counts holds, a settled one does not.
const (
	balanceSQL   = `SELECT COALESCE(SUM(amount), 0) FROM credit_entries WHERE holder = $1 AND kind NOT IN ('hold','release')`
	availableSQL = `SELECT COALESCE(SUM(amount), 0) FROM credit_entries WHERE holder = $1`
)

// Balance sums everything but the holds, which are commitments rather than
// spend.
func (s *PGStore) Balance(ctx context.Context, h Holder) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, balanceSQL, string(h)).Scan(&n)
	return n, err
}

// Available sums everything including the holds: what may still be committed.
func (s *PGStore) Available(ctx context.Context, h Holder) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, availableSQL, string(h)).Scan(&n)
	return n, err
}

// Entries lists a holder's ledger, newest first.
func (s *PGStore) Entries(ctx context.Context, h Holder, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = defaultEntryLimit
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, holder, amount, kind, COALESCE(job_id, ''), COALESCE(project_id, ''), COALESCE(project_name, ''), actor, COALESCE(external_ref, ''), created_at
		FROM credit_entries WHERE holder = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`, string(h), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Holder, &e.Amount, &e.Kind, &e.JobID, &e.ProjectID, &e.ProjectName, &e.Actor, &e.ExternalRef, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Topup creates credits in one email's wallet.
func (s *PGStore) Topup(ctx context.Context, byAdmin access.Principal, email string, amount int64) error {
	if normalizeEmail(email) == "" {
		return ErrNoEmail
	}
	if amount <= 0 {
		return ErrNotPositive
	}
	if err := requireAdmin(ctx, s.auth, byAdmin); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return s.insertTx(ctx, tx, Entry{
			Holder: Wallet(email), Amount: amount, Kind: KindTopup, Actor: byAdmin.Email,
		})
	})
}

// TopupPaid posts a purchased credit into a wallet, idempotent on ref. No
// principal: authority came from a verified webhook signature, upstream.
func (s *PGStore) TopupPaid(ctx context.Context, email string, credited int64, ref, actor string) error {
	if err := checkPaid(email, credited, ref); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return s.insertPaidTx(ctx, tx, Entry{
			Holder: Wallet(email), Amount: credited, Kind: KindTopup, Actor: actor, ExternalRef: ref,
		})
	})
}

// RefundPaid reverses a purchase with a negative adjust, idempotent on ref. The
// wallet may go negative — the debt a clawed-back purchase leaves.
func (s *PGStore) RefundPaid(ctx context.Context, email string, amount int64, ref, actor string) error {
	if err := checkPaid(email, amount, ref); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return s.insertPaidTx(ctx, tx, Entry{
			Holder: Wallet(email), Amount: -amount, Kind: KindAdjust, Actor: actor, ExternalRef: ref,
		})
	})
}

// EntryByExternalRef finds the entry carrying ref. The reference is unique, so
// there is at most one.
func (s *PGStore) EntryByExternalRef(ctx context.Context, ref string) (Entry, bool, error) {
	if strings.TrimSpace(ref) == "" {
		return Entry{}, false, ErrNoRef
	}
	var e Entry
	err := s.pool.QueryRow(ctx, `
		SELECT id, holder, amount, kind, COALESCE(job_id, ''), COALESCE(project_id, ''), COALESCE(project_name, ''), actor, COALESCE(external_ref, ''), created_at
		FROM credit_entries WHERE external_ref = $1 LIMIT 1`, ref).
		Scan(&e.ID, &e.Holder, &e.Amount, &e.Kind, &e.JobID, &e.ProjectID, &e.ProjectName, &e.Actor, &e.ExternalRef, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	return e, true, nil
}

// BalancesFor folds every requested holder in one aggregate query rather than a
// SUM per row. A holder with no entry does not appear; the caller reads a
// missing key as zero.
func (s *PGStore) BalancesFor(ctx context.Context, holders []Holder) (map[Holder]int64, error) {
	out := make(map[Holder]int64, len(holders))
	if len(holders) == 0 {
		return out, nil
	}
	keys := make([]string, len(holders))
	for i, h := range holders {
		keys[i] = string(h)
	}
	rows, err := s.pool.Query(ctx,
		`SELECT holder, COALESCE(SUM(amount), 0) FROM credit_entries
		 WHERE holder = ANY($1) AND kind NOT IN ('hold','release') GROUP BY holder`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		var n int64
		if err := rows.Scan(&h, &n); err != nil {
			return nil, err
		}
		out[Holder(h)] = n
	}
	return out, rows.Err()
}

// NegativeWallets returns every wallet in the red in one predicate — a GROUP BY
// with a HAVING over the fold — most-owed first. The 'user:' prefix keeps pots
// out: a negative pot is a legitimate overdraft, not a debt an account owes.
func (s *PGStore) NegativeWallets(ctx context.Context) ([]WalletBalance, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT holder, SUM(amount) AS bal FROM credit_entries
		 WHERE kind NOT IN ('hold','release') AND holder LIKE 'user:%'
		 GROUP BY holder HAVING SUM(amount) < 0 ORDER BY bal ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WalletBalance{}
	for rows.Next() {
		var h string
		var n int64
		if err := rows.Scan(&h, &n); err != nil {
			return nil, err
		}
		if email, ok := WalletEmail(Holder(h)); ok {
			out = append(out, WalletBalance{Email: email, Balance: n})
		}
	}
	return out, rows.Err()
}

// TotalOutstanding sums every holder's settled balance — wallets and pots, less
// open holds — the credits sold and not yet delivered as compute.
func (s *PGStore) TotalOutstanding(ctx context.Context) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM credit_entries WHERE kind NOT IN ('hold','release')`,
	).Scan(&n)
	return n, err
}

// Allocate moves credits from the caller's wallet into a project's pot.
func (s *PGStore) Allocate(ctx context.Context, by access.Principal, project, projectName string, amount int64) error {
	if err := checkMove(by.Email, project, amount); err != nil {
		return err
	}
	if err := requireRole(ctx, s.members, by, project, access.RoleCollaborator); err != nil {
		return err
	}
	return s.transfer(ctx, Wallet(by.Email), Pot(project), amount, KindAllocate, by.Email, project, projectName)
}

// Reclaim moves credits from a project's pot back to the caller's wallet.
func (s *PGStore) Reclaim(ctx context.Context, by access.Principal, project, projectName string, amount int64) error {
	if err := checkMove(by.Email, project, amount); err != nil {
		return err
	}
	if err := requireRole(ctx, s.members, by, project, access.RoleLead); err != nil {
		return err
	}
	return s.transfer(ctx, Pot(project), Wallet(by.Email), amount, KindReclaim, by.Email, project, projectName)
}

// transfer writes both sides of a move in one transaction, after checking the
// source can afford it *inside* that transaction.
//
// The check and the write cannot be two statements on two connections: the
// balance and the debit are the same table, so two concurrent moves would each
// read the other's uncommitted row as absent and both would be admitted. The
// advisory lock on the source holder is what makes the read authoritative for
// the rest of the transaction — the structure the enqueue gate uses for exactly
// the same reason (011, after 006l).
//
// The source is checked against its *available* balance, so credits already
// held for a running session cannot be reclaimed out from under it.
func (s *PGStore) transfer(ctx context.Context, from, to Holder, amount int64, kind Kind, actor, projectID, projectName string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, string(from)); err != nil {
			return err
		}
		var have int64
		if err := tx.QueryRow(ctx, availableSQL, string(from)).Scan(&have); err != nil {
			return err
		}
		if have < amount {
			return ErrInsufficient
		}
		if err := s.insertTx(ctx, tx, Entry{Holder: from, Amount: -amount, Kind: kind, Actor: actor, ProjectID: projectID, ProjectName: projectName}); err != nil {
			return err
		}
		return s.insertTx(ctx, tx, Entry{Holder: to, Amount: amount, Kind: kind, Actor: actor, ProjectID: projectID, ProjectName: projectName})
	})
}

// insertSQL is the one INSERT, so a kind added later has one place to be
// written. insertPaidSQL is the same statement with the idempotency clause a
// payment-referenced entry needs (see insertPaidTx).
const (
	insertSQL = `
		INSERT INTO credit_entries (id, holder, amount, kind, job_id, project_id, project_name, actor, external_ref, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`
	insertPaidSQL = insertSQL + `
		ON CONFLICT (external_ref) WHERE external_ref IS NOT NULL DO NOTHING`
)

// insertArgs mints an id, defaults the clock, and nils the optional string
// columns, so the two INSERT statements share one argument shape.
func (s *PGStore) insertArgs(e Entry) ([]any, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = s.clock().UTC()
	}
	return []any{
		id, string(e.Holder), e.Amount, string(e.Kind),
		nilIfEmpty(e.JobID), nilIfEmpty(e.ProjectID), nilIfEmpty(e.ProjectName),
		e.Actor, nilIfEmpty(e.ExternalRef), e.CreatedAt,
	}, nil
}

// nilIfEmpty maps an empty snapshot string to a NULL column, so an absent
// job/project/reference reads as NULL rather than "".
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// insertTx appends one entry inside a caller's transaction.
func (s *PGStore) insertTx(ctx context.Context, tx pgx.Tx, e Entry) error {
	args, err := s.insertArgs(e)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, insertSQL, args...)
	return err
}

// insertPaidTx appends a payment-referenced entry, idempotently. A retried
// webhook posts the same external_ref twice; the partial unique index turns the
// second insert into a no-op, so a balance that is a fold cannot double. It is
// the insert — not a read-then-insert check — that dedupes, because a check on a
// second connection races the write it means to protect (011's rule for the
// hold, applied to a payment).
func (s *PGStore) insertPaidTx(ctx context.Context, tx pgx.Tx, e Entry) error {
	args, err := s.insertArgs(e)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, insertPaidSQL, args...)
	return err
}

// Fund moves credits from an email's wallet into a project's pot, with no
// membership check — see creationOps for why the create path cannot ask that
// question.
func (s *PGStore) Fund(ctx context.Context, email, project, projectName string, amount int64) error {
	if err := checkMove(email, project, amount); err != nil {
		return err
	}
	return s.transfer(ctx, Wallet(email), Pot(project), amount, KindAllocate, normalizeEmail(email), project, projectName)
}

// Unwind returns everything a project's pot can still commit to an email's
// wallet, as the system. The amount is read inside the transaction that moves
// it, under the same advisory lock every transfer takes, so it cannot be read
// before a concurrent hold and written after one.
func (s *PGStore) Unwind(ctx context.Context, project, email string) error {
	if err := checkMove(email, project, 1); err != nil {
		return err
	}
	from, to := Pot(project), Wallet(email)
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, string(from)); err != nil {
			return err
		}
		var amount int64
		if err := tx.QueryRow(ctx, availableSQL, string(from)).Scan(&amount); err != nil {
			return err
		}
		if amount <= 0 {
			return nil
		}
		// Torn down, so no name to snapshot; the id keeps the two sides agreeing.
		if err := s.insertTx(ctx, tx, Entry{Holder: from, Amount: -amount, Kind: KindReclaim, Actor: ActorSystem, ProjectID: project}); err != nil {
			return err
		}
		return s.insertTx(ctx, tx, Entry{Holder: to, Amount: amount, Kind: KindReclaim, Actor: ActorSystem, ProjectID: project})
	})
}
