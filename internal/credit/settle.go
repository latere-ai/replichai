package credit

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Settling a session releases the reserve it was holding and charges what it
// actually cost. Like Hold, it must happen in the *caller's* transaction — the
// one that writes the terminal phase — so a session can never be terminal and
// unsettled, and each store exposes the shape its caller can use.
//
// It is exactly-once per session, enforced by a partial unique index on the
// charge rather than by the caller remembering: a retried or concurrent
// terminal transition finds the charge already there and does nothing. A
// correction after the fact is an `adjust`, which carries no such constraint.

// held sums a session's open hold — everything reserved less everything already
// released — so settlement returns exactly what is outstanding rather than an
// amount the caller has to remember.
const heldSQL = `
	SELECT COALESCE(SUM(amount), 0) * -1 FROM credit_entries
	WHERE job_id = $1 AND kind IN ('hold','release')`

// Settle releases a session's hold and charges cost against the holder, once.
// It reports whether it settled: false means the session was already settled,
// which is not an error.
func (s *MemStore) Settle(_ context.Context, h Holder, jobID string, cost int64) (bool, error) {
	if cost < 0 {
		return false, ErrNotPositive
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var held int64
	for _, e := range s.entries {
		if e.JobID != jobID {
			continue
		}
		if e.Kind == KindCharge {
			return false, nil // already settled
		}
		if e.Kind.commits() {
			held -= e.Amount
		}
	}
	if held > 0 {
		if err := s.append(Entry{
			Holder: h, Amount: held, Kind: KindRelease, JobID: jobID, Actor: ActorSystem,
		}); err != nil {
			return false, err
		}
	}
	// The charge is written even when it is zero: it is the marker that says
	// this session settled, and without it a retry would release a second time.
	return true, s.append(Entry{
		Holder: h, Amount: -cost, Kind: KindCharge, JobID: jobID, Actor: ActorSystem,
	})
}

// SettleTx releases a session's hold and charges cost inside the caller's
// transaction. It reports whether it settled; false means another writer got
// there first, which is the exactly-once guarantee doing its job rather than a
// failure.
func (s *PGStore) SettleTx(ctx context.Context, tx pgx.Tx, h Holder, jobID string, cost int64) (bool, error) {
	if cost < 0 {
		return false, ErrNotPositive
	}
	// The charge goes in first, and its conflict is what makes this
	// exactly-once: if a concurrent or retried settlement already wrote one,
	// nothing is inserted and no hold is released twice. Doing the release
	// first would let a retry release again before discovering the charge.
	id, err := newID()
	if err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO credit_entries (id, holder, amount, kind, job_id, actor, created_at)
		VALUES ($1,$2,$3,'charge',$4,$5,$6)
		ON CONFLICT (job_id) WHERE kind = 'charge' DO NOTHING`,
		id, string(h), -cost, jobID, ActorSystem, s.clock().UTC())
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil // already settled
	}
	var held int64
	if err := tx.QueryRow(ctx, heldSQL, jobID).Scan(&held); err != nil {
		return false, err
	}
	if held <= 0 {
		return true, nil
	}
	return true, s.insertTx(ctx, tx, Entry{
		Holder: h, Amount: held, Kind: KindRelease, JobID: jobID, Actor: ActorSystem,
	})
}
