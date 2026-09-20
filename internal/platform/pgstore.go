// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"github.com/latere-ai/replichai/internal/draft"
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

// Settings reads the platform row. It fails closed: a missing row or a
// failing query yields the conservative default rather than an unlimited
// one, and the error is returned with it so the caller can log it.
func (s *PGStore) Settings(ctx context.Context) (Settings, error) {
	fallback := Settings{
		StartReserve: DefaultStartReserve, OverdraftCap: DefaultOverdraftCap, Rates: credit.DefaultCard(),
		SpreadBps: DefaultSpreadBps, SpreadFixedMicro: DefaultSpreadFixedMicro, MinTopupMicro: DefaultMinTopupMicro,
	}
	var out Settings
	var card, drafting []byte
	err := s.pool.QueryRow(ctx,
		`SELECT start_reserve, overdraft_cap, rate_card, spread_bps, spread_fixed_micro, min_topup_micro, draft, updated_by, updated_at FROM platform_settings WHERE id = 1`,
	).Scan(&out.StartReserve, &out.OverdraftCap, &card, &out.SpreadBps, &out.SpreadFixedMicro, &out.MinTopupMicro, &drafting, &out.UpdatedBy, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return fallback, nil
	}
	if err != nil {
		return fallback, err
	}
	// An unreadable drafting config is treated as no drafting model, which
	// is a state everything downstream already handles.
	if err := json.Unmarshal(drafting, &out.Draft); err != nil {
		out.Draft = draft.Config{}
	}
	if err := json.Unmarshal(card, &out.Rates); err != nil || len(out.Rates.Models) == 0 {
		// An unreadable or empty card would price a session at nothing.
		// Fall back to the compiled-in list.
		out.Rates = credit.DefaultCard()
	}
	return out, nil
}

// SetSettings writes the platform row, recording the admin who did it. The
// row is pinned to id 1, so the write is an upsert and never creates a second
// row.
func (s *PGStore) SetSettings(ctx context.Context, byAdmin access.Principal, next Settings) error {
	if err := next.validate(); err != nil {
		return err
	}
	if err := requireAdmin(ctx, s.auth, byAdmin); err != nil {
		return err
	}
	if len(next.Rates.Models) == 0 {
		next.Rates = credit.DefaultCard()
	}
	// rate_card and draft are both jsonb NOT NULL, and each encoding binds as
	// text: whenever parameters travel in the text format, a byte slice is
	// sent as bytea and reaches the server as a hex literal, which jsonb
	// refuses with SQLSTATE 22P02. See encodeStatus in internal/job for why
	// text is bound rather than trusting the DSN's exec mode.
	encodedCard, err := json.Marshal(next.Rates)
	if err != nil {
		return err
	}
	card := string(encodedCard)
	encodedDraft, err := json.Marshal(next.Draft)
	if err != nil {
		return err
	}
	drafting := string(encodedDraft)
	_, err = s.pool.Exec(ctx, `
		INSERT INTO platform_settings (id, start_reserve, overdraft_cap, rate_card, spread_bps, spread_fixed_micro, min_topup_micro, draft, updated_by, updated_at)
		VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE
		SET start_reserve      = EXCLUDED.start_reserve,
		    overdraft_cap      = EXCLUDED.overdraft_cap,
		    rate_card          = EXCLUDED.rate_card,
		    spread_bps         = EXCLUDED.spread_bps,
		    spread_fixed_micro = EXCLUDED.spread_fixed_micro,
		    min_topup_micro    = EXCLUDED.min_topup_micro,
		    draft              = EXCLUDED.draft,
		    updated_by         = EXCLUDED.updated_by,
		    updated_at         = EXCLUDED.updated_at`,
		next.StartReserve, next.OverdraftCap, card, next.SpreadBps, next.SpreadFixedMicro, next.MinTopupMicro, drafting, byAdmin.Email, s.clock().UTC())
	return err
}
