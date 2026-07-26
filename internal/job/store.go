package job

import "context"

// Store is where jobs live. It is an interface so the HTTP layer (006a) and the
// worker test against an in-memory fake with no database; the Postgres
// implementation lands against this same contract (006b's build sequence).
//
// The queue is the table: a worker claims a submitted job and drives it through
// the phases, writing each transition back. There is no external broker.
type Store interface {
	// Enqueue records a new submission as a submitted job.
	Enqueue(ctx context.Context, principal, ref string, k Kind, project string) (Job, error)
	// Get returns one job by id, or ErrNotFound.
	Get(ctx context.Context, id string) (Job, error)
	// Claim atomically takes one submitted job into resolving and returns it, or
	// reports false when none is waiting. Two concurrent claims return disjoint
	// jobs — in Postgres by SELECT … FOR UPDATE SKIP LOCKED, in memory by a lock.
	Claim(ctx context.Context) (Job, bool, error)
	// Advance moves a job to a new phase through the one transition function,
	// applying mutate to set fields (the error on failure, the repo on
	// publishing, the latest status). It refuses a transition the state machine
	// forbids with ErrIllegalTransition.
	Advance(ctx context.Context, id string, to Phase, mutate func(*Job)) error
}
