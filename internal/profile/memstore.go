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
	// A handle is unique across the platform. Changing your own frees the old
	// one; taking somebody else's is refused.
	if p.Handle != "" {
		for principal, other := range s.profiles {
			if other.Handle == p.Handle && principal != p.Principal {
				return Profile{}, ErrHandleTaken
			}
		}
	}
	p.UpdatedAt = s.clock()
	s.profiles[p.Principal] = p
	return p, nil
}

// ByHandle resolves a public profile by handle.
func (s *MemStore) ByHandle(_ context.Context, handle string) (Profile, error) {
	handle = strings.ToLower(strings.TrimSpace(handle))
	if handle == "" {
		return Profile{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.profiles {
		if p.Handle == handle && p.Public {
			return p, nil
		}
	}
	return Profile{}, ErrNotFound
}

// PrincipalHandle returns a principal's handle when their profile is public.
func (s *MemStore) PrincipalHandle(_ context.Context, principal string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.profiles[principal]
	if !ok || !p.Public || p.Handle == "" {
		return "", false, nil
	}
	return p.Handle, true, nil
}
