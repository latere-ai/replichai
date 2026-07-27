# 006g — Projects and replication sessions

**Status:** implemented
**Scope:** the entity layer. A **project** is a first-class record a user owns — a research container that holds **one or more replication sessions**, each a durable run that targets a paper. The common case is one project, one replication; a project may also gather several. A child of [006](006-web-application.md), which decided "[the project is the tenant](006-web-application.md)".
**Depends on:** [006b](006b-job-model-and-persistence.md) (the job store and worker that a session's run rides on), [006c](006c-identity-and-access.md) (the principal that owns a project). Revises [006e](006e-frontend.md)'s "one project per job" and [007b](007b-experiments-over-instruments.md)'s free-string `ProjectID`.

## Problem

The vision is a researcher who owns a **project** and, inside it, runs one or more **replication sessions** — each targeting a paper — trying again, comparing runs, keeping the history. What shipped is a flat list where "project" is an unindexed string on a disposable `job` row, there is no session concept, and nothing stops a double-clicked submit from spawning two concurrent runs of the same paper. The words in the specs — "the project is the tenant … it holds one or more runs" ([006](006-web-application.md)) — were never made concrete: no `projects` table, no `Project` type, no session. Every richer feature (a transcript per session in [006h](006h-session-transcript-viewer.md), collaborators and the public flip in [006f](006f-taking-a-reproduction-out.md)) has no entity to hang on. This spec builds that entity.

## Decisions

**A project is a first-class record the user owns: a container of sessions.** `projects` is a real table — `(id, owner_principal, title, visibility, created_at)` — not a string threaded through `jobs`. A project is the *research container*; it holds one or more replication sessions and is where collaboration and the public flip attach. The **paper is a property of the session, not the project** — a project usually gathers replications of one paper (and its title defaults to that paper), but the model does not forbid a user grouping several papers' replications under one project. Rejected: a project as a free-text label (today's model) — it makes "my projects" un-listable as projects and gives collaboration and publishing nothing to key on. Rejected: welding paper identity onto the project — it contradicts "one project can hold one or multiple replication workloads" and would fragment a user's work by paper.

**A session is the durable run record — the elevated job — and it carries the paper.** A replication session *is* a `job` row (006b) with a real `project_id` foreign key to `projects`; its `ref` resolves to the paper identity (001) it targets. It is the durable session a user comes back to see — its phase timeline, its managed-agents run handle, its token usage, its verdict, and the repository it published — retained, not garbage-collected. The worker, the phase state machine, and `SELECT … FOR UPDATE SKIP LOCKED` are unchanged; this spec adds the parent, the FK, and the guard, not a second execution engine. Rejected: a separate `sessions` table beside `jobs` — it would duplicate the lifecycle and split the run record in two.

**Submitting a paper opens a session in a project — a new one, or one the user chose.** A submit (`POST /papers {url, project?}`) resolves the paper identity (001), then: with no `project`, creates a fresh project (title defaulted from the paper) and opens its first session; with a `project` the caller owns, adds a session to it. So the simple path is one submit → one project, one replication, with no project management; a user who wants to gather several replications passes an existing project. `GET /projects` and `GET /projects/{id}` list and open them; a project is born with its first session, never empty.

**No two *active* sessions for the same paper — the double-trigger guard.** Deliberate re-replication is allowed; an accidental double-fire is not. Two partial unique indexes over non-terminal, keyed sessions (`WHERE phase NOT IN ('done','failed') AND paper_key <> ''`) make it structural: one on `(project_id, paper_key)`, so two people cannot double-run the same paper in a shared project; one on `(principal, paper_key)`, so one person cannot double-run the same paper across their own projects — which is the case that matters, because the default submit opens a *new* project and a per-project index alone would never see the collision. A double-submit is refused `409 active_session`; a submit after the prior finished starts a fresh session; a *different* paper is never blocked; and an experiment (empty key) never guards. The per-principal index survives a retry, a reload, and a second tab — which a client idempotency key would not. Rejected: one-active-session-per-project — it would wrongly serialise different papers a project deliberately gathers; and a per-project index alone — it leaves the bare-submit path, the one that costs a run twice, unguarded.

