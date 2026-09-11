// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import (
	"context"
	"errors"
	"fmt"

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
// bootstrap path project creation uses. It binds the lead's principal in the
// same statement, so an owner is never listed as a pending invitation to their
// own project.
func (m *PGMembership) SeedLead(ctx context.Context, project string, lead Principal) error {
	email := normalizeEmail(lead.Email)
	if project == "" || email == "" {
		return ErrNoEmail
	}
	if lead.Sub == "" {
		return m.upsert(ctx, project, email, RoleLead)
	}
	// COALESCE keeps a binding that already exists: seeding is idempotent and
	// re-runs on every membership change, and it must never rebind a row.
	_, err := m.pool.Exec(ctx, `
		INSERT INTO project_members (project, email, role, principal) VALUES ($1,$2,$3,$4)
		ON CONFLICT (project, email) DO UPDATE
		SET role=EXCLUDED.role, principal=COALESCE(project_members.principal, EXCLUDED.principal)`,
		project, email, int16(RoleLead), lead.Sub)
	return err
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
			bound, err := decodeRole(r)
			if err != nil {
				return err
			}
			role, found = bound, true
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
		// Decoded before the UPDATE: binding is one-way, so a row carrying a role
		// this package never wrote must not consume the principal on its way out.
		seeded, err := decodeRole(r)
		if err != nil {
			return err
		}
		switch outcomeOf(principal, p.Sub) {
		case bindForeign:
			return nil
		case bindMine: // bound by this principal's own parallel first request
			role, found = seeded, true
			return nil
		case bindClaim:
		}
		if _, err := tx.Exec(ctx,
			`UPDATE project_members SET principal=$1 WHERE project=$2 AND email=$3`, p.Sub, project, email); err != nil {
			return err
		}
		role, found = seeded, true
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
		role, err := decodeRole(r)
		if err != nil {
			return nil, fmt.Errorf("member %s of %s: %w", mem.Email, project, err)
		}
		mem.Role, mem.Bound = role, principal != nil
		out = append(out, mem)
	}
	return out, rows.Err()
}

// BindAll binds every unbound membership seeded against this principal's
// verified email. See Membership.BindAll for why it is a login-time operation.
//
// One statement, so a concurrent login cannot bind the same row twice: the
// NOT EXISTS clause keeps a principal to one row per project even when their
// address at the identity provider changed between invitations.
func (m *PGMembership) BindAll(ctx context.Context, p Principal) (int, error) {
	email := normalizeEmail(p.Email)
	if p.Sub == "" || email == "" {
		return 0, nil
	}
	tag, err := m.pool.Exec(ctx, `
		UPDATE project_members AS t SET principal=$1
		WHERE t.email=$2 AND t.principal IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM project_members AS o
		    WHERE o.project=t.project AND o.principal=$1
		  )`, p.Sub, email)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
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

// errRoleRange reports a role column holding a value no role of this package
// names. It is an error rather than a silent RoleNone for the same reason a bad
// tier is: the row decides what somebody may touch in a project, so a value
// nobody here could have written is corruption to be reported, not read.
var errRoleRange = errors.New("access: member role out of range")

// decodeRole turns a scanned SMALLINT into a Role, failing closed on anything
// outside the closed set of roles.
//
// The column is SMALLINT with no CHECK, so it can hold whatever an out-of-band
// write left there, and a plain Role(r) conversion would truncate rather than
// refuse: a 258 arrives as 2, which is RoleLead — ownership of the project and
// its membership list.
func decodeRole(r int16) (Role, error) {
	switch r {
	case int16(RoleNone):
		return RoleNone, nil
	case int16(RoleCollaborator):
		return RoleCollaborator, nil
	case int16(RoleLead):
		return RoleLead, nil
	default:
		return RoleNone, fmt.Errorf("%w: %d", errRoleRange, r)
	}
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
