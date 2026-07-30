package project

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

// projectColumns is the column list every read selects, in scan order.
const projectColumns = `id, owner, title, visibility, sponsored, created_at, signed_off_by, signed_off_at, attestation_version,
	signed_off_name, signed_off_institution, signed_off_contact, signed_off_orcid, signed_off_scholar, signed_off_email`

// scanProject reads one row in projectColumns order.
func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Owner, &p.Title, &p.Visibility, &p.Sponsored, &p.CreatedAt,
		&p.SignedOffBy, &p.SignedOffAt, &p.AttestationVersion,
		&p.SignedOffName, &p.SignedOffInstitution, &p.SignedOffContact,
		&p.SignedOffORCID, &p.SignedOffScholar, &p.SignedOffEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

// Create inserts a new private project.
func (s *PGStore) Create(ctx context.Context, owner, title string) (Project, error) {
	if strings.TrimSpace(owner) == "" {
		return Project{}, ErrNoOwner
	}
	id, err := newID()
	if err != nil {
		return Project{}, err
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO projects (id, owner, title, visibility, created_at)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING `+projectColumns,
		id, owner, title, VisibilityPrivate, s.clock())
	return scanProject(row)
}

// Get returns one project by id.
func (s *PGStore) Get(ctx context.Context, id string) (Project, error) {
	return scanProject(s.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = $1`, id))
}

// ListByOwner returns an owner's projects, newest first.
func (s *PGStore) ListByOwner(ctx context.Context, owner string) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectColumns+` FROM projects WHERE owner = $1 ORDER BY created_at DESC, id DESC`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetTitle updates a project's title, ignoring a missing project.
func (s *PGStore) SetTitle(ctx context.Context, id, title string) error {
	_, err := s.pool.Exec(ctx, `UPDATE projects SET title = $2 WHERE id = $1`, id, title)
	return err
}

// SetSponsored records or withdraws the owner's standing consent.
func (s *PGStore) SetSponsored(ctx context.Context, id string, on bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE projects SET sponsored=$2 WHERE id=$1`, id, on)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetPublic marks a project public and records the sign-off. One-way.
func (s *PGStore) SetPublic(ctx context.Context, id string, so SignOff) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE projects SET visibility='public', signed_off_by=$2, signed_off_at=$3, attestation_version=$4,
			signed_off_name=$5, signed_off_institution=$6, signed_off_contact=$7,
			signed_off_orcid=$8, signed_off_scholar=$9, signed_off_email=$10
		WHERE id=$1`,
		id, so.By, s.clock(), so.AttestationVersion,
		so.Name, so.Institution, so.Contact, so.ORCID, so.Scholar, so.Email)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListPublic returns every public project, newest first.
func (s *PGStore) ListPublic(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectColumns+` FROM projects WHERE visibility='public' ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Delete removes a project by id, idempotently.
func (s *PGStore) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	return err
}
