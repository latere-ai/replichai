# 006g — Projects and replication sessions

**Status:** draft
**Scope:** the missing entity layer. A **project** becomes a first-class record a user owns — one paper's replication effort — that holds **many replication sessions**, each a durable run of that paper. This replaces the beta shortcut where a project was a bare string and one job *was* the project. A child of [006](006-web-application.md), which already decided "[the project is the tenant](006-web-application.md)".
**Depends on:** [006b](006b-job-model-and-persistence.md) (the job store and worker that a session's run rides on), [006c](006c-identity-and-access.md) (the principal that owns a project). Revises [006e](006e-frontend.md)'s "one project per job" and [007b](007b-experiments-over-instruments.md)'s free-string `ProjectID`.

## Problem

The vision is a researcher who creates a **project** for a paper and, inside it, runs one or more **replication sessions** — trying again, comparing runs, keeping the history. What shipped is a flat list where "project" is an unindexed string on a disposable `job` row, there is no session concept, and nothing stops a double-clicked submit from spawning two concurrent runs of the same paper. The words in the specs — "the project is the tenant … it holds one or more runs" ([006](006-web-application.md)) — were never made concrete: no `projects` table, no `Project` type, no session. Every richer feature (a transcript per session in [006h](006h-session-transcript-viewer.md), collaborators and the public flip in [006f](006f-taking-a-reproduction-out.md)) has no entity to hang on. This spec builds that entity.

## Decisions

**A project is a first-class record: one paper, one owner, many sessions.** `projects` is a real table — `(id, owner_principal, paper_key, paper_slug, title, visibility, created_at)` — not a string threaded through `jobs`. A project *is* one paper's replication effort (its identity is the paper's canonical key from [001](001-paper-identity-and-slugs.md)); the sessions inside it are repeat attempts at that paper. Rejected: a project as a free-text label (today's model) — it makes "my projects" un-listable as projects, gives collaboration and publishing nothing to key on, and lets the same paper fragment across differently-spelled strings.

**A session is the durable run record — the elevated job.** A replication session *is* a `job` row (006b) with a real `project_id` foreign key to `projects`. The one change to 006b's model is durability of meaning: the row is no longer "disposable history" but the session the user came back to see — its phase timeline, its managed-agents run handle, its token usage, its verdict, and the repository it published. The worker, the phase state machine, and `SELECT … FOR UPDATE SKIP LOCKED` are unchanged; this spec adds the parent, the FK, and the guard, not a second execution engine. Rejected: a separate `sessions` table beside `jobs` — it would duplicate the lifecycle and split the run record in two.

**Submitting a paper resolves to a project, then opens a session.** A submit (`POST /papers`, today) first resolves the paper identity (001), then finds or creates *this owner's* project for that paper key, then enqueues a session in it. So a user never hand-manages projects to get started — the project appears the first time they replicate a paper, and a second replication of the same paper lands as a second session in the same project, not a new project. Explicit `GET /projects` and `GET /projects/{id}` list and open them; a project is never created empty (it is born with its first session).

**One project, at most one *active* session — the double-trigger guard.** Multiple replications of a paper are allowed, but not two at once: a partial unique index admits only one non-terminal session per project (`UNIQUE (project_id) WHERE phase NOT IN ('done','failed')`), so an accidental double-submit is refused with a `409` naming the running session, while a submit after the prior session finished starts a fresh one. A short-lived idempotency key on the submit additionally collapses a double-clicked button into one session. Rejected: a global "one run at a time" lock — it needlessly serialises different papers; and relying on the button alone — it does not survive a retry or a second tab.

**Projects and experiments share one project namespace.** [007b](007b-experiments-over-instruments.md)'s `experiment.ProjectID` stops being a free string and becomes the same `projects.id` foreign key. There is exactly one "project" in the system, owned here; audits (sessions) and experiments both reference it. Migrating now — while there are three projects — avoids two divergent project namespaces later.

**The migration turns the CLI backfill into real projects.** The three reproductions produced before the service existed were backfilled ([decision](006b-job-model-and-persistence.md)) as `done` job rows with `project_id` = a slug string. The `projects` migration reads the distinct `(principal, project_id)` pairs already in `jobs`, creates a project row for each with the paper identity recovered from the manifest/registry, and repoints the sessions' FK — so the existing data becomes three one-session projects with no re-run, and the migration's own test case is those three rows.

## Interface (sketch)

```go
type Visibility string // private | public

type Project struct {
    ID        string
    Owner     string     // owning principal (006c)
    PaperKey  string     // canonical identity (001); a project is one paper
    PaperSlug string
    Title     string
    Visibility Visibility // private by default; the public flip is 006f
    CreatedAt time.Time
    // made-public provenance (made_public_by/at) is added by 006f, not here.
}

type ProjectStore interface {
    // FindOrCreate returns this owner's project for a paper, creating it if absent.
    FindOrCreate(ctx, owner string, id paper.Identity) (Project, error)
    Get(ctx, id string) (Project, error)
    ListByOwner(ctx, owner string) ([]Project, error)
    Sessions(ctx, projectID string) ([]job.Job, error) // the project's sessions, newest-first
}

// Enqueue gains the project it belongs to and refuses a second active session.
// job.Store.Enqueue(ctx, principal, ref, kind, projectID) already carries projectID;
// the partial unique index is what makes the guard structural, and Enqueue maps
// its violation to ErrActiveSession (a 409), not a 500.
```

Routes (gated, 006a): `GET /projects` (the owner's projects), `GET /projects/{id}` (a project and its sessions). `POST /papers` keeps its shape but now resolves → find-or-create project → open session, and answers `409 active_session` when one is already running.

## Acceptance criteria

1. A `projects` table exists with a project owned by a principal and keyed on a paper; `GET /projects` lists only the caller's own projects, and `GET /projects/{id}` returns a project with its sessions, refusing a project the caller does not own.
2. Submitting a paper creates the owner's project on first replication and reuses it on a second — asserted by two submits of the same paper landing as two sessions in one project, and two different papers as two projects.
3. The double-trigger guard holds: a submit while a non-terminal session exists for the project is refused `409 active_session` and creates nothing; a submit after the prior session is `done`/`failed` opens a new session. Proven against Postgres so the partial unique index is exercised, not just the in-memory check.
4. A session is a `job` with a `project_id` foreign key to `projects`; the worker, phases, and claim path are unchanged, and a session survives a restart (006b's durability) and remains listed under its project.
5. `experiment.ProjectID` references `projects.id`; a test creates a project, an audit session and an experiment in it, and both resolve to the same project.
6. The migration turns the three backfilled job rows into three one-session projects with the paper identity recovered and the session FK repointed — with no new run and no data loss — verified as the migration's test.

## Out of scope

The transcript viewer over a session's run ([006h](006h-session-transcript-viewer.md)). Collaborators on a project and the public flip with its sign-off ([006f](006f-taking-a-reproduction-out.md)) — this spec makes `visibility` and `owner`, those consume them. The project→session UI ([006e](006e-frontend.md) revision) beyond the list and detail data these routes expose. Per-user credentials and billing (deferred with the rest of multi-tenancy). Renaming the `job` package or table to "session": the domain word is session, the mechanism stays `job`; a package rename is a separate, explicit refactor.
