// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package project

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
)

// MemStore is an in-memory Store for tests and for the rest of 006 to build
// against with no database.
type MemStore struct {
	mu       sync.Mutex
	projects map[string]Project
	order    []string // creation order, so ListByOwner is newest-first in reverse
	papers   map[string][]Paper
	clock    func() time.Time
	// sessions moves the runs of a paper that is being moved (035). Nil means
	// MovePaper refuses rather than stranding them.
	sessions SessionMover
}

// MemOption configures a MemStore.
type MemOption func(*MemStore)

// WithSessions sets the store that holds a paper's sessions, so a move takes
// them with it (035). Without it MovePaper returns ErrNoSessionMover.
func WithSessions(m SessionMover) MemOption {
	return func(s *MemStore) { s.sessions = m }
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
	id := newID()
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

// ListByOwner returns an owner's projects, newest first, excluding the trash.
func (s *MemStore) ListByOwner(_ context.Context, owner string) ([]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Project
	for _, v := range slices.Backward(s.order) {
		if p := s.projects[v]; p.Owner == owner && !p.Trashed() {
			out = append(out, p)
		}
	}
	return out, nil
}

// ListTrashed returns an owner's trashed projects, newest-trashed first.
func (s *MemStore) ListTrashed(_ context.Context, owner string) ([]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Project
	for _, id := range s.order {
		if p := s.projects[id]; p.Owner == owner && p.Trashed() {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(a, b Project) int { return b.DeletedAt.Compare(*a.DeletedAt) })
	return out, nil
}

// Trash moves a project to the trash, keeping the first timestamp.
func (s *MemStore) Trash(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return ErrNotFound
	}
	if !p.Trashed() {
		now := s.clock()
		p.DeletedAt = &now
		s.projects[id] = p
	}
	return nil
}

// Restore takes a project back out of the trash.
func (s *MemStore) Restore(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return ErrNotFound
	}
	p.DeletedAt = nil
	s.projects[id] = p
	return nil
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

// SetDescription writes what a project says it is about (038).
func (s *MemStore) SetDescription(_ context.Context, id, description string) error {
	if len(description) > MaxDescription {
		return ErrDescriptionTooLong
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return ErrNotFound
	}
	p.Description = description
	s.projects[id] = p
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
	for _, v := range slices.Backward(s.order) {
		if p := s.projects[v]; p.Visibility == VisibilityPublic && !p.Trashed() {
			out = append(out, p)
		}
	}
	return out, nil
}

// ListAll returns every project, oldest first.
func (s *MemStore) ListAll(_ context.Context) ([]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Project, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.projects[id])
	}
	return out, nil
}

// ModifiedAt returns each named project's creation-or-newest-paper time.
func (s *MemStore) ModifiedAt(_ context.Context, ids []string) (map[string]time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]time.Time, len(ids))
	for _, id := range ids {
		p, ok := s.projects[id]
		if !ok {
			continue
		}
		at := p.CreatedAt
		for _, pp := range s.papers[id] {
			if pp.AddedAt.After(at) {
				at = pp.AddedAt
			}
		}
		out[id] = at
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
	return s.papersOf(projectID), nil
}

// PapersByProjects returns the papers of every named project that exists. An
// id with no papers is left out rather than refused: this is a list's read, and
// one project deleted between two calls must not fail the whole list.
func (s *MemStore) PapersByProjects(_ context.Context, ids []string) (map[string][]Paper, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]Paper, len(ids))
	for _, id := range ids {
		if _, ok := s.projects[id]; !ok {
			continue
		}
		if papers := s.papersOf(id); len(papers) > 0 {
			out[id] = papers
		}
	}
	return out, nil
}

// papersOf is one project's papers, ordered. The caller holds the lock.
func (s *MemStore) papersOf(projectID string) []Paper {
	// A copy: the caller must not be able to edit the store through the slice.
	out := make([]Paper, len(s.papers[projectID]))
	copy(out, s.papers[projectID])
	// Sorted rather than left in insertion order. The two agreed until a paper
	// could arrive from another project (035) carrying an AddedAt older than
	// everything already here. Postgres orders the same way.
	slices.SortStableFunc(out, func(a, b Paper) int {
		if c := a.AddedAt.Compare(b.AddedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.Key, b.Key)
	})
	return out
}

// EnrichPaper fills a paper's empty fields, ignoring a missing project or paper.
func (s *MemStore) EnrichPaper(_ context.Context, projectID, key, title string, rec paper.Record) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.papers[projectID] {
		if p.Key == key {
			changed := p.Enrich(title, rec)
			s.papers[projectID][i] = p
			return changed, nil
		}
	}
	return false, nil
}

// EditPaper records a correction, and refuses one aimed at a paper that is not
// there.
func (s *MemStore) EditPaper(_ context.Context, projectID, key string, edit PaperEdit) (Paper, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.papers[projectID] {
		if p.Key == key {
			edit.Apply(&p)
			s.papers[projectID][i] = p
			return clonePaper(p), nil
		}
	}
	return Paper{}, ErrPaperNotFound
}

// clonePaper copies the slices a caller could otherwise mutate through.
func clonePaper(p Paper) Paper {
	p.Record.Authors = append([]string(nil), p.Record.Authors...)
	p.Edited = append([]string(nil), p.Edited...)
	return p
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

// MovePaper moves a paper to another project and takes its sessions with it.
// The paper moves first and is put back if the sessions do not follow, so a
// failure leaves the state as it started.
//
// It is not atomic and cannot be: this store has no transaction, and the lock
// is released while the session mover runs. A concurrent reader in that window
// would see the paper in its new project with its runs still in the old one.
// That window is unobservable in the uses this store has, tests and a
// no-database run, and Postgres, which production uses, does the whole move in
// one transaction.
func (s *MemStore) MovePaper(ctx context.Context, fromID, toID, key string) error {
	if s.sessions == nil {
		return ErrNoSessionMover
	}
	s.mu.Lock()
	err := s.detachPaper(fromID, toID, key)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := s.sessions.MovePaperSessions(ctx, fromID, toID, key); err != nil {
		// Put the paper back, so a failed move leaves the two projects as they
		// were rather than splitting the paper from its runs.
		s.mu.Lock()
		_ = s.detachPaper(toID, fromID, key)
		s.mu.Unlock()
		return err
	}
	return nil
}

// detachPaper moves one paper row between two projects under the held lock. It
// decides every refusal a move can make, so the reverse move used to undo a
// failed move takes the same path as the move itself.
func (s *MemStore) detachPaper(fromID, toID, key string) error {
	if _, ok := s.projects[fromID]; !ok {
		return ErrNotFound
	}
	if _, ok := s.projects[toID]; !ok {
		return ErrNotFound
	}
	for _, have := range s.papers[toID] {
		if have.Key == key {
			return ErrPaperExists
		}
	}
	for i, p := range s.papers[fromID] {
		if p.Key != key {
			continue
		}
		s.papers[fromID] = append(s.papers[fromID][:i], s.papers[fromID][i+1:]...)
		s.papers[toID] = append(s.papers[toID], p)
		return nil
	}
	return ErrPaperNotFound
}
