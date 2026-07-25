# Specs

Designs, and the record of what got built. One spec per tightly-scoped feature; each states the problem, the decisions, the acceptance criteria, and what it deliberately leaves out. This page is the map: what exists today, how it fits, what is only designed, and the order it gets built.

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

**Implemented specs:** 001 (identity), 002 (publishing + the redistribution gate), 003 (registry), 004 (the Go port), 005 (transcript). **006f is implemented at its core** (mount, publish, finish, verifier, invite) with two pieces still open (below).

**Designed, not built:** the web application 006 and its children 006a–006e, and the replication track 007a–007d.

## Architecture at a glance

**The neutral core, the named edges.** The orchestrator (`internal/audit`) and the contract (`internal/provider`) name only roles; the one backend adapter (`internal/provider/anthropic`) and the composition root (`cmd/`) name the vendor. A `memory` provider runs nothing, so the whole orchestrator is testable offline.

**The package map:**

| Package | Role |
|---|---|
| `internal/paper` | resolve a reference to a stable identity + slug (001) |
| `internal/method` | the method — instructions, rubric, task template — and its digest |
| `internal/provider` | the neutral backend contract; `anthropic` and `memory` adapters |
| `internal/audit` | the orchestrator: run an audit, optionally into a mounted repo |
| `internal/publish` | the manifest, the redistribution gate, naming, the filesystem forge |
| `internal/github` | the small GitHub client publishing and the registry need |
| `internal/registry` | the derived registry: relate reproductions of one paper (003) |
| `cmd/replichai` | the CLI: `audit`, `finish`, `resolve`, `publish`, `providers` |

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
- **Closed beta, fail-closed access** — anyone signs in with a latere account; only seed users a platform admin grants reach the operator UI. (006c)

**Still open in 006f:** the two-token split (a fine-grained mount token distinct from the org token — one token for now) and the trusted-only public-visibility flip (needs 006c's tiers). The flip has a prerequisite: **009**, secret containment. Because the agent commits live, nothing filters before the push, and a secret can reach the tree, the history, or the transcript. `VerifyTree` covers the head tree and reaches the manifest; `TranscriptLeaks` only warns, which diverges from 005. Deferred for the MVP and safe to defer only because nothing makes a repository public today.

## The spec map

| # | Spec | Status | Depends on |
|---|---|---|---|
| 001 | [Paper identity and slugs](001-paper-identity-and-slugs.md) | implemented | — |
| 002 | [Publishing reproductions](002-publishing-reproductions.md) | implemented | 001 |
| 003 | [The reproduction registry](003-reproduction-registry.md) | implemented | 001, 002 |
| 004 | [Port the core to Go](004-go-port.md) | implemented | — |
| 005 | [Capture and publish the reproduction transcript](005-transcript.md) | implemented | 002, 006f |
| 006 | [The web application](006-web-application.md) | draft | 002, (003, 005) |
| 006a | [HTTP API and streaming](006a-http-api-and-streaming.md) | draft | 006b, (003, 005) |
| 006b | [Job model and persistence](006b-job-model-and-persistence.md) | draft | 002, 004 |
| 006c | [Identity, tiers, and the access gate](006c-identity-and-access.md) | draft | 006b |
| 006d | [PDF ingestion](006d-pdf-ingestion.md) | draft | 001, 006a |
| 006e | [Frontend](006e-frontend.md) | draft | 006a |
| 006f | [Taking a reproduction out](006f-taking-a-reproduction-out.md) | core built | 002, 004, (006c) |
| 007 | [From reproduction to replication](007-from-reproduction-to-replication.md) | draft | 002, 004, 006f |
| 007a | [The study protocol as an artifact](007a-the-study-protocol-as-an-artifact.md) | draft | 004 |
| 007b | [Experiments over instruments](007b-experiments-over-instruments.md) | draft | 007a, 006f |
| 007c | [The synthetic pilot](007c-the-synthetic-pilot.md) | draft | 007b |
| 007d | [Hosting a study](007d-hosting-a-study.md) | draft | 007b, 006a–006c, 006e |
| 008 | [Drive as the artifact data plane](008-drive-data-plane.md) | deferred | 006 (MVP first) |
| 009 | [Secret containment when the agent commits live](009-secret-containment-when-the-agent-commits-live.md) | deferred | 002, 005, 006f |

## The three tracks

**The engine (001–005) — done.** 001 decides what a paper *is*; 002 decides where a reproduction *goes* and gates what may be republished; 003 relates repeat reproductions of one paper by identity; 004 is the record of why the core is Go. 005 closed the evidence layer: the agent's full session transcript is fetched and committed, by the system, in the same finalize step as the manifest — so each reproduction is claim + evidence + verdict.

**The web application (006).** A single-tenant service that wraps the engine: submit a paper (URL or dropped PDF), watch it run, be granted the resulting repository, and inspect the transcript and cost afterwards. It spawns six children — **006a** the HTTP API and SSE, **006b** the job model and Postgres, **006c** identity and the fail-closed access gate, **006d** PDF ingestion, **006e** the frontend, **006f** taking a reproduction out (the mount publish, invitation, and visibility flip — the one child already built at its core). Closed beta: signing in grants nothing until a platform admin seeds you.

**Replication (007) — the track past the inner loop.** It turns a reproduction into a re-runnable **instrument** — the study protocol, the analysis entrypoint, and a servability verdict, declared as data (**007a**) — then generalises the unit of work: an **experiment** binds one instrument to one **data source**, of which there are four. The paper's released data gives re-analysis; a researcher's uploaded dataset gives a new finding on the paper's instrument (**007b**); an LLM cohort gives a *pilot* that shakes the apparatus down and never becomes evidence about people (**007c**); real participants, through a page the platform hosts, give re-observation (**007d**). One generalisation, four bindings — "run it on my own data" is a row in a table, not a feature of its own.

What 007 does **not** do is touch the audit: the never-simulate-the-outer-loop boundary is a rubric failure condition and the product's thesis. 007 also revises **006b** (a job runs an audit *or* an experiment) and **006c** (project membership beside the tiers) — written before either is built, because afterwards it would be a migration.

## Build sequence

Engine first; the web shell last, because every screen it renders needs an engine capability the earlier specs deliver.

```
002 ✓ → 003 ✓ → 006f-core ✓ → 005 ✓ → 007a (next) → 007b → 007c
     → 006a / 006b (HTTP + Postgres) → 006c (identity) → 006d (PDF) → 006e (frontend) → 007d → 008 (drive)
```

Done so far: 001–005 and 006f's core. **Next: 007a** (the study protocol as an artifact). 007a–007c are engine work and slot in before the server; 007d comes after it, because a hosted study needs the server, the database, and identity underneath. Within 007 the order inverts how the capabilities were first asked for — the synthetic pilot (007c) comes third, because a cohort is only checkable where the paper released real data to check it against.
