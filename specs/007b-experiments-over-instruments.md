# 007b — Experiments over instruments

**Status:** implemented (engine half — the Postgres store and the HTTP/UI surface remain, per Out of scope)
**Scope:** the project / instrument / experiment domain model, and its two offline data sources — re-running a reproduction's analysis on the paper's released data, and on a dataset a researcher brings. A child of [007](007-from-reproduction-to-replication.md).
**Depends on:** [007a](007a-the-study-protocol-as-an-artifact.md) (the instrument declaration it executes), [006f](006f-taking-a-reproduction-out.md) (the repository mount), [004](004-go-port.md) (the provider seam).

## Problem

[007a](007a-the-study-protocol-as-an-artifact.md) leaves a declared instrument that nothing runs. This spec builds the executor, and in doing so has to answer the question the rest of the track depends on: what is the unit of work once "audit a paper" is no longer the only thing the platform does?

It is worth building the two *offline* sources first — the paper's own released data, and a dataset the researcher uploads. Neither needs participants, consent, a public write path, or a new authenticated surface, so they exercise the whole domain model at the cheapest possible risk. And the second of them is already a product on its own: a researcher pointing a published paper's analysis pipeline at their own data, to see whether the paper's effect appears in it, is the "test a new hypothesis on top of existing research" capability, delivered as one binding of a general mechanism rather than a feature of its own.

## Decisions

**An instrument is resolved from its repository, never stored.** An instrument is `instrument.json` plus `src/` in a published reproduction, read at a pinned commit. Postgres holds a *reference* — repository and commit SHA — and no copy of the declaration.

This is [003](003-reproduction-registry.md)'s rule, applied one level up: the manifests are the source of truth and nothing may drift from them. It also buys reproducibility for free — an experiment records the exact commit it ran, so a result stays attributable to a specific instrument even after the reproduction gains commits. The SHA comes from `HeadCommit`, which [006f](006f-taking-a-reproduction-out.md) already reads.

**An experiment is stored, because it is the durable record of what was run against what.** The store holds the experiment: its project, its instrument reference and commit, its data source and kind, the resolved command, the outcome, and checksums of what went in and came out. Result *artifacts* — the analysis outputs — go to an artifact store the deployment owns, keyed by experiment.

This does not violate 006b's "jobs are disposable, reproductions are not". An experiment is neither: it is a *finding*, and the platform is the only thing that holds it. Losing an experiment row loses a result nobody else has a copy of, so the row is durable in a way a job row is not, and the two are separate tables for that reason.

**This spec is the engine half, and it ships before Postgres exists.** 007b sits before 006a/006b in the build sequence, so it cannot depend on a database or a job table that has not been built. It therefore delivers `experiment.Store` and `ArtifactStore` as *interfaces* with in-memory and filesystem implementations, and the runner, resolution, and comparison on top of them.

This is 006b's own pattern, borrowed deliberately — "the store is an interface … so handlers and lifecycle logic test against an in-memory fake with no database". The Postgres implementation of `experiment.Store`, its migration, and the `Kind`-dispatch wiring that lets a job run a `Runner` all land **when 006b lands**, against the interfaces this spec fixes. So the engine is testable and complete first, and the persistence is a later implementation of a settled contract rather than a schema guessed at in advance.

**A job will run an audit or an experiment; the job state machine survives.** When 006b arrives, its lifecycle, its `FOR UPDATE SKIP LOCKED` worker, and its progress observation are reused unchanged — a job gains a *kind* and a project, and dispatches to `audit.Auditor` or `experiment.Runner`. That is the whole of the revision 007 announced, and 006b already carries it. Nothing in *this* spec builds a lifecycle: the runner is called directly, and by a worker later.

**An experiment executes in the provider sandbox, on a read-only mount.** The run mounts the instrument repository and attaches the dataset as a session resource, then executes `analysis.command`. It reuses `provider.Provider` unchanged — an experiment is a run with an empty rubric and no revision budget (`Rubric: ""`, `MaxRevisions: 0`), not a second execution substrate. Two tightenings of [006f](006f-taking-a-reproduction-out.md)'s token model, both structural:

- **No push credential ever enters an experiment sandbox.** An experiment reads an instrument; it has no business writing to the reproduction. The mount carries a `ReadOnly` intent, which makes 006f's two blast radii into three, the smallest of them being this one. As built, `ReadOnly` is an honest signal, not a guarantee the backend imposes: the managed-agents `github_repository` resource has no read-only field, so the enforcement is the *token's* GitHub scope — a read-only-scoped mount token. That scoping is 006f's still-open two-token split, so the seam expresses the intent today and a read-only token makes it real when 006f lands.
- **The dataset lands outside the mount**, as 006f's bounded-workspace rule already requires of every download, and results are written outside it too. So a researcher's uploaded data physically cannot be committed into a reproduction repository, which is the primary control behind 007's "uploaded data is not ours to redistribute".

Rejected, again and for the reason 007 gives: a container runner in the server. Instrument code is agent-written and uploaded data is untrusted; the managed sandbox is the component built to hold exactly that pair.

**The experiment worker executes; it does not repair.** The experiment task — data in `method/`, like every other instruction this project gives a worker — says: run the declared command on the given input, capture its real output, and stop. It does not debug the analysis, adjust it to fit the data, or substitute an approach that works.

This is the honesty rule of the whole platform pointed at a new target. An analysis that fails on conforming data is a *finding about the instrument*, and an agent that quietly fixes it has replaced the instrument with a different one and reported the result as the paper's. "It did not run, and here is the error" is a passing outcome, exactly as `UNVERIFIED.md`'s blockers are.

