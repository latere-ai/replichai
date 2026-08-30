// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package platform

import (
	"context"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
)

// MemStore is an in-memory Store for tests and for offline runs with no
// database. The Postgres implementation lands against the same contract test.
type MemStore struct {
	mu       sync.Mutex
	settings Settings
	auth     Authority
	clock    func() time.Time
}

// NewMemStore builds an in-memory platform store seeded with the conservative
// default. auth may be nil, in which case every caller is treated as an admin —
// for offline runs with no access store wired, never for a served deployment.
func NewMemStore(auth Authority) *MemStore {
	return &MemStore{
		settings: Settings{
			StartReserve: DefaultStartReserve, OverdraftCap: DefaultOverdraftCap, Rates: credit.DefaultCard(),
			SpreadBps: DefaultSpreadBps, SpreadFixedMicro: DefaultSpreadFixedMicro, MinTopupMicro: DefaultMinTopupMicro,
		},
		auth:  auth,
		clock: time.Now,
	}
}

// requireAdmin re-checks the caller's tier, so an admin-only write holds even
// if a route were mounted without the tier guard. Shared by both stores: the
// check is the same belt-and-braces one wherever the settings row lives, and a
// second copy is a second thing to keep in step.
//
// A nil authority means every caller is an admin — the offline run with no
// access store wired, never a served deployment.
func requireAdmin(ctx context.Context, auth Authority, p access.Principal) error {
	if auth == nil {
		return nil
	}
	tier, _, err := auth.Resolve(ctx, p)
	if err != nil {
		return err
	}
	if tier != access.TierAdmin {
		return ErrNotAdmin
	}
	return nil
}

// Settings reads the platform row.
func (s *MemStore) Settings(context.Context) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings, nil
}

// SetSettings writes the platform row, recording the admin who did it.
func (s *MemStore) SetSettings(ctx context.Context, byAdmin access.Principal, next Settings) error {
	if err := next.validate(); err != nil {
		return err
	}
	if err := requireAdmin(ctx, s.auth, byAdmin); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(next.Rates.Models) == 0 {
		next.Rates = credit.DefaultCard()
	}
	next.UpdatedBy, next.UpdatedAt = byAdmin.Email, s.clock().UTC()
	s.settings = next
	return nil
}
