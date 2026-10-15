---
title: "One run is one draw"
status: designed
---

# 046 — One run is one draw

**Status:** runs per paper, schema 2 of `verdict.json` and the run record built; the rest designed.

**Scope:** making N runs of one paper a unit the platform can run, record and report on; treating the environment as something every run records and a study varies on purpose; and reporting what a set of runs shows rather than presenting one run's verdict as the paper's.

**Depends on:** [045h](045h-many-papers-at-once.md) (the batch), [021a](021a-the-findings-as-data.md) (the findings schema), [003](003-reproduction-registry.md) (the registry), [045f](045f-the-grader-cannot-edit-what-it-grades.md) (the grader), [044](044-a-run-that-produced-nothing.md) (runs that produced nothing), [005](005-transcript.md) (the transcript).

## Problem

A session's verdict is presented as the paper's verdict. It is one draw. Forty-nine runs of three papers on 10 and 11 September 2026 ([`docs/repeat-runs-pilot.md`](../docs/repeat-runs-pilot.md)) showed how far apart draws land and why:

- Two agent systems ran every paper under one method digest. Component counts varied about eight times more under one than the other, and the systems placed the inner/outer boundary differently. The manifest the registry reads records neither system.
- Which copy of the paper a run could read depended on its network policy, so identical computations were compared against different reported values.
- Identical disagreements with the paper were labeled *verified* by one run and *partial* by another.
- The in-loop grader's revision changed measured fields before they were recorded, and the number of rounds depended on the system.
- Half the findings files were unreadable to the system over one mistyped field, and a project file could not hold two runs of one paper without the runs colliding.

The environment moves the result as much as anything the pilot measured. A platform that reports one run as the answer, and records too little to say what that run ran under, reports the environment's luck as a fact about the paper.

## Decisions

### 1. Runs per paper are a field of the project file (built)

`runs: N` on the file or on one paper; a paper listed twice is two runs. Each run is its own session, repository and output directory, and resume matches a run by paper and run number. Before this, the batch keyed everything by the paper reference, so a resumed study silently dropped its failed and pending repeats and two concurrent runs could share a repository.

Not yet carried through: `replichai push` files each run as its own session under the paper, which is right, but the record it files has no run number, so the app cannot say which run of a study a session was. The manifest does say (decision 3); the app's own record does not yet.

### 2. Schema 2 of `verdict.json` (built)

A claim carries a list of mismatches, each with the paper's value and the run's value as text and, where they are single numbers, as numbers with a unit. Text fields accept whatever JSON the agent wrote and keep it. Each row is decoded on its own, so a malformed row is a problem at that row instead of an empty file, and `released` gains `not_fetched` for a fetch the run was not allowed or able to make. Schema 1 files still read. The method changed, so its digest moved: runs before and after are not comparable under one digest, which is what the digest is for.

### 3. Every run records what it ran under, in the manifest (built)

`replication.json` carries a `run` block. It says where the run sits (the project, and which run of the paper it was, of how many), what it ran on (the runtime and session, the harness and its version, the model the runtime resolved and the one that was asked for, the sandbox, the network policy with the size and a digest of its allowlist), what it was given (a prompt digest over the exact instructions, task and rubric, and the build of replichai that ran it), and how it went (start and finish, the terminal state and its reason, the grading rounds, the tokens and the cost the harness reported). `publish.RunOf` builds it from the session's status for the CLI and the server alike. A session on the operator's machine adds the harness, the sandbox and the rounds from its own session directory, so they are right even when the run committed nothing.

A manifest is written for every run that was given a repository, including one that failed before its session existed, whose record says `failed` for `not_started`. The attempt count of a study is then a fact on record rather than an inference from timestamps.

The manifest schema stayed at 1, as it did for the findings block: the record is optional and additive, and every manifest published before it still reads.

A run is finalized more than once, and each finalize knows a different amount, so the record is merged rather than overwritten. A finalize that knows nothing keeps the earlier record. One of the same session fills what it does not know from the earlier record: `replichai finish` has the session's status and not the model that was asked for or the prompt, which only the starting process knew. An attempt that never started leaves the record of a session that did run, whose work the repository still holds. A different session replaces the record whole.

Four things the record does not carry, each for a reason:

