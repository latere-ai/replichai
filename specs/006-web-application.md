# 006 — The web application

**Status:** draft
**Scope:** turning the audit engine into a single-tenant web service — submit a paper, watch it run, and be granted ownership of the reproduction. This is a design spec; it decides the shape and spawns the tightly-scoped child specs that build it.
**Depends on:** 002 (publishing). Consumes 003 (registry) and 005 (transcript).

## Problem

The engine is a command-line tool: one paper per invocation, output to a local directory, progress on stderr. That serves an operator at a terminal. It does not serve a researcher who wants to drop a paper into a page, walk away, and come back to a repository that is *theirs* — with the run's reasoning, tool calls, and cost there to inspect.

The core was built for this turn. `internal/audit` returns values instead of printing and streams progress through a `WithObserver` callback, "so an HTTP handler can wrap it unchanged" — its own words. So this spec is about everything *around* the engine: accepting a submission, running it asynchronously, streaming its progress, granting ownership of the result, and letting the run be inspected after the fact. The engine itself changes only where a seam is genuinely missing (the transcript, 005).

## What the engine already gives us

| Need | Seam that exists |
|---|---|
| Resolve a URL to a paper identity | `paper.Resolve` (001) |
| Run the audit, get a graded result | `audit.Auditor.Run` → `audit.Result` |
| Live progress | `audit.WithObserver(func(provider.RunStatus))` |
| Token cost | `provider.Usage` on the run |
| Publish to a repository | 002 |
| The finished-reproduction list | 003 (derived from GitHub) |
| The full session trace | 005 (transcript) |

The web layer is new packages and a new `cmd/`, not a rewrite of any of these.

## Decisions

**Single-tenant, one backend credential.** One deployment, one `open-hai` organisation, one `REPLICHAI_API_KEY` runs every audit. This defers the genuinely hard problems — per-user credentials, tenant isolation, billing — to a later product. What single-tenant does *not* mean is single-user: many people sign in and submit; they share one backend and one organisation.

**Two kinds of state, two homes.** This is the load-bearing decision, and it reconciles with 003's "derived, not stored":

| State | Home | Why |
|---|---|---|
| Job lifecycle — a paper submitted, now resolving, now running, its progress and error | **Postgres** | Ephemeral orchestration state that spans requests and outlives the process; GitHub cannot hold it |
| The finished-reproduction registry | **Derived from GitHub** (003) | Already decided; the manifests are the source of truth, and nothing should drift from them |

So the database is for *jobs in flight*, not for reproductions. A completed job's durable record is its published repository, read back through 003. Deleting the job row loses nothing that matters.

**Ownership by invitation, not delegation.** A user is granted their reproduction repository by *invitation*: the backend, holding the organisation's admin token, adds the user's GitHub username as a collaborator (`PUT /repos/{owner}/{repo}/collaborators/{username}`), which for a non-member creates an invitation the user accepts. The consequence is the whole trust model:

- OAuth is used only to **verify identity** — which GitHub account is really this person — never to act on their behalf.
- The service **never holds a user's repository credentials.** Trust flows outward: the organisation invites; the user accepts.
- This is a composable step *after* 002 publishes. 002 stays identity-agnostic (it publishes to the organisation); the web layer adds "invite the submitter" on top, using the repo handle 002 returns.

Rejected: OAuth with `repo` scope so the service creates the repo under the user's account. It would make the service hold write credentials for every user's GitHub, invert the trust, and scatter reproductions across personal accounts instead of the one organisation the registry (003) reads.

**Asynchronous jobs, streamed progress.** An audit runs up to an hour (`audit.DefaultTimeout`), so a submission cannot block a request. Submitting enqueues a job; a worker runs the audit; the observer callback and the transcript events (005) stream to the browser. For a single-tenant, low-volume service the worker is an in-process goroutine pool over a Postgres-backed job table — no external queue to operate. Rejected for now: Redis / a dedicated queue; it is infrastructure the volume does not yet justify, and the job table already gives durability and retry.

**Progress is server-sent, not bidirectional.** Progress is one-way (server → browser), so Server-Sent Events, not WebSocket. Simpler to serve, proxy, and reconnect.

**Deployment is a compose file.** Single-tenant means the entire system is Postgres + the server + one credential, brought up with `docker compose`. This is where the local Postgres first floated early in the project finally earns its place — not as the registry's store (that stayed derived), but as the job orchestrator's.

## The specs this spawns

Each is tightly scoped and gets its own design/implementation pass:

| Child | Scope | Notes |
|---|---|---|
| **006a — HTTP API & progress streaming** | The server: submit, status, SSE event stream, and read endpoints wrapping 003/005 | wraps `internal/audit`, adds no engine logic |
| **006b — Job model & persistence** | The job lifecycle state machine and its Postgres schema; the in-process worker | submitted → resolving → running → publishing → done / failed |
| **006c — Identity & ownership** | GitHub OAuth (identity only) and the collaborator-invitation step | extends 002's publish; no `repo`-scope tokens |
| **006d — PDF ingestion** | Drag-and-drop upload → identity | un-defers 001's PDF-extraction path (text → DOI/arXiv, else content digest) |
| **006e — Frontend** | Submit (URL or drop), live progress, hindsight inspection of transcript and usage | consumes 006a's API |

## Interface (the API surface, sketched)

```
POST   /papers                 { url } | multipart PDF        -> { job_id }
GET    /jobs/{id}                                             -> job status + phase
GET    /jobs/{id}/events        (SSE)                         -> progress + transcript events
POST   /jobs/{id}/claim         { github_login }              -> invitation sent
GET    /reproductions                                         -> registry (003), derived
GET    /reproductions/{id}                                    -> manifest + verdict + rate
GET    /reproductions/{id}/transcript                         -> transcript (005)
GET    /auth/github  ·  GET /auth/github/callback             -> identity only
```

Exact shapes are 006a's to settle; this fixes the surface, not the field names.

## Acceptance criteria

These are invariants the whole must hold, verified as the children land:

1. `internal/audit` and `internal/provider` change only by *additive* seams (the transcript, 005). The web is new packages and a new `cmd/`, never a rewrite of the engine.
2. Job orchestration state lives in Postgres; the finished-reproduction registry is still derived from GitHub — no second source of truth for completed reproductions.
3. The service never stores a user's GitHub credentials. Repository ownership is granted only by invitation, and a test asserts no `repo`-scope token is ever requested or held.
4. A submitted audit runs asynchronously; no HTTP request blocks for the run, and progress is observable while it runs.
5. The whole system comes up locally with `docker compose` and one backend credential — no per-user secrets.
6. Every read surface (registry, a reproduction, its transcript) is the same data the CLI and GitHub already hold, presented — never a divergent copy.

## Out of scope

Multi-tenancy, tenant isolation, per-user backend credentials, and billing — the single-tenant scoping exists precisely to defer these. Making repositories public (002 keeps them private; publishing is a separate human decision). The engine's internals. The choice of frontend framework, which is 006e's to make.
