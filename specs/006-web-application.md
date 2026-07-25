# 006 — The web application

**Status:** draft
**Scope:** turning the audit engine into a web application where each user owns private research **projects** — submit a paper, watch it run, iterate the project, and, when the owner is ready, publish it to a public zone. Open at first only to an invited set of seed users. This is a design spec; it decides the shape and spawns the tightly-scoped child specs that build it.
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
| The finished-reproduction list | 003 (derived from the forge) |
| The full session trace | 005 (transcript) |

The web layer is new packages and a new `cmd/`, not a rewrite of any of these.

## Decisions

**The project is the tenant.** There is no organisation in the product — only individual users, each owning private research **projects**. A project is one replication effort; it holds one or more runs and their artifacts (the reproduction, later the 007 experiments), and the user iterates it and manages its lifecycle. Private by default; the *owner* decides when it goes public. Collaboration is per-project invitation, not an org. This is the ownership model, and it is the app's — not the forge's, not the IDP's.

Three roles fall out, all project-scoped except the first: **platform admin** (oversees everything, controls the invite-only login gate), **project admin** (the owning user), **project contributor** (invited into a private project).

**One backend credential.** One deployment, one `REPLICHAI_API_KEY` runs every audit — one shared credential paying for every run, which is the other reason access starts closed (see the gate below). Per-user credentials and billing are deferred.