**Input is validated in the server, before a sandbox exists.** `analysis.input` from 007a declares columns, types, units, and which are required. The server checks an uploaded dataset against it and rejects a mismatch with a message naming the offending column. A run is never started to discover inside a script that column three is a string. This is cheap, it is a better error, and it keeps malformed input out of the sandbox entirely.

**An instrument with a null `analysis` cannot be experimented on, and the refusal is up front.** 007a makes non-extraction a first-class answer; the consequence here is that a reproduction may simply not be executable, and the platform says so when the experiment is created rather than after a run that had nothing to run.

**A `released`-source result is compared against `protocol.reported`, and the comparison is a report, not a verdict.** Re-running the paper's own data against the paper's own pipeline has a right answer, and 007a recorded it. So the platform lays the two side by side — recomputed statistic against reported statistic, with the delta — and stops there.

It does not conclude that the paper reproduced or failed to. That is [007](007-from-reproduction-to-replication.md)'s out-of-scope line and it holds for the same reason `procedure.md` §7 exists: the platform reports statistics; researchers draw and defend conclusions. A divergence may be a rounding convention, a software version, or a real error, and telling those apart is the work the report is *for*.

**Uploaded datasets are deletable, and deletion is real.** A researcher's data is theirs. Deleting a dataset removes the file, the artifacts derived from it, and the checksums — leaving the experiment row as a record that a run happened, with its inputs gone. Rejected: soft deletion, which would keep other people's data on our disk after they asked us not to.

## Interface (sketch)

```go
// internal/experiment: the domain model and the runner.
package experiment

type Source string // released | uploaded | synthetic | human

type Instrument struct { // resolved, never stored
	Repo   publish.Repo
	Commit string             // pinned; results attribute to this
	Decl   instrument.Instrument
}

type Experiment struct {
	ID, ProjectID string
	Instrument    InstrumentRef // repo + commit
	Source        Source
	DatasetID     string        // uploaded / collected; empty for released
	Command       string        // resolved from Decl.Analysis
	Outcome       Outcome       // ran | failed-to-run, with the real output
	Artifacts     []Artifact    // checksummed
}

// Run executes one experiment in the provider sandbox: read-only instrument
// mount, dataset (input) attached outside it, declared command, captured
// output. input is the bytes to analyse — the researcher's dataset for an
// uploaded source, the paper's released data for a released one.
func (r *Runner) Run(ctx context.Context, e *Experiment, inst Instrument, input []byte) (Outcome, error)

// Compare lays a released-source outcome against Decl.Protocol.Reported.
// It reports deltas; it does not judge them.
func Compare(o Outcome, reported []instrument.Reported) Comparison
```

## Acceptance criteria

1. An instrument resolves from a published repository at a pinned commit, and the store keeps no copy of `instrument.json` or `src/`. Asserted by resolving twice across a new commit and getting the pinned declaration both times.
2. Everything here runs with no database: `experiment.Store` and `ArtifactStore` have in-memory and filesystem implementations, and the full runner path is tested against them. No Postgres schema, migration, or job-kind wiring is in this spec's scope — those land with 006b, against these interfaces, and 006b's criterion 7 covers the lifecycle.
3. No push credential reaches an experiment sandbox: a test asserts the experiment run's mount is `ReadOnly` and carries the read-only-scoped mount token, never the organisation token. Full scope enforcement is the token's GitHub permission — 006f's still-open two-token split — so this asserts the seam's intent and the absence of the org token, which is the honest state until 006f lands.
4. Uploaded data cannot be committed into a reproduction repository. Asserted structurally — the dataset and the outputs are placed outside the mount — and by the resulting tree containing neither.
5. A dataset conforming to `analysis.input` is accepted and a non-conforming one is refused before any run starts, with the offending column named.
6. An analysis that fails on conforming data yields a recorded failure with its real output, and the run is not retried with a modified command. A test asserts the worker's instruction contains no license to repair, and that a failed outcome is a complete, reportable result.
7. An experiment against an instrument with `analysis: null` is refused at creation.
8. `Compare` reports every `reported` entry against its recomputed counterpart with a delta, and emits no pass/fail judgement — asserted on the type, so a later change cannot add one silently.
9. Deleting a dataset removes the file and its derived artifacts; the experiment row survives with inputs absent. Asserted by reading the store, not by a flag.
10. `verdict` and `rate` in the 002 manifest are untouched by every path in this spec — 007's criterion 3, tested here for the two offline sources.
11. No network and no credential: the runner is tested against the in-process provider and a fake forge.

## Out of scope

The Postgres implementation of `experiment.Store`, its migration, and the job `Kind` dispatch — all deferred to 006b, which is sequenced after this spec and already revised to expect them. The `synthetic` and `human` sources — declared in the `Source` type so the model is whole, implemented in [007c](007c-the-synthetic-pilot.md) and [007d](007d-hosting-a-study.md). The HTTP surface and UI for creating experiments and uploading datasets, which belong to 006a/006e once this exists. Project membership rows, which 006c gains under [007](007-from-reproduction-to-replication.md)'s revision. Publishing an experiment result back into a reproduction repository: `released`-source results are the only ones eligible and it is a later, separate decision. Judging whether a replication succeeded. Any statistical work of our own — the analysis is the paper's pipeline, and this spec runs it rather than improving on it. Datasets too large for an attached session resource, and the streaming or chunked path they would need.
