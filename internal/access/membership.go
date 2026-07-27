package access

import (
	"context"
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
// separate is what makes "membership admits nobody to the platform" structural
// rather than a promise.
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

// SeedLead creates the first lead of a project with no lead check — the
// bootstrap path project creation (007) uses, mirroring SeedAdmin. It seeds by
// email like every other membership, and matches the Postgres signature.
func (m *MemMembership) SeedLead(_ context.Context, project, email string) error {
	email = normalizeEmail(email)
	if project == "" || email == "" {
		return ErrNoEmail
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.put(project, email, RoleLead)
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
