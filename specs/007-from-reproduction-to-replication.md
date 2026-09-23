---
title: "From reproduction to replication"
status: draft
---

# 007 — From reproduction to replication

**One decision below was reversed.** This spec scopes a project as the replication effort around *one paper*; [012](012-a-project-holds-papers.md) made a project hold many, and [015](015-a-verdict-belongs-to-a-session.md)/[023a](023a-experiments-persisted-and-dispatched.md) key an experiment to the session that produced its instrument rather than to the project. The two revisions this spec asked for did land (a job runs an audit *or* an experiment, and project membership sits beside the tiers), and the firewall holds: `internal/audit` never imports `internal/experiment`. The product half of 007b/007c is now the [023](023-the-outer-loop-as-a-product.md) chain.

**Status:** draft
**Scope:** extending the platform past the inner loop: turning a reproduction into a re-runnable *instrument*, and letting a researcher bind that instrument to new data, whether their own dataset, a synthetic cohort, or real participants recruited through a page the platform hosts. This is a design spec; it decides the domain model and spawns the tightly-scoped child specs that build it.
**Depends on:** [002](002-publishing-reproductions.md) (the manifest and the redistribution gate), [004](004-go-port.md) (the provider seam an experiment run reuses), [006f](006f-taking-a-reproduction-out.md) (the repository mount). Revises [006b](006b-job-model-and-persistence.md) and [006c](006c-identity-and-access.md) before either is built.

## Problem

The engine audits one loop. [`docs/procedure.md`](../docs/procedure.md) §7 already names what that leaves undone:

> Reproducing a paper's statistics from its released data does not re-validate its claim about the world; that claim rests on the outer loop and on the sample that was collected. An audit that blurs the two overstates what it proved.

That is correct, and today it is also a dead end. The audit can say *re-observation is what this claim needs* and then stop, because nothing in the platform can re-observe anything. A researcher who reads a reproduction and wants to re-run the study is given a verdict, a boundary table, and an implementation of the half that was never in doubt.

Meanwhile the reproduction repository holds something more useful than it currently declares. `src/` is a working implementation of the paper's analysis; the audit has read the paper closely enough to know its study protocol; the boundary table already states which components need people. Those three facts are an experimental *instrument* (an apparatus, a protocol, and an analysis pipeline), described in prose and therefore not runnable by anyone but a human reading it.

This spec makes the instrument concrete, and gives re-observation a mechanism. It does not touch the audit.

## The constraint that shapes everything

**The audit's method does not change, and nothing here becomes reproduction evidence.**

"Never simulate the outer loop" is not a stylistic preference in this codebase. It is [`method/instructions.md`](../method/instructions.md):39, it is a failing condition in [`method/rubric.md`](../method/rubric.md):15 ("no simulated user-study results"), it is regression-tested in [`evals/case-01/expected.md`](../evals/case-01/expected.md):35 (the outer loop "must **not** be simulated or estimated"), and it is the product's thesis in [`README.md`](../README.md):12. A platform that relaxes it to ship a feature breaks its golden set and its credibility claim in the same commit.

So the outer loop is executed by a **new engine downstream of the audit artifact**, never by a change to the audit's method:

- The audit's `verdict` and `rate` are written by an audit and by nothing else. No experiment, of any data source, ever writes them. This is asserted, not merely intended (criterion 3).
- Both golden cases keep passing byte-for-byte on their existing expectations. 007a adds an *additive* deliverable and an additive rubric criterion; it changes no existing one.
- The word for an LLM cohort is **synthetic pilot**, never "simulating the outer loop". A pilot is by construction pre-study: it shapes what you will run, and it never establishes that you ran it. Naming matters here because the forbidden phrase is the accurate description of what a careless version of this feature would be.

**The dependency is one-way, which the compiler can enforce.** "An experiment never writes the audit's fields" is the weaker half of the firewall. The stronger half is that **the audit never reads an experiment at all**: not a pilot, not a cohort, not a re-analysis. An auditor that could ingest experiment output would be able to let a synthetic cohort inform a boundary classification or a rate without ever writing `verdict` itself, which is the same violation by another route.

So the package direction is the control, and it is checked rather than intended:

```
internal/instrument   the schema alone — imports neither of the below
internal/experiment → internal/instrument, internal/provider, internal/publish
internal/audit      → internal/method, internal/provider, internal/instrument
                      and NEVER internal/experiment
```

