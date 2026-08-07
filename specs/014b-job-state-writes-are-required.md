# 014b — Job-state writes are required

**Status:** built.

**Scope:** stop the worker from continuing after a run observation or terminal transition failed to persist. This is the durable lifecycle only; business-level best-effort enrichment is unchanged.

**Depends on:** [006b](006b-job-model-and-persistence.md), [011](011-what-a-session-costs.md), [014a](014a-managed-runs-close-truthfully.md).

## Problem

The worker ignores `Store.Observe` and final `Store.Advance` errors. Losing the first observation loses the managed session id needed for restart reconciliation and cancellation; losing usage undercharges the run. Losing the final `done` write strands a successfully published job while `Process` reports success. Even the fallback failure write is discarded without a log or returned error.

## Decisions

**An observation is part of the run, not telemetry.** The first `Observe` error is retained and interrupts the per-job context. When the runner returns, that storage error takes precedence over phase progress: the job cannot publish or become done after its durable run id/status was lost.

**Terminal writes are checked.** The final transition to `done` is attempted with a small bounded retry because publishing is already complete and idempotent. Exhaustion is returned from `Process`, leaving the job in `publishing` for reconciliation rather than inventing a failed reproduction.

**Failure writes report their own failure.** `fail` returns an error, and `stop`/`Process` propagate a joined lifecycle-plus-persistence error when the terminal failure could not be recorded. The pool logs processing errors instead of sleeping silently.

**Corrupt billing input fails closed.** A malformed persisted rate card makes `scanJob` fail; it never silently becomes a zero-price card.

## Acceptance criteria

1. A failed observation interrupts the run, prevents publish/done, and surfaces when its failure state cannot be persisted.
2. A transient final transition failure is retried and reaches `done`; a persistent one is returned and leaves `publishing` recoverable.
3. A failed failure-write is returned and logged rather than disappearing.
4. Invalid persisted rate-card JSON makes the job unreadable with a decode error rather than pricing it at zero.

## Tests

- Worker failure stores reproduce observation, final transition, and failure-transition errors.
- A pool test captures logs for a surfaced processing error.
- The Postgres scan contract injects an incompatible JSON rate card and expects a decode error.

## Outcome

Observation failures now interrupt the per-job context and become the job's durable failure instead of allowing publication. Success transitions retry three times and surface persistent failure while leaving `publishing` recoverable; failure writes return joined errors and pool/reconciliation goroutines log them. Corrupt rate cards now fail decoding rather than silently pricing a run at zero.
