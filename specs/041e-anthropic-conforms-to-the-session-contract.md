# 041e — Anthropic conforms to the session contract

**Status:** implemented through the managed compatibility adapter, with two deviations worth naming. The Anthropic adapter was never built at `internal/managed/anthropic`: the SDK stays in `internal/provider/anthropic`, behind `internal/managed/legacy.Runtime`, which `internal/runtimes/builtin` alone constructs. And the removal is half-done — the old resource contract (`WorkspaceSpec`, `WorkerSpec`, `RunHandle`, `EnsureWorkspace`, `EnsureWorker`, `StartRun`, `CloseRun`) still stands in `internal/provider`, still called directly by `internal/audit` and `cmd/server/runner.go`, so acceptance criteria 4 and 8 do not yet hold. The global registry and the blank imports are gone as specified.

**Scope:** migrate memory and Anthropic Managed Agents to `internal/managed.Runtime`; add the reusable conformance suite and mocked Anthropic transaction tests; remove the old provider registry/resource contract and all non-adapter Anthropic usage parsing/imports; preserve current audit, experiment, publication, restart, cancellation, and billing behavior.

**Depends on:** [041a](041a-one-managed-session-contract.md), [041b](041b-one-managed-session-lifecycle.md), [041c](041c-a-session-remembers-its-runtime.md), [041d](041d-models-and-meters-name-their-source.md).

## Problem

The seam is not proven by types or fakes alone. The production adapter must realize the new semantic request without weakening it, every existing caller must use the controller/router, and the adapter's partial-failure behavior must be demonstrated at its HTTP boundary. Until then, “OpenAI-ready” only means another interface exists beside the Anthropic path.

## Anthropic mapping

`internal/managed/anthropic` keeps all Anthropic SDK types, endpoint knowledge, model matching, event decoding, and raw usage translation. Its public constructor receives typed config and an injectable client/transport; it does not read environment variables or self-register.

`Start(SessionSpec)` realizes vendor resources privately:

1. validate the complete spec and capability set with zero remote writes;
2. resolve the exact Anthropic `ModelRef` deterministically from the requested preference;
3. create the environment from supported package/network policy;
4. create the agent/version from instructions and resolved model;
5. upload attachments and construct repository/file resources;
6. create the managed session and send the task kickoff;
7. include `user.define_outcome` only when `Evaluation != nil`;
8. return a complete reference containing the managed-session id and every owned resource locator required for rollback/release.

The names environment, agent, version, and `user.define_outcome` do not escape the adapter or appear in neutral JSON. Private helpers may retain the current endpoint mapping.

Capability validation is honest:

- `pip` is supported; an unimplemented package manager is rejected rather than ignored;
- Anthropic's open/limited network modes map only when their semantics satisfy the requested policy; an arbitrary domain allowlist is unsupported until the vendor API can enforce it;
- a read-write GitHub repository uses the existing GitHub resource; read-only is advertised only if enforcement is demonstrated rather than assumed from a token comment;
- evaluation is supported through the native outcome/revision mechanism;
- attachments honor limits before upload.

The adapter's locator is versioned, redacted, and records ownership. Caller-owned repository references are never released. Created sessions, temporary uploads, and any environment/agent objects that the API allows the caller to archive/delete are released idempotently in reverse dependency order. A vendor object with no deletion API is represented as archived/retained in adapter diagnostics rather than silently claimed deleted.

`Poll` maps vendor state and grader records to neutral monotonic status. `Events` maps every event once, including normalized per-event usage; raw JSON remains attached only as evidence. `Cancel` interrupts a live session. `Release` archives/releases owned resources but does not run before retention. Not-found after a prior terminal/release observation is idempotent success.

## Memory mapping

`internal/managed/memory` is the reference implementation, not merely a command demo. It supports deterministic model resolution, graded and ungraded sessions, status transitions, events, artifacts, cancel, retention, and release entirely in memory. It validates the same base spec and can be configured to reject capabilities or fail at each lifecycle boundary.

Memory references use runtime `memory`, a stable id, and a versioned locator sufficient to simulate restart when the store is shared. Its resolved model is provider `memory` with explicit zero-cost rate coverage.

The reusable conformance suite is black-box against `managed.Runtime` and runs in full against memory. Adapter-specific harnesses opt into supported capabilities and provide mutation/evidence probes; the mocked Anthropic client runs the same applicable cases.

## Removal and dependency firewall

Once both adapters and all consumers migrate:

