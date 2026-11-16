// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	// gate orders the operations that cross into the project store. Enqueue and
	// ImportLocal read a project while holding mu, and the project store reads
	// the sessions while holding its own lock when it publishes, trashes or
	// moves a paper, so each side takes gate before its own lock. Neither side
	// takes it from inside a call the other makes while holding it.
	gate  sync.Mutex
	mu    sync.Mutex
	jobs  map[string]Job
	order []string // submission order, so Claim is FIFO
	// deleted holds the ids of sessions the owner deleted (032). It is a set
	// beside the map rather than a field on Job for the same reason the Postgres
	// column is never selected: a deleted session never leaves the store, so
	// nothing downstream should be able to read a flag saying it did.
	deleted map[string]bool
	// leases records which worker is driving each job and when it last reported
	// that it was alive. It is kept beside the map for the same reason as
	// deleted, and matches the Postgres side, where the lease columns are never
	// selected. A lease is a fact about the process driving a session, not
	// about the session.
	leases map[string]lease
	// cancelAsked holds the ids of sessions a person asked to stop, the memory
	// side of the Postgres cancel_requested_at column.
	cancelAsked map[string]bool
	clock       func() time.Time
	// projects reads a project's visibility and trash state for the check a
	// new session makes. Nil means no project is refused.
	projects ProjectLookup
	// credits and reserve are the enqueue gate (011). Both nil means unmetered,
	// which is the mode for an offline run and every test that is not about
	// money.
	credits *credit.MemStore
	reserve credit.Reserve
	card    credit.CardSnapshot
}

// lease is one job's driver and when it last reported that it was alive.
type lease struct {
	by string
	at time.Time
}

// ProjectLookup reads one project's visibility and whether it is in the trash.
// found is false for a project the lookup does not know, which no check
// refuses.
type ProjectLookup func(ctx context.Context, id string) (visibility string, trashed, found bool, err error)

// WithProjects makes the store refuse a session for a project that is public or
// in the trash, as the Postgres store does by reading the project row.
func WithProjects(f ProjectLookup) MemOption {
	return func(s *MemStore) { s.projects = f }
}

// UseProjects sets the project lookup after construction, for a composition
// that builds the job store before the project store it reads.
func (s *MemStore) UseProjects(f ProjectLookup) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projects = f
}

// Gate is the lock a project store takes around a publish, a trash or a move
// before its own lock, so a session start and any of them are one at a time
// without the two stores' locks being taken in opposite orders. The project
// store finds it through the session mover it is given.
func (s *MemStore) Gate() sync.Locker { return &s.gate }

