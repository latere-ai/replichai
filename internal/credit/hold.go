package credit

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Holding credits is what the enqueue gate does, and it is deliberately not on
// the Store interface: a hold has to happen in the *caller's* transaction — the
// one that inserts the session — and a transaction handle cannot be passed
// through an interface both stores satisfy.
//
// So each store exposes the shape its caller can use: the Postgres one takes a
// pgx.Tx, the in-memory one takes nothing and locks itself. The job store holds
// whichever it was built with. The contract that matters is asserted by driving
// both through the same tests, not by a shared signature.

// Hold reserves amount against a holder, refusing with ErrInsufficient when
// there is not that much available. It is the in-memory half of the enqueue
// gate.
func (s *MemStore) Hold(_ context.Context, h Holder, jobID string, amount int64) error {
	if amount <= 0 {
		return ErrNotPositive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sum(h, true) < amount {
		return ErrInsufficient
	}
	return s.append(Entry{Holder: h, Amount: -amount, Kind: KindHold, JobID: jobID, Actor: ActorSystem})
}

// HoldTx reserves amount against a holder inside the caller's transaction,
// refusing with ErrInsufficient when there is not that much available.
//
// The read and the write are the same table, so this cannot be a check on one
// connection and an insert on another: two concurrent submits into one project
// would each read the other's uncommitted hold as absent and both be admitted.
// The advisory lock on the holder makes the read authoritative for the rest of
// the transaction — the structure 006l established for the session counter,
// which is now a structure about money.
func (s *PGStore) HoldTx(ctx context.Context, tx pgx.Tx, h Holder, jobID string, amount int64) error {
	if amount <= 0 {
		return ErrNotPositive
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, string(h)); err != nil {
		return err
	}
	var have int64
	if err := tx.QueryRow(ctx, availableSQL, string(h)).Scan(&have); err != nil {
		return err
	}
	if have < amount {
		return ErrInsufficient
	}
	return s.insertTx(ctx, tx, Entry{
		Holder: h, Amount: -amount, Kind: KindHold, JobID: jobID, Actor: ActorSystem,
	})
}
