# 006b — Job model and persistence

**Status:** built
**Scope:** the job lifecycle — its states, its Postgres home, and the in-process worker that drives it — so a submission survives the request that made it. A child of [006](006-web-application.md).
**Depends on:** [002](002-publishing-reproductions.md) (publishing is a job phase), [004](004-go-port.md) (the `audit.Auditor` a worker calls). Revised by [007](007-from-reproduction-to-replication.md): a job runs an audit *or* an experiment, and belongs to a project.

## Problem

An audit runs for hours, and the browser that submitted it will be long gone. The submission therefore has to become a durable record that outlives the request, the connection, and a server restart — a *job* — with somewhere to live and something to drive it. This is the one place the project keeps mutable state, and getting its boundary right is what keeps 003's "derived, not stored" registry intact.

## Decisions

**Jobs live in Postgres; reproductions never do.** The database holds work *in flight* — a paper submitted, now resolving, now running — and the deployment's own authorisation trail. It does not hold finished reproductions: those are their published repositories, read back through 003. This split is the load-bearing rule. A job row is disposable; deleting it loses a bit of history, not a reproduction. The registry stays derived, so nothing in Postgres can drift from what the forge actually holds.

**A job is the unit of *execution*, and it runs an audit or an experiment.** [007](007-from-reproduction-to-replication.md) puts a project / instrument / experiment model *above* the job; it does not replace the job. A job gains a `Kind` and a project, and the phases below are identical for both kinds — the same claim path, the same transition function, the same progress observation, a different callee. This spec is written with that in place because 007 landed before it was built; retrofitting a kind onto a live table would have been a migration.

**A job row stays disposable, but an experiment row is not, so they are different tables.** An experiment is a *finding* — the record of what was run against what data, with results nobody else holds a copy of. Losing it loses something real, which is exactly what "a job row is disposable" says a job is not. [007b](007b-experiments-over-instruments.md) owns that table; this one owns the job, and the split keeps the rule above honest instead of quietly widening it.

**One explicit state machine, its transitions the only writer.** A job moves through a fixed set of phases, and only the worker advances it, through one function that persists each transition:

```
submitted → resolving → running → publishing → done
                     ↘         ↘            ↘
                        failed (from any active phase, with the error kept)
```

