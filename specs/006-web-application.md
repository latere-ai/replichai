# 006 — The web application

**Status:** draft
**Scope:** turning the audit engine into a single-tenant web service — submit a paper, watch it run, and be granted ownership of the reproduction — open at first only to an invited set of seed users. This is a design spec; it decides the shape and spawns the tightly-scoped child specs that build it.
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

**Single-tenant, one backend credential.** One deployment, one `open-hai` organisation, one `REPLICHAI_API_KEY` runs every audit. This defers the genuinely hard problems — per-user credentials, tenant isolation, billing — to a later product. What single-tenant does *not* mean is single-user: many people sign in and submit; they share one backend and one organisation. It is one shared credential paying for every run, which is the other reason access starts closed — see the gate below.

**Three kinds of state, three homes.** This is the load-bearing decision, and it reconciles with 003's "derived, not stored":

| State | Home | Why |
|---|---|---|
| Job lifecycle — a paper submitted, now resolving, now running, its progress and error | **Postgres** | Ephemeral orchestration state that spans requests and outlives the process; GitHub cannot hold it |
| Access grants and GitHub links — who the platform admin has let in, and which GitHub account each proved | **Postgres** | Durable authorisation the deployment owns; neither auth nor GitHub is asked to model it |
| The finished-reproduction registry | **Derived from GitHub** (003) | Already decided; the manifests are the source of truth, and nothing should drift from them |

So the database holds *jobs in flight* and *who may start one*, never reproductions. A completed job's durable record is its published repository, read back through 003. Deleting the job row loses nothing that matters; deleting a grant row only revokes access.

**Two identity facts, two purposes.** Signing in is latere's job; being invited to a repository is GitHub's. The web layer keeps both and never conflates them:

| Fact | Established by | Used for |
|---|---|---|
| latere principal (`sub`, email) | latere auth OIDC (`latere.ai/x/pkg/oidc`) | Who this person is; the access gate below |
| GitHub login | A separate, one-time **link** step, OAuth-verified | The target of the repository invitation |

The link is stored against the principal, not the session — one row per principal, so a claim months later still knows where to send the invitation. Whether it is a column on the grant row or its own table is 006c/006f's to settle; it is written only by the link callback, never by a request body.

The GitHub login is *proven*, never typed. An earlier sketch had `claim { github_login }` take the name as a parameter; a wrong or hostile value would hand collaborator access on a private reproduction to a stranger, so the claim carries no name — it invites the linked account or fails. The GitHub OAuth here requests identity only; `repo` scope is never requested, at any point, for any user.

**Access by invitation only — a closed beta, fail-closed.** Anyone with a latere account can sign in. Signing in grants *nothing*. The operator surface — submitting papers, watching jobs, claiming reproductions, reading a run — is open only to principals a platform admin has granted access to. An authenticated, ungranted user is a first-class state, not an error: the UI tells them they are not yet a seed user and to contact the latere platform owners.

