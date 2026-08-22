package job

import (
	"context"
	"errors"
	"log"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/provider"
)

// MemStore is an in-memory Store for tests and for the rest of 006 to build
// against with no database. It copies on every boundary, so a caller cannot
// mutate stored state through a value it holds.
type MemStore struct {
	mu    sync.Mutex
	jobs  map[string]Job
	order []string // submission order, so Claim is FIFO
	// deleted holds the ids of sessions the owner threw away (032). It is a set
	// beside the map rather than a field on Job for the same reason the Postgres
	// side is a column nobody selects: a deleted session never leaves the store,
	// so nothing downstream should be able to read a flag saying it did.
	deleted map[string]bool
	// leases records which worker is driving each job and when it last said so
	// — beside the map for the same reason as deleted, and matching the Postgres
	// side, where they are columns no read selects. A lease is a fact about the
	// process driving a session, not about the session.
	leases map[string]lease
	clock  func() time.Time
	// credits and reserve are the enqueue gate (011). Both nil means unmetered,
	// which is what an offline run and every test that is not about money want.
	credits *credit.MemStore
	reserve credit.Reserve
	card    credit.CardSnapshot
}

// lease is one job's driver and when it last proved it was alive.
type lease struct {
	by string
	at time.Time
}

// MemOption configures a MemStore.
type MemOption func(*MemStore)

// WithClock sets the time source, so tests get deterministic timestamps.
func WithClock(f func() time.Time) MemOption {
	return func(s *MemStore) { s.clock = f }
}

// WithCredits meters submissions against the project's credits (011). Without
// it the store is unmetered.
func WithCredits(c *credit.MemStore, r credit.Reserve) MemOption {
	return func(s *MemStore) { s.credits, s.reserve = c, r }
}

// WithRates prices a settled session from the card in force when it started.
// Without it a metered store still holds and releases the reserve but charges
// nothing — what a test about the gate rather than about money wants.
func WithRates(c credit.CardSnapshot) MemOption {
	return func(s *MemStore) { s.card = c }
}

// settleLocked releases a session's hold and charges what it actually cost,
// under the same lock that wrote its terminal phase. A session is never
// terminal-but-unsettled, and settling twice is refused by the ledger rather
// than guarded here.
func (s *MemStore) settleLocked(ctx context.Context, j Job) {
	if s.credits == nil || j.ProjectID == "" {
		return
	}
	if _, err := s.credits.Settle(ctx, credit.Pot(j.ProjectID), j.ID, j.cost()); err != nil {
		// A settlement that could not be written must not fail the transition:
		// a session stuck non-terminal is worse than one whose charge has to be
		// reconciled. The hold stays open until it is.
		log.Printf("settle session %s: %v", j.ID, err)
	}
}

// takeReserve refuses a submission the project cannot afford to start, and
// holds the reserve against its pot when it can. The hold is what stops a burst
// of concurrent submits each being admitted against the same credits.
func (s *MemStore) takeReserve(ctx context.Context, project, jobID string) error {
	if s.credits == nil || s.reserve == nil {
		return nil
	}
	amount, err := s.reserve(ctx)
	if err != nil {
		return err
	}
	if amount <= 0 {
		return nil
	}
	if err := s.credits.Hold(ctx, credit.Pot(project), jobID, amount); err != nil {
		if errors.Is(err, credit.ErrInsufficient) {
			return ErrNoCredits
		}
		return err
	}
	return nil
}