**Projects and experiments share one project namespace.** [007b](007b-experiments-over-instruments.md)'s `experiment.ProjectID` stops being a free string and becomes the same `projects.id` foreign key. There is exactly one "project" in the system, owned here; audits (sessions) and experiments both reference it. Migrating now — while there are three projects — avoids two divergent project namespaces later.

**The migration turns the CLI backfill into real projects.** The three reproductions produced before the service existed were backfilled ([decision](006b-job-model-and-persistence.md)) as `done` job rows with `project_id` = a slug string. The `projects` migration reads the distinct `(principal, project_id)` pairs already in `jobs` and creates a project for each, *reusing the slug as the project id* so each session's `project_id` already points at its project — no repoint. The title is the slug (SQL only, no network — the migration must not depend on the forge to boot); the real paper titles are a one-off data fix after deploy. The session→project foreign key is added `NOT VALID`, so any pre-existing unkeyed row is grandfathered while every new session must reference a real project. The existing data becomes three one-session projects with no re-run, and the migration's own test case is those three rows.

## Interface (sketch)

```go
type Visibility string // private | public

type Project struct {
    ID         string
    Owner      string     // owning principal (006c)
    Title      string     // defaults from the first session's paper
    Visibility Visibility // private by default; the public flip is 006f
    CreatedAt  time.Time
    // made-public provenance (signed_off_by/at, attestation_version) is 006f, not here.
}

type ProjectStore interface {
    // Create opens a new project for an owner (title from the first paper).
    Create(ctx, owner, title string) (Project, error)
    Get(ctx, id string) (Project, error)
    ListByOwner(ctx, owner string) ([]Project, error)
    Sessions(ctx, projectID string) ([]job.Job, error) // the project's sessions, newest-first
}

// A session is enqueued into a project; the paper it targets is keyed offline by
// paper.Key(job.Ref) at submit — no network, so a submit never rides arXiv's
// availability, and the worker's Resolve phase does the enriching lookup. Two
// partial unique indexes over non-terminal keyed sessions — (project_id,
// paper_key) and (principal, paper_key) — make the double-trigger guard
// structural, and Enqueue maps their violation to ErrActiveSession (a 409).
```

Routes (gated, 006a): `GET /projects` (the owner's projects, each with a session count), `GET /projects/{id}` (a project and its sessions). `POST /papers {url, project?}` opens a session — creating a project when `project` is absent, adding to the owned one when present — and answers `409 active_session` when an active session for that paper already exists in the project.

## Acceptance criteria

1. A `projects` table exists with a project owned by a principal; `GET /projects` lists only the caller's own projects (with a session count), and `GET /projects/{id}` returns a project with its sessions, refusing a project the caller does not own.
2. Submitting a paper with no `project` creates a fresh project and its first session; submitting with an owned `project` adds a session to it — asserted by a bare submit yielding one project/one session, and a second submit into that project yielding a second session under the same project.
3. The double-trigger guard holds: a submit while a non-terminal session for that paper exists in the project is refused `409 active_session` and creates nothing; a submit after the prior session is `done`/`failed` opens a new session, and a different paper in the same project is never blocked. Proven against Postgres so the partial unique index on `(project_id, paper_key)` is exercised, not just the in-memory check.
4. A session is a `job` with a `project_id` foreign key to `projects`; the worker, phases, and claim path are unchanged, and a session survives a restart (006b's durability) and remains listed under its project.
5. `experiment.ProjectID` references `projects.id`; a test creates a project, an audit session and an experiment in it, and both resolve to the same project.
6. The migration turns the three backfilled job rows into three one-session projects with the paper identity recovered and the session FK repointed — with no new run and no data loss — verified as the migration's test.

## Out of scope

The transcript viewer over a session's run ([006h](006h-session-transcript-viewer.md)). Collaborators on a project and the public flip with its sign-off ([006f](006f-taking-a-reproduction-out.md)) — this spec makes `visibility` and `owner`, those consume them. The project→session UI ([006e](006e-frontend.md) revision) beyond the list and detail data these routes expose. Per-user credentials and billing (deferred with the rest of multi-tenancy). Renaming the `job` package or table to "session": the domain word is session, the mechanism stays `job`; a package rename is a separate, explicit refactor.
