# 044c — A lease says the driver is alive, not that something changed

**Status:** built.

**Scope:** `Store.Touch`, a worker heartbeat over the jobs it drives, yielding a lease it has lost, and a fast-forward retry in `github.CommitFiles`.

**Depends on:** [044](044-a-run-that-produced-nothing.md), [006b](006b-job-model-and-persistence.md), [041b](041b-one-managed-session-lifecycle.md).

## Problem

Two replicas run in production, and the lease is what keeps them off each other's runs. `Store.Observe` is the only thing that refreshes it, and its own doc comment explains why that is enough:

> An observation is the driver saying it is still there, and it arrives on every poll, so the lease stays fresh for as long as the run does without a second heartbeat to keep in step.

It does not arrive on every poll. The controller calls the `Status` hook only when the observation differs from the last one:

```go
if hooks.Status != nil && !sameObservation(previous, status) { … }
```

A run that is thinking — no state change, no new usage — is silent, and silence is indistinguishable from a dead process. `LeaseTTL` is five minutes. In the incident `pjtqq` claimed the job at 07:39 and drove it correctly; at 07:44:35 `b6xzt` swept, found a lease five minutes and twenty-six seconds old, and adopted a run that was perfectly healthy. From then on two processes polled one backend session, and both of them finalized it:

```
07:59:00  A: PutFile   replication.json      → ok
07:59:09  A: CommitFiles transcript          → ok        head moves
07:59:09  B: CommitFiles transcript          → 422        parent is stale
07:59:11  A: PutFile   UNVERIFIED.md         → ok
          B: retain evidence: commit transcript: … 422 → job recorded FAILED
```

The row ended `failed` describing a publish that had in fact succeeded. `reconcile`'s comment predicts precisely this — *"two drivers poll it, close it and publish it, and the one that loses the race records a failure over the other's success"* — and the guard it describes did not hold, because the lease measures change rather than life.

The same gap covers the resolving phase, which `reconcile` already worries about in prose: a paper fetch and a repository creation report no status at all, so a slow resolve has always been a cold lease waiting for a sweep.

## What changes

**`Store.Touch(ctx, id, worker) (held bool, err error)`.** It bumps the lease timestamp and nothing else, and it refuses to steal: the update matches only a non-terminal row whose `adopted_by` is this worker. `held = false` means the row is terminal or somebody else now holds it. In Postgres it is one `UPDATE … WHERE id=$1 AND adopted_by=$2 AND phase NOT IN ('done','failed')`; in memory it is the same predicate under the lock. It is in the store contract test, so both implementations answer alike.

**The worker heartbeats what it is driving.** `w.running` already names every job this process drives, under the lock that `driving` and `interrupt` share. A ticker at a third of the lease TTL touches each of them. This lives entirely in `internal/job`: no new `managed.Hooks` member, no controller change, and it covers *every* phase — including the quiet resolve that no observation has ever reached.

**A driver that has lost its lease yields.** When `Touch` reports `held = false` for a job still in `w.running`, another process owns the row; this one interrupts its own run rather than driving to a second finalize. This is the guarantee, not the heartbeat: the heartbeat makes a wrongful adoption rare, and yielding makes a wrongful adoption harmless. A terminal row also reports `held = false`, which is the same instruction — stop driving — and the run's own terminal write is already refused by the state machine.

**`CommitFiles` retries a stale parent.** A `422` on the ref update means the branch moved under it. Re-reading the ref and rebuilding the commit costs three requests and covers two real cases: GitHub's ref reads lagging a Contents-API write by a moment, and this repository's own manifest commit landing immediately before the transcript. It is hardening and the spec says so plainly — in a genuine two-driver race a retry only lets both drivers commit, and the lease is what has to prevent that.

## Acceptance

- `Touch` refreshes a lease held by the caller, reports `held = false` for a lease held by another worker or a terminal row, and does not modify `adopted_by`.
- The store contract test covers `Touch` for `MemStore` and `PGStore` alike.
- A worker driving a job that emits no observation keeps its lease across more than one TTL, and a second worker's sweep does not adopt it. This is the test the incident asks for.
- A worker whose job is adopted elsewhere interrupts its own run at the next heartbeat.
- `CommitFiles` re-reads the ref and retries once on a `422`, and gives up with the original error if the retry also fails.

## Not in scope

Raising `LeaseTTL`, which widens the window without closing it. Making finalize itself mutually exclusive across replicas — the lease is that mechanism, and this spec repairs it rather than adding a second one beside it.
