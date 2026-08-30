---
title: "Managed runs close truthfully"
status: built
---

# 014a — Managed runs close truthfully

**Status:** built.

**Scope:** make managed-run teardown observable from the Anthropic adapter through the audit/experiment orchestrators and the server cancel route. A caller may tolerate a cleanup error explicitly; no layer may turn it into success implicitly.

**Depends on:** [004](004-go-port.md), [006b](006b-job-model-and-persistence.md), [011](011-what-a-session-costs.md).

## Problem

`anthropic.Provider.CloseRun` sends `user.interrupt` and archives the session, but discards both errors. The audit and experiment orchestrators discard `CloseRun` again, and `job.Worker.Cancel` discards the runner's close error before making the local row terminal. A backend outage therefore produces the strongest possible lie: the API says the session stopped while the agent may still be working and spending.

## Decisions

**Teardown is two attempted operations and one result.** `CloseRun` always attempts interrupt and archive in that order. Explicit 404/409 responses are idempotent success; other failures are wrapped with their operation and joined, so archive is still attempted after an interrupt failure.

**Primary work and cleanup both survive.** Audit and experiment methods use named returns and join cleanup errors with the primary result. The existing detached, bounded cleanup context stays: cancellation must not cancel its own teardown.

**Cancellation becomes terminal only after remote stop succeeds.** `Worker.Cancel` reads the job and closes a persisted backend run before cancelling the local driver or terminalising the row. A read or close failure reaches the HTTP route as `cancel_failed`, and the row stays active so the owner can retry. A queued run with no backend id still cancels locally.

## Acceptance criteria

1. A 500 from interrupt or archive is returned; both endpoints are attempted and errors from both are preserved.
2. An already terminal/archived 404 or 409 remains idempotent success.
3. Audit and experiment callers receive cleanup failures, joined with a primary failure when both occur.
4. A failed backend close makes session cancellation fail without terminalising the durable row; retrying after recovery succeeds.

## Tests

- Anthropic wire tests assert endpoint order, payload, joined failures, and 404/409 idempotence.
- Audit and experiment unit tests inject cleanup-only and primary-plus-cleanup failures.
- Worker and HTTP tests inject `Runner.Close` failure and assert a retryable active row plus `500 cancel_failed`.

## Outcome

Teardown is now an ordered interrupt-and-archive operation whose non-idempotent failures are joined and returned. Audit and experiment cleanup preserves both the primary and cleanup error, and worker cancellation stops a persisted backend handle before making the local row terminal, so a failed stop remains retryable.
