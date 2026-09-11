// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGStore is the Postgres implementation of Store, satisfying the same contract
// the in-memory store does.
type PGStore struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

// NewPGStore builds a store over a pgx pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool, clock: time.Now}
}

// profileColumns is the column list every read selects, in scan order.
const profileColumns = `principal, display_name, institution, contact_email, orcid, scholar_url, updated_at, handle, public`

// scanProfile reads one row in profileColumns order.
func scanProfile(row pgx.Row) (Profile, error) {
	var p Profile
	// handle is NULL when unset, so the unique index constrains only the people
	// who chose one; it reads back as the empty string.
	var handle *string
	err := row.Scan(&p.Principal, &p.DisplayName, &p.Institution, &p.Contact, &p.ORCID, &p.Scholar,
		&p.UpdatedAt, &handle, &p.Public)
	if err != nil {
		return Profile{}, err
	}
	if handle != nil {
		p.Handle = *handle
	}
	return p, nil
}

// nullableHandle maps the empty handle to NULL on the way in.
func nullableHandle(handle string) *string {
	if handle == "" {
		return nil
	}
	return &handle
}

// Get returns a principal's profile, or the zero profile if they have none.
// It never returns a not-found error, so first use renders like any other.
func (s *PGStore) Get(ctx context.Context, principal string) (Profile, error) {
	p, err := scanProfile(s.pool.QueryRow(ctx, `SELECT `+profileColumns+` FROM profiles WHERE principal = $1`, principal))
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{Principal: principal}, nil
	}
	if err != nil {
		return Profile{}, err
	}
	return p, nil
}

// Save validates and upserts a principal's profile.
func (s *PGStore) Save(ctx context.Context, p Profile) (Profile, error) {
	if strings.TrimSpace(p.Principal) == "" {
		return Profile{}, ErrNoPrincipal
	}
	p = p.Normalize()
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO profiles (principal, display_name, institution, contact_email, orcid, scholar_url, updated_at, handle, public)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (principal) DO UPDATE SET
			display_name  = EXCLUDED.display_name,
			institution   = EXCLUDED.institution,
			contact_email = EXCLUDED.contact_email,
			orcid         = EXCLUDED.orcid,
			scholar_url   = EXCLUDED.scholar_url,
			updated_at    = EXCLUDED.updated_at,
			handle        = EXCLUDED.handle,
			public        = EXCLUDED.public
		RETURNING `+profileColumns,
		p.Principal, p.DisplayName, p.Institution, p.Contact, p.ORCID, p.Scholar, s.clock(),
		nullableHandle(p.Handle), p.Public)
	saved, err := scanProfile(row)
	if err != nil && isUniqueViolation(err) {
		// The unique index decides who holds a handle, not a read-then-write
		// in this function. Two people claiming one name at the same instant
		// must not both succeed.
		return Profile{}, ErrHandleTaken
	}
	return saved, err
}

// isUniqueViolation reports whether an error is Postgres' unique-constraint code.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ByHandle resolves a public profile by handle. A private profile and an
// unknown handle return the same error deliberately.
func (s *PGStore) ByHandle(ctx context.Context, handle string) (Profile, error) {
	handle = strings.ToLower(strings.TrimSpace(handle))
	if handle == "" {
		return Profile{}, ErrNotFound
	}
	p, err := scanProfile(s.pool.QueryRow(ctx,
		`SELECT `+profileColumns+` FROM profiles WHERE handle = $1 AND public`, handle))
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	if err != nil {
		return Profile{}, err
	}
	return p, nil
}

// PrincipalHandle returns a principal's handle when their profile is public.
func (s *PGStore) PrincipalHandle(ctx context.Context, principal string) (string, bool, error) {
	var handle *string
	err := s.pool.QueryRow(ctx,
		`SELECT handle FROM profiles WHERE principal = $1 AND public`, principal).Scan(&handle)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if handle == nil || *handle == "" {
		return "", false, nil
	}
	return *handle, true, nil
}
