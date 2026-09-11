// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import (
	"cmp"
	"context"
	"slices"
	"sync"
)

// member is one project-membership row.
type member struct {
	Email     string
	Principal string // bound one-way on first login; empty until then
	Role      Role
}

// MemMembership is an in-memory Membership. It is a distinct store from the
// grant store: a role and a tier are different relations, and keeping them
// separate makes it structurally impossible for a membership to admit anybody
// to the platform.
type MemMembership struct {
	mu sync.Mutex
	// members[project][email] = member; bound[project][principal] = email.
	members map[string]map[string]member
	bound   map[string]map[string]string
}

// NewMemMembership builds an empty in-memory membership store.
func NewMemMembership() *MemMembership {
	return &MemMembership{
		members: map[string]map[string]member{},
		bound:   map[string]map[string]string{},
	}
}

// SeedLead creates the first lead of a project with no lead check. It is the
// bootstrap path project creation (007) uses, mirroring SeedAdmin. It seeds by
// email like every other membership and binds the lead's principal at once, so
// an owner is never listed as a pending invitation to their own project.
func (m *MemMembership) SeedLead(_ context.Context, project string, lead Principal) error {
	email := normalizeEmail(lead.Email)
	if project == "" || email == "" {
		return ErrNoEmail
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.put(project, email, RoleLead)
	if lead.Sub == "" {
		return nil
	}
	// Bind, unless this project already holds a row for someone else on this
	// email or another row for this principal, which are the rules Role and
	// BindAll follow.
	row := m.members[project][email]
	if _, held := m.bound[project][lead.Sub]; held || row.Principal != "" {
		return nil
	}
	row.Principal = lead.Sub
	m.members[project][email] = row
	if m.bound[project] == nil {
		m.bound[project] = map[string]string{}
	}
	m.bound[project][lead.Sub] = email
	return nil
}

// Add invites an email into a project. The caller must be the project's lead.
func (m *MemMembership) Add(ctx context.Context, byLead Principal, project, email string, r Role) error {
	if err := m.requireLead(ctx, byLead, project); err != nil {
		return err
	}
	email = normalizeEmail(email)
	if email == "" {
		return ErrNoEmail
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.put(project, email, r)
	return nil
}

// Remove drops a member by email. The caller must be the project's lead.
func (m *MemMembership) Remove(ctx context.Context, byLead Principal, project, email string) error {
	if err := m.requireLead(ctx, byLead, project); err != nil {
		return err
	}
	email = normalizeEmail(email)
	m.mu.Lock()
	defer m.mu.Unlock()
	if row, ok := m.members[project][email]; ok {
		if row.Principal != "" {
			delete(m.bound[project], row.Principal)
		}
		delete(m.members[project], email)
	}
	return nil
}

// Role returns a principal's role in a project, binding a seeded membership on
// first login.
func (m *MemMembership) Role(_ context.Context, p Principal, project string) (Role, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Already bound: resolve by principal, never by email again.
	if email, ok := m.bound[project][p.Sub]; ok {
		return m.members[project][email].Role, true, nil
	}
	// Seed by the IdP-authenticated email, only if not already bound to someone else.
	if p.Email != "" {
		email := normalizeEmail(p.Email)
		if row, ok := m.members[project][email]; ok && row.Principal == "" {
			row.Principal = p.Sub
			m.members[project][email] = row
			if m.bound[project] == nil {
				m.bound[project] = map[string]string{}
			}
			m.bound[project][p.Sub] = email
			return row.Role, true, nil
		}
	}
	return RoleNone, false, nil
}

// List returns a project's members, sorted by email for a stable view.
func (m *MemMembership) List(_ context.Context, project string) ([]Member, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Member, 0, len(m.members[project]))
	for email, row := range m.members[project] {
		out = append(out, Member{Email: email, Role: row.Role, Bound: row.Principal != ""})
	}
	slices.SortFunc(out, func(a, b Member) int { return cmp.Compare(a.Email, b.Email) })
	return out, nil
}

// BindAll binds every unbound membership seeded against this principal's
// verified email. See Membership.BindAll for why it is a login-time operation.
func (m *MemMembership) BindAll(_ context.Context, p Principal) (int, error) {
	email := normalizeEmail(p.Email)
	if p.Sub == "" || email == "" {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bound := 0
	for project, rows := range m.members {
		row, ok := rows[email]
		if !ok || row.Principal != "" {
			continue
		}
		// Never a second row for this principal in one project: an email change
		// at the IdP must not make somebody a member twice.
		if _, held := m.bound[project][p.Sub]; held {
			continue
		}
		row.Principal = p.Sub
		rows[email] = row
		if m.bound[project] == nil {
			m.bound[project] = map[string]string{}
		}
		m.bound[project][p.Sub] = email
		bound++
	}
	return bound, nil
}

// Projects returns the project ids this principal is a bound member of.
func (m *MemMembership) Projects(_ context.Context, principalSub string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for project, byPrincipal := range m.bound {
		if _, ok := byPrincipal[principalSub]; ok {
			out = append(out, project)
		}
	}
	slices.Sort(out)
	return out, nil
}

// put writes a member's role while preserving any binding. Caller holds the lock.
func (m *MemMembership) put(project, email string, r Role) {
	if m.members[project] == nil {
		m.members[project] = map[string]member{}
	}
	row := m.members[project][email]
	row.Email = email
	row.Role = r
	m.members[project][email] = row
}

// requireLead refuses anyone who is not the project's lead.
func (m *MemMembership) requireLead(ctx context.Context, p Principal, project string) error {
	role, ok, err := m.Role(ctx, p, project)
	if err != nil {
		return err
	}
	if !ok || role != RoleLead {
		return ErrNotLead
	}
	return nil
}
