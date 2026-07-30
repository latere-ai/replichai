package profile

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
const profileColumns = `principal, display_name, institution, contact_email, orcid, scholar_url, updated_at`

// scanProfile reads one row in profileColumns order.
func scanProfile(row pgx.Row) (Profile, error) {
	var p Profile
	err := row.Scan(&p.Principal, &p.DisplayName, &p.Institution, &p.Contact, &p.ORCID, &p.Scholar, &p.UpdatedAt)
	if err != nil {
		return Profile{}, err
	}
	return p, nil
}

// Get returns a principal's profile, or the zero profile if they have none —
// never a not-found error, so first use renders like any other.
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
		INSERT INTO profiles (principal, display_name, institution, contact_email, orcid, scholar_url, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (principal) DO UPDATE SET
			display_name  = EXCLUDED.display_name,
			institution   = EXCLUDED.institution,
			contact_email = EXCLUDED.contact_email,
			orcid         = EXCLUDED.orcid,
			scholar_url   = EXCLUDED.scholar_url,
			updated_at    = EXCLUDED.updated_at
		RETURNING `+profileColumns,
		p.Principal, p.DisplayName, p.Institution, p.Contact, p.ORCID, p.Scholar, s.clock())
	return scanProfile(row)
}
