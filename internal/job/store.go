// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"time"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/provider"
)

// Submission is one request to open a session. It is a struct rather than a
// positional parameter list because Principal, Ref, Project and PaperKey are
// all plain strings, and swapping two of them would fail silently.
type Submission struct {
	// Principal is the submitter and the session's owner.
	Principal string
	// Ref is the paper reference or experiment reference being run.
	Ref string
	// Kind is the type of work the job runs: an audit or an experiment.
	Kind Kind
	// Project is the project the session belongs to.
	Project string
	// PaperKey is the offline paper identity (006g). When non-empty it keys the
	// double-trigger guard.
	PaperKey string
	// Tier is the model tier the submitter chose for this session (031). Empty
	// means no tier was chosen and the engine's default applies. It is copied
	// onto the job rather than read here, because the worker that runs the
	// session sees only the row, and this struct does not outlive Enqueue.
	Tier credit.Preference
	// Runtime is the runtime the submitter chose (048), canonical; empty is
	// the deployment's default at the time the worker runs it.
	Runtime string
	// Model is one exact model the submitter chose, provider/id, or empty
	// when they chose a tier.
	Model string
	// Payer is who pays for the session, from the runtime's entry. A
	// person-paid session takes no reserve and is never settled.
	Payer Payer
}

// Import is a session somebody executed on their own machine, submitted as a
// finished record rather than as work to do (045k).
//
// It is a distinct type from Submission, and ImportLocal is a distinct entry
// point from Enqueue, because the difference is structural rather than a flag:
// no reserve is taken, nothing is priced, and no worker ever claims it. A
// boolean on Submission would let a single missed branch charge an operator
// for compute they bought themselves.
type Import struct {
	// Principal is who submits the record and is the session's owner.
	Principal string
	// Project is the project it belongs to.
	Project string
	// Ref is the paper reference.
	Ref string
	// PaperKey is the offline paper identity, which attaches the session to a
	// paper the project already holds.
	PaperKey string
	// Runtime is the managed runtime that executed it.
	Runtime string
	// ExternalID is that runtime's own session id. Together with Runtime it
	// makes a re-push update the same row rather than create a second one.
	ExternalID string
	// Phase is the phase the session ended in. Only a terminal phase may be
	// imported: a run still in progress is not a record, and a non-terminal
	// row would be a job that no worker owns and no lease heartbeats.
	Phase Phase
	// Status is the last observation: model, usage, verdict, times.
	Status provider.RunStatus
	// RepoName is the repository the reproduction was pushed to, which holds
	// its evidence.
	RepoName string
	// Err is the failure cause, when the session failed.
	Err string
	// StartedAt and FinishedAt are when the work happened on the operator's
	// machine, not when the record arrived here.
	StartedAt  time.Time
	FinishedAt time.Time
}