- remove `WorkspaceSpec`, `WorkerSpec`, `RunSpec`, `WorkspaceRef`, `WorkerRef`, `RunHandle`, `EnsureWorkspace`, `EnsureWorker`, `StartRun`, `CloseRun`, `DefaultName`, global `Register/Get/Available`, and init registration;
- remove command blank imports and direct Anthropic construction outside `internal/runtimes/builtin`;
- remove Anthropic `model_usage` parsing from `internal/httpapi`; use `managed.Event.Usage`;
- update comments/docs/help/env examples/deployment from “provider session/worker” to runtime/session language while retaining a documented legacy config alias;
- keep the Anthropic Go SDK dependency because the adapter uses it, but forbid it outside `internal/managed/anthropic`.

A repository architecture test walks Go imports/AST rather than grepping comments. Vendor SDK imports are allowed only in their adapter. Concrete adapter imports are allowed only in their own tests and `internal/runtimes/builtin`. Core packages may import `internal/managed` and the router/controller, never an adapter.

## Compatibility behavior

Through this leaf:

- the production deployment remains configured to `anthropic-managed`;
- server local default remains memory and CLI legacy default remains Anthropic as defined in 041c;
- model preference mapping preserves the same Opus/Sonnet/Haiku choices, but no longer falls back to an arbitrary first catalog item;
- audit prompt, rubric, revision budget, repository live-write behavior, artifacts, transcript ordering/raw evidence, verdict, and usage totals remain the same;
- experiments/cohorts become truly ungraded: Anthropic receives no empty outcome definition and terminal status carries no verdict;
- cancellation still interrupts and archives, now with evidence retention before archive/release;
- public API compatibility fields remain while runtime/model provenance is additive.

Any behavior change discovered during migration is either an explicit correction above with a failing regression test or a new spec; it is not hidden in the adapter rewrite.

## Contract and regression tests

The suite must cover:

1. valid graded start carries exact task/instructions/model/rubric/revision budget; valid ungraded start emits no evaluator/outcome field;
2. unsupported sandbox/resource fields fail before the first HTTP request;
3. second attachment upload, environment create, agent create, session create, and kickoff failures compensate every earlier owned mutation or report the retained cleanup locator;
4. cumulative usage and per-event deltas preserve Anthropic cache read and 5m/1h creation classes exactly;
5. state progression and terminal verdict invariants, including vendor idle/grader timing, are stable across repeated polls;
6. cancel and release are separately idempotent; events/artifacts are readable after terminal/cancel and before release;
7. locator JSON round-trips restart data and contains no request content, repository credential, API key, attachment bytes, or raw transcript;
8. a contract violation returned by a mocked client is surfaced rather than normalized into success.

## Acceptance criteria

1. Memory and mocked Anthropic pass the reusable runtime conformance suite for every advertised capability; new/changed adapter and conformance code exceeds 90% statement coverage.
2. Every startup mutation boundary has a reproducible rollback test. The test fails against the old “return error and orphan” behavior and passes with transactional start.
3. An ungraded experiment HTTP capture contains no Anthropic outcome/evaluator payload; a graded audit contains the unchanged rubric and positive revision budget.
4. Audit, experiment runner, and cohort memory end-to-end tests execute only through controller/router and retain transcript/artifacts before release.
5. Server end-to-end covers enqueue, immediate ref persistence, restart/resume, SSE status/transcript, terminal settlement, publication evidence, and release.
6. Cancellation end-to-end covers a live job, partial transcript retention, terminal usage settlement, idempotent repeated cancellation, and cleanup state.
7. Existing credit regression fixtures produce identical Anthropic cost after provider-qualified migration; HTTP event usage tests require no vendor-shaped `Raw` payload.
8. The import firewall passes and the old resource contract/global registry/blank imports are deleted, not deprecated.
9. README, architecture, deployment docs, example environment, CLI help, and server boot diagnostics describe runtime versus model provider accurately.
10. `go test -race ./...`, frontend tests, the real-browser end-to-end suite, build, and lint pass before the final implementation push.

## Commit sequence

Implementation lands as independently green commits: neutral types and conformance fixtures; controller and consumer migration on memory; catalog/router and persistence migration; metering/rate migration; Anthropic adapter with rollback; composition/docs and old-contract deletion. Each behavior correction lands with its regression test in the same commit.

## Out of scope

- Calling OpenAI: 041f.
- Calling Topos, Cella, Lux, or Drive: 041g.
- Replacing the Anthropic adapter with Lux's Anthropic compatibility surface. Managed Agents lifecycle endpoints are not a model-generation dialect.

## Outcome

Anthropic is constructed only at `internal/runtimes/builtin` and is exposed to consumers only as a managed runtime. Ungraded work emits no evaluator payload. Vendor transcript usage is normalized in the adapter. Uploaded file ids, workspace, worker revision, and model are retained in the opaque locator; failures roll back uploads, sessions, agent, and environment, and successful release cleans owned resources after retention. The old protocol types remain internal to the compatibility adapter rather than being deleted outright, which kept existing behavior and tests stable while removing them from application consumers.
