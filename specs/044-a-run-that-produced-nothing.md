# 044 — A run that produced nothing must not report success

**Status:** designed; children carry the work.

**Scope:** the incident of 2026-08-22, its four independent defects, and the four children that fix them. No new product surface.

**Depends on:** [005](005-transcript.md), [006f](006f-taking-a-reproduction-out.md), [006i](006i-server-side-mount-and-publish.md), [009](009-secret-containment-when-the-agent-commits-live.md), [021a](021a-the-findings-as-data.md), [041e](041e-anthropic-conforms-to-the-session-contract.md).

## The incident

A session against `10.1145/359784.359832` ran twenty minutes, cost $2.21, and produced a repository containing nothing but what the system itself wrote:

```
caad86a 07:59:11 chore: record that the findings could not be read   system
d26c799 07:59:09 chore: reproduction transcript                      system
1f77105 07:59:00 chore: reproduction manifest                        system
ac60e5f 07:39:09 chore: record paper identity                        system
e99452b 07:39:06 Initial commit                                      system
```

The session showed `FAILED` with `retain evidence: commit transcript: PATCH …/git/refs/heads/main: unexpected status 422`, and `UNVERIFIED.md` said *the run did not write verdict.json*. Both statements are true and neither is the story. Four separate things went wrong, and each one alone would have been enough to lose the run.

## The four defects

```
agent writes /workspace/repro ──git commit──▶ sandbox         ✗ never pushed        (044a)
             │
             └──cp──▶ /mnt/session/outputs ──▶ session files   ✗ server drops them  (044b)

worker A ─drives─▶ job ◀─adopts─ worker B    ✗ lease says "changed", not "alive" (044c)

FinalizeLive ─▶ "the run did not write verdict.json"  ✗ the repository is empty   (044d)
```

**1 — The agent committed and never pushed.** `transcript.jsonl` records five `git add -A && git commit` calls in `/workspace/repro` and no `git push` anywhere. Event 180's own tool result says it: *"Your branch is ahead of 'origin/main' by 1 commit. (use "git push" to publish your local commits)"*. `mountProtocol` asks for a push in prose; nothing enforces it. The sandbox was released and every commit went with it — including the `verdict.json` the agent really did write, at `/workspace/repro/verdict.json`.

**2 — The server throws away the artifacts channel.** The agent also copied its deliverable out (events 164, 168, 310, 320, 408: `cp /workspace/repro/{README.md,REPRODUCIBILITY.md,verdict.json,instrument.json,src/*} /mnt/session/outputs/`), and those copies succeeded. Those files become session files, reachable through `EvidenceReader.Fetch`. The CLI harvests them — `collectManagedArtifacts` in `internal/audit/session.go`. The server's `Retain` hook binds `evidence.Artifacts` and then calls `FinalizeLive(… events …)` only. This run was recoverable and the server discarded it.

**3 — Two drivers on one job.** Two replicas run in production. `pjtqq` claimed the job at 07:39 and drove it correctly. At 07:44:35 `b6xzt` logged `worker: sweep adopted 1 stranded run(s)` and adopted the same live run. `LeaseTTL` is five minutes and the lease is refreshed only by `Store.Observe`, which the controller calls only when `!sameObservation(previous, status)` — so five quiet minutes read as a dead driver. Both finalized at 07:59; one won the ref update, the loser got `422` (not a fast-forward) and recorded `FAILED` over the other's success. `reconcile`'s own comment predicts this outcome word for word; the guard failed because it conflates *changed* with *alive*.

**4 — The report names the wrong problem.** "the run did not write `verdict.json`" reads as *the findings were not machine-readable*. The truth was *the repository is empty*. A reader of that repository cannot tell a run that produced a reproduction with prose-only findings from a run that produced nothing at all.

## The children

| Child | Fixes | What it does |
| --- | --- | --- |
| [044a](044a-the-mount-is-pushed.md) | 1 | The mount protocol demands a push after every commit, and the rubric grades the pushed state as evidence. |
| [044b](044b-the-server-harvests-the-session.md) | 2 | The server's `Retain` hook commits what the session produced, through the same `VerifyTree` gate the mount has. |
| [044c](044c-a-lease-says-it-is-alive.md) | 3 | The worker heartbeats every job it drives, and yields the one it has lost. |
| [044d](044d-an-empty-repository-says-so.md) | 4 | Finalize distinguishes *no findings* from *no reproduction*, and says which. |

## Why 044a and 044b are both needed

They fail differently. 044a is prevention and costs nothing, but it depends on the agent doing as it is told — which is exactly the assumption that broke. 044b is recovery and does not depend on the agent's discipline, but it only works when the agent put something in the outputs directory, and it moves files across the redistribution boundary that the mount was built to be (006f: *only what lands in it is published*). Neither alone closes the hole: with only 044a a stubborn agent still burns the money and produces nothing; with only 044b a run that never copied anything out is still lost.

The boundary is what 044b must not weaken. Everything it commits goes through `publish.VerifyTree` first, and a flagged path is left out and named, rather than committed and reported.

## Not in scope

- Pushing on the agent's behalf. `managed.Runtime` is `Events` / `ListArtifacts` / `FetchArtifact` / `Cancel` / `Release` with no exec, so nothing outside the sandbox can reach `/workspace/repro`. Harvesting the mount directory is not available and this spec does not pretend otherwise.
- Retrying a failed run. A run that produced nothing has still spent its money; what changes here is that it is reported as what it was.
