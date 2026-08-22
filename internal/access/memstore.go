package access

import (
	"cmp"
	"context"
	"slices"
	"strings"
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
	// overrides holds active freeze overrides, keyed by normalized email (022d).
	// A cleared override is deleted rather than kept, which the FreezeOverrides
	// interface cannot tell apart from the Postgres store's soft clear.
	overrides map[string]freezeGrant
	clock     func() time.Time
}

// freezeGrant is one active override: who granted it and when.
type freezeGrant struct {
	grantedBy string
	grantedAt time.Time
}

// NewMemStore builds an empty in-memory access store.
func NewMemStore() *MemStore {
	return &MemStore{
		grants:         map[string]Grant{},
		principalEmail: map[string]string{},
		overrides:      map[string]freezeGrant{},
		clock:          time.Now,
	}
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
	// A revoked address stays revoked. This list is replayed on every startup
	// from configuration, so un-revoking here would turn a ban into something
	// that lasts until the next deploy — the same failure 006q's recorded
	// revocation exists to prevent, one restart further out. Re-admitting is an
	// admin's deliberate act, and it goes through Grant.
	if g, ok := s.grants[email]; ok && g.Revoked() {
		return nil
	}
	s.upsert(email, tier, "bootstrap")
	return nil
}

// SeedAdmin is SeedGrant at TierAdmin: the startup path that writes the
// platform-admin list. It inherits SeedGrant's rules, so a revoked address is
// not re-admitted by appearing on that list again.
func (s *MemStore) SeedAdmin(ctx context.Context, email string) error {
	return s.SeedGrant(ctx, email, TierAdmin)
}

// Resolve returns a principal's tier, binding a seeded grant on first login.
func (s *MemStore) Resolve(_ context.Context, p Principal) (Tier, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Already bound: resolve by principal, never by email again (one-way).
	if email, ok := s.principalEmail[p.Sub]; ok {
		g := s.grants[email]
		if g.Revoked() {
			return TierNone, false, nil
		}
		return g.Tier, true, nil
	}
	// Seed by the IdP-authenticated email, and only if the grant is not already
	// bound to someone else — a second principal is refused, not admitted onto
	// the grant. The email is auth's assertion for this authenticated session
	// (not a self-asserted value), and the operator whitelist is the guard, so
	// no separate email_verified flag is required.
	if p.Email != "" {
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

// SelfGrant admits a principal who has just proven their identity (006q).
func (s *MemStore) SelfGrant(ctx context.Context, p Principal) (Tier, bool, error) {
	// An existing grant — seeded, granted, or already self-granted — is resolved
	// and left exactly as it is. Only an address nobody has a row for is new.
	if tier, ok, err := s.Resolve(ctx, p); err != nil || ok {
		return tier, ok, err
	}
	email := normalizeEmail(p.Email)
	if p.Sub == "" || email == "" {
		return TierNone, false, nil
	}
	s.mu.Lock()
	if _, exists := s.grants[email]; exists {
		// Revoked, or bound to somebody else. Either way this is not a new
		// arrival and self-granting would undo a decision somebody made.
		s.mu.Unlock()
		return TierNone, false, nil
	}
	s.upsert(email, TierUser, "self")
	s.mu.Unlock()
	return s.Resolve(ctx, p)
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

// OverrideActive reports whether an uncleared freeze override is in force.
func (s *MemStore) OverrideActive(_ context.Context, email string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.overrides[normalizeEmail(email)]
	return ok, nil
}

// GrantOverride records a freeze override for email, naming the admin.
func (s *MemStore) GrantOverride(_ context.Context, byAdmin Principal, email string) error {
	e := normalizeEmail(email)
	if e == "" {
		return ErrNoEmail
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overrides[e] = freezeGrant{grantedBy: byAdmin.Email, grantedAt: s.clock().UTC()}
	return nil
}

// ClearOverride retires the freeze override for email, if any.
func (s *MemStore) ClearOverride(_ context.Context, email string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.overrides, normalizeEmail(email))
	return nil
}

// Revoke bans an email. Admin only; idempotent. See Access.Revoke for why this
// records rather than deletes.
func (s *MemStore) Revoke(ctx context.Context, byAdmin Principal, email string) error {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return err
	}
	email = normalizeEmail(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.grants[email]
	if !ok || g.Revoked() {
		// Nothing to ban, or already banned: the first revocation is the one
		// that counts, and a second must not rewrite when it happened.
		return nil
	}
	at := s.clock()
	g.RevokedAt, g.RevokedBy, g.Tier = &at, byAdmin.Sub, TierNone
	s.grants[email] = g
	// The binding stays. It is what keeps a re-login by the same person landing
	// on the revoked row instead of creating a fresh one.
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
	slices.SortFunc(out, func(a, b Grant) int { return cmp.Compare(a.Email, b.Email) })
	return out, nil
}

// ListPage returns one page of grants ordered by email, after opts.Cursor and
// matching opts.Query. It over-reads by one to learn whether a next page exists.
func (s *MemStore) ListPage(ctx context.Context, byAdmin Principal, opts ListOpts) ([]Grant, string, error) {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return nil, "", err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	cursor := normalizeEmail(opts.Cursor)
	q := normalizeEmail(opts.Query)
	s.mu.Lock()
	defer s.mu.Unlock()
	all := make([]Grant, 0, len(s.grants))
	for _, g := range s.grants {
		all = append(all, g)
	}
	slices.SortFunc(all, func(a, b Grant) int { return cmp.Compare(a.Email, b.Email) })
	out := make([]Grant, 0, limit+1)
	for _, g := range all {
		if g.Email <= cursor {
			continue
		}
		if q != "" && !strings.Contains(g.Email, q) {
			continue
		}
		out = append(out, g)
		if len(out) > limit {
			break
		}
	}
	next := ""
	if len(out) > limit {
		next = out[limit-1].Email
		out = out[:limit]
	}
	return out, next, nil
}

// GrantsFor returns the grants for a set of emails, unordered, skipping any that
// do not exist.
func (s *MemStore) GrantsFor(ctx context.Context, byAdmin Principal, emails []string) ([]Grant, error) {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Grant, 0, len(emails))
	for _, e := range emails {
		if g, ok := s.grants[normalizeEmail(e)]; ok {
			out = append(out, g)
		}
	}
	return out, nil
}

// OverridesActiveFor reports which of a batch of emails carry an active override.
// A missing email is absent from the map, not false — it simply has none.
func (s *MemStore) OverridesActiveFor(_ context.Context, emails []string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(emails))
	for _, e := range emails {
		ne := normalizeEmail(e)
		if _, ok := s.overrides[ne]; ok {
			out[ne] = true
		}
	}
	return out, nil
}

// upsert writes a grant's tier while preserving any existing binding, and
// clears any revocation: granting is how an admin re-admits somebody, so the
// two cannot be true at once. Caller holds the lock.
func (s *MemStore) upsert(email string, tier Tier, by string) {
	g := s.grants[email] // zero value when new
	g.Email = email
	g.Tier = tier
	g.GrantedBy = by
	g.GrantedAt = s.clock()
	g.RevokedAt, g.RevokedBy = nil, ""
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