`resolving` runs `paper.Resolve` (for an experiment, it resolves the instrument at its pinned commit); `running` is the audit or the experiment; `publishing` is 006f (or 002's local path), and an experiment passes straight through it — there is nothing to publish. The phase and its timestamps are what `GET /jobs/{id}` reports and what the SSE stream narrates. A phase is never inferred from side effects; it is a column.

**An in-process worker over the job table — no external queue.** For a single-tenant, low-volume beta, the queue *is* the table: a small pool of goroutines claims `submitted` rows with `SELECT … FOR UPDATE SKIP LOCKED`, runs them, and writes each transition back. Durability and retry come from the row, not from Redis. On restart, a job left `running` by a crash is visible and can be reaped. Rejected: a dedicated broker — it is operational weight the volume does not justify, and it would put job state in a second place.

**Progress is observed into the row, streamed from it.** The worker passes `audit.WithObserver` a callback that records the latest `RunStatus` (phase, usage) onto the job. The SSE endpoint (006a) tails that, so a viewer who connects late still sees where the run is, and a viewer who never connects costs nothing. The run does not depend on anyone watching.

**A user reads back their own jobs — one project, one session.** `Store.ListByPrincipal` returns the caller's jobs newest-first, exposed as the gated `GET /jobs` and rendered as the frontend's "My projects". The scope is the authenticated principal alone; the query filters on `principal`, so a caller never sees another's work, and the list is server truth, not browser-local state. For the beta this treats **one job as one project** — the durable session record of a submission — deferring 007's `(principal, project, role)` membership; a finished job's title, verdict, and forge link come from the join to the published reproduction (003), never stored on the row. Reproductions that predate the service (produced by the `replichai audit` CLI) are backfilled as `done` job rows carrying their manifest's true `created` timestamp and `open-hai/<name>` repo, so provenance is not fabricated as a service run.

**The backend session is what says a run failed — not a clock.** The server waits on a run with no wall-clock deadline (`audit.WithoutTimeout`); only a session the backend reports as failed, a person cancelling it, or a backend that stops answering for several observations in a row moves the job to `failed`. The engine's `DefaultTimeout` (an hour) survives for the CLI, where a human driving one run may want a bound, but on the server it was a second, weaker opinion about an outcome the session already knows: a run that graded at sixty-three minutes was marked `failed` while its agent had already committed the whole reproduction to the mounted repository, and nothing then published the manifest. Because an unbounded wait makes hundreds of observations rather than a hundred, a single failed `Poll` no longer ends the wait either — a consecutive-failure budget does. What still bounds a genuinely hung run is the backend's own session lifetime and the operator's cancel, both of which are real signals rather than a guess.

**Migrations are in the binary.** Schema is versioned SQL applied at startup, so `docker compose up` is the whole install. 006c adds its `access_grants` and link tables against the same foundation.

## Interface (sketch)

```go
type Phase string // submitted | resolving | running | publishing | done | failed
type Kind  string // audit | experiment  (007)

type Job struct {
    ID        string
    Kind      Kind         // what this job runs; the phases are the same either way
    Principal string       // who submitted it (006c)
    ProjectID string       // the project it belongs to (007); empty for a bare audit
    Ref       string       // the paper reference, or the experiment id for Kind=experiment
    Phase     Phase
    Status    provider.RunStatus // latest observation while running
    RepoName  string       // set once publishing begins
    Err       string       // set with the failed phase
    // timestamps per transition
}

type Store interface {
    Enqueue(ctx, principal, ref string, k Kind, project string) (Job, error)
    Get(ctx, id string) (Job, error)
    ListByPrincipal(ctx, principal string) ([]Job, error) // the caller's own jobs, newest-first
    Claim(ctx) (Job, bool, error)          // FOR UPDATE SKIP LOCKED
    Advance(ctx, id string, to Phase, mutate func(*Job)) error
}

// Worker pulls claimed jobs and drives them through the phases — an audit via
// audit.Auditor, an experiment via experiment.Runner (007b). One lifecycle,
// one claim path, one progress observation; only the callee differs.
```

The store is an interface; the HTTP layer (006a) and the worker depend on it, so handlers and lifecycle logic test against an in-memory fake with no database.

## Acceptance criteria

1. A submitted job persists and is readable after the submitting request returns; a simulated restart with a job mid-`running` leaves it visible and reapable, not lost.
2. Every phase change goes through the one transition function and is durable; a test walks a job submitted → done and asserts each phase and timestamp was written.
3. A failure in any active phase moves the job to `failed` with the error recorded, and never to `done`.
4. Two workers do not run the same job: concurrent `Claim` calls hand out disjoint jobs (`SKIP LOCKED`), asserted under contention.
5. The latest `RunStatus` is recorded on the job as the audit observes it, and is available to a reader who connects after the run started.
6. The store interface has an in-memory implementation the rest of 006 tests against, with no Postgres required for a unit test.
7. Both kinds walk the same phases through the same transition function, asserted by driving an audit job and an experiment job through the identical lifecycle; only the callee differs. No second state machine exists.

## Out of scope

The HTTP surface (006a) and who may enqueue (006c) — this spec is the store and the worker. The experiment table and runner ([007b](007b-experiments-over-instruments.md)) and the project table ([006c](006c-identity-and-access.md)): this spec's job row *references* a project and dispatches to a runner, and defines neither. The registry (003), which is derived, not a table here. The publishing mechanics of the `publishing` phase (002 for the local path, 006f for GitHub). Retry policy tuning and dead-letter handling beyond "a crashed job is visible" — a beta reaps by hand.
