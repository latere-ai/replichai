package profile

import (
	"context"
	"strings"
	"sync"
	"time"
)

// MemStore is an in-memory Store for tests and for a run with no database.
type MemStore struct {
	mu       sync.Mutex
	profiles map[string]Profile
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
	s := &MemStore{profiles: map[string]Profile{}, clock: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Get returns a principal's profile, or the zero profile if they have none.
func (s *MemStore) Get(_ context.Context, principal string) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.profiles[principal]
	if !ok {
		return Profile{Principal: principal}, nil
	}
	return p, nil
}

// Save validates and writes a principal's profile.
func (s *MemStore) Save(_ context.Context, p Profile) (Profile, error) {
	if strings.TrimSpace(p.Principal) == "" {
		return Profile{}, ErrNoPrincipal
	}
	p = p.Normalize()
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p.UpdatedAt = s.clock()
	s.profiles[p.Principal] = p
	return p, nil
}
