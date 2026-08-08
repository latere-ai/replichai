package job

import (
	"context"

	"github.com/latere-ai/replichai/internal/provider"
)

// Submission is one request to open a session. It is a struct rather than a
// widening positional signature: Principal, Ref, Project and PaperKey are all
// bare strings, and getting two of them the wrong way round would be silent.
type Submission struct {
	// Principal is who submitted — the session's owner.
	Principal string
	// Ref is the paper reference or experiment reference being run.
	Ref string
	// Kind is what the job runs (an audit or an experiment).
	Kind Kind
	// Project is the project the session belongs to.
	Project string
	// PaperKey is the offline paper identity (006g). When non-empty it keys the
	// double-trigger guard.
	PaperKey string
}

// Store is where jobs live. It is an interface so the HTTP layer (006a) and the
// worker test against an in-memory fake with no database; the Postgres
// implementation lands against this same contract (006b's build sequence).
//
// The queue is the table: a worker claims a submitted job and drives it through
// the phases, writing each transition back. There is no external broker.
type Store interface {
	// Enqueue records a new submission as a submitted job — a session in a
	// project. A submit for a paper already running (in the project, or anywhere
	// the principal owns) is refused with ErrActiveSession rather than opening a
	// second concurrent run; a submit the project cannot afford to start is
	// refused with ErrNoCredits, and one it can afford holds the start reserve
	// against its pot (011). Both checks happen in the writing transaction, so
	// neither is racy and neither can be skipped by a new caller.
	Enqueue(ctx context.Context, s Submission) (Job, error)
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
	// DeleteByProject removes every session belonging to a project — what a
	// permanent project delete has to do before the project row goes, since
	// jobs.project_id references it and the foreign key does not cascade (019).
	// Idempotent: a project with no sessions is not an error.
	DeleteByProject(ctx context.Context, projectID string) error
	// StatusByProjects summarises the sessions of each named project — the total,
	// how many are still running, the newest session's phase, and when one last
	// changed — so a project list shows status at a glance in one query.
	//
	// It is scoped by project, not by principal (018). Scoping by the caller
	// counted only the sessions they submitted, so a project a collaborator had
	// run in reported one count to its owner and another to that collaborator;
	// merging the two lists put those two answers side by side. A project's
	// sessions are the project's, whoever started them.
	StatusByProjects(ctx context.Context, ids []string) (map[string]ProjectStatus, error)
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
	// Accrued is what every session still running in a project has cost so far,
	// priced from each one's own snapshotted rates (011). It is project-wide on
	// purpose: accrued spend does not reach the ledger until a session settles,
	// so a watchdog looking only at the session in front of it would let N
	// concurrent runs each stay under the overdraft floor and jointly sail past
	// it. This is the number that makes the floor a property of the project.
	Accrued(ctx context.Context, project string) (int64, error)
}
