package access

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGMembership is the Postgres implementation of Membership, satisfying the same
// contract as the in-memory store.
type PGMembership struct {
	pool *pgxpool.Pool
}

// NewPGMembership builds a membership store over a pgx pool.
func NewPGMembership(pool *pgxpool.Pool) *PGMembership {
	return &PGMembership{pool: pool}
}

// SeedLead creates the first lead of a project with no lead check — the
// bootstrap path project creation uses.
func (m *PGMembership) SeedLead(ctx context.Context, project, email string) error {
	email = normalizeEmail(email)
	if project == "" || email == "" {
		return ErrNoEmail
	}
	return m.upsert(ctx, project, email, RoleLead)
}

// Add invites an email into a project. The caller must be the project's lead.
func (m *PGMembership) Add(ctx context.Context, byLead Principal, project, email string, r Role) error {
	if err := m.requireLead(ctx, byLead, project); err != nil {
		return err
	}
	email = normalizeEmail(email)
	if email == "" {
		return ErrNoEmail
	}
	return m.upsert(ctx, project, email, r)
}

// Remove drops a member by email. The caller must be the project's lead.
func (m *PGMembership) Remove(ctx context.Context, byLead Principal, project, email string) error {
	if err := m.requireLead(ctx, byLead, project); err != nil {
		return err
	}
	_, err := m.pool.Exec(ctx,
		`DELETE FROM project_members WHERE project=$1 AND email=$2`, project, normalizeEmail(email))
	return err
}

// Role returns a principal's role in a project, binding a seeded membership on
// first login.
func (m *PGMembership) Role(ctx context.Context, p Principal, project string) (Role, bool, error) {
	var role Role
	var found bool
	err := pgx.BeginFunc(ctx, m.pool, func(tx pgx.Tx) error {
		var r int16
		err := tx.QueryRow(ctx,
			`SELECT role FROM project_members WHERE project=$1 AND principal=$2`, project, p.Sub).Scan(&r)
		if err == nil {
			role, found = Role(r), true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if p.Email == "" {
			return nil
		}
		email := normalizeEmail(p.Email)
		var principal *string
		err = tx.QueryRow(ctx,
			`SELECT role, principal FROM project_members WHERE project=$1 AND email=$2 FOR UPDATE`, project, email).Scan(&r, &principal)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if principal != nil {
			return nil
		}
		if _, err := tx.Exec(ctx,
			`UPDATE project_members SET principal=$1 WHERE project=$2 AND email=$3`, p.Sub, project, email); err != nil {
			return err
		}
		role, found = Role(r), true
		return nil
	})
	return role, found, err
}

// List returns a project's members, sorted by email.
func (m *PGMembership) List(ctx context.Context, project string) ([]Member, error) {
	rows, err := m.pool.Query(ctx,
		`SELECT email, role, principal FROM project_members WHERE project=$1 ORDER BY email`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var mem Member
		var r int16
		var principal *string
		if err := rows.Scan(&mem.Email, &r, &principal); err != nil {
			return nil, err
		}
		mem.Role, mem.Bound = Role(r), principal != nil
		out = append(out, mem)
	}
	return out, rows.Err()
}

// Projects returns the project ids this principal is a bound member of.
func (m *PGMembership) Projects(ctx context.Context, principalSub string) ([]string, error) {
	rows, err := m.pool.Query(ctx,
		`SELECT project FROM project_members WHERE principal=$1 ORDER BY project`, principalSub)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (m *PGMembership) upsert(ctx context.Context, project, email string, r Role) error {
	_, err := m.pool.Exec(ctx, `
		INSERT INTO project_members (project, email, role) VALUES ($1,$2,$3)
		ON CONFLICT (project, email) DO UPDATE SET role=EXCLUDED.role`,
		project, email, int16(r))
	return err
}

func (m *PGMembership) requireLead(ctx context.Context, p Principal, project string) error {
	role, ok, err := m.Role(ctx, p, project)
	if err != nil {
		return err
	}
	if !ok || role != RoleLead {
		return ErrNotLead
	}
	return nil
}
