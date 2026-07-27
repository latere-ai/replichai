package project

import (
	"context"
	"strings"
	"sync"
	"time"
)

// MemStore is an in-memory Store for tests and for the rest of 006 to build
// against with no database.
type MemStore struct {
	mu       sync.Mutex
	projects map[string]Project
	order    []string // creation order, so ListByOwner is newest-first in reverse
	clock    func() time.Time
}

// MemOption configures a MemStore.
type MemOption func(*MemStore)

// WithClock sets the time source, so tests get deterministic timestamps.
func WithClock(f func() time.Time) MemOption {
	return func(s *MemStore) { s.clock = f }
}

// NewMemStore builds an empty in-memory store.
func NewMemStore(opts ...MemOption) *MemStore {
	s := &MemStore{projects: map[string]Project{}, clock: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Create opens a new private project for an owner.
func (s *MemStore) Create(_ context.Context, owner, title string) (Project, error) {
	if strings.TrimSpace(owner) == "" {
		return Project{}, ErrNoOwner
	}
	id, err := newID()
	if err != nil {
		return Project{}, err
	}
	p := Project{ID: id, Owner: owner, Title: title, Visibility: VisibilityPrivate, CreatedAt: s.clock()}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects[id] = p
	s.order = append(s.order, id)
	return p, nil
}

// Get returns one project by id.
func (s *MemStore) Get(_ context.Context, id string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	return p, nil
}

// ListByOwner returns an owner's projects, newest first.
func (s *MemStore) ListByOwner(_ context.Context, owner string) ([]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Project
	for i := len(s.order) - 1; i >= 0; i-- {
		if p := s.projects[s.order[i]]; p.Owner == owner {
			out = append(out, p)
		}
	}
	return out, nil
}

// SetTitle updates a project's title, ignoring a missing project.
func (s *MemStore) SetTitle(_ context.Context, id, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.projects[id]; ok {
		p.Title = title
		s.projects[id] = p
	}
	return nil
}

// SetPublic marks a project public and records the sign-off. One-way.
func (s *MemStore) SetPublic(_ context.Context, id, signedOffBy, attestationVersion string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return ErrNotFound
	}
	now := s.clock()
	p.Visibility = VisibilityPublic
	p.SignedOffBy = signedOffBy
	p.SignedOffAt = &now
	p.AttestationVersion = attestationVersion
	s.projects[id] = p
	return nil
}

// ListPublic returns every public project, newest first.
func (s *MemStore) ListPublic(_ context.Context) ([]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Project
	for i := len(s.order) - 1; i >= 0; i-- {
		if p := s.projects[s.order[i]]; p.Visibility == VisibilityPublic {
			out = append(out, p)
		}
	}
	return out, nil
}

// Delete removes a project by id, idempotently.
func (s *MemStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.projects, id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return nil
}
