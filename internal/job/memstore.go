package job

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/provider"
)

// MemStore is an in-memory Store for tests and for the rest of 006 to build
// against with no database. It copies on every boundary, so a caller cannot
// mutate stored state through a value it holds.
type MemStore struct {
	mu    sync.Mutex
	jobs  map[string]Job
	order []string // submission order, so Claim is FIFO
	clock func() time.Time
}

// MemOption configures a MemStore.
type MemOption func(*MemStore)

// WithClock sets the time source, so tests get deterministic timestamps.
func WithClock(f func() time.Time) MemOption {
	return func(s *MemStore) { s.clock = f }
}

// NewMemStore builds an empty in-memory store.
func NewMemStore(opts ...MemOption) *MemStore {
	s := &MemStore{jobs: map[string]Job{}, clock: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Enqueue records a new submitted job, refusing a paper already running under
// the double-trigger guard.
func (s *MemStore) Enqueue(_ context.Context, principal, ref string, k Kind, project, paperKey string) (Job, error) {
	if strings.TrimSpace(ref) == "" {
		return Job{}, ErrEmptyRef
	}
	id, err := newID()
	if err != nil {
		return Job{}, err
	}
	now := s.clock()
	j := Job{
		ID: id, Kind: k, Principal: principal, ProjectID: project, Ref: ref, PaperKey: paperKey,
		Phase: PhaseSubmitted, CreatedAt: now, UpdatedAt: now,
		PhaseAt: map[Phase]time.Time{PhaseSubmitted: now},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// The guard: at most one non-terminal session per paper, per project and per
	// principal. An empty key (an experiment, or a paper that could not be keyed)
	// never guards. This mirrors the two partial unique indexes Postgres enforces,
	// so the in-memory and database stores refuse the same double-fire.
	if paperKey != "" {
		for _, other := range s.jobs {
			if other.PaperKey != paperKey || other.Phase.Terminal() {
				continue
			}
			if other.ProjectID == project || other.Principal == principal {
				return Job{}, ErrActiveSession
			}
		}
	}
	s.jobs[id] = j
	s.order = append(s.order, id)
	return cloneJob(j), nil
}

// Get returns a copy of one job.
func (s *MemStore) Get(_ context.Context, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	return cloneJob(j), nil
}

// ListByPrincipal returns a principal's jobs, newest first.
func (s *MemStore) ListByPrincipal(_ context.Context, principal string) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Job
	// s.order is oldest-first; walk it in reverse for newest-first.
	for i := len(s.order) - 1; i >= 0; i-- {
		if j := s.jobs[s.order[i]]; j.Principal == principal {
			out = append(out, cloneJob(j))
		}
	}
	return out, nil
}

// Claim takes the oldest submitted job into resolving and returns it. The lock
// makes the take atomic, so two concurrent claims never hand out the same job.
func (s *MemStore) Claim(_ context.Context) (Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.order {
		j := s.jobs[id]
		if j.Phase != PhaseSubmitted {
			continue
		}
		s.transition(&j, PhaseResolving, nil)
		s.jobs[id] = j
		return cloneJob(j), true, nil
	}
	return Job{}, false, nil
}

// Advance moves a job through the one transition function, refusing anything the
// state machine forbids.
func (s *MemStore) Advance(_ context.Context, id string, to Phase, mutate func(*Job)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if !CanTransition(j.Phase, to) {
		return illegal(j.Phase, to)
	}
	s.transition(&j, to, mutate)
	s.jobs[id] = j
	return nil
}

// Observe records the latest run status without a phase change.
func (s *MemStore) Observe(_ context.Context, id string, status provider.RunStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return ErrNotFound
	}
	j.Status = status
	j.UpdatedAt = s.clock()
	s.jobs[id] = j
	return nil
}

// Active returns copies of every non-terminal job, in submission order.
func (s *MemStore) Active(_ context.Context) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Job
	for _, id := range s.order {
		if j := s.jobs[id]; !j.Phase.Terminal() {
			out = append(out, cloneJob(j))
		}
	}
	return out, nil
}

// transition applies a validated phase change: run the caller's mutation, then
// stamp the new phase and its timestamp. It assumes the transition is legal —
// Advance and Claim check first.
func (s *MemStore) transition(j *Job, to Phase, mutate func(*Job)) {
	if mutate != nil {
		mutate(j)
	}
	now := s.clock()
	j.Phase = to
	j.UpdatedAt = now
	if j.PhaseAt == nil {
		j.PhaseAt = map[Phase]time.Time{}
	} else {
		j.PhaseAt = clonePhaseAt(j.PhaseAt)
	}
	j.PhaseAt[to] = now
}

func cloneJob(j Job) Job {
	j.PhaseAt = clonePhaseAt(j.PhaseAt)
	return j
}

func clonePhaseAt(m map[Phase]time.Time) map[Phase]time.Time {
	if m == nil {
		return nil
	}
	c := make(map[Phase]time.Time, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
