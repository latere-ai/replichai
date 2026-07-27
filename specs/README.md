# Specs

Designs, and the record of what got built. One spec per tightly-scoped feature; each states the problem, the decisions, the acceptance criteria, and what it deliberately leaves out. This page is the map: what exists today, how it fits, what is only designed, and the order it gets built.

Feasibility questions and scope extensions that are *not* on the roadmap — multi-study papers, hard-to-replicate instruments — live in [`BACKLOG.md`](BACKLOG.md), deliberately unsequenced.

## What replichai is

Give it a paper. It resolves the paper's identity, runs an agent that reproduces the machine-runnable inner loop, publishes the reproduction as its own GitHub repository with the agent's commit history, and returns an independent verdict — recording every decision the paper left unwritten. It never simulates the outer loop (the human study); placing and defending that boundary is the point.

## Current status

**Built and working today — the engine and the live publish loop:**

```
replichai audit <paper> --publish [--detach] [--invite <login>]
   resolve identity (001)                          internal/paper
   → registry warns on prior reproductions,        internal/registry (003)
     allocates <canonical-slug>-<suffix>
   → create a private repo under open-hai          internal/github
   → mount it into the agent's sandbox             internal/provider (RepoMount → github_repository)
   → the agent commits its reproduction LIVE       internal/audit (006f)
replichai finish <owner>/<repo> <paper> --session <id>
   → reattach, wait, verify the committed tree,    internal/publish (VerifyTree)
     commit replication.json, invite the user      internal/github (PutFile, Invite)
```

This was proven end to end: a real reproduction (`open-hai/short-form-videos-…-wtmk`) with agent-authored history, an independent manifest, and a collaborator invitation. `audit --publish` blocks; `--detach` starts it and hands you a `finish` command to run later, since the agent commits independently of the CLI.

**Implemented:** 001 (identity), 002 (publishing + the redistribution gate), 003 (registry), 004 (the Go port), 005 (transcript), 007a (the instrument), 007b/007c (experiments and the synthetic pilot — engine halves), and the **web application core**: 006a (HTTP + SSE), 006b (job model + Postgres), 006c (OIDC login + the fail-closed access gate). **006f is implemented at its core** (mount, publish, finish, verifier, invite); two pieces stay open (below). **010** provisions production — domain, database, k8s manifests, and a tag-triggered deploy pipeline.

**The app runs end to end**: `docker compose up` (or `make up`) brings up Postgres + the server, which serves the React frontend (006e) and the API. A researcher signs in, submits a paper URL, and watches its phases stream live to `done` behind the real fail-closed gate. Production is provisioned and fail-closed.

**Built (pending a full prod run):** [006i](006i-server-side-mount-and-publish.md) wires the CLI's live-mount publish into the server `engineRunner`, so a web-submitted session gets a real repository, manifest, and committed transcript rather than evaporating with the sandbox — the backend half of 006f, with the CLI's finalize extracted into a shared `publish.FinalizeLive` so the two paths cannot drift.

**Designed, being built:** [006j](006j-the-new-project-flow.md) makes "New project" a deliberate act — name it, preview each paper link (`POST /resolve`), start several papers, and invite collaborators, through a dedicated `POST /projects`.

**Designed, not built:** 006d (PDF ingestion) and the hosted-study spec 007d. The 007b/007c Postgres stores and the experiment HTTP surface land with 006g/006e. 006c's forge-account link lands with its consumer, 006f.

**Built and deployed:** the project/session rework — 006g (projects as first-class records holding replication sessions, the double-trigger guard, the CLI backfill migrated into three one-session projects) and 006h (the session transcript viewer: trajectory + token usage, usage derived from the transcript for backfilled sessions).

## Architecture at a glance

**The neutral core, the named edges.** The orchestrator (`internal/audit`) and the contract (`internal/provider`) name only roles; the one backend adapter (`internal/provider/anthropic`) and the composition root (`cmd/`) name the vendor. A `memory` provider runs nothing, so the whole orchestrator is testable offline.

**The package map:**

| Package | Role |
|---|---|
| `internal/paper` | resolve a reference to a stable identity + slug (001) |
| `internal/method` | the method — instructions, rubric, task template, experiment doctrine — and its digest |
| `internal/instrument` | the `instrument.json` schema and its validation (007a) |
| `internal/provider` | the neutral backend contract; `anthropic` and `memory` adapters |
| `internal/audit` | the orchestrator: run an audit, optionally into a mounted repo |
| `internal/experiment` | run a reproduction's declared analysis on new data (007b); never imported by `audit` |
| `internal/publish` | the manifest, the redistribution gate, naming, the filesystem forge |
| `internal/github` | the small GitHub client publishing and the registry need |
| `internal/registry` | the derived registry: relate reproductions of one paper (003) |
| `internal/job` | the job lifecycle: the state machine, the store seam, the worker (006b) |
| `internal/access` | the authorisation model: grants, tiers, project membership, seed-and-bind (006c) |
| `internal/httpapi` | the HTTP server: submit, status, SSE, read surfaces, the gate seam (006a) |
| `internal/webauth` | the OIDC login, session cookie, gate policy, and dev bypass (006c) |
| `internal/spa` | embeds and serves the built frontend as the app shell (006e) |
| `frontend/` | the researcher UI — bun + React, i18n en/zh (006e) |
| `cmd/replichai` | the CLI: `audit`, `finish`, `resolve`, `publish`, `providers` |
| `cmd/server` | the web app composition root: wires the engine + stores + gate, runs the worker + HTTP server (006a/b) |
| `deploy/` + `.github/` | k8s kustomize (prod/staging) and the CI/release pipeline (010) |

