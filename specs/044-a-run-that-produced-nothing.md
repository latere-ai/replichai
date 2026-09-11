---
title: "A run that produced nothing must not report success"
status: built
---

# 044 — A run that produced nothing must not report success

**Status:** built. All four children shipped.

**Scope:** the incident of 2026-08-22, its four independent defects, and the four children that fix them. No new product surface.

**Depends on:** [005](005-transcript.md), [006f](006f-taking-a-reproduction-out.md), [006i](006i-server-side-mount-and-publish.md), [009](009-secret-containment-when-the-agent-commits-live.md), [021a](021a-the-findings-as-data.md), [041e](041e-anthropic-conforms-to-the-session-contract.md).

## The incident

A session against `10.1145/359784.359832` ran twenty minutes, cost $2.21, and produced a repository containing only what the system itself wrote:

```
caad86a 07:59:11 chore: record that the findings could not be read   system
d26c799 07:59:09 chore: reproduction transcript                      system
1f77105 07:59:00 chore: reproduction manifest                        system
ac60e5f 07:39:09 chore: record paper identity                        system
e99452b 07:39:06 Initial commit                                      system
```

The session showed `FAILED` with `retain evidence: commit transcript: PATCH …/git/refs/heads/main: unexpected status 422`, and `UNVERIFIED.md` said *the run did not write verdict.json*. Both statements are true and neither explains what happened. Four separate things went wrong, and each one alone would have been enough to lose the run.

## The four defects

```
agent writes /workspace/repro ──git commit──▶ sandbox         ✗ never pushed        (044a)
             │
             └──cp──▶ /mnt/session/outputs ──▶ session files   ✗ server drops them  (044b)

worker A ─drives─▶ job ◀─adopts─ worker B    ✗ lease says "changed", not "alive" (044c)

FinalizeLive ─▶ "the run did not write verdict.json"  ✗ the repository is empty   (044d)
```

**1. The agent committed and never pushed.** `transcript.jsonl` records five `git add -A && git commit` calls in `/workspace/repro` and no `git push` anywhere. Event 180's own tool result says so: *"Your branch is ahead of 'origin/main' by 1 commit. (use "git push" to publish your local commits)"*. `mountProtocol` asks for a push in prose; nothing enforces it. The sandbox was released and every commit was lost with it, including the `verdict.json` the agent did write, at `/workspace/repro/verdict.json`.

**2. The server discards the artifacts channel.** The agent also copied its deliverables out (events 164, 168, 310, 320, 408: `cp /workspace/repro/{README.md,REPRODUCIBILITY.md,verdict.json,instrument.json,src/*} /mnt/session/outputs/`), and those copies succeeded. Those files become session files, reachable through `EvidenceReader.Fetch`. The CLI harvests them (`collectManagedArtifacts` in `internal/audit/session.go`). The server's `Retain` hook binds `evidence.Artifacts` and then calls `FinalizeLive(… events …)` only. This run was recoverable and the server discarded it.

**3. Two drivers on one job.** Two replicas run in production. `pjtqq` claimed the job at 07:39 and drove it correctly. At 07:44:35 `b6xzt` logged `worker: sweep adopted 1 stranded run(s)` and adopted the same live run. `LeaseTTL` is five minutes and the lease is refreshed only by `Store.Observe`, which the controller calls only when `!sameObservation(previous, status)`, so five quiet minutes look like a dead driver. Both finalised at 07:59; one won the ref update, the loser got `422` (not a fast-forward) and recorded `FAILED` over the other's success. `reconcile`'s own comment predicts this outcome exactly; the guard failed because it treats a change as proof of liveness.

**4. The report names the wrong problem.** "the run did not write `verdict.json`" reads as *the findings were not machine-readable*. The truth was *the repository is empty*. A reader of that repository cannot tell a run that produced a reproduction with prose-only findings from a run that produced nothing.

## The children

| Child | Fixes | What it does |
| --- | --- | --- |
| [044a](044a-the-mount-is-pushed.md) | 1 | The mount protocol demands a push after every commit, and the rubric grades the pushed state as evidence. |
| [044b](044b-the-server-harvests-the-session.md) | 2 | The server's `Retain` hook commits what the session produced, through the same `VerifyTree` gate the mount has. |
| [044c](044c-a-lease-says-it-is-alive.md) | 3 | The worker heartbeats every job it drives, and yields the one it has lost. |
| [044d](044d-an-empty-repository-says-so.md) | 4 | Finalize distinguishes *no findings* from *no reproduction*, and says which. |

## Why 044a and 044b are both needed

