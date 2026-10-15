---
title: "One managed-session lifecycle"
status: implemented
---

# 041b — One managed-session lifecycle

**Status:** implemented.

**Scope:** add one controller for start, durable handoff, wait/retry, evidence retention, cancellation, and release; migrate audit, experiment runner, and model cohort away from assembling or polling runtime resources themselves. Preserve their product outputs.

**Depends on:** [041a](041a-one-managed-session-contract.md), [005](005-transcript.md), [006f](006f-taking-a-reproduction-out.md), [014a](014a-managed-runs-close-truthfully.md).

## Problem

Audit, experiments, and model cohorts each resolve a model, ensure a workspace, ensure a worker, start a run, poll, and close. Their failure policies already disagree: audit tolerates transient poll failures while experiment paths fail on the first one. Cleanup timeouts are copied. A handle is observable to the server only after a poll, leaving a crash window after remote start. Startup can orphan uploaded files or a created session when the next vendor call fails.

`CloseRun` also combines three different acts: interrupt live work, archive/delete remote resources, and make evidence potentially unreadable. Audit defers it inside `Run`, while server publication asks for the transcript only after `Run` returns. Anthropic archive happens to retain events; an OpenAI release that deletes a container need not.

The split lands in `managed.Runtime`, not in `provider.Provider`. `CloseRun` survives at the provider boundary that [014a](014a-managed-runs-close-truthfully.md) designed, and stays the teardown call on the legacy path: `internal/managed/legacy` maps both `Cancel` and `Release` onto it, `internal/audit` still polls and closes a `provider.RunHandle` directly, and `engineRunner.Close` falls back to it when there is no controller or no locator. This spec wraps 014a; it does not retire it.

## Decision: one controller owns the ordering

`internal/managed.Controller` is the only application-facing lifecycle API. It receives a `RuntimeRouter`, clock, retry policy, cleanup timeout, and observer. It exposes complete execution and restart entry points rather than the adapter's individual methods:

```go
type Hooks struct {
    Started func(context.Context, SessionRef) error
    Checkpoint func(context.Context, previous, next SessionRef) error
    Status  func(context.Context, Status) error
    Retain  func(context.Context, EvidenceReader) error
}

func (c *Controller) Execute(ctx context.Context, spec SessionSpec, h Hooks) (Result, error)
func (c *Controller) Resume(ctx context.Context, ref SessionRef, h Hooks) (Result, error)
func (c *Controller) Cancel(ctx context.Context, ref SessionRef, h Hooks) (Result, error)
```

The three operations follow one state machine:

```
validate -> start -> Started(ref) -> wait terminal -> Retain(evidence) -> release
                    ^ persisted here       |              |
context cancelled ----------------------> cancel ----------+
```

`Started` runs synchronously immediately after `Runtime.Start` and before the first poll. The server hook persists the full `SessionRef`; CLI and pure engine callers may use a no-op. If `Started` fails, the controller cancels, retains whatever evidence exists, releases, and returns the persistence error joined with cleanup errors. No successful remote start can become invisible merely because a poll did not happen.

When a runtime advances its internal multi-stage orchestration, `Poll` returns a status containing the same runtime/id and a higher-revision locator. `Checkpoint(previous, next)` is synchronously compare-and-swap persisted before the controller emits status or calls the adapter again. A checkpoint failure stops progression and enters the same cancel/retain policy. Adapters use a deterministic idempotency key for every stage-creating mutation, so a crash after the remote write but before checkpoint persistence replays to the same remote object rather than duplicating it.

`Status` receives the initial pending observation and every distinct later observation. Observer failure is treated as a caller failure: cancel, retain, release. Duplicate runtime observations are not emitted.

`Retain` is the evidence handoff. `EvidenceReader` exposes terminal/partial status, normalized events, artifact metadata, and bounded artifact fetch while the remote resources still exist. For a reproduction, the hook writes/commits the transcript and completes publication evidence. For an experiment or cohort, it stores the transcript and required output artifacts. Only a successful `Retain` permits release.

If retention fails, the reference remains durable and the controller deliberately does **not** release. The job records `cleanup_pending` with the retention error, and reconciliation retries `Resume`/retention. Preserving recoverable evidence is more important than eager remote cleanup. A later explicit admin purge may release it under the existing destructive-action rules.

## Waiting and failure policy

One `WaitPolicy` governs all callers:

- poll immediately, then exponential delay from 2 seconds, capped at 15 seconds;
- retry only errors marked temporary, respecting `Retry-After` when present;
- cap consecutive transient failures (default 5) and elapsed silence (default 2 minutes), both configurable for tests;
- reset the consecutive count after a successful poll;
- stop immediately on a terminal status, context cancellation, a permanent error, or violated status invariant;
- use an injected clock and jitter source so tests contain no sleeps.

The controller enforces reference equality, state monotonicity, terminal stability, usage monotonicity, and graded-verdict invariants on every observation. An adapter contract violation is permanent and names the runtime.