`internal/audit` imports only `method` and `provider` today, and gains `instrument` for the schema validation [007a](007a-the-study-protocol-as-an-artifact.md) folds into its grade-and-revise loop. That is a schema package, with no executor behind it. The edge from `audit` to `experiment` does not exist and a test asserts it never appears. An import cycle is a build failure; this is the same guarantee, made explicit in the direction that matters.

The consequence in product terms: LLM cohorts exist for work that happens *after* an audit is finished and graded: exercising an apparatus, testing how an effect behaves across distributions the paper never sampled, probing the instrument's sensitivity. None of it flows back. An audit run twice on the same paper produces the same verdict whether a thousand pilots have run against its instrument or none have.

## Decisions

**A reproduction yields an instrument.** Beside 002's `replication.json`, the audit writes `instrument.json`, the declared, machine-readable half of what the reports say in prose:

| Field | Contents | When absent |
|---|---|---|
| `protocol` | the extracted study design: conditions, assignment, trial structure, measures, the analysis plan | `null` with a stated reason — the paper does not describe it in enough detail |
| `analysis` | the entrypoint contract for `src/`: the command, the input data schema it expects, the outputs it produces | `null` with a stated reason — no runnable analysis was recovered |
| `servability` | whether a machine can serve this protocol to participants, with a justification against declared criteria | never absent; `false` with a reason is the common answer |

`instrument.json` is additive. A paper whose protocol cannot be extracted and whose analysis cannot be declared produces a reproduction exactly as good as it produces today, plus an accurate record of why it is not an instrument. This is the same posture as `UNVERIFIED.md`: the blocker is the finding.

**An experiment binds one instrument to one data source.** This is the central generalization, and it is why this is one mechanism rather than four features:

| Data source | Where the data comes from | What a result establishes | May be published |
|---|---|---|---|
| `released` | the paper's own released data | **re-analysis** — the paper's statistics recomputed | yes, as today |
| `uploaded` | a dataset the researcher brings | a new finding, theirs, on the paper's instrument | no — it is their data, not ours to redistribute |
| `synthetic` | an LLM cohort driven through `protocol` | **nothing about people** — a pilot, and labeled as one | the pilot report only, stamped synthetic |
| `human` | participants, via a page the platform hosts | **re-observation** — the claim tested against a new sample | never; it is participant data |

One analysis pipeline runs over all four, because the pipeline is `instrument.analysis` and the sources differ only in where the input file came from. The "establishes" column is not documentation. It is the epistemic record `procedure.md` §7 demands, moved from prose into the domain model, and it is what keeps a synthetic pilot from being mistaken for a replication.

The fourth request this track opened with, letting a researcher test a new hypothesis on top of existing research, is the `uploaded` row. It needs no feature of its own.

**A project owns the work, and researchers are named by email.** A project is the replication effort around one paper: its instrument, its experiments, its collected data, and its people. Membership is `(principal, project, role)` with roles *lead* and *collaborator*, seeded by verified email and bound one-way to a principal on first sign-in.

That binding is not a new mechanism. It is exactly what [006c](006c-identity-and-access.md) already decided for `access_grants`: seed by an `email_verified` claim, bind on first login, refuse a second principal presenting the same email afterwards. Project membership generalizes that table's shape; it does not set up a second membership system beside it.

**Project membership is orthogonal to platform access, and is not a way up the tiers.** A project lead may add a colleague by email. That does *not* admit them to the platform: they still need a platform grant from an admin before they can sign in and operate, and the UI says so plainly when a lead adds someone ungranted. The beta stays fail-closed and there is still no self-serve path up the tier ladder.

Rejected: having a project invitation seed a seed-tier grant. It is the obvious convenience and it is precisely the self-serve escalation [006](006-web-application.md) rejected: one lead could then admit anyone to a shared credential.

**Participant responses are a third origin class, and they are structurally unpublishable.** `publish.Classify` today keys on `Origin`: worker-written versus third-party-fetched. Participant data is neither, and the consequences are substantive:

