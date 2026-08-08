package project

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// foreignKeyViolation is the SQLSTATE a paper insert returns when the project it
// names is not there.
const foreignKeyViolation = "23503"

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
const projectColumns = `id, owner, title, visibility, credit_cap, created_at, signed_off_by, signed_off_at, attestation_version,
	signed_off_name, signed_off_institution, signed_off_contact, signed_off_orcid, signed_off_scholar, signed_off_email,
	withdrawn_at, withdrawn_by, withdrawn_reason`

// scanProject reads one row in projectColumns order.
func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Owner, &p.Title, &p.Visibility, &p.CreditCap, &p.CreatedAt,
		&p.SignedOffBy, &p.SignedOffAt, &p.AttestationVersion,
		&p.SignedOffName, &p.SignedOffInstitution, &p.SignedOffContact,
		&p.SignedOffORCID, &p.SignedOffScholar, &p.SignedOffEmail,
		&p.WithdrawnAt, &p.WithdrawnBy, &p.WithdrawnReason)
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

// SetCreditCap overrides this project's overdraft floor; zero restores the
// platform default.
func (s *PGStore) SetCreditCap(ctx context.Context, id string, cap int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE projects SET credit_cap = $2 WHERE id = $1`, id, cap)
	return err
}

// SetPublic marks a project public and records the sign-off, superseding any
// earlier withdrawal.
func (s *PGStore) SetPublic(ctx context.Context, id string, so SignOff) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE projects SET visibility='public', signed_off_by=$2, signed_off_at=$3, attestation_version=$4,
			signed_off_name=$5, signed_off_institution=$6, signed_off_contact=$7,
			signed_off_orcid=$8, signed_off_scholar=$9, signed_off_email=$10,
			withdrawn_at=NULL, withdrawn_by='', withdrawn_reason=''
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

// SetPrivate withdraws a publication, recording the retraction beside the
// sign-off it ends. The visibility predicate makes it a no-op on a project that
// is already private, so a repeated call cannot overwrite the moment the
// publication actually ended — which costs one extra read to tell that apart
// from a missing project.
func (s *PGStore) SetPrivate(ctx context.Context, id string, w Withdrawal) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE projects SET visibility='private', withdrawn_at=$2, withdrawn_by=$3, withdrawn_reason=$4
		WHERE id=$1 AND visibility='public'`,
		id, s.clock(), w.By, w.Reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Either the project is missing or it was already private. Only the first
		// is an error.
		if _, err := s.Get(ctx, id); err != nil {
			return err
		}
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

// ModifiedAt returns each named project's creation-or-newest-paper time, in one
// query. The LEFT JOIN is what makes a project with no papers answer its
// creation time rather than dropping out of the result.
func (s *PGStore) ModifiedAt(ctx context.Context, ids []string) (map[string]time.Time, error) {
	if len(ids) == 0 {
		return map[string]time.Time{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, GREATEST(p.created_at, COALESCE(MAX(pp.added_at), p.created_at))
		FROM projects p
		LEFT JOIN project_papers pp ON pp.project_id = p.id
		WHERE p.id = ANY($1)
		GROUP BY p.id, p.created_at`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]time.Time{}
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		out[id] = at
	}
	return out, rows.Err()
}

// Delete removes a project by id, idempotently.
func (s *PGStore) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	return err
}

// paperColumns is the column list every paper read selects, in scan order.
const paperColumns = `paper_key, ref, title, added_by, added_at`

// AddPaper records a paper the project is about. The primary key is what makes
// a duplicate impossible, so the refusal is read off the insert rather than
// guessed at with a prior SELECT — two adds racing must not both succeed.
func (s *PGStore) AddPaper(ctx context.Context, projectID string, p Paper) error {
	if p.AddedAt.IsZero() {
		p.AddedAt = s.clock()
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO project_papers (project_id, paper_key, ref, title, added_by, added_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (project_id, paper_key) DO NOTHING`,
		projectID, p.Key, p.Ref, p.Title, p.AddedBy, p.AddedAt)
	if err != nil {
		// The foreign key is the only other way this fails on well-formed input,
		// and it means the project is not there.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation {
			return ErrNotFound
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPaperExists
	}
	return nil
}

// ListPapers returns a project's papers in the order they were added.
func (s *PGStore) ListPapers(ctx context.Context, projectID string) ([]Paper, error) {
	// The project read comes first so a missing project is ErrNotFound rather
	// than an empty list — "no papers" and "no project" are different answers.
	if _, err := s.Get(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+paperColumns+` FROM project_papers
		WHERE project_id=$1 ORDER BY added_at, paper_key`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Paper
	for rows.Next() {
		var p Paper
		if err := rows.Scan(&p.Key, &p.Ref, &p.Title, &p.AddedBy, &p.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetPaperTitle fills in a title, ignoring a missing project or paper.
func (s *PGStore) SetPaperTitle(ctx context.Context, projectID, key, title string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE project_papers SET title=$3 WHERE project_id=$1 AND paper_key=$2`, projectID, key, title)
	return err
}

// RemovePaper drops a paper from a project, idempotently.
func (s *PGStore) RemovePaper(ctx context.Context, projectID, key string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM project_papers WHERE project_id=$1 AND paper_key=$2`, projectID, key)
	return err
}
