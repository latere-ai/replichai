package platform

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
)

// PGStore is the Postgres implementation of Store, against the same contract
// the in-memory one satisfies.
type PGStore struct {
	pool  *pgxpool.Pool
	auth  Authority
	clock func() time.Time
}

// NewPGStore builds a platform store over a pgx pool.
func NewPGStore(pool *pgxpool.Pool, auth Authority) *PGStore {
	return &PGStore{pool: pool, auth: auth, clock: time.Now}
}

// requireAdmin re-checks the caller's tier, so an admin-only write holds even
// if a route were mounted without the tier guard.
func (s *PGStore) requireAdmin(ctx context.Context, p access.Principal) error {
	if s.auth == nil {
		return nil
	}
	tier, _, err := s.auth.Resolve(ctx, p)
	if err != nil {
		return err
	}
	if tier != access.TierAdmin {
		return ErrNotAdmin
	}
	return nil
}

// Settings reads the platform row, fail-closed: a missing row or a failing
// query yields the conservative default rather than unlimited, and returns the
// error alongside so the caller can log it.
func (s *PGStore) Settings(ctx context.Context) (Settings, error) {
	fallback := Settings{StartReserve: DefaultStartReserve, Rates: credit.DefaultCard()}
	var out Settings
	var card []byte
	err := s.pool.QueryRow(ctx,
		`SELECT start_reserve, rate_card, updated_by, updated_at FROM platform_settings WHERE id = 1`,
	).Scan(&out.StartReserve, &card, &out.UpdatedBy, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return fallback, nil
	}
	if err != nil {
		return fallback, err
	}
	if err := json.Unmarshal(card, &out.Rates); err != nil || len(out.Rates.Models) == 0 {
		// An unreadable or empty card is the one value that would price a
		// session at nothing. Fall back to the compiled-in list.
		out.Rates = credit.DefaultCard()
	}
	return out, nil
}

// SetSettings writes the platform row, recording the admin who did it. The row
// is pinned to id 1, so this is an upsert and never a second row.
func (s *PGStore) SetSettings(ctx context.Context, byAdmin access.Principal, next Settings) error {
	if err := next.validate(); err != nil {
		return err
	}
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return err
	}
	if len(next.Rates.Models) == 0 {
		next.Rates = credit.DefaultCard()
	}
	card, err := json.Marshal(next.Rates)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO platform_settings (id, start_reserve, rate_card, updated_by, updated_at)
		VALUES (1, $1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE
		SET start_reserve = EXCLUDED.start_reserve,
		    rate_card     = EXCLUDED.rate_card,
		    updated_by    = EXCLUDED.updated_by,
		    updated_at    = EXCLUDED.updated_at`,
		next.StartReserve, card, byAdmin.Email, s.clock().UTC())
	return err
}
