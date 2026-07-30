package platform

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/access"
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
	fallback := Settings{DefaultSessionQuota: DefaultSessionQuota}
	var out Settings
	err := s.pool.QueryRow(ctx,
		`SELECT default_session_quota, updated_by, updated_at FROM platform_settings WHERE id = 1`,
	).Scan(&out.DefaultSessionQuota, &out.UpdatedBy, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return fallback, nil
	}
	if err != nil {
		return fallback, err
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
	_, err := s.pool.Exec(ctx, `
		INSERT INTO platform_settings (id, default_session_quota, updated_by, updated_at)
		VALUES (1, $1, $2, $3)
		ON CONFLICT (id) DO UPDATE
		SET default_session_quota = EXCLUDED.default_session_quota,
		    updated_by            = EXCLUDED.updated_by,
		    updated_at            = EXCLUDED.updated_at`,
		next.DefaultSessionQuota, byAdmin.Email, s.clock().UTC())
	return err
}

// SetTopup sets one email's extra headroom; zero removes it.
func (s *PGStore) SetTopup(ctx context.Context, byAdmin access.Principal, email string, extra int) error {
	email = normalizeEmail(email)
	if email == "" {
		return ErrNoEmail
	}
	if extra < 0 {
		return ErrNegative
	}
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return err
	}
	if extra == 0 {
		_, err := s.pool.Exec(ctx, `DELETE FROM quota_topups WHERE email = $1`, email)
		return err
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO quota_topups (email, extra, granted_by, granted_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (email) DO UPDATE
		SET extra = EXCLUDED.extra, granted_by = EXCLUDED.granted_by, granted_at = EXCLUDED.granted_at`,
		email, extra, byAdmin.Email, s.clock().UTC())
	return err
}

// Topups lists every top-up, email-ordered.
func (s *PGStore) Topups(ctx context.Context, byAdmin access.Principal) ([]Topup, error) {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT email, extra, granted_by, granted_at FROM quota_topups ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Topup{}
	for rows.Next() {
		var t Topup
		if err := rows.Scan(&t.Email, &t.Extra, &t.GrantedBy, &t.GrantedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Allowance composes the default with this email's top-up, and reports an admin
// as unlimited.
func (s *PGStore) Allowance(ctx context.Context, p access.Principal) (int, bool, error) {
	if s.auth != nil {
		tier, _, err := s.auth.Resolve(ctx, p)
		if err != nil {
			return 0, false, err
		}
		if tier == access.TierAdmin {
			return 0, true, nil
		}
	}
	settings, err := s.Settings(ctx)
	if err != nil {
		// Fail-closed: the conservative default, never unlimited.
		return settings.DefaultSessionQuota, false, err
	}
	var extra int
	err = s.pool.QueryRow(ctx, `SELECT extra FROM quota_topups WHERE email = $1`, normalizeEmail(p.Email)).Scan(&extra)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return settings.DefaultSessionQuota, false, err
	}
	return settings.DefaultSessionQuota + extra, false, nil
}
