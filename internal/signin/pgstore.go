// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package signin

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore is the Postgres Store. Rotate holds the person's row lock for the
// length of the refresh, so a second process refreshing the same person
// waits and then reads the token the first one stored.
type PGStore struct {
	pool   *pgxpool.Pool
	sealer *Sealer
	now    func() time.Time
}

// NewPGStore builds a store over a pool.
func NewPGStore(pool *pgxpool.Pool, sealer *Sealer) *PGStore {
	return &PGStore{pool: pool, sealer: sealer, now: time.Now}
}

// Put records the person's refresh token.
func (s *PGStore) Put(ctx context.Context, subject, refresh string) error {
	sealed, err := s.sealer.Seal(subject, refresh)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO platform_signins (subject, sealed, updated_at) VALUES ($1, $2, $3)
		ON CONFLICT (subject) DO UPDATE SET sealed = EXCLUDED.sealed, updated_at = EXCLUDED.updated_at`,
		subject, sealed, s.now())
	return err
}

// Rotate replaces the person's refresh token with what next returns.
func (s *PGStore) Rotate(ctx context.Context, subject string, next func(string) (string, error)) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var sealed []byte
		err := tx.QueryRow(ctx, `SELECT sealed FROM platform_signins WHERE subject = $1 FOR UPDATE`, subject).Scan(&sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoSignIn
		}
		if err != nil {
			return err
		}
		current, err := s.sealer.Open(subject, sealed)
		if err != nil {
			return err
		}
		replacement, err := next(current)
		if err != nil {
			return err
		}
		resealed, err := s.sealer.Seal(subject, replacement)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE platform_signins SET sealed = $2, updated_at = $3 WHERE subject = $1`, subject, resealed, s.now())
		return err
	})
}

// Forget removes the person's sign-in.
func (s *PGStore) Forget(ctx context.Context, subject string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM platform_signins WHERE subject = $1`, subject)
	return err
}