// NewMemStore builds an empty in-memory store.
func NewMemStore(opts ...MemOption) *MemStore {
	s := &MemStore{jobs: map[string]Job{}, deleted: map[string]bool{}, leases: map[string]lease{}, clock: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Enqueue records a new submitted job, refusing a paper already running under
// the double-trigger guard and a submit past the payer's quota.
func (s *MemStore) Enqueue(ctx context.Context, sub Submission) (Job, error) {
	if strings.TrimSpace(sub.Ref) == "" {
		return Job{}, ErrEmptyRef
	}
	id, err := newID()
	if err != nil {
		return Job{}, err
	}
	now := s.clock()
	j := Job{
		ID: id, Kind: sub.Kind, Principal: sub.Principal, ProjectID: sub.Project, Ref: sub.Ref,
		PaperKey: sub.PaperKey, Tier: sub.Tier,
		Phase: PhaseSubmitted, CreatedAt: now, UpdatedAt: now,
		PhaseAt: map[Phase]time.Time{PhaseSubmitted: now},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// The guard: at most one non-terminal session per paper, per project and per
	// principal. An empty key (an experiment, or a paper that could not be keyed)
	// never guards. This mirrors the two partial unique indexes Postgres enforces,
	// so the in-memory and database stores refuse the same double-fire.
	if sub.PaperKey != "" {
		for _, other := range s.jobs {
			if other.PaperKey != sub.PaperKey || other.Phase.Terminal() {
				continue
			}
			if other.ProjectID == sub.Project || other.Principal == sub.Principal {
				return Job{}, ErrActiveSession
			}
		}
	}
	// Pricing is resolved before the reserve is held, not after: the in-memory
	// store has no transaction to roll back, so a hold taken and then abandoned
	// by a refusal would sit against the project forever.
	rates, err := s.snapshot(ctx)
	if err != nil {
		return Job{}, err
	}
	j.Rates = rates
	// The gate (011): the project must be able to commit the start reserve, and
	// committing it is what stops a burst of submits all reading the same
	// balance.
	if err := s.takeReserve(ctx, sub.Project, id); err != nil {
		return Job{}, err
	}
	s.jobs[id] = j
	s.order = append(s.order, id)
	return cloneJob(j), nil
}

// snapshot reads the price list this session will be charged at.
//
// Two states, and the difference matters. No card wired at all is the deliberate
// unpriced mode — an offline run, or a test about the gate rather than about
// money — and a session there costs nothing. A card that is wired but cannot be
// read is a fault, and falling back to nothing would make every session free
// for as long as the fault lasted, which is the silent underspend this whole
// spec exists to close. It resolves to the compiled-in list instead.
func (s *MemStore) snapshot(ctx context.Context) (credit.Card, error) {
	return resolveCard(ctx, s.card)
}

// Get returns a copy of one job. A deleted session is ErrNotFound (032): this
// one gate is what closes the per-session reads — the job, its transcript,
// verdict, artifacts, event stream and cancel all resolve through here.
func (s *MemStore) Get(_ context.Context, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || s.deleted[id] {
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
	for _, v := range slices.Backward(s.order) {
		if s.deleted[v] {
			continue
		}
		if j := s.jobs[v]; j.Principal == principal {
			out = append(out, cloneJob(j))
		}
	}
	return out, nil
}

// ListByProject returns a project's sessions, newest first.
func (s *MemStore) ListByProject(_ context.Context, projectID string) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Job
	for _, v := range slices.Backward(s.order) {
		if s.deleted[v] {
			continue
		}
		if j := s.jobs[v]; j.ProjectID == projectID {
			out = append(out, cloneJob(j))
		}
	}
	return out, nil
}

// DeleteByProject removes every session belonging to a project.
func (s *MemStore) DeleteByProject(_ context.Context, projectID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.order[:0]
	for _, id := range s.order {
		if s.jobs[id].ProjectID == projectID {
			delete(s.jobs, id)
			delete(s.deleted, id)
			continue
		}
		kept = append(kept, id)
	}
	s.order = kept
	return nil
}

// MovePaperSessions repoints one paper's sessions at another project (035), the
// in-memory half of project.MovePaper. Deleted sessions move too, and nothing
// else on the row is touched — see the Postgres mover for why updated_at in
// particular is left where it is.
func (s *MemStore) MovePaperSessions(_ context.Context, fromID, toID, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, j := range s.jobs {
		if j.ProjectID == fromID && j.PaperKey == key {
			j.ProjectID = toID
			s.jobs[id] = j
		}
	}
	return nil
}

// Delete marks one session deleted (032). Idempotent, and a success on an id
// that was never here — removal asks for an absence, and an absence is what the
// caller gets either way.
func (s *MemStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted[id] = true
	return nil
}

// StatusByProjects summarises the sessions of each named project.
// SpendSince sums each project's session costs from `since` onwards. Cost comes
// from the same withCost the read paths use, so the tile and the row can never
// disagree about what a session cost.
//
// Deleted sessions are counted, and that is the one read path where they are
// (032). Their charge is still in the ledger; dropping it here would show an
// owner one total in the rail and another in their wallet.
func (s *MemStore) SpendSince(_ context.Context, ids []string, since time.Time) (map[string]int64, error) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int64{}
	for _, id := range s.order {
		j := s.jobs[id]
		if !want[j.ProjectID] || j.UpdatedAt.Before(since) {
			continue
		}
		out[j.ProjectID] += j.withCost().Cost
	}
	return out, nil
}

func (s *MemStore) StatusByProjects(_ context.Context, ids []string) (map[string]ProjectStatus, error) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]ProjectStatus{}
	latestAt := map[string]time.Time{}
	// s.order is oldest-first; the last non-before wins as the newest.
	for _, id := range s.order {
		j := s.jobs[id]
		if !want[j.ProjectID] || s.deleted[id] {
			continue
		}
		st := out[j.ProjectID]
		st.Total++
		if !j.Phase.Terminal() {
			st.Running++
		}
		if j.RepoName != "" {
			st.Published++
		}
		if at, ok := latestAt[j.ProjectID]; !ok || !j.CreatedAt.Before(at) {
			st.Latest = j.Phase // st already carries the prior latest; update only when newer
			latestAt[j.ProjectID] = j.CreatedAt
		}
		if j.UpdatedAt.After(st.LatestAt) {
			st.LatestAt = j.UpdatedAt
		}
		out[j.ProjectID] = st
	}
	return out, nil
}