// checkProject refuses a session for a project that is public or in the trash.
// The caller holds the lock.
func (s *MemStore) checkProject(ctx context.Context, project string) error {
	if s.projects == nil || project == "" {
		return nil
	}
	visibility, trashed, found, err := s.projects(ctx, project)
	if err != nil {
		return fmt.Errorf("read project %s: %w", project, err)
	}
	if !found {
		return nil
	}
	return projectRefusal(visibility, trashed)
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
// nothing, which is the mode for a test about the gate rather than about money.
func WithRates(c credit.CardSnapshot) MemOption {
	return func(s *MemStore) { s.card = c }
}

// settleLocked releases a session's hold and charges what it cost, under the
// same lock that wrote its terminal phase. A session is therefore never
// terminal and unsettled, and the ledger refuses a second settlement, so there
// is no check here.
func (s *MemStore) settleLocked(ctx context.Context, j Job) {
	if s.credits == nil || j.ProjectID == "" || !j.Payer.PaysProject() {
		return
	}
	if _, err := s.credits.Settle(ctx, credit.Pot(j.ProjectID), j.ID, j.cost()); err != nil {
		// A settlement that could not be written must not fail the transition:
		// a session stuck non-terminal is worse than one whose charge has to be
		// reconciled. The hold stays open until the charge is reconciled.
		slog.WarnContext(ctx, "settle session", "session", j.ID, "error", err)
	}
}

// takeReserve refuses a submission the project cannot afford to start, and
// holds the reserve against its pot when it can. The hold stops a burst of
// concurrent submits from each being admitted against the same credits.
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

// NewMemStore builds an empty in-memory store that reads the wall clock and is
// unmetered. Options replace those defaults; see WithClock and WithCredits.
func NewMemStore(opts ...MemOption) *MemStore {
	s := &MemStore{jobs: map[string]Job{}, deleted: map[string]bool{}, leases: map[string]lease{}, clock: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ImportLocal records a session executed on somebody's own machine. It is a
// separate entry point from Enqueue rather than a flag on it because of the
// credit path: Enqueue takes the start reserve inside the write, and a boolean
// asking it not to would let a single missed branch charge an operator for
// compute they bought themselves. This function has no path to takeReserve.
func (s *MemStore) ImportLocal(ctx context.Context, in Import) (Job, error) {
	if strings.TrimSpace(in.Ref) == "" {
		return Job{}, ErrEmptyRef
	}
	if !in.Phase.Terminal() {
		return Job{}, fmt.Errorf("%w: a session in phase %q is not a record", ErrIllegalTransition, in.Phase)
	}
	if strings.TrimSpace(in.Runtime) == "" || strings.TrimSpace(in.ExternalID) == "" {
		return Job{}, fmt.Errorf("%w: an imported session needs a runtime and an id", ErrEmptyRef)
	}
	if strings.TrimSpace(in.PaperKey) == "" {
		return Job{}, ErrNoPaperKey
	}
	now := s.clock()
	s.gate.Lock()
	defer s.gate.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkProject(ctx, in.Project); err != nil {
		return Job{}, err
	}
	// The import is idempotent on (principal, external id): an operator's
	// re-push finds their own row and never another operator's. The record is
	// finished, so a re-push with the same content returns it and any other is
	// refused, which is the rule the Postgres store applies under a row lock.
	for _, id := range s.order {
		existing := s.jobs[id]
		if existing.Origin.OrDefault() != OriginLocal || existing.Principal != in.Principal || existing.ExternalID != in.ExternalID {
			continue
		}
		same, err := sameImport(existing, in)
		if err != nil {
			return Job{}, err
		}
		return cloneJob(same), nil
	}
	id := newID()
	imported := Job{
		ID: id, Kind: KindAudit, Principal: in.Principal, ProjectID: in.Project, Ref: in.Ref,
		PaperKey: in.PaperKey, Origin: OriginLocal, ExternalID: in.ExternalID,
		CreatedAt: now, UpdatedAt: now, PhaseAt: map[Phase]time.Time{},
	}
	applyImport(&imported, in, now)
	s.jobs[id] = imported
	s.order = append(s.order, id)
	return imported.withCost(), nil
}

// applyImport writes what the operator's machine reported onto the row. The
// times are when the work happened on that machine, not when the record
// arrived.
func applyImport(j *Job, in Import, now time.Time) {
	j.Phase, j.Status, j.RepoName, j.Err = in.Phase, in.Status, in.RepoName, in.Err
	j.StopReason = StopReason(importStop(in))
	j.Ref, j.PaperKey, j.ProjectID = in.Ref, in.PaperKey, in.Project
	j.UpdatedAt = now
	if j.PhaseAt == nil {
		j.PhaseAt = map[Phase]time.Time{}
	}
	if !in.StartedAt.IsZero() {
		j.CreatedAt = in.StartedAt
		j.PhaseAt[PhaseSubmitted] = in.StartedAt
	}
	if !in.FinishedAt.IsZero() {
		j.PhaseAt[in.Phase] = in.FinishedAt
	}
}

// Enqueue records a new submitted job. It refuses a paper already running,
// under the double-trigger guard, and a submit the project's pot cannot
// reserve for.
func (s *MemStore) Enqueue(ctx context.Context, sub Submission) (Job, error) {
	if strings.TrimSpace(sub.Ref) == "" {
		return Job{}, ErrEmptyRef
	}
	if needsPaperKey(sub.Kind) && strings.TrimSpace(sub.PaperKey) == "" {
		return Job{}, ErrNoPaperKey
	}
	id := newID()
	now := s.clock()
	j := Job{
		ID: id, Kind: sub.Kind, Principal: sub.Principal, ProjectID: sub.Project, Ref: sub.Ref,
		PaperKey: sub.PaperKey, Tier: sub.Tier, Runtime: sub.Runtime, Model: sub.Model, Payer: sub.Payer,
		Phase: PhaseSubmitted, CreatedAt: now, UpdatedAt: now,
		PhaseAt: map[Phase]time.Time{PhaseSubmitted: now},
	}
	s.gate.Lock()
	defer s.gate.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkProject(ctx, sub.Project); err != nil {
		return Job{}, err
	}
	// The double-trigger guard: at most one non-terminal session per paper, per
	// project and per principal. An empty key, which only an experiment has,
	// is never guarded. This mirrors the two partial unique indexes Postgres
	// enforces, so the in-memory and database stores refuse the same
	// duplicate.
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
	// by a refusal would remain against the project forever.
	// A person-paid session is priced and charged by the platform that runs
	// it, so it snapshots no card and holds no reserve (048).
	if sub.Payer.PaysProject() {
		rates, err := s.snapshot(ctx)
		if err != nil {
			return Job{}, err
		}
		j.Rates = rates
		// The credit gate (011): the project must be able to commit the
		// start reserve, and committing it stops a burst of submits from
		// all reading the same balance.
		if err := s.takeReserve(ctx, sub.Project, id); err != nil {
			return Job{}, err
		}
	}
	s.jobs[id] = j
	s.order = append(s.order, id)
	return cloneJob(j), nil
}

// snapshot reads the price list this session will be charged at.
//
// Two cases differ. No card configured is the deliberate unpriced mode, for an
// offline run or a test about the gate rather than about money, and a session
// there costs nothing. A card that is configured but cannot be read is a
// fault, and falling back to an empty card would make every session free for
// as long as the fault lasted, which is the silent underspend 011 exists to
// prevent. It resolves to the compiled-in list instead.
func (s *MemStore) snapshot(ctx context.Context) (credit.Card, error) {
	return resolveCard(ctx, s.card)
}

// Get returns a copy of one job. A deleted session returns ErrNotFound (032).
// This single check covers every per-session read, because the job, its
// transcript, verdict, artifacts, event stream and cancel all resolve through
// Get.
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

// PaperRunning reports whether one paper has a session in flight in a project.
// It is the in-memory half of the check a paper move makes before it moves the
// sessions.
func (s *MemStore) PaperRunning(_ context.Context, projectID, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.ProjectID == projectID && j.PaperKey == key && j.Phase.Active() {
			return true, nil
		}
	}
	return false, nil
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

// MovePaperSessions moves one paper's sessions to another project (035). It is
// the in-memory half of project.MovePaper. Deleted sessions move too, and
// nothing else on the row changes. See MovePaperSessionsTx on PGStore for why
// updated_at in particular is left unchanged.
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

// Delete marks one session deleted (032). It is idempotent and succeeds on an
// id that was never stored, because the caller asked for the session to be
// absent and it is.
func (s *MemStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted[id] = true
	return nil
}

// SpendSince sums each project's metered session costs from since onwards.
// Cost comes from the same withCost the read paths use, so the tile and the
// row cannot disagree about what a session cost.
//
// Deleted sessions are counted, and this is the only read path where they are
// (032). Their charge is still in the ledger, and dropping it here would show
// an owner one total in the rail and another in their wallet.
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
		// A locally executed session is not counted. The rail shows what this
		// project has cost, and this deployment charged nothing for such a
		// session. This is the opposite of 035's moved session, which is still
		// counted because the ledger still holds its charge.
		if !j.Origin.Metered() {
			continue
		}
		out[j.ProjectID] += j.withCost().Cost
	}
	return out, nil
}

// StatusByProjects summarizes the sessions of each named project: how many
// there are, how many are still running, how many have a repository name,
// which is set once publishing begins, and the newest one's phase.
//
// Deleted sessions are left out, unlike in SpendSince. A tile counting sessions
// reports what is in the project, and a removed session is not in it. A tile
// counting money reports what was charged, and a removed session was still
// charged.
func (s *MemStore) StatusByProjects(_ context.Context, ids []string) (map[string]ProjectStatus, error) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]ProjectStatus{}
	latestAt := map[string]time.Time{}
	// s.order is oldest-first; the last row whose CreatedAt is not earlier than
	// the current newest becomes the newest.
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