- **The paper copy and the artifact snapshot.** The system does not hand the run its paper yet (decision 5), and a hash of whatever the agent fetched would misstate who fixed the input.
- **An error message.** The reason is one of a closed list. A manifest may be made public, and nothing has scanned a runtime's error text for a path or a credential.
- **The prompt digest of a resumed session.** The specification was fixed by the process that started it. Computing one from the method as it is now would describe a session that may have been given something else, so it is left empty.
- **A study identifier.** The project and the run number place a run well enough for one project file. A study that spans several needs an identifier of its own, which belongs with the study view (decision 9). Until then `project` holds two kinds of value: the name a project file gives, for a run on the operator's machine, and the app's project id, for a run the server started.

Still short: a hosted runtime reports no harness version, sandbox or grading rounds, so its record has the model, the times and the ending and little else. A session that started and then never reached the step that finalizes it leaves no manifest, though its reference is kept and `replichai finish` writes one. And the record `replichai push` files with the app has no run number yet.

### 4. The first graded attempt is kept (designed)

Each grading round snapshots `verdict.json` under `grade/`, and a study may run with grading but no revision. Analysis reads round 0: what the run produced before the rubric pushed it. Revisions still happen for a reproduction a person will read; they are recorded, not hidden.

### 5. Every run of a study gets the same inputs (designed)

The system fetches the paper once per study, hashes it, and mounts it read-only. Released artifacts are snapshotted the same way. The network policy is a named profile, not whatever the harness allows. A run that still cannot reach something records `not_fetched`, which is a fact about the run.

### 6. The system decides whether a number matched (designed)

A study's claims are frozen per paper by two people, with their agreement reported, and handed to every run. The run writes what it observed for each claim; the system compares it with the reported value against a tolerance registered per claim type. The agent's own label stays in the file as its account and is never the outcome analyzed.

### 7. A blocker names its cause (designed)

A blocked claim records one of: environment (refused fetch, dependency, time or compute limit), paper (unspecified, contradictory), artifacts (missing, dead, broken), or agent. Environment-caused failures can then be separated from paper-caused ones, which is the distinction the pilot could not make.

### 8. Schema problems reach the revision deterministically (designed)

The local runtime runs `verdict.Validate` after the worker stage and gives the problems to the judge and to the revision, so a file the system cannot read is never passed by a model's reading of the rubric. The hosted runtime grades on the vendor's side and cannot take this check inside its loop; it keeps the finalize-time check.

### 9. Report a study, not a run (designed)

A study screen shows, per claim, how many runs reproduced it under each environment profile, the paper's instability (the chance two runs disagree about a claim), and the difference the environment made. A single session's verdict says it is one run. The statistics are in [`docs/dispersion-model.md`](../docs/dispersion-model.md). The screen is prototyped and reviewed before it is wired.

## The construct

Decided on 15 October 2026: a study's primary measure is per-claim reproducibility given the released materials. For each result a paper reports, the outcome is whether a run reproduced it from the paper and what its authors released. The environment (agent system, model, network, inputs) is recorded on every run and varied on purpose across a study, so its effect is measured rather than assumed. This is what a corpus of papers that released code and data, with outcomes verified by people, can validate.

Underspecification of the prose is a secondary claim. It is made only through the designed contrasts in the model document (the same papers with one stated detail withheld, and with code withheld), never read off run-to-run spread.

For the platform, decisions 5 and 6 carry this measure: the released materials are snapshotted as part of every run's fixed inputs, and the outcome analyzed is the system's match against the claim list, not the agent's label.

## Acceptance criteria

1. A project file with `runs: 3` for one paper produces three sessions, three repositories and three output directories, and a resume after one failure runs exactly the failed and pending runs. (Built.)
2. Every pilot mismatch shape and `not_fetched` validate under schema 2; one malformed row is a problem at that row and the rest of the file survives; schema 1 files still validate. (Built. Of the 29 findings files the pilot runs published, 27 validate and all 214 mismatches are read; the other two record a search as found with no address, which is a rule the files break.)
3. A manifest names system, model, network profile, prompt digest, project, run number, terminal reason and grading rounds, and one exists for a run that never started. (Built, for a session on the operator's machine; a hosted session's record is sparser. The paper copy hash waits for decision 5.)
4. A study can run with grading and zero revisions, and round 0's findings are on record for every run.
5. `tools/repeat-runs/analyze.py` reproduces its report from manifests alone, with no transcript reading. (Partly: it reads the agent system and how a run ended from the run record, and falls back to `environment.json` for runs finalized before the record.)
6. A study screen shows per-claim reproduction counts per profile, and a session verdict says it is one run.

## Not in scope

Moving the engine onto the Latere platform's sessions and runtimes; that comes after the observed issues are resolved. The outer loop. Choosing the corpus, which is a research decision.
