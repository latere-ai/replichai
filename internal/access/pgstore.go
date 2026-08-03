package access

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGAccess is the Postgres implementation of Access. It satisfies the same
// contract the in-memory store does; the seed-and-bind is a row lock, so a
// concurrent first login binds exactly once.
type PGAccess struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

// NewPGAccess builds an access store over a pgx pool.
func NewPGAccess(pool *pgxpool.Pool) *PGAccess {
	return &PGAccess{pool: pool, clock: time.Now}
}

// SeedGrant creates or promotes a grant at a tier with no admin check — the
// bootstrap path for the admin and seed whitelists, applied at startup.
func (s *PGAccess) SeedGrant(ctx context.Context, email string, tier Tier) error {
	email = normalizeEmail(email)
	if email == "" {
		return ErrNoEmail
	}
	// A revoked address stays revoked. This list is replayed on every startup
	// from configuration (ADMIN_EMAILS), so clearing the revocation
	// here would make a ban last only until the next deploy. Re-admitting is an
	// admin's deliberate act and goes through Grant.
	_, err := s.pool.Exec(ctx, `
		INSERT INTO access_grants (email, tier, granted_by, granted_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (email) DO UPDATE SET
			tier=EXCLUDED.tier, granted_by=EXCLUDED.granted_by, granted_at=EXCLUDED.granted_at
		WHERE access_grants.revoked_at IS NULL`,
		email, int16(tier), "bootstrap", s.clock())
	return err
}

// SeedAdmin bootstraps an admin grant.
func (s *PGAccess) SeedAdmin(ctx context.Context, email string) error {
	return s.SeedGrant(ctx, email, TierAdmin)
}

// Resolve returns a principal's tier, binding a seeded grant on first login.
func (s *PGAccess) Resolve(ctx context.Context, p Principal) (Tier, bool, error) {
	var tier Tier
	var granted bool
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// Already bound: resolve by principal. A revoked row resolves to nobody,
		// exactly as a missing one does — the gate must not be able to tell the
		// difference, and neither must a banned person.
		var t int16
		var revoked bool
		err := tx.QueryRow(ctx,
			`SELECT tier, revoked_at IS NOT NULL FROM access_grants WHERE principal=$1`, p.Sub).Scan(&t, &revoked)
		if err == nil {
			if revoked {
				return nil
			}
			tier, granted = Tier(t), true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// Seed by the IdP-authenticated email, binding only an unbound grant.
		if p.Email == "" {
			return nil
		}
		email := normalizeEmail(p.Email)
		var principal *string
		err = tx.QueryRow(ctx,
			`SELECT tier, principal, revoked_at IS NOT NULL FROM access_grants WHERE email=$1 FOR UPDATE`,
			email).Scan(&t, &principal, &revoked)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if principal != nil { // bound to someone else already
			return nil
		}
		if revoked { // a ban is not an invitation to bind
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE access_grants SET principal=$1 WHERE email=$2`, p.Sub, email); err != nil {
			return err
		}
		tier, granted = Tier(t), true
		return nil
	})
	return tier, granted, err
}

// Grant seeds or promotes a grant by email. Admin only.
func (s *PGAccess) Grant(ctx context.Context, byAdmin Principal, email string, tier Tier) error {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return err
	}
	email = normalizeEmail(email)
	if email == "" {
		return ErrNoEmail
	}
	return s.upsert(ctx, email, tier, byAdmin.Sub)
}

// Revoke bans an email. Admin only; idempotent. See Access.Revoke for why this
// records rather than deletes.
//
// The WHERE clause is what makes a second revocation a no-op rather than a
// rewrite of when the first happened. The binding is deliberately left in
// place: it is what keeps a re-login by the same person landing on this row
// instead of creating a fresh, unrevoked one.
func (s *PGAccess) Revoke(ctx context.Context, byAdmin Principal, email string) error {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE access_grants SET revoked_at=$1, revoked_by=$2, tier=$3
		WHERE email=$4 AND revoked_at IS NULL`,
		s.clock(), byAdmin.Sub, int16(TierNone), normalizeEmail(email))
	return err
}

// SelfGrant admits a principal who has just proven their identity (006q).
//
// One statement, so two concurrent sign-ins cannot both insert: the conflict
// clause updates nothing, which leaves a seeded admin at their tier and a
// revoked address revoked. The follow-up Resolve is what binds and reports.
func (s *PGAccess) SelfGrant(ctx context.Context, p Principal) (Tier, bool, error) {
	if tier, ok, err := s.Resolve(ctx, p); err != nil || ok {
		return tier, ok, err
	}
	email := normalizeEmail(p.Email)
	if p.Sub == "" || email == "" {
		return TierNone, false, nil
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO access_grants (email, tier, granted_by, granted_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (email) DO NOTHING`,
		email, int16(TierUser), "self", s.clock()); err != nil {
		return TierNone, false, err
	}
	return s.Resolve(ctx, p)
}

// List returns every grant, sorted by email. Admin only.
func (s *PGAccess) List(ctx context.Context, byAdmin Principal) ([]Grant, error) {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT email, COALESCE(principal,''), tier, granted_by, granted_at, revoked_at, revoked_by
		 FROM access_grants ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		var g Grant
		var t int16
		if err := rows.Scan(&g.Email, &g.Principal, &t, &g.GrantedBy, &g.GrantedAt, &g.RevokedAt, &g.RevokedBy); err != nil {
			return nil, err
		}
		g.Tier = Tier(t)
		out = append(out, g)
	}
	return out, rows.Err()
}

// LinkedAccount returns a principal's proven forge login.
func (s *PGAccess) LinkedAccount(ctx context.Context, principalSub, forge string) (string, bool, error) {
	var login string
	err := s.pool.QueryRow(ctx,
		`SELECT login FROM forge_links WHERE principal=$1 AND forge=$2`, principalSub, forge).Scan(&login)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return login, true, nil
}

// LinkAccount records a proven forge login against a principal.
func (s *PGAccess) LinkAccount(ctx context.Context, principalSub, forge, login string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO forge_links (principal, forge, login) VALUES ($1,$2,$3)
		ON CONFLICT (principal, forge) DO UPDATE SET login=EXCLUDED.login`,
		principalSub, forge, login)
	return err
}

// upsert writes a grant's tier while preserving any binding, and clears any
// revocation: granting is how an admin re-admits somebody, so the two cannot be
// true of one row at once.
func (s *PGAccess) upsert(ctx context.Context, email string, tier Tier, by string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO access_grants (email, tier, granted_by, granted_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (email) DO UPDATE SET
			tier=EXCLUDED.tier, granted_by=EXCLUDED.granted_by, granted_at=EXCLUDED.granted_at,
			revoked_at=NULL, revoked_by=''`,
		email, int16(tier), by, s.clock())
	return err
}

// requireAdmin resolves the caller and refuses anyone below TierAdmin.
func (s *PGAccess) requireAdmin(ctx context.Context, p Principal) error {
	t, ok, err := s.Resolve(ctx, p)
	if err != nil {
		return err
	}
	if !ok || t != TierAdmin {
		return ErrNotAdmin
	}
	return nil
}