// RequestCancel records that a person asked to stop the session, so a failure
// written afterward is recorded as the stop it is.
func (s *MemStore) RequestCancel(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if j.Phase.Terminal() {
		return nil
	}
	if s.cancelAsked == nil {
		s.cancelAsked = map[string]bool{}
	}
	s.cancelAsked[id] = true
	return nil
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
		return cloneJob(j), nil // already done or failed; nothing to stop
	}
	now := s.clock()
	j.Phase = PhaseFailed
	j.Err = CancelCause
	j.StopReason = StopCanceled
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

// Claim moves the oldest submitted job into resolving and returns it. The lock
// makes the claim atomic, so two concurrent claims never return the same job.
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

// Adopt leases a job in flight to one driver, under the store's single lock.
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
// under the store's single lock.
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
	if err := s.checkHolder(ctx, id); err != nil {
		return err
	}
	if err := Phases.Check(j.Phase, to); err != nil {
		return err
	}
	s.transition(&j, to, mutate)
	if to == PhaseFailed && s.cancelAsked[id] {
		j.Err, j.StopReason = CancelCause, StopCanceled
	}
	s.jobs[id] = j
	if to.Terminal() {
		s.settleLocked(ctx, j)
	}
	return nil
}

// checkHolder refuses with ErrLeaseLost when the context names a driver and
// that driver does not hold the session's lease. The caller holds the lock.
func (s *MemStore) checkHolder(ctx context.Context, id string) error {
	worker := workerFrom(ctx)
	if worker == "" {
		return nil
	}
	if held := s.leases[id]; held.by != worker {
		return fmt.Errorf("%w: session %s is held by %q", ErrLeaseLost, id, held.by)
	}
	return nil
}