**Storage: the forge for the MVP, a `Store` seam for later.** The proven GitHub workflow (006f) is the MVP's artifact store: each project's runs are repositories under `open-hai`, private by default. The app serves their file tree and downloads by *pass-through* — read-only over the forge API — so a user who never connects a GitHub account still browses and downloads artifacts (OSF-style) in the UI, and a project going public simply lists it in the public zone. A user who *does* connect their GitHub account additionally gets real repository operation (collaborator access, external clone/push, and an optional public flip of the repository itself). Repository access, file reads, and visibility go behind a single `Store` interface — one GitHub implementation now — so **integrating [drive](https://drive.latere.ai) later** (its S3-backed git hosting, sharing, and public zone, with GitHub demoted to an optional mirror) is a second implementation, not a rewrite. That integration is deferred; see [008](008-drive-data-plane.md).

**State homes.** Reconciling with 003's "derived, not stored":

| State | Home | Why |
|---|---|---|
| Projects, membership, visibility, and lifecycle — the app's ownership model | **Postgres** | The product's own model, which neither the forge nor the IDP holds |
| Job lifecycle — a paper submitted, now resolving, now running, its progress and error | **Postgres** | Ephemeral orchestration state that spans requests and outlives the process |
| Access grants, linked forge accounts, and who made what public — authorisation and audit trail | **Postgres** | Durable state the deployment owns |
| Reproduction artifacts | **The `Store`** (forge for the MVP) | Repositories with agent-authored history; the app reads them through the seam |
| The finished-reproduction registry | **Derived from the store** (003) | The manifests are the source of truth; nothing should drift from them |

So Postgres holds the ownership model, jobs in flight, and who may start one — never the artifacts, and never a second copy of the registry.

**The identity provider is an OIDC provider and nothing more.** latere auth is where our users already have accounts, so it is the deployment's IDP — but the coupling stops at the standard. What we consume is one ID token payload:

| Claim | Use |
|---|---|
| `sub` | The stable principal. Every row we own keys on it. |
| `email` + `email_verified` | Seeding a grant for someone who has not signed in yet, and nothing else |
| name / picture, from `/userinfo` | Display only, never stored |

Nothing else. No IDP-issued roles, scopes, org membership, superadmin flag, delegation, or billing claim reaches an authorisation decision here — those belong to the identity product's model, and reading them would weld this one to it. **Authentication is portable; authorisation is ours.** The contract is the standard and only the standard: discover the issuer, run a public PKCE login, verify the ID token, keep `sub` and the verified email, discard the rest. Which library implements that is 006c's to pick — a conformant one, not a vendor SDK whose value is the claims we have just refused. Swapping the issuer is then a config change.

Rejected: gating on an IDP-issued `replichai:operate` scope or membership of a seed org. It looks like less code, but it puts our product's access policy in another service's schema and admin UI, and makes the beta undeployable against any other issuer.

**GitHub is optional, and contained behind one seam.** The product is about reproductions. GitHub is where they happen to be hosted — a *forge*, the word 002 already chose — and it stays that way in the code and in the UI:

- **In the code**, the web layer reaches GitHub only through 002's forge adapter, plus one account-link adapter for the OAuth handshake. Nothing else imports a GitHub client. The job model, the access gate, the registry reader, and the HTTP handlers speak `Repo`, *account*, and *visibility*; only the adapter knows those mean collaborator invitations and `PATCH /repos/{owner}/{repo}`.
- The two verbs this spec needs — `InviteCollaborator(ctx, repo, account)` and `SetVisibility(ctx, repo, public)` — do **not** go on `publish.Forge`. That interface is the publishing seam: `Exists`, `Create`, `Push`, all called inside one publish. These are user-driven actions taken days later, and 003 already wants a read side of its own. They land on their own small interface (`publish.Granter`, say) that the same GitHub adapter satisfies, so the filesystem forge is not made to stub verbs it has no meaning for. One adapter, several narrow interfaces — the shape `provider.Provider` and `Classify` already set here.
- **In the UI**, GitHub appears on exactly one screen: the finished reproduction's *take it out* panel, for a user who wants to own it. It is absent from sign-in, from submitting, from watching a run, and from reading a result. A user who never wants a repository never sees the word.
- Consequently `/me` reports *linked accounts*, not a `github_login`, and the link routes are `/{forge}`-parameterised. GitHub is the only implementation today; it is not the only shape the surface admits.

A user with no forge account runs an audit end to end, watches it, and reads its result, verdict, and transcript in the web application. What they cannot do without linking is receive the repository as a collaborator, because that is a forge-side grant and the forge needs an account to grant it to. Making the reproduction *public* needs no account at all — the organisation already owns the repository, and the flip is the organisation's to make (see the trusted tier below).

The link is stored against the principal, not the session — one row per principal and forge, so a claim months later still knows where to send the invitation. It is written only by the link callback, never by a request body: the account is *proven*, never typed. An earlier sketch had `claim { github_login }` take the name as a parameter, and a wrong or hostile value would have handed collaborator access on a private reproduction to a stranger; the claim now carries no name — it invites the linked account or fails. The link's OAuth requests identity only; `repo` scope is never requested, at any point, for any user.

**Access by invitation only — a closed beta, fail-closed.** Anyone the issuer authenticates can sign in. Signing in grants *nothing*. The operator surface — submitting papers, watching jobs, claiming reproductions, reading a run — is open only to principals a platform admin has granted access to. An authenticated, ungranted user is a first-class state, not an error: the UI tells them they are not yet a seed user and to contact the latere platform owners.

- Grants live in an `access_grants` table: `email` (primary key, how an admin seeds someone who has never signed in), `principal` (bound on that person's first login, unique thereafter), `tier`, `granted_by`, `granted_at`.
- Seeding by email is what makes the flow work: an admin invites a colleague who has no account with the issuer yet, and the row binds to the principal when they first arrive. Match on an `email_verified` claim, never a self-asserted address, and only until the row is bound. **Binding is one-way**: once `principal` is set, email is not consulted again for that row, and a second principal presenting the same email is refused rather than admitted onto someone else's grant.
- **Fail-closed structurally.** An empty table admits nobody. The gate is a single check the operator routes cannot be mounted without, not a condition sprinkled through handlers — the same shape as 002's redistribution gate.
- No development shortcut may confer a grant. A stub issuer for local work authenticates a synthetic principal; it never seeds a row, and a deployment reachable off loopback refuses to start with one configured.

Rejected: an env-var list of emails — cheap, but every grant costs a redeploy and leaves no record of who admitted whom.

The grant table is an ACL, not a user table: it stores a principal, an email used once as a seed key, and a tier. No name, no avatar, no profile. Display data is read from `/userinfo` per request and never persisted.

> **Superseded by the project-as-tenant decision above, and to be reworked when 006c/006f are built.** The sections below describe an earlier model where a platform *trusted tier* could make *any* reproduction public. Under the revised model, publishing is the **project owner's** action on **their own** project: the roles are platform admin / project admin (owner) / project contributor, and the public flip is authorised by project ownership, not a cross-project tier. The redistribution-gate guarantees below still hold; only *who* may flip changes. The prose is kept for its gate-and-audit reasoning until those child specs are rewritten.

**Three roles, and only one of them can make something public.** The `tier` on a grant is the whole authorisation model; there is no self-serve path up it.

| Role | Who sets it | May |
|---|---|---|
| **Platform admin** | A bootstrap list of admin emails in the deployment env, seeded into the table at startup; admins may promote others | Grant, revoke, and promote — and everything below |
| **Trusted user** | A platform admin, explicitly | Everything below, **and** publish a reproduction publicly |
| **Seed user** | A platform admin | Submit papers, watch runs, read results and transcripts, claim ownership if they have linked a forge account |

Everyone else — signed in, ungranted — sees the not-invited screen.

**Making a reproduction public is a trusted action, and it is one-way.** 002 publishes to the organisation privately and leaves the decision to go public to a human; this is the surface for that decision, and it needs no forge account on the user's side. The backend calls `SetVisibility` with the organisation's own credential. The constraints:

- **Trusted tier only.** A seed user sees the control and is told it needs trusted access; the server refuses regardless of what the UI shows.
- **002's gate is the reason this is safe, and it is not bypassable here.** Only worker-written files were ever pushed, so going public redistributes nothing third-party by construction. The flip therefore refuses any repository it cannot account for as its own gated publication — no `replication.json`, an unknown schema, or a manifest whose `redistribution` block is missing means no flip.
- **Treated as irreversible.** Public content is forked, cached, and crawled within minutes; flipping back is not an undo. It requires an explicit confirmation and records `made_public_by` and `made_public_at` in Postgres alongside the grants — audit state the deployment owns, and the one fact neither the manifest nor the forge records, since the forge knows a repository is public but not which of our principals decided it.
- **Trust, not ownership, is the check.** Any trusted user may publish any reproduction in the organisation, including one they did not submit. That is deliberate for a small closed beta of colleagues who can already read every reproduction, and the recorded decider is what makes it accountable. Scoping the flip to the submitter is a change to make when the population outgrows that assumption.
- The registry (003) still derives from the forge, so a newly public reproduction needs no separate index entry — it is the same repository, differently visible.

**Ownership by invitation, not delegation.** A user is granted their reproduction repository by *invitation*: the backend, holding the organisation's credential, calls `InviteCollaborator` with the linked account, which for a non-member creates an invitation the user accepts. The consequence is the whole trust model:

- The link's OAuth is used only to **verify identity** — which account is really this person — never to act on their behalf.
- The service **never holds a user's repository credentials.** Trust flows outward: the organisation invites; the user accepts.
- This is a composable step *after* 002 publishes. 002 stays identity-agnostic (it publishes to the organisation); the web layer adds "invite the submitter" on top, using the `publish.Repo` handle 002 returns.

Rejected: OAuth with write scope so the service creates the repository under the user's own account. It would make the service hold write credentials for every user's forge, invert the trust, and scatter reproductions across personal accounts instead of the one organisation the registry (003) reads.

**Asynchronous jobs, streamed progress.** An audit runs up to an hour (`audit.DefaultTimeout`), so a submission cannot block a request. Submitting enqueues a job; a worker runs the audit; the observer callback and the transcript events (005) stream to the browser. For a single-tenant, low-volume service the worker is an in-process goroutine pool over a Postgres-backed job table — no external queue to operate. Rejected for now: Redis / a dedicated queue; it is infrastructure the volume does not yet justify, and the job table already gives durability and retry.

**Progress is server-sent, not bidirectional.** Progress is one-way (server → browser), so Server-Sent Events, not WebSocket. Simpler to serve, proxy, and reconnect.

**Deployment is a compose file.** Single-tenant means the entire system is Postgres + the server + one credential, brought up with `docker compose`. This is where the local Postgres first floated early in the project finally earns its place — not as the registry's store (that stayed derived), but as the job orchestrator's.

## The specs this spawns

Each is tightly scoped and gets its own design/implementation pass:

| Child | Scope | Notes |
|---|---|---|
| **006a — HTTP API & progress streaming** | The server: submit, status, SSE event stream, and read endpoints wrapping 003/005 | wraps `internal/audit`, adds no engine logic; exposes its route set so 006c's gate test can enumerate it |
| **006b — Job model & persistence** | The job lifecycle state machine and its Postgres schema; the in-process worker | submitted → resolving → running → publishing → done / failed |
| **006c — Identity, tiers & the access gate** | Standard-OIDC sign-in, the `access_grants` table with its tier, the fail-closed gate, and the platform-admin grant endpoints | consumes `sub` + verified email only; seeds by email, binds on first login |
| **006d — PDF ingestion** | Drag-and-drop upload → identity | un-defers 001's PDF-extraction path (text → DOI/arXiv, else content digest) |
| **006e — Frontend** | Submit (URL or drop), live progress, hindsight inspection of transcript and usage, the not-invited screen, and the two exits (link/claim, go public) | consumes 006a's API; all copy in en and zh |
| **006f — Taking a reproduction out** | The optional account link and collaborator invitation, and the trusted-only visibility flip | adds the `Granter` interface beside 002's `Forge`, not onto it; the only package that names GitHub; the flip re-checks 002's manifest |

## Interface (the API surface, sketched)

```
open to anyone the issuer authenticates
GET    /login  ·  /callback  ·  /logout                       -> standard OIDC session
GET    /me                                                    -> { principal, tier, linked_accounts }

behind the access gate (granted seed users only)
POST   /papers                 { url } | multipart PDF        -> { job_id }
GET    /jobs/{id}                                             -> job status + phase
GET    /jobs/{id}/events        (SSE)                         -> progress + transcript events
POST   /jobs/{id}/claim                                       -> invites the linked account (409 if unlinked)
GET    /reproductions                                         -> registry (003), derived
GET    /reproductions/{id}                                    -> manifest + verdict + rate
GET    /reproductions/{id}/transcript                         -> transcript (005)
GET    /links/{forge}  ·  GET /links/{forge}/callback         -> optional; prove an account, identity scope only
                                                                 {forge} is a closed set — today, only `github`

trusted users only
POST   /reproductions/{id}/visibility  { public: true }       -> flips the org repository public, one-way

platform admin only
GET    /admin/access  ·  POST /admin/access { email, tier }  ·  DELETE /admin/access/{email}
```

`GET /me` is deliberately outside the gate: it is how the frontend learns to render the not-invited screen instead of the operator UI, and how it knows whether to offer the visibility control. Everything the gate covers returns `403` with a machine-readable reason — `not_invited` for an ungranted principal, `not_trusted` for a seed user reaching for the visibility flip — including the read surfaces, because 002 keeps reproduction repositories private and a registry listing is not public information.

Exact shapes are 006a's to settle; this fixes the surface, not the field names.

## Acceptance criteria

These are invariants the whole must hold, verified as the children land:

1. `internal/audit` and `internal/provider` change only by *additive* seams (the transcript, 005). The web is new packages and a new `cmd/`, never a rewrite of the engine.
2. Job orchestration and access grants live in Postgres; the finished-reproduction registry is still derived from the forge — no second source of truth for completed reproductions.
3. The service never stores a user's repository credentials. Ownership is granted only by invitation to a *linked, OAuth-verified* account — never to a client-supplied name — and no write scope on a user's forge account is ever requested or held.
4. A submitted audit runs asynchronously; no HTTP request blocks for the run, and progress is observable while it runs.
5. The whole system comes up locally with `docker compose` and one backend credential — no per-user secrets.
6. Every read surface (registry, a reproduction, its transcript) is the same data the CLI and the forge already hold, presented — never a divergent copy.
7. The access gate is fail-closed: with an empty `access_grants` table, an authenticated session reaches no operator route. A test enumerates the gated routes and asserts each answers `not_invited` for an authenticated, ungranted principal — enumerated from the router, so a new route added without a grant check fails the test rather than silently shipping open.
8. A grant seeded by a verified email admits that person on their first sign-in and binds to their principal; a second principal presenting that email afterwards is refused. Revoking the grant locks the holder out on the next request, with no session left standing.
9. A forge account is required for nothing but the collaborator invitation. A test drives an unlinked seed user from submission to a read result and transcript end to end; `claim` is the only route that answers differently (`409`).
10. Only a trusted user can make a reproduction public — with no forge account linked — and only one this service published under 002's gate. A seed user's flip is refused server-side even when the request is forged past the UI, and a repository with no accountable manifest is refused for everyone.
11. The IDP is replaceable and the forge is contained. Every gate and tier test runs against a stub issuer, with no claim beyond `sub` and verified email reaching an authorisation decision; every job, gate, and API test runs against fake forge interfaces, with GitHub named only inside its adapter and the account-link adapter.

## Out of scope

Multi-tenancy, tenant isolation, per-user backend credentials, and billing — the single-tenant scoping exists precisely to defer these. Self-serve access: no signup, no waitlist queue, no automated approval — a platform admin grants access out of band, and the not-invited screen says so. Reading anything from the IDP beyond the account payload — its roles, orgs, delegations, and billing are another product's model, not ours. A second forge implementation: the seam admits one, but GitHub is the only adapter this track builds. Clearing a third-party file for redistribution — 002 defers it and going public inherits that deferral, so a public reproduction is still worker-written files only. Un-publishing: flipping a repository back to private is a manual operation on the forge, not a product feature, because it does not undo anything. The engine's internals. The choice of frontend framework, which is 006e's to make.