// Cancel stops a non-terminal session, idempotently.
func (s *MemStore) Cancel(ctx context.Context, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	if j.Phase.Terminal() {
		return cloneJob(j), nil // already done or failed — nothing to stop
	}
	now := s.clock()
	j.Phase = PhaseFailed
	j.Err = CancelCause
	j.UpdatedAt = now
	j.PhaseAt = clonePhaseAt(j.PhaseAt)
	if j.PhaseAt == nil {
		j.PhaseAt = map[Phase]time.Time{}
	}
	j.PhaseAt[PhaseFailed] = now
	s.jobs[id] = j
	s.settleLocked(ctx, j)
	return cloneJob(j), nil
}

// Claim takes the oldest submitted job into resolving and returns it. The lock
// makes the take atomic, so two concurrent claims never hand out the same job.
func (s *MemStore) Claim(_ context.Context, worker string) (Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.order {
		j := s.jobs[id]
		if j.Phase != PhaseSubmitted {
			continue
		}
		s.transition(&j, PhaseResolving, nil)
		s.jobs[id] = j
		s.leases[id] = lease{by: worker, at: s.clock()}
		return cloneJob(j), true, nil
	}
	return Job{}, false, nil
}

// Adopt leases a job in flight to one driver, under the lock that is this
// store's whole concurrency story.
func (s *MemStore) Adopt(_ context.Context, id, worker string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j.Phase.Terminal() {
		return false, nil
	}
	now := s.clock()
	held, leased := s.leases[id]
	if leased && held.by != worker && now.Sub(held.at) < ttl {
		return false, nil
	}
	s.leases[id] = lease{by: worker, at: now}
	return true, nil
}

// Touch refreshes this worker's lease and reports whether it still holds it,
// under the lock that is this store's whole concurrency story.
func (s *MemStore) Touch(_ context.Context, id, worker string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok || j.Phase.Terminal() {
		return false, nil
	}
	held, leased := s.leases[id]
	if !leased || held.by != worker {
		return false, nil
	}
	s.leases[id] = lease{by: worker, at: s.clock()}
	return true, nil
}

// Advance moves a job through the one transition function, refusing anything the
// state machine forbids.
func (s *MemStore) Advance(ctx context.Context, id string, to Phase, mutate func(*Job)) error {
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
	if to.Terminal() {
		s.settleLocked(ctx, j)
	}
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
	if held, leased := s.leases[id]; leased {
		s.leases[id] = lease{by: held.by, at: j.UpdatedAt}
	}
	return nil
}

// Accrued sums what every in-flight session in a project has cost so far.
func (s *MemStore) Accrued(_ context.Context, project string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, j := range s.jobs {
		if j.ProjectID == project && !j.Phase.Terminal() {
			n += j.cost()
		}
	}
	return n, nil
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

// cloneJob copies a job on its way out of the store — the one boundary every
// read crosses, which is why the derived cost is filled in here rather than at
// each of the eight call sites.
func cloneJob(j Job) Job {
	j = j.withCost()
	j.PhaseAt = clonePhaseAt(j.PhaseAt)
	return j
}

func clonePhaseAt(m map[Phase]time.Time) map[Phase]time.Time {
	if m == nil {
		return nil
	}
	c := make(map[Phase]time.Time, len(m))
	maps.Copy(c, m)
	return c
}