They fail differently. 044a is prevention and costs nothing, but it depends on the agent following the instruction, which is exactly the assumption that broke. 044b is recovery and does not depend on the agent's discipline, but it only works when the agent put something in the outputs directory, and it moves files across the redistribution boundary that the mount was built to be (006f: *only what lands in it is published*). Neither alone closes the gap: with only 044a an agent that ignores the instruction still spends the money and produces nothing; with only 044b a run that never copied anything out is still lost.

044b must not weaken the boundary. Everything it commits goes through `publish.VerifyTree` first, and a flagged path is left out and named, rather than committed and reported.

## Not in scope

- Pushing on the agent's behalf. `managed.Runtime` is `Events` / `ListArtifacts` / `FetchArtifact` / `Cancel` / `Release` with no exec, so nothing outside the sandbox can reach `/workspace/repro`. Harvesting the mount directory is not possible and this spec does not claim otherwise.
- Retrying a failed run. A run that produced nothing has still spent its money; what changes here is that it is reported accurately.

## Outcome

All four shipped, one commit each plus the store change 044c needed first.

| Child | Commits | What landed |
| --- | --- | --- |
| 044a | `044a: the mount is pushed…` | `mountProtocol` states the push as its own step with its consequence, `method/rubric.md` gains criterion 9, and a test refuses to let the push slide back into the commit sentence. |
| 044b | `044b: the server harvests…` | `publish.CommitOutputs` and `harvestOutputs` in the server's `Retain` hook. |
| 044c | `044c: a lease can be refreshed…`, `044c: a driver heartbeats…`, `044c: a commit retries…` | `Store.Touch` on both stores and the contract test; `Worker.heartbeat`/`beat`; the fast-forward retry in `github.CommitFiles`. |
| 044d | `044d: an empty repository says so` | `holdsReproduction`, `Findings.Reproduction`, and the second wording for `UNVERIFIED.md`. |

Seven things the design did not anticipate.

**The incident's test is exact, not analogous.** `TestRetainHarvestsARunThatNeverPushed` runs the real `engineRunner` over a repository holding only `README.md` and `.replichai/paper.json` and a runtime whose files exist only as session artifacts. Without the harvest it fails by printing the incident's own manifest, `Present:false Problems:[the run did not write verdict.json]`, and the incident's own `UNVERIFIED.md`, character for character. The regression test and the post-mortem are the same artifact.

**The heartbeat belongs to the worker, not the controller.** The design assumed a new `managed.Hooks` member driven from the controller's poll loop. `w.running` already names every job the process drives, under the lock `driving` and `interrupt` share, so a ticker over it needs no contract change. It also covers the resolving phase, which no observation has ever reached and which `reconcile`'s own comment had already flagged as a cold lease waiting for a sweep. The narrower fix also covered more.

**`Store.Observe`'s doc comment described the bug.** It claimed an observation "arrives on every poll, so the lease stays fresh for as long as the run does without a second heartbeat to keep in step." That was the design; the controller's `!sameObservation` guard made it false, and nothing rechecked the sentence. The comment now says what the code does, and points at `Touch` for what it used to promise.

**Two of the fakes were hiding the behaviour under test.** `fakeLiveForge` and `fakeForge` wrote committed files into a map and never into the tree `TreePaths` returns, so a finalize running after a harvest read the pre-harvest tree, and the harvest e2e passed for the wrong reason, because `readFindings` reads the file map. Both fakes now track what they commit, which let the ordering assertion become real: move `harvestOutputs` after `FinalizeLive` and the test fails with the incident's own `UNVERIFIED.md` text.

**One assertion the contract test could not have caught by accident.** `beat` interrupts any job whose `Touch` reports `held=false`, and almost every production job arrives through `Claim`, not `Adopt`. `PGStore.Claim` does write `adopted_by`, but the existing lease assertions could not tell: `Adopt`'s predicate refuses a just-claimed row whether or not the holder was named, while `Touch`'s does not. A `Claim` → `Touch` assertion is now in the contract, because without it a claim that recorded only a timestamp would make every ordinary run yield itself within a third of the lease.

**Criterion 9 had to say when it applies.** The mount protocol is appended only when a repository is mounted, but the rubric is passed to the grader unconditionally, so a local run with no mount would have been graded against a remote it does not have, and sent back to revise against an impossible criterion. The criterion now names the condition and passes trivially without one.

**The `422` retry is justified for an ordinary reason.** It was scoped as hardening against the two-driver race, where it does not help: both drivers simply commit. What it covers is a single driver: this repository writes the manifest through the Contents API and the transcript through the Git Data API moments later, and GitHub's ref reads can lag that write. That path had been one replica lag away from the same `422` all along, with no second driver involved.