- It never enters a reproduction repository, a mount, or a manifest. It lives in Postgres and is exported directly to the project's researchers. The redistribution gate therefore never sees it, because it is never in a tree. That is the primary control, and it is structural.
- It reaches an agent sandbox only as the de-identified dataset it is: attached outside the mount, exactly as an uploaded dataset is, so the project's own analysis can run over it. Forbidding the sandbox outright would make a `human`-source experiment impossible, since analyzing a new sample means running the instrument's pipeline. What makes that safe is a property of the collection schema, not a promise: [007d](007d-hosting-a-study.md) admits only the protocol's declared closed-form measures plus an opaque token, so there is no field an identifier could occupy.
- No new `Origin` case is needed for that, and this is worth stating because the opposite is the intuitive assumption. `internal/publish/gate.go` is an **allowlist**: `Classify` includes `OriginWorker` and withholds everything else, and `originOf` maps every path outside the fixed report names and `src/` to `OriginThirdParty`. So a participant-data file appearing in a tree is *already* withheld and already bars the visibility flip, with no change at all. Adding a participant case would sharpen the refusal's reason string; it would not add a control.
- The allowlist cuts the other way for the track's own deliverables, which is the consequence to act on: `instrument.json` ([007a](007a-the-study-protocol-as-an-artifact.md)) and `study/` ([007d](007d-hosting-a-study.md)) are worker-written but unknown to `originOf`, so they would be withheld from every publication *silently*, because withholding is the safe direction and raises no error. Both specs add themselves to the allowlist with a test.
- [003](003-reproduction-registry.md)'s registry is derived from the forge. Participant data can never reach it, by the same structural fact.

**Hosting a study opens an anonymous write path, and that inverts the app's posture.** Every route [006](006-web-application.md) designs is behind a sign-in and a grant. A public participant page is the first surface where an unauthenticated stranger *writes* to Postgres. This is new attack surface, not a configuration change, and 007d owns it: per-run capability tokens, a declared response schema that rejects anything unshaped, rate and volume caps per run, and a run that can be closed.

**The participant page is served from the instrument, on a separate origin.** The `study/` bundle is a static, self-contained web app the agent writes from `protocol`; the platform serves it and accepts responses against `protocol.measures`. So the platform holds no per-paper code: it is a generic protocol runner and static host, and stays paper-agnostic.

But that bundle is *agent-written code served to the public*. It never runs on the origin that holds the session cookie: it is served from a distinct origin under a strict CSP, and the platform's own pages embed it only in a sandboxed frame. An agent-authored script and an authenticated session must not be able to reach each other.

**An experiment run reuses the provider sandbox; it does not add a second execution path.** Running agent-written analysis code over a researcher's uploaded dataset is untrusted code over untrusted data. The platform already has exactly one component built to contain that: the managed-agent sandbox behind `provider.Provider`. An experiment run is a provider run with the instrument repository mounted ([006f](006f-taking-a-reproduction-out.md)'s `RepoMount`) and the dataset attached as a session resource, carrying a *minimal* task (execute the declared entrypoint, capture its real output), not the audit method.

Rejected: a container runner inside the server. It is a second sandbox to secure, size, and operate, for code the existing sandbox already isolates better than a shared server process would.

**Ethics approval is the researcher's, asserted explicitly and recorded.** The platform does not grant, review, or infer ethics approval. Opening a human cohort requires an explicit attestation naming the approving body and its reference, recorded against the run with the attesting principal, and consent is shown to every participant before the first trial. Recruitment and payment stay outside: the platform issues a participation link and a completion code, and the researcher brings their own panel.

## The specs this spawns

Each is tightly scoped and gets its own implementation pass. The order is deliberate and inverts the order these capabilities were first asked for; see below.

| Child | Scope | Depends on |
|---|---|---|
| **[007a](007a-the-study-protocol-as-an-artifact.md) — The study protocol as an artifact** | `instrument.json`: protocol extraction, the analysis entrypoint contract, and the servability assessment; the additive rubric criterion and the eval expectations | 004 |
| **[007b](007b-experiments-over-instruments.md) — Experiments over instruments** | the instrument/experiment/data-source domain model, and its two offline sources: `released` and `uploaded`; execution in the provider sandbox | 007a, 006f |
| **[007c](007c-the-synthetic-pilot.md) — The synthetic pilot** | the `synthetic` source: an LLM cohort driven through `protocol`, its indelible labeling, and its calibration against papers that released real data | 007b |
| **[007d](007d-hosting-a-study.md) — Hosting a study for participants** | the `human` source: the hosted page on its own origin, consent, the anonymous write path, per-project collection and export, retention | 007b, 006a, 006b, 006c, 006e |

