// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package project

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/paper"
)

// foreignKeyViolation is the SQLSTATE a paper insert returns when the project it
// names is not there, and uniqueViolation the one a move returns when the
// destination already holds the paper.
const (
	foreignKeyViolation = "23503"
	uniqueViolation     = "23505"
)

// PGSessionMover is SessionMover's Postgres half: it moves a paper's sessions
// inside the caller's transaction, so the paper and its runs arrive together or
// not at all. Satisfied by the job store.
type PGSessionMover interface {
	MovePaperSessionsTx(ctx context.Context, tx pgx.Tx, fromID, toID, key string) error
}

// PGStore is the Postgres implementation of Store, satisfying the same contract
// the in-memory store does.
type PGStore struct {
	pool  *pgxpool.Pool
	clock func() time.Time
	// sessions carries a moved paper's runs across (035). Nil means MovePaper
	// refuses rather than stranding them.
	sessions PGSessionMover
}

// PGOption configures a PGStore.
type PGOption func(*PGStore)

// WithPGSessions wires the store that holds a paper's sessions, so a move takes
// them with it inside the move's own transaction (035). Without it MovePaper
// returns ErrNoSessionMover.
func WithPGSessions(m PGSessionMover) PGOption {
	return func(s *PGStore) { s.sessions = m }
}

// NewPGStore builds a store over a pgx pool.
func NewPGStore(pool *pgxpool.Pool, opts ...PGOption) *PGStore {
	s := &PGStore{pool: pool, clock: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// projectColumns is the column list every read selects, in scan order.
const projectColumns = `id, owner, title, description, visibility, credit_cap, created_at, signed_off_by, signed_off_at, attestation_version,
	signed_off_name, signed_off_institution, signed_off_contact, signed_off_orcid, signed_off_scholar, signed_off_email,
	withdrawn_at, withdrawn_by, withdrawn_reason, deleted_at`

// scanProject reads one row in projectColumns order.
func scanProject(row pgx.Row) (Project, error) {
	var p Project
	err := row.Scan(&p.ID, &p.Owner, &p.Title, &p.Description, &p.Visibility, &p.CreditCap, &p.CreatedAt,
		&p.SignedOffBy, &p.SignedOffAt, &p.AttestationVersion,
		&p.SignedOffName, &p.SignedOffInstitution, &p.SignedOffContact,
		&p.SignedOffORCID, &p.SignedOffScholar, &p.SignedOffEmail,
		&p.WithdrawnAt, &p.WithdrawnBy, &p.WithdrawnReason, &p.DeletedAt)
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

// ListByOwner returns an owner's projects, newest first, excluding the trash.
func (s *PGStore) ListByOwner(ctx context.Context, owner string) ([]Project, error) {
	return s.list(ctx, `WHERE owner = $1 AND deleted_at IS NULL ORDER BY created_at DESC, id DESC`, owner)
}

// ListTrashed returns an owner's trashed projects, newest-trashed first.
func (s *PGStore) ListTrashed(ctx context.Context, owner string) ([]Project, error) {
	return s.list(ctx, `WHERE owner = $1 AND deleted_at IS NOT NULL ORDER BY deleted_at DESC, id DESC`, owner)
}

// Trash moves a project to the trash. The NULL predicate keeps the first
// timestamp, so a repeated call cannot rewrite the moment it left the list —
// which costs one extra read to tell "already trashed" from "not there".
func (s *PGStore) Trash(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE projects SET deleted_at=$2 WHERE id=$1 AND deleted_at IS NULL`, id, s.clock())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.Get(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Restore takes a project back out of the trash.
func (s *PGStore) Restore(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE projects SET deleted_at=NULL WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// list runs one project read with a shared column list and scan.
func (s *PGStore) list(ctx context.Context, where string, args ...any) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectColumns+` FROM projects `+where, args...)
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

// SetDescription writes what a project says it is about, and reports a project
// that is not there — this one is somebody typing, not an enrichment.
func (s *PGStore) SetDescription(ctx context.Context, id, description string) error {
	if len(description) > MaxDescription {
		return ErrDescriptionTooLong
	}
	tag, err := s.pool.Exec(ctx, `UPDATE projects SET description = $2 WHERE id = $1`, id, description)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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

// ListPublic returns every public project, newest first, excluding the trash.
func (s *PGStore) ListPublic(ctx context.Context) ([]Project, error) {
	return s.list(ctx, `WHERE visibility='public' AND deleted_at IS NULL ORDER BY created_at DESC, id DESC`)
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

// ListAll returns every project, oldest first — the platform's own read.
func (s *PGStore) ListAll(ctx context.Context) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY created_at, id`)
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

// paperColumns is the column list every paper read selects, in scan order.
const paperColumns = `paper_key, ref, title, added_by, added_at,
	authors, venue, year, volume, issue, pages, publisher, abstract, edited`

// scanPaper reads one paper row in paperColumns order.
func scanPaper(row interface{ Scan(...any) error }) (Paper, error) { return scanPaperWith(row) }

// scanPaperWith reads one paper row whose select prefixes paperColumns with the
// destinations given — the multi-project read selects the project id first, and
// the column list must stay one string for the two reads to agree.
func scanPaperWith(row interface{ Scan(...any) error }, before ...any) (Paper, error) {
	var p Paper
	err := row.Scan(append(before, &p.Key, &p.Ref, &p.Title, &p.AddedBy, &p.AddedAt,
		&p.Record.Authors, &p.Record.Venue, &p.Record.Year,
		&p.Record.Volume, &p.Record.Issue, &p.Record.Pages, &p.Record.Publisher, &p.Record.Abstract, &p.Edited)...)
	return p, err
}

// AddPaper records a paper the project is about. The primary key is what makes
// a duplicate impossible, so the refusal is read off the insert rather than
// guessed at with a prior SELECT — two adds racing must not both succeed.
func (s *PGStore) AddPaper(ctx context.Context, projectID string, p Paper) error {
	if p.AddedAt.IsZero() {
		p.AddedAt = s.clock()
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO project_papers
			(project_id, paper_key, ref, title, added_by, added_at,
			 authors, venue, year, volume, issue, pages, publisher, abstract)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (project_id, paper_key) DO NOTHING`,
		projectID, p.Key, p.Ref, p.Title, p.AddedBy, p.AddedAt,
		nonNil(p.Record.Authors), p.Record.Venue, p.Record.Year,
		p.Record.Volume, p.Record.Issue, p.Record.Pages, p.Record.Publisher, p.Record.Abstract)
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
		p, err := scanPaper(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PapersByProjects returns the papers of every named project in one query,
// ordered within each project the way ListPapers orders them. A project with no
// papers is simply absent from the map — the same answer, one fewer allocation.
func (s *PGStore) PapersByProjects(ctx context.Context, ids []string) (map[string][]Paper, error) {
	if len(ids) == 0 {
		return map[string][]Paper{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT project_id, `+paperColumns+` FROM project_papers
		WHERE project_id = ANY($1) ORDER BY project_id, added_at, paper_key`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Paper{}
	for rows.Next() {
		var projectID string
		p, err := scanPaperWith(rows, &projectID)
		if err != nil {
			return nil, err
		}
		out[projectID] = append(out[projectID], p)
	}
	return out, rows.Err()
}

// nonNil keeps a nil slice out of a NOT NULL text[] column.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// EnrichPaper fills a paper's empty fields from what a source answered,
// ignoring a missing project or paper.
//
// It reads the row, applies the rule, and writes it back inside one transaction:
// the rule depends on what is already there ("fill only what is empty, and never
// what a person edited"), so a read outside the write's transaction would let a
// correction land between the two and be overwritten by this.
func (s *PGStore) EnrichPaper(ctx context.Context, projectID, key, title string, rec paper.Record) (bool, error) {
	changed := false
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+paperColumns+`
			FROM project_papers WHERE project_id=$1 AND paper_key=$2 FOR UPDATE`, projectID, key)
		p, err := scanPaper(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // best-effort: nothing to enrich
		}
		if err != nil {
			return err
		}
		if !p.Enrich(title, rec) {
			return nil
		}
		changed = true
		_, err = tx.Exec(ctx, `
			UPDATE project_papers SET title=$3, authors=$4, venue=$5, year=$6,
				volume=$7, issue=$8, pages=$9, publisher=$10, abstract=$11
			WHERE project_id=$1 AND paper_key=$2`,
			projectID, key, p.Title, nonNil(p.Record.Authors), p.Record.Venue, p.Record.Year,
			p.Record.Volume, p.Record.Issue, p.Record.Pages, p.Record.Publisher, p.Record.Abstract)
		return err
	})
	return changed, err
}

// EditPaper records a correction and marks the fields it wrote, in one
// transaction for the same reason enrichment is: the edited set is read, added
// to, and written back.
func (s *PGStore) EditPaper(ctx context.Context, projectID, key string, edit PaperEdit) (Paper, error) {
	var out Paper
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `SELECT `+paperColumns+`
			FROM project_papers WHERE project_id=$1 AND paper_key=$2 FOR UPDATE`, projectID, key)
		p, err := scanPaper(row)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPaperNotFound
		}
		if err != nil {
			return err
		}
		edit.Apply(&p)
		out = p
		_, err = tx.Exec(ctx, `
			UPDATE project_papers SET title=$3, authors=$4, venue=$5, year=$6,
				volume=$7, issue=$8, pages=$9, publisher=$10, abstract=$11, edited=$12
			WHERE project_id=$1 AND paper_key=$2`,
			projectID, key, p.Title, nonNil(p.Record.Authors), p.Record.Venue, p.Record.Year,
			p.Record.Volume, p.Record.Issue, p.Record.Pages, p.Record.Publisher, p.Record.Abstract, nonNil(p.Edited))
		return err
	})
	return out, err
}