// Store persists jobs. It is an interface so that the HTTP layer (006a) and the
// worker can be tested against an in-memory implementation with no database,
// and the Postgres implementation satisfies the same contract (006b).
//
// The table is the queue: a worker claims a submitted job and drives it through
// the phases, writing each transition back. There is no external broker.
type Store interface {
	// ImportLocal records a session that was executed elsewhere. It never
	// takes a credit reserve and never prices anything, because whoever ran
	// the session bought the compute from the vendor. Re-importing the same
	// (runtime, external id) updates that row rather than creating a second
	// one, so an interrupted upload is resumed by running it again.
	ImportLocal(ctx context.Context, in Import) (Job, error)

	// Enqueue records a new submission as a submitted job, which is a session
	// in a project. A submit for a paper that is already running, in the
	// project or in any project the principal owns, is refused with
	// ErrActiveSession rather than opening a second concurrent run. A submit
	// the project cannot afford to start is refused with ErrNoCredits, and one
	// it can afford holds the start reserve against its pot (011). Both checks
	// run in the writing transaction, so neither is racy and neither can be
	// skipped by a new caller.
	Enqueue(ctx context.Context, s Submission) (Job, error)
	// Get returns one job by id, or ErrNotFound.
	Get(ctx context.Context, id string) (Job, error)
	// ListByPrincipal returns a principal's own jobs, newest first, so a
	// signed-in user sees the projects they submitted. An unknown principal
	// yields no rows and no error. The ownership scope is the caller's sub,
	// which the caller enforces.
	ListByPrincipal(ctx context.Context, principal string) ([]Job, error)
	// ListByProject returns a project's sessions, newest first, for the detail
	// view of one project (006g). An unknown project yields no rows and no
	// error. The caller checks ownership of the project against the project
	// store.
	ListByProject(ctx context.Context, projectID string) ([]Job, error)
	// DeleteByProject removes every session belonging to a project. A permanent
	// project delete must call it before the project row is removed, because
	// jobs.project_id references the project and the foreign key does not
	// cascade (019). It is a hard delete and removes already-deleted sessions
	// with the rest. It is idempotent: a project with no sessions is not an
	// error.
	DeleteByProject(ctx context.Context, projectID string) error
	// Delete removes one session from the app (032). It is a soft delete: the
	// row stays, because ledger_entries.grp points at it and 019 requires that
	// removing something must not remove the record of what it cost. After a
	// delete, Get, ListByProject, ListByPrincipal and StatusByProjects stop
	// returning the session. The filter is applied once in the store rather
	// than by each caller, so no read path can miss it.
	//
	// SpendSince deliberately keeps counting a deleted session. The ledger still
	// holds the charge, and a spend figure that dropped on delete would show
	// the same owner two different totals.
	//
	// Delete is idempotent and unconditional. Whether a session is finished
	// enough to be removed is the caller's decision, in the same way that 019
	// puts the running-session refusal in the handler rather than in the
	// project store.
	Delete(ctx context.Context, id string) error
	// StatusByProjects summarizes the sessions of each named project: the total,
	// how many are still running, the newest session's phase, and when one last
	// changed. A project list shows status from one query.
	//
	// It is scoped by project, not by principal (018). Scoping by the caller
	// counted only the sessions they submitted, so a project a collaborator had
	// run in reported one count to its owner and another to that collaborator,
	// and merging the two lists showed both. A project's sessions belong to the
	// project, whoever started them.
	StatusByProjects(ctx context.Context, ids []string) (map[string]ProjectStatus, error)
	// SpendSince sums the cost of each named project's sessions from since
	// onwards, in micro-USD (021e).
	//
	// The window is on when a session last changed, not when it started: a run
	// that began five weeks ago and settled yesterday spent yesterday's money. A
	// session still running counts what it has spent so far, because the wallet
	// has already committed it.
	SpendSince(ctx context.Context, ids []string, since time.Time) (map[string]int64, error)
	// Cancel stops a non-terminal session, moving it to failed with a "cancelled"
	// cause, and returns the resulting job. It is idempotent: cancelling an
	// already-terminal session returns it unchanged. It does not go through the
	// phase machine, because a stop must succeed from any active phase.
	Cancel(ctx context.Context, id string) (Job, error)
	// Claim atomically moves one submitted job into resolving and returns it, or
	// reports false when none is waiting. Two concurrent claims return disjoint
	// jobs: in Postgres by SELECT ... FOR UPDATE SKIP LOCKED, in memory by a
	// lock.
	//
	// It leases the row to worker in the same statement. The claim already makes
	// this the only process driving the job; the lease records that fact for the
	// other processes, whose startup reconciliation would otherwise adopt a run
	// this one is driving.
	Claim(ctx context.Context, worker string) (Job, bool, error)
	// Adopt leases a job in flight to one driver and reports whether this caller
	// won it. Reconciliation uses it to pick up a run a restart stranded without
	// two replicas picking up the same one.
	//
	// A lease is won when the row is non-terminal and is either unleased, held
	// by this same worker (so a pod that crashed and came back under its own
	// name resumes its own work), or older than ttl, which means its driver
	// stopped reporting that it was alive. A live driver refreshes its lease on
	// every observation, so ttl bounds how long a dead driver keeps its run,
	// not how long a run may take.
	Adopt(ctx context.Context, id, worker string, ttl time.Duration) (bool, error)
	// Touch refreshes the lease worker holds on a job and reports whether it
	// still holds it. It reports that the driver is alive, which is a different
	// fact from Observe's report that the status changed: a run whose model is
	// still thinking reports no new status for minutes at a time, and without a
	// heartbeat a reconciling replica cannot distinguish that silence from a
	// dead process (044c).
	//
	// It never takes a lease. Only a non-terminal row already adopted by this
	// worker is refreshed, and adopted_by is not written, so held=false means
	// the row went terminal or another worker now owns it. In both cases the
	// caller must stop driving rather than race the new owner to the finish.
	Touch(ctx context.Context, id, worker string) (held bool, err error)
	// Advance moves a job to a new phase through the single transition function,
	// applying mutate to set fields such as the error on failure, the repository
	// on publishing, and the latest status. It refuses a transition the state
	// machine forbids with ErrIllegalTransition.
	Advance(ctx context.Context, id string, to Phase, mutate func(*Job)) error
	// Observe records the latest run status on a job without changing its phase,
	// so a reader who connects mid-run sees where it is. Progress is not a
	// transition.
	//
	// It also refreshes an existing lease, but it is not what keeps a lease
	// alive: an observation arrives only when something changed, so a quiet run
	// makes none, and Touch is the heartbeat (044c). A job nobody has leased
	// stays unleased.
	Observe(ctx context.Context, id string, status provider.RunStatus) error
	// Active returns the jobs that are not terminal, so a restart can find work
	// a crash left in flight and reconcile it.
	Active(ctx context.Context) ([]Job, error)
	// Accrued returns what every session still running in a project has cost so
	// far, priced from each session's own snapshotted rates (011). It is
	// project-wide because accrued spend does not reach the ledger until a
	// session settles, so a check that looked only at one session would let N
	// concurrent runs each stay under the overdraft floor and jointly exceed it.
	// This number makes the floor a property of the project.
	Accrued(ctx context.Context, project string) (int64, error)
}