// Observe records the latest run status without a phase change. It refuses a
// driver that does not hold the session's lease and a session that already
// finished.
func (s *MemStore) Observe(ctx context.Context, id string, status provider.RunStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if j.Phase.Terminal() {
		return fmt.Errorf("%w: session %s is %s", ErrSessionFinished, id, j.Phase)
	}
	if err := s.checkHolder(ctx, id); err != nil {
		return err
	}
	j.Status = status
	j.UpdatedAt = s.clock()
	s.jobs[id] = j
	if held, leased := s.leases[id]; leased {
		s.leases[id] = lease{by: held.by, at: j.UpdatedAt}
	}
	return nil
}

// SetRepo records the repository a session was given. It is refused like an
// observation: for a session that finished or one another driver holds.
func (s *MemStore) SetRepo(ctx context.Context, id, repo string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if j.Phase.Terminal() {
		return fmt.Errorf("%w: session %s is %s", ErrSessionFinished, id, j.Phase)
	}
	if err := s.checkHolder(ctx, id); err != nil {
		return err
	}
	j.RepoName = repo
	j.UpdatedAt = s.clock()
	s.jobs[id] = j
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

// transition applies a validated phase change: it runs the caller's mutation,
// then records the new phase and its timestamp. It assumes the transition is
// legal; Advance and Claim check first.
func (s *MemStore) transition(j *Job, to Phase, mutate func(*Job)) {
	if mutate != nil {
		mutate(j)
	}
	now := s.clock()
	j.Phase = to
	j.UpdatedAt = now
	j.StopReason = stopFor(to, j.StopReason)
	if j.PhaseAt == nil {
		j.PhaseAt = map[Phase]time.Time{}
	} else {
		j.PhaseAt = clonePhaseAt(j.PhaseAt)
	}
	j.PhaseAt[to] = now
}

// cloneJob copies a job on its way out of the store. Every read crosses this
// boundary, so the derived cost is filled in here rather than at each of the
// eight call sites.
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
