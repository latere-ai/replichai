package platform

import (
	"context"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/access"
)

// MemStore is an in-memory Store for tests and for offline runs with no
// database. The Postgres implementation lands against the same contract test.
type MemStore struct {
	mu       sync.Mutex
	settings Settings
	auth     Authority
	clock    func() time.Time
}

// MemOption configures a MemStore.
type MemOption func(*MemStore)

// WithClock sets the time source, for deterministic tests.
func WithClock(f func() time.Time) MemOption { return func(s *MemStore) { s.clock = f } }

// NewMemStore builds an in-memory platform store seeded with the conservative
// default. auth may be nil, in which case every caller is treated as an admin —
// for offline runs with no access store wired, never for a served deployment.
func NewMemStore(auth Authority, opts ...MemOption) *MemStore {
	s := &MemStore{
		settings: Settings{StartReserve: DefaultStartReserve},
		auth:     auth,
		clock:    time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// requireAdmin re-checks the caller's tier, so an admin-only write holds even
// if a route were mounted without the tier guard.
func (s *MemStore) requireAdmin(ctx context.Context, p access.Principal) error {
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
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next.UpdatedBy, next.UpdatedAt = byAdmin.Email, s.clock().UTC()
	s.settings = next
	return nil
}
