// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
			bound, err := decodeTier(t)
			if err != nil {
				return err
			}
			tier, granted = bound, true
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
		if revoked { // a ban is not an invitation to bind, nor a grant to resolve
			return nil
		}
		// Decoded before the UPDATE: binding is one-way, so a row carrying a tier
		// this package never wrote must not consume the principal on its way out.
		seeded, err := decodeTier(t)
		if err != nil {
			return err
		}
		switch outcomeOf(principal, p.Sub) {
		case bindForeign: // bound to someone else already
			return nil
		case bindMine: // this principal bound it a moment ago, on a parallel request
			tier, granted = seeded, true
			return nil
		case bindClaim:
		}
		if _, err := tx.Exec(ctx, `UPDATE access_grants SET principal=$1 WHERE email=$2`, p.Sub, email); err != nil {
			return err
		}
		tier, granted = seeded, true
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

// OverrideActive reports whether an uncleared freeze override is in force for
// email (022d). No admin check: it is a read the gate makes on every request.
func (s *PGAccess) OverrideActive(ctx context.Context, email string) (bool, error) {
	var active bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM freeze_overrides WHERE email=$1 AND cleared_at IS NULL)`,
		normalizeEmail(email)).Scan(&active)
	return active, err
}

// GrantOverride records a freeze override, (re)activating it. Admin only, the
// same belt-and-braces check every other administrative write here makes. The
// upsert clears any prior cleared_at, so granting after a clear starts a fresh
// override. The admin is recorded by principal, as Grant and Revoke record
// them, so the audit trail names an admin one way.
func (s *PGAccess) GrantOverride(ctx context.Context, byAdmin Principal, email string) error {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return err
	}
	e := normalizeEmail(email)
	if e == "" {
		return ErrNoEmail
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO freeze_overrides (email, granted_by, granted_at, cleared_at)
		VALUES ($1,$2,$3,NULL)
		ON CONFLICT (email) DO UPDATE SET granted_by=EXCLUDED.granted_by, granted_at=EXCLUDED.granted_at, cleared_at=NULL`,
		e, byAdmin.Sub, s.clock().UTC())
	return err
}

// ClearOverride retires the override for email, if one is in force. It records
// the clear rather than deleting the row, so the audit trail of who was waived
// and when survives.
func (s *PGAccess) ClearOverride(ctx context.Context, email string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE freeze_overrides SET cleared_at=$2 WHERE email=$1 AND cleared_at IS NULL`,
		normalizeEmail(email), s.clock().UTC())
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
	return scanGrants(rows)
}

// ListPage returns one page of grants ordered by email, after opts.Cursor and
// matching opts.Query, in one keyset query. It over-reads by one row to learn
// whether a further page exists without a second count.
func (s *PGAccess) ListPage(ctx context.Context, byAdmin Principal, opts ListOpts) ([]Grant, string, error) {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return nil, "", err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultPageLimit
	}
	cursor := strings.ToLower(strings.TrimSpace(opts.Cursor))
	q := strings.ToLower(strings.TrimSpace(opts.Query))
	// The cursor is a keyset on email, not an offset: a page is the emails after
	// the last one seen, so an inserted grant never shifts a boundary. The '%' is
	// wrapped here rather than in SQL so an empty query is a plain match-all.
	like := "%" + q + "%"
	rows, err := s.pool.Query(ctx,
		`SELECT email, COALESCE(principal,''), tier, granted_by, granted_at, revoked_at, revoked_by
		 FROM access_grants
		 WHERE email > $1 AND ($2 = '' OR email ILIKE $3)
		 ORDER BY email LIMIT $4`, cursor, q, like, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out, err := scanGrants(rows)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		next = out[limit-1].Email
		out = out[:limit]
	}
	return out, next, nil
}

// GrantsFor returns the grants for a set of emails in one query, unordered.
func (s *PGAccess) GrantsFor(ctx context.Context, byAdmin Principal, emails []string) ([]Grant, error) {
	if err := s.requireAdmin(ctx, byAdmin); err != nil {
		return nil, err
	}
	if len(emails) == 0 {
		return nil, nil
	}
	keys := make([]string, len(emails))
	for i, e := range emails {
		keys[i] = strings.ToLower(strings.TrimSpace(e))
	}
	rows, err := s.pool.Query(ctx,
		`SELECT email, COALESCE(principal,''), tier, granted_by, granted_at, revoked_at, revoked_by
		 FROM access_grants WHERE email = ANY($1)`, keys)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGrants(rows)
}

// errTierRange reports a tier column holding a value no tier of this package
// names. It is an error rather than a silent TierNone because the grants table
// is the authorisation decision: a value nobody here could have written is
// corruption, and corruption in an ACL should be read about, not interpreted.
var errTierRange = errors.New("access: grant tier out of range")

// decodeTier turns a scanned SMALLINT into a Tier, failing closed on anything
// outside the closed set of tiers.
//
// The column is SMALLINT with no CHECK, so it can hold whatever an out-of-band
// write left there, and a plain Tier(t) conversion would truncate rather than
// refuse: a 259 arrives as 3, which is TierAdmin, and so would -253. Comparing
// against the tiers themselves means a value this package never wrote can only
// ever be refused, never promoted.
func decodeTier(t int16) (Tier, error) {
	switch t {
	case int16(TierNone):
		return TierNone, nil
	case int16(TierUser):
		return TierUser, nil
	case int16(TierAdmin):
		return TierAdmin, nil
	default:
		return TierNone, fmt.Errorf("%w: %d", errTierRange, t)
	}
}

// scanGrants reads a rows cursor of the grant columns into Grants, in order.
func scanGrants(rows pgx.Rows) ([]Grant, error) {
	var out []Grant
	for rows.Next() {
		var g Grant
		var t int16
		if err := rows.Scan(&g.Email, &g.Principal, &t, &g.GrantedBy, &g.GrantedAt, &g.RevokedAt, &g.RevokedBy); err != nil {
			return nil, err
		}
		tier, err := decodeTier(t)
		if err != nil {
			return nil, fmt.Errorf("grant %s: %w", g.Email, err)
		}
		g.Tier = tier
		out = append(out, g)
	}
	return out, rows.Err()
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
	return RequireAdmin(ctx, s, p)
}