Runtime `failed` is a normal terminal observation, not a poll error. It still passes through retention and release, and its final cumulative usage is returned for settlement. A poll outage that exhausts policy triggers cancellation; cancellation failure does not erase the original outage. The two exhaustion budgets are recorded separately: exhausting the silence budget yields `ReasonTimedOut`, exhausting the consecutive-failure budget yields the runtime failure reason, and silence takes precedence when both are exhausted on the same poll. A session that stopped answering timed out; it did not fail.

## Cancellation and release

`Cancel` requests that live execution stop. It is idempotent: already terminal/not-found-after-terminal is success. After a cancellation request, the controller polls within the cleanup timeout for a terminal observation when the runtime can report one, captures partial evidence, calls `Retain`, and then calls `Release`.

`Release` frees only resources the session owns. It is idempotent and safe after process restart. It never edits a mounted Git repository or deletes durable artifacts retained by the application. Adapter-owned temporary uploads, agents/environments created only for the run, containers, and temporary workspaces are listed in the adapter locator and released in dependency order.

Cleanup uses `context.WithoutCancel` plus a bounded cleanup timeout, so cancelling the user's request does not cancel the cleanup call. Errors are joined in causal order: execution/retention error first, then cancel, terminal wait, and release. A successful run whose release fails returns its successful result plus a typed `CleanupError`, so the job can settle usage and schedule cleanup without reporting falsely that the reproduction failed.

## Transactional start

`Runtime.Start` owns a transaction-like creation ledger internally. Every successful remote mutation appends a compensating action. On a later failure it compensates in reverse order under the cleanup timeout. It returns an error only after compensation has been attempted and joins any rollback error.

Examples:

- Anthropic: upload A, upload B, create session, send kickoff. Failure at upload B removes A; failure at kickoff interrupts/archives the session and removes session-owned uploads.
- OpenAI: create container, upload inputs, create response. Failure creating the response deletes the owned container and uploads.
- Latere: create/attach Cella sandbox, begin Topos trace (Cella is Latere's sandbox control plane, Topos its agent runtime, and Drive its file storage service). Failure after attach stops the run and releases the sandbox/Drive claim through their owning APIs.

Pre-existing repositories, Drive workspaces, and caller-owned resources are references, not owned resources, and are never compensated. Ownership is encoded per locator entry and covered by adapter tests.

## Consumer migration

- `internal/audit` builds one `SessionSpec` from paper prompt, repository, attachments, sandbox policy, and non-nil evaluation. It no longer imports workspace/worker/run refs or loops over `Poll`.
- `internal/experiment.Runner` builds an ungraded spec (`Evaluation: nil`) and retains its output artifact. It no longer encodes “ungraded” as empty rubric and zero revisions.
- `internal/experiment` model cohort uses the same controller and wait policy, with its cohort prompt and ungraded spec.
- `cmd/server/runner` persists `Started(ref)` before emitting the running observation. Resume passes the stored reference. Publication/transcript capture moves into `Retain`; it never asks a released runtime for evidence.
- CLI finish and server live transcript accept a `SessionRef`, not a bare run id. A live transcript is a read through the router and does not release.

## Acceptance criteria

1. Audit, experiment runner, and model cohort construct one `SessionSpec` and call only `Controller.Execute`/`Resume`; no consumer names workspace, worker, agent revision, environment, or vendor session.
2. A recording fake proves the exact order `Start → Started → Poll* → Retain → Release`, with `Started` before the first poll, every advanced locator checkpointed before the next call, and evidence readable throughout `Retain`.
3. A server restart test starts through runtime A, persists the reference before any poll, reconstructs the runner, resumes, retains transcript/artifacts, and releases successfully.
4. Table tests inject identical temporary/permanent poll failures into all three consumers and prove the same retry, cancellation, retention, and error-joining behavior with a fake clock.
5. Cancellation tests prove `Cancel` precedes terminal wait/retention/release, partial events are retained, repeated cancel/release succeeds, and a cancelled request still gets the cleanup timeout.
6. Retention failure leaves remote resources and the durable reference intact, marks cleanup pending, and succeeds when reconciliation retries; it never calls release early.
7. Startup failure at every mutation boundary compensates all owned resources in reverse order. A mocked Anthropic HTTP test proves failed second upload, failed session creation, and failed kickoff leave no orphan.
8. A successful execution with release failure returns the result and cumulative usage plus `CleanupError`; exactly-once settlement still occurs.
9. Existing audit and experiment memory end-to-end tests remain green, and a new controller-level end-to-end test covers start through retained transcript/artifact and release.
10. New/changed controller code exceeds 90% statement coverage.

## Out of scope

- Choosing which runtime receives a new session and persisting its schema: 041c.
- Changing usage/rate structures: 041d.
- Adapter migration details: 041e.
- A general distributed workflow engine. This controller owns one managed session and its evidence boundary.

## Outcome

`managed.Controller` centralizes validation, start notification, checkpointing, monotonic polling, transient retry, cancellation, evidence retention, and release. Audit, experiment runner, and model cohort construct one `SessionSpec`; server and CLI use the same controller. Controller tests cover ordering, restart, cancellation, retention failure, cleanup error joining, and complete memory-runtime execution.
