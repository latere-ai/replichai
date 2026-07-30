package platform

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/access"
)

// MemStore is an in-memory Store for tests and for offline runs with no
// database. The Postgres implementation lands against the same contract test.
type MemStore struct {
	mu       sync.Mutex
	settings Settings
	topups   map[string]Topup // keyed by normalized email
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
		settings: Settings{DefaultSessionQuota: DefaultSessionQuota},
		topups:   map[string]Topup{},
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

// SetTopup sets one email's extra headroom; zero removes it.
func (s *MemStore) SetTopup(ctx context.Context, byAdmin access.Principal, email string, extra int) error {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if extra == 0 {
		delete(s.topups, email)
		return nil
	}
	s.topups[email] = Topup{Email: email, Extra: extra, GrantedBy: byAdmin.Email, GrantedAt: s.clock().UTC()}
	return nil
}

// Topups lists every top-up, email-ordered.
func (s *MemStore) Topups(ctx context.Context, byAdmin access.Principal) ([]Topup, error) {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Topup, 0, len(s.topups))
	for _, t := range s.topups {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

// Allowance composes the default with this email's top-up, and reports an admin
// as unlimited.
func (s *MemStore) Allowance(ctx context.Context, p access.Principal) (int, bool, error) {
	if s.auth != nil {
		tier, _, err := s.auth.Resolve(ctx, p)
		if err != nil {
			return 0, false, err
		}
		if tier == access.TierAdmin {
			return 0, true, nil
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings.DefaultSessionQuota + s.topups[normalizeEmail(p.Email)].Extra, false, nil
}
