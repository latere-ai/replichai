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
	papers   map[string][]Paper
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
	s := &MemStore{projects: map[string]Project{}, papers: map[string][]Paper{}, clock: time.Now}
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

// SetCreditCap overrides this project's overdraft floor; zero restores the
// platform default.
func (s *MemStore) SetCreditCap(_ context.Context, id string, cap int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.projects[id]; ok {
		p.CreditCap = cap
		s.projects[id] = p
	}
	return nil
}

// SetPublic marks a project public and records the sign-off, superseding any
// earlier withdrawal.
func (s *MemStore) SetPublic(_ context.Context, id string, so SignOff) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return ErrNotFound
	}
	now := s.clock()
	p.Visibility = VisibilityPublic
	p.SignedOffBy = so.By
	p.SignedOffAt = &now
	p.AttestationVersion = so.AttestationVersion
	p.SignedOffName = so.Name
	p.SignedOffInstitution = so.Institution
	p.SignedOffContact = so.Contact
	p.SignedOffORCID = so.ORCID
	p.SignedOffScholar = so.Scholar
	p.SignedOffEmail = so.Email
	p.WithdrawnAt = nil
	p.WithdrawnBy = ""
	p.WithdrawnReason = ""
	s.projects[id] = p
	return nil
}

// SetPrivate withdraws a publication, recording the retraction beside the
// sign-off it ends. A project that is already private is left exactly as it is.
func (s *MemStore) SetPrivate(_ context.Context, id string, w Withdrawal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return ErrNotFound
	}
	if p.Visibility != VisibilityPublic {
		return nil
	}
	now := s.clock()
	p.Visibility = VisibilityPrivate
	p.WithdrawnAt = &now
	p.WithdrawnBy = w.By
	p.WithdrawnReason = w.Reason
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

// Delete removes a project by id, and its papers with it, idempotently.
func (s *MemStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.projects, id)
	delete(s.papers, id)
	for i, oid := range s.order {
		if oid == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
	return nil
}

// AddPaper records a paper the project is about, refusing a duplicate key.
func (s *MemStore) AddPaper(_ context.Context, projectID string, p Paper) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[projectID]; !ok {
		return ErrNotFound
	}
	for _, have := range s.papers[projectID] {
		if have.Key == p.Key {
			return ErrPaperExists
		}
	}
	if p.AddedAt.IsZero() {
		p.AddedAt = s.clock()
	}
	s.papers[projectID] = append(s.papers[projectID], p)
	return nil
}

// ListPapers returns a project's papers in the order they were added.
func (s *MemStore) ListPapers(_ context.Context, projectID string) ([]Paper, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[projectID]; !ok {
		return nil, ErrNotFound
	}
	// A copy: the caller must not be able to edit the store through the slice.
	out := make([]Paper, len(s.papers[projectID]))
	copy(out, s.papers[projectID])
	return out, nil
}

// SetPaperTitle fills in a title, ignoring a missing project or paper.
func (s *MemStore) SetPaperTitle(_ context.Context, projectID, key, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.papers[projectID] {
		if p.Key == key {
			s.papers[projectID][i].Title = title
			return nil
		}
	}
	return nil
}

// RemovePaper drops a paper from a project, idempotently.
func (s *MemStore) RemovePaper(_ context.Context, projectID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.papers[projectID] {
		if p.Key == key {
			s.papers[projectID] = append(s.papers[projectID][:i], s.papers[projectID][i+1:]...)
			return nil
		}
	}
	return nil
}
