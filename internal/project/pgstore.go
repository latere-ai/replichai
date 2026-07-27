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
const projectColumns = `id, owner, title, visibility, created_at, signed_off_by, signed_off_at, attestation_version`

// scanProject reads one row in projectColumns order.
func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Owner, &p.Title, &p.Visibility, &p.CreatedAt,
		&p.SignedOffBy, &p.SignedOffAt, &p.AttestationVersion)
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

// SetPublic marks a project public and records the sign-off. One-way.
func (s *PGStore) SetPublic(ctx context.Context, id, signedOffBy, attestationVersion string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE projects SET visibility='public', signed_off_by=$2, signed_off_at=$3, attestation_version=$4
		WHERE id=$1`,
		id, signedOffBy, s.clock(), attestationVersion)
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