- Grants live in an `access_grants` table: `email` (primary key, how an admin seeds someone who has never signed in), `principal` (bound on that person's first login, unique thereafter), `granted_by`, `granted_at`.
- Seeding by email is what makes the flow work: an admin invites a colleague who has no latere `sub` yet, and the row binds to the principal when they first arrive. Match on `principal` once bound; fall back to verified email only until then.
- **Fail-closed structurally.** An empty table admits nobody. The gate is a single check the operator routes cannot be mounted without, not a condition sprinkled through handlers — the same shape as 002's redistribution gate.
- Two roles, named: the **platform admin** manages the grant list (auth's `IsSuperadmin`, plus a bootstrap list of admin emails in the deployment env); the **seed user** uses the operator UI. There is no self-serve path between them.
- `AUTH_DEV_BYPASS` must never confer a grant on a non-loopback deployment. The dev bypass is for local work, not a back door into the beta.

Rejected: an env-var list of emails — cheap, but every grant costs a redeploy and leaves no record of who admitted whom. Deferred: delegating the decision to auth's org RBAC (a `replichai:operate` scope, membership of a seed org). It is the better long-run home, but the browser cookie session surfaces `Sub`/`Email`, not scopes or org, so it is a cross-repo project rather than a config choice. The gate is one seam; moving it there later changes one function.

An allowlist keyed on `principal_id` is not the local user table auth's integration guide forbids. It stores no profile — no name, no avatar, no email of record beyond the seed key. Display data still comes from `/userinfo`.

**Ownership by invitation, not delegation.** A user is granted their reproduction repository by *invitation*: the backend, holding the organisation's admin token, adds the user's linked GitHub username as a collaborator (`PUT /repos/{owner}/{repo}/collaborators/{username}`), which for a non-member creates an invitation the user accepts. The consequence is the whole trust model:

- GitHub OAuth is used only to **verify identity** — which GitHub account is really this person — never to act on their behalf.
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
| **006c — Identity & the access gate** | latere OIDC sign-in, the `access_grants` table, the fail-closed gate, and the platform-admin grant endpoints | `latere.ai/x/pkg/oidc`; seeds by email, binds on first login |
| **006d — PDF ingestion** | Drag-and-drop upload → identity | un-defers 001's PDF-extraction path (text → DOI/arXiv, else content digest) |
| **006e — Frontend** | Submit (URL or drop), live progress, hindsight inspection of transcript and usage, and the not-invited screen | consumes 006a's API; all copy in en and zh |
| **006f — GitHub linking & ownership** | The one-time OAuth link that proves a GitHub login, and the collaborator invitation on claim | extends 002's publish; no `repo`-scope tokens |

## Interface (the API surface, sketched)

```
open to any latere account
GET    /login  ·  /callback  ·  /logout  ·  /logout/notify    -> latere OIDC session
GET    /me                                                    -> { principal, granted, github_login? }

behind the access gate (granted seed users only)
POST   /papers                 { url } | multipart PDF        -> { job_id }
GET    /jobs/{id}                                             -> job status + phase
GET    /jobs/{id}/events        (SSE)                         -> progress + transcript events
POST   /jobs/{id}/claim                                       -> invites the linked GitHub account
GET    /reproductions                                         -> registry (003), derived
GET    /reproductions/{id}                                    -> manifest + verdict + rate
GET    /reproductions/{id}/transcript                         -> transcript (005)
GET    /link/github  ·  GET /link/github/callback             -> prove a GitHub login, identity scope only

platform admin only
GET    /admin/access  ·  POST /admin/access { email }  ·  DELETE /admin/access/{email}
```

`GET /me` is deliberately outside the gate: it is how the frontend learns to render the not-invited screen instead of the operator UI. Everything the gate covers returns `403` with a machine-readable `not_invited` reason — including the read surfaces, because 002 keeps reproduction repositories private and a registry listing is not public information.

Exact shapes are 006a's to settle; this fixes the surface, not the field names.

## Acceptance criteria

These are invariants the whole must hold, verified as the children land:

1. `internal/audit` and `internal/provider` change only by *additive* seams (the transcript, 005). The web is new packages and a new `cmd/`, never a rewrite of the engine.
2. Job orchestration and access grants live in Postgres; the finished-reproduction registry is still derived from GitHub — no second source of truth for completed reproductions.
3. The service never stores a user's GitHub credentials. Repository ownership is granted only by invitation to a *linked, OAuth-verified* account — never to a client-supplied name — and a test asserts no `repo`-scope token is ever requested or held.
4. A submitted audit runs asynchronously; no HTTP request blocks for the run, and progress is observable while it runs.
5. The whole system comes up locally with `docker compose` and one backend credential — no per-user secrets.
6. Every read surface (registry, a reproduction, its transcript) is the same data the CLI and GitHub already hold, presented — never a divergent copy.
7. The access gate is fail-closed: with an empty `access_grants` table, a valid latere session reaches no operator route. A test enumerates the gated routes and asserts each answers `not_invited` for an authenticated, ungranted principal — enumerated from the router, so a new route added without a grant check fails the test rather than silently shipping open.
8. A grant seeded by email admits that person on their first sign-in and binds to their principal; revoking the grant locks them out on the next request, with no session left standing.

## Out of scope

Multi-tenancy, tenant isolation, per-user backend credentials, and billing — the single-tenant scoping exists precisely to defer these. Self-serve access: no signup, no waitlist queue, no automated approval — a platform admin grants access out of band, and the not-invited screen says so. Moving authorisation into auth's org RBAC (deferred behind the gate seam, above). Making repositories public (002 keeps them private; publishing is a separate human decision). The engine's internals. The choice of frontend framework, which is 006e's to make.
