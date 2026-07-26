package access

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemStore is an in-memory Access for tests and for the rest of 006 to build
// against with no database. The Postgres implementation lands against the same
// contract.
type MemStore struct {
	mu sync.Mutex
	// grants is keyed by normalized email; principalEmail indexes bound grants
	// by principal so a resolve by principal is O(1) and never rebinds.
	grants         map[string]Grant
	principalEmail map[string]string
	// links[principalSub][forge] = proven login.
	links map[string]map[string]string
	clock func() time.Time
}

// MemOption configures a MemStore.
type MemOption func(*MemStore)

// WithClock sets the time source, for deterministic tests.
func WithClock(f func() time.Time) MemOption { return func(s *MemStore) { s.clock = f } }

// NewMemStore builds an empty in-memory access store.
func NewMemStore(opts ...MemOption) *MemStore {
	s := &MemStore{
		grants:         map[string]Grant{},
		principalEmail: map[string]string{},
		links:          map[string]map[string]string{},
		clock:          time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// SeedGrant creates or promotes a grant at a tier with no admin check — the
// bootstrap path for the platform-admin and seed-user email lists, applied at
// startup. A development shortcut it is not: it is the deployment operator
// writing the initial whitelist, it seeds by email like every other grant, and
// it records granted_by="bootstrap" so the trail shows how a grant arrived.
// Runtime grants still go through an admin (/admin/access).
func (s *MemStore) SeedGrant(_ context.Context, email string, tier Tier) error {
	email = normalizeEmail(email)
	if email == "" {
		return ErrNoEmail
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsert(email, tier, "bootstrap")
	return nil
}

// SeedAdmin bootstraps an admin grant.
func (s *MemStore) SeedAdmin(ctx context.Context, email string) error {
	return s.SeedGrant(ctx, email, TierAdmin)
}

// Resolve returns a principal's tier, binding a seeded grant on first login.
func (s *MemStore) Resolve(_ context.Context, p Principal) (Tier, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Already bound: resolve by principal, never by email again (one-way).
	if email, ok := s.principalEmail[p.Sub]; ok {
		return s.grants[email].Tier, true, nil
	}
	// Seed by a verified email, and only if the grant is not already bound to
	// someone else — a second principal is refused, not admitted onto the grant.
	if p.EmailVerified && p.Email != "" {
		email := normalizeEmail(p.Email)
		if g, ok := s.grants[email]; ok && g.Principal == "" {
			g.Principal = p.Sub
			s.grants[email] = g
			s.principalEmail[p.Sub] = email
			return g.Tier, true, nil
		}
	}
	return TierNone, false, nil
}

// Grant seeds or promotes a grant by email. Admin only.
func (s *MemStore) Grant(ctx context.Context, byAdmin Principal, email string, tier Tier) error {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return err
	}
	email = normalizeEmail(email)
	if email == "" {
		return ErrNoEmail
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsert(email, tier, byAdmin.Sub)
	return nil
}

// Revoke removes a grant by email. Admin only; idempotent.
func (s *MemStore) Revoke(ctx context.Context, byAdmin Principal, email string) error {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return err
	}
	email = normalizeEmail(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.grants[email]; ok {
		if g.Principal != "" {
			delete(s.principalEmail, g.Principal)
		}
		delete(s.grants, email)
	}
	return nil
}

// List returns every grant, sorted by email. Admin only.
func (s *MemStore) List(ctx context.Context, byAdmin Principal) ([]Grant, error) {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Grant, 0, len(s.grants))
	for _, g := range s.grants {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

// LinkedAccount returns a principal's proven forge login.
func (s *MemStore) LinkedAccount(_ context.Context, principalSub, forge string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	login, ok := s.links[principalSub][forge]
	return login, ok, nil
}

// LinkAccount records a proven forge login against a principal.
func (s *MemStore) LinkAccount(_ context.Context, principalSub, forge, login string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.links[principalSub] == nil {
		s.links[principalSub] = map[string]string{}
	}
	s.links[principalSub][forge] = login
	return nil
}

// upsert writes a grant's tier while preserving any existing binding. Caller
// holds the lock.
func (s *MemStore) upsert(email string, tier Tier, by string) {
	g := s.grants[email] // zero value when new
	g.Email = email
	g.Tier = tier
	g.GrantedBy = by
	g.GrantedAt = s.clock()
	s.grants[email] = g
}

// requireAdmin resolves the caller and refuses anyone below TierAdmin.
func (s *MemStore) requireAdmin(ctx context.Context, p Principal) error {
	t, ok, err := s.Resolve(ctx, p)
	if err != nil {
		return err
	}
	if !ok || t != TierAdmin {
		return ErrNotAdmin
	}
	return nil
}
