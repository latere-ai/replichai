package job

import (
	"context"

	"github.com/latere-ai/replichai/internal/provider"
)

// Store is where jobs live. It is an interface so the HTTP layer (006a) and the
// worker test against an in-memory fake with no database; the Postgres
// implementation lands against this same contract (006b's build sequence).
//
// The queue is the table: a worker claims a submitted job and drives it through
// the phases, writing each transition back. There is no external broker.
type Store interface {
	// Enqueue records a new submission as a submitted job — a session in a
	// project. paperKey is the offline paper identity (006g); when non-empty it
	// keys the double-trigger guard, and a submit for a paper already running
	// (in the project, or anywhere the principal owns) is refused with
	// ErrActiveSession rather than opening a second concurrent run.
	Enqueue(ctx context.Context, principal, ref string, k Kind, project, paperKey string) (Job, error)
	// Get returns one job by id, or ErrNotFound.
	Get(ctx context.Context, id string) (Job, error)
	// ListByPrincipal returns a principal's own jobs, newest first, so a signed-in
	// user sees the projects they submitted. An unknown principal yields no rows,
	// never an error — the ownership scope is the caller's sub, enforced above.
	ListByPrincipal(ctx context.Context, principal string) ([]Job, error)
	// ListByProject returns a project's sessions, newest first — the detail view
	// of one project (006g). An unknown project yields no rows, never an error;
	// the caller's ownership of the project is checked against the project store.
	ListByProject(ctx context.Context, projectID string) ([]Job, error)
	// StatusByProject returns, for one owner, a summary of each of their
	// projects' sessions — the total, how many are still running, and the newest
	// session's phase — so the project list shows status at a glance in one query.
	StatusByProject(ctx context.Context, owner string) (map[string]ProjectStatus, error)
	// Cancel stops a non-terminal session, moving it to failed with a "cancelled"
	// cause, and returns the resulting job. It is idempotent: cancelling an
	// already-terminal session returns it unchanged. It never routes through the
	// phase machine — a stop must win from any active phase.
	Cancel(ctx context.Context, id string) (Job, error)
	// Claim atomically takes one submitted job into resolving and returns it, or
	// reports false when none is waiting. Two concurrent claims return disjoint
	// jobs — in Postgres by SELECT … FOR UPDATE SKIP LOCKED, in memory by a lock.
	Claim(ctx context.Context) (Job, bool, error)
	// Advance moves a job to a new phase through the one transition function,
	// applying mutate to set fields (the error on failure, the repo on
	// publishing, the latest status). It refuses a transition the state machine
	// forbids with ErrIllegalTransition.
	Advance(ctx context.Context, id string, to Phase, mutate func(*Job)) error
	// Observe records the latest run status on a job without changing its phase,
	// so a reader who connects mid-run sees where it is. Progress is not a
	// transition.
	Observe(ctx context.Context, id string, status provider.RunStatus) error
	// Active returns the jobs that are not terminal, so a restart can find work
	// a crash left mid-flight and reap it.
	Active(ctx context.Context) ([]Job, error)
}