**Sequence, and why it is not the order these were asked for.** The synthetic pilot was the first capability requested and is third here. A synthetic cohort is only *checkable* where the paper released real participant data: you validate it against the paper's own reported effect, which is ground truth you have. Before the experiment machinery and a `released` baseline exist, a synthetic pilot emits numbers nobody can falsify, which is the failure mode this whole spec is built to avoid. So: protocol extraction, then the offline bindings, then synthetic against a known answer, then human cohorts last, because they carry the compliance and public-write surface.

Against the project's existing plan, 007a–007c slot in as engine work after 005 and before the web shell; 007d comes after 006e:

> 006f → 003 → 005 → **007a → 007b → 007c** → 006a/006b → 006c → 006d → 006e → **007d**

## What this revises

Both are drafted and unbuilt, which is why this spec is written now rather than after them. Reworking a draft is an edit; reworking a shipped schema is a migration.

- **[006b](006b-job-model-and-persistence.md)**: a job is still the unit of *execution*, and its state machine survives. What changes is that a job now runs an audit *or* an experiment, and belongs to a project. The domain gains project / instrument / experiment *above* the job; the job table is not replaced.
- **[006c](006c-identity-and-access.md)**: `access_grants` keeps its global tiers unchanged and gains the project-membership table alongside it, sharing the seed-by-email and bind-on-first-login rules rather than reimplementing them.

## Acceptance criteria

Invariants for the whole track, verified as the children land:

1. Both golden cases pass on their existing expectations, unchanged. The audit's method gains deliverables and never loses a prohibition; `evals/case-01`'s "must not be simulated or estimated" stays true of every audit the platform runs.
2. An instrument is optional and accurate: a paper with no extractable protocol and no declarable analysis produces a reproduction of undiminished quality plus a stated reason, and never a fabricated protocol.
3. `verdict` and `rate` in a 002 manifest are written only by an audit. A test drives an experiment of every data source and asserts neither field moves.
4. The audit never reads an experiment. `internal/audit` does not import `internal/experiment`, directly or transitively, and a test walks the import graph and fails if that edge appears. An audit re-run on a paper produces the same verdict whether or not pilots have run against its instrument, asserted end to end, not only on the import graph.
5. A result carries its data source indelibly. No report, export, or API response derived from a `synthetic` cohort can be read as evidence about people, and a test asserts the label survives every path out of the system.
6. Participant data never enters a repository, a mount, a manifest, or the registry, and reaches a sandbox only as a de-identified dataset placed outside the mount. Asserted structurally (by where the writes go and by the collection schema admitting no identifier field) and again by `Classify`'s allowlist withholding any such path presented to it. Conversely, `instrument.json` and `study/` are added to that allowlist and a test asserts each survives a publish, since a worker-written file the allowlist does not know is withheld silently.
7. The participant page is served from an origin that holds no session cookie, and the platform's own pages embed it only sandboxed. Asserted against the served headers, not by convention.
8. Project membership admits nobody to the platform. A test adds an ungranted email to a project and asserts that principal still reaches no operator route on first sign-in.
9. An experiment executes only in the provider sandbox. No path in the server runs instrument code in-process or in a container it manages.
10. A human cohort cannot open without a recorded ethics attestation and a servable protocol, and both refusals are server-side.
11. Every new surface is tested without network or credential, on the project's existing terms: fake forge, stub issuer, in-process provider.

## Out of scope

Recruitment and payment: no panel integration, no participant compensation, no screening; the platform issues a link and a completion code. Ethics review of any kind: the platform records an attestation and never evaluates it. Multi-tenancy and per-user backend credentials, still deferred by 006's single-tenant scoping. Longitudinal and multi-session studies, physical apparatus, confederates, and interviewer-led protocols: the servability criteria exclude them and 007a states so rather than pretending otherwise. Qualitative coding of participant responses. Meta-analysis across projects. Deciding that a replication *succeeded or failed*: the platform runs the experiment and reports the statistics; whether a new sample upholds a paper's claim is the researchers' conclusion to draw and defend, not a verdict this engine issues.