// RemovePaper drops a paper from a project, idempotently.
func (s *PGStore) RemovePaper(ctx context.Context, projectID, key string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM project_papers WHERE project_id=$1 AND paper_key=$2`, projectID, key)
	return err
}

// MovePaper refiles a paper under another project, and its sessions with it,
// in one transaction (035). The paper row and the job rows live in different
// tables owned by different packages but in the same database, so the seam is
// the transaction: this store opens it and hands it to the mover, exactly as
// the job store hands its own transaction to the credit ledger at settlement.
//
// The UPDATE is the duplicate check. A destination that already holds the key
// breaks the primary key, and reading that off the error is what keeps two
// concurrent moves from both believing they were first — the same reason
// AddPaper does not SELECT before it inserts.
func (s *PGStore) MovePaper(ctx context.Context, fromID, toID, key string) error {
	if s.sessions == nil {
		return ErrNoSessionMover
	}
	// Both projects are read outside the transaction only to tell a missing
	// project from a missing paper; inside it, the UPDATE is still the authority
	// on what actually moved.
	if _, err := s.Get(ctx, fromID); err != nil {
		return err
	}
	if _, err := s.Get(ctx, toID); err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE project_papers SET project_id=$2
			WHERE project_id=$1 AND paper_key=$3`, fromID, toID, key)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return ErrPaperExists
			}
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrPaperNotFound
		}
		return s.sessions.MovePaperSessionsTx(ctx, tx, fromID, toID, key)
	})
}