**The trust model — a reproduction is a claim / evidence / verdict triad**, and who authors each is deliberate: the subject of an audit never writes its own grade.

| Layer | Author | Artifact |
|---|---|---|
| Claim | the agent, in the sandbox | the reproduction — code, history, `REPRODUCIBILITY.md` |
| Evidence | the backend's event log, fetched by the system | the transcript (005) |
| Verdict | the system, outside the sandbox | `replication.json` |

**Two publish paths, on purpose.** The *mount* path (006f) is the real one: the agent commits into a live GitHub repo, and the gate becomes a post-run *verifier* (`VerifyTree`, a blocklist of third-party data, because an agent-authored repo is a full project, not a fixed file set). The *filesystem* path (002: `Forge`/`FSForge`, `Classify` allow-list, the `publish` CLI command) stays as the offline, no-backend way to exercise publishing.

## Load-bearing decisions (audit these)

- **The project is the tenant** — no org in the product; each user owns private projects (owner + invited contributors), private by default, made public by the owner. One backend credential pays for every run. The app owns the ownership model; artifacts live in a `Store` (the GitHub forge for the MVP, [drive](https://drive.latere.ai) later, behind one seam — 008). (006)
- **The registry is derived, not stored** — read the org's manifests; no second source of truth. Job-lifecycle state (a later web concern) is the only thing that gets a database. (003, 006b)
- **Ownership by invitation, never delegation** — GitHub OAuth proves identity only; the org invites the user's linked login as a collaborator. The service never holds a user's repo credentials. (006f, 006c)
- **The redistribution gate fails closed** — third-party data is never published; the method keeps downloads out of the mounted repo, and the verifier bars a leaked repo from ever going public. (002, 006f)
- **Never simulate the outer loop** — outer-loop execution is a downstream engine, `verdict`/`rate` stay audit-only, a synthetic cohort is labelled indelibly. Regression-tested in `evals/case-01`. (007)
- **Closed beta, fail-closed access** — anyone signs in with a latere account; only invited users a platform admin grants reach the operator UI. (006c)

**Still open in 006f:** the two-token split (a fine-grained mount token distinct from the org token — one token for now), the per-project collaboration API, and the owner's public flip gated by a recorded responsibility sign-off (joining the public zone). The flip has a prerequisite: **009**, secret containment. Because the agent commits live, nothing filters before the push, and a secret can reach the tree, the history, or the transcript. `VerifyTree` covers the head tree and reaches the manifest; `TranscriptLeaks` only warns, which diverges from 005. Deferred for the MVP and safe to defer only because nothing makes a repository public today.

## The spec map

| # | Spec | Status | Depends on |
|---|---|---|---|
| 001 | [Paper identity and slugs](001-paper-identity-and-slugs.md) | implemented | — |
| 002 | [Publishing reproductions](002-publishing-reproductions.md) | implemented | 001 |
| 003 | [The reproduction registry](003-reproduction-registry.md) | implemented | 001, 002 |
| 004 | [Port the core to Go](004-go-port.md) | implemented | — |
| 005 | [Capture and publish the reproduction transcript](005-transcript.md) | implemented | 002, 006f |
| 006 | [The web application](006-web-application.md) | draft | 002, (003, 005) |
| 006a | [HTTP API and streaming](006a-http-api-and-streaming.md) | built | 006b, (003, 005) |
| 006b | [Job model and persistence](006b-job-model-and-persistence.md) | built | 002, 004 |
| 006c | [Identity, tiers, and the access gate](006c-identity-and-access.md) | implemented | 006b |
| 006d | [PDF ingestion](006d-pdf-ingestion.md) | draft | 001, 006a |
| 006e | [Frontend](006e-frontend.md) | implemented | 006a |
| 006f | [Taking a reproduction out](006f-taking-a-reproduction-out.md) | core built | 002, 004, 006g, (006c) |
| 006g | [Projects and replication sessions](006g-projects-and-replication-sessions.md) | implemented, deployed | 006b, 006c |
| 006h | [Session transcript viewer](006h-session-transcript-viewer.md) | implemented, deployed | 006g, 005, 003 |
| 007 | [From reproduction to replication](007-from-reproduction-to-replication.md) | draft | 002, 004, 006f |
| 007a | [The study protocol as an artifact](007a-the-study-protocol-as-an-artifact.md) | implemented | 004 |
| 007b | [Experiments over instruments](007b-experiments-over-instruments.md) | engine built | 007a, 006f |
| 007c | [The synthetic pilot](007c-the-synthetic-pilot.md) | engine built | 007b |
| 007d | [Hosting a study](007d-hosting-a-study.md) | draft | 007b, 006a–006c, 006e |
| 008 | [Drive as the artifact data plane](008-drive-data-plane.md) | deferred | 006 (MVP first) |
| 009 | [Secret containment when the agent commits live](009-secret-containment-when-the-agent-commits-live.md) | deferred | 002, 005, 006f |
| 010 | [Deployment and release](010-deployment-and-release.md) | built | 006a, 006b, 006c |

## The three tracks

**The engine (001–005) — done.** 001 decides what a paper *is*; 002 decides where a reproduction *goes* and gates what may be republished; 003 relates repeat reproductions of one paper by identity; 004 is the record of why the core is Go. 005 closed the evidence layer: the agent's full session transcript is fetched and committed, by the system, in the same finalize step as the manifest — so each reproduction is claim + evidence + verdict.

**The web application (006).** A single-tenant service that wraps the engine: submit a paper (URL or dropped PDF), watch it run, be granted the resulting repository, and inspect the transcript and cost afterwards. It spawns six children — **006a** the HTTP API and SSE, **006b** the job model and Postgres, **006c** identity and the fail-closed access gate, **006d** PDF ingestion, **006e** the frontend, **006f** taking a reproduction out (the mount publish, invitation, and visibility flip — the one child already built at its core). Closed beta: signing in grants nothing until a platform admin seeds you.

**Replication (007) — the track past the inner loop.** It turns a reproduction into a re-runnable **instrument** — the study protocol, the analysis entrypoint, and a servability verdict, declared as data (**007a**) — then generalises the unit of work: an **experiment** binds one instrument to one **data source**, of which there are four. The paper's released data gives re-analysis; a researcher's uploaded dataset gives a new finding on the paper's instrument (**007b**); an LLM cohort gives a *pilot* that shakes the apparatus down and never becomes evidence about people (**007c**); real participants, through a page the platform hosts, give re-observation (**007d**). One generalisation, four bindings — "run it on my own data" is a row in a table, not a feature of its own.

What 007 does **not** do is touch the audit: the never-simulate-the-outer-loop boundary is a rubric failure condition and the product's thesis. 007 also revises **006b** (a job runs an audit *or* an experiment) and **006c** (project membership beside the tiers) — written before either is built, because afterwards it would be a migration.

## Build sequence

Engine first; the web shell last, because every screen it renders needs an engine capability the earlier specs deliver.

```
002 ✓ → 003 ✓ → 006f-core ✓ → 005 ✓ → 007a ✓ → 007b ✓ → 007c ✓
     → 006a ✓ / 006b ✓ (runs on Postgres via compose) → 006c (identity, next) → 006d (PDF) → 006e (frontend) → 007d → 008
```

Done so far: 001–005, 006f's core, 007a, the engine halves of 007b and 007c, and **006a and 006b** — the job state machine and its in-process worker (`internal/job`, with in-memory and Postgres stores behind one contract), the HTTP router, SSE stream, read surfaces, and fail-closed gate seam (`internal/httpapi`), and the composition root (`cmd/server`) that wires them to the engine. **The web app runs locally**: `docker compose up` brings up Postgres + the server, a submitted paper walks `submitted → done` through the real HTTP + worker path, and the job survives a restart. It defaults to the in-process memory provider (no API key); `--provider anthropic` does real audits.

**006c's core is built**: the authorisation model (`internal/access` — grants, three tiers, project membership, seed-by-verified-email with one-way binding, in-memory and Postgres against one contract) and the fail-closed gate policy (`internal/webauth`) that resolves a grant per request. What remains of 006c is the **OIDC login front door** — sign-in via `latere.ai/x/pkg/oidclogin`, the session cookie holding only `sub`+email, `/me`, the `/admin/access` endpoints, the dev-bypass off-loopback refusal, and rewiring `cmd/server` from the `--dev` gate to the real one. Until that lands the server still runs behind `--dev`.

**The web application is usable end to end.** What remains is depth, not the core loop: **006f packaged** for reuse (the `publishing` phase — a pass-through in the server today, so runs are graded but not yet pushed to a repository), **006d** (dropping a PDF to submit), a server-side job list, and 007d.

**Production is provisioned** ([010](010-deployment-and-release.md)): `replichai.latere.ai` DNS, the `replichai` database and its secret (terraform, applied), the k8s kustomize manifests (`deploy/prod` in the `latere` namespace), and the CI/release pipeline — a `v*` tag builds, deploys to prod, and smokes. Prod is **fail-closed**: with no login yet it admits nobody, and the release smoke fails if the dev bypass ever reaches production.

Still open in the server: the experiment `Kind` in `job.Runner` (the dispatch is there; the experiment callee is 007b's server wiring), the Postgres stores for 007b/007c, and reading a transcript through `ReproReader`.

Within 007 the order inverted how the capabilities were first asked for — the synthetic pilot (007c) came third, because a cohort is only checkable where the paper released real data to check it against.
