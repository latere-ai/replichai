---
title: "Latere runs a session"
status: designed
---

# 041g — Latere runs a session

**Status:** designed, not built. **Production enablement is blocked on durable replay at the in-flight model/tool boundary** (below). The adapter and fake/local integration may be built after 041e, but it must not be offered as a hosted runtime until that prerequisite is demonstrated.

**Scope:** add `latere-topos`, a platform-native runtime composed from the embeddable Topos runtime, Lux native model access, and Cella sandboxes. Preserve replichai's durable session/evidence/billing contract, use Latere Auth delegation and cost attribution, and leave Drive mount lifecycle with Cella/Drive. Do not route managed sessions through a vendor compatibility surface.

**Depends on:** [041e](041e-anthropic-conforms-to-the-session-contract.md), the current `latere.ai/x/topos` public runtime/sandbox interfaces, and the platform contracts documented at [platform.latere.ai](https://platform.latere.ai/): Lux native dialect, Cella sandbox API, Latere Auth delegation, and Drive/Cella mount lifecycle.

## Why Latere is a composition, not a third model provider

The direct adapters bind lifecycle and inference to one vendor. Latere deliberately separates them:

- **Topos** runs the agent loop, tools, transcript, evaluation turns, and event trace;
- **Cella** owns sandbox creation, exact command execution, filesystem, network/secret policy, and destruction;
- **Lux** provides one native model dialect, provider-qualified routing, provider-key custody, usage/cost records, and compatibility-loss reporting;
- **Auth** proves which principal/service the work acts for and mints delegated credentials;
- **Drive** owns durable workspaces and their attach/materialize/sync/release contract when that integration is available.

The normalised identities therefore look like:

```
SessionRef.Runtime       = latere-topos
SessionRef.ID            = replichai stable session id
SessionRef.Locator       = Topos journal + Cella sandbox/command ids
Status.Model.Provider    = Lux effective provider (openai, anthropic, ...)
Status.Model.ID          = Lux effective model id
```

Replichai may route `best` to `openai/gpt-…` and `balanced` to `anthropic/claude-…` without changing runtime code. The route snapshots one exact namespaced Lux target (or a virtual-key alias proven single-target) for the whole session, so fallback cannot silently change model/provider and invalidate the rate snapshot.

## Runtime shape

The adapter uses `topos.Runner.Turn`, not an Anthropic-like fake worker resource. It creates and owns one Cella sandbox, runs deterministic bootstrap directly through the `sandbox.Provider` exec/file API, then drives these Topos turns against that caller-owned sandbox:

1. worker turn with original instructions/task;
2. evaluator turn when `Evaluation != nil`, with rubric, read-only evidence tools, and strict verdict schema;
3. revision worker turn with evaluator feedback when required;
4. repeat evaluation until pass or budget exhausted.

Topos already defines the transcript as canonical turn state and accepts `InitialTranscript` to resume. Its observer emits lifecycle, text, assistant, tool, usage, and stop events with stable session/agent joins. The adapter pins a reviewed Topos module version and maps those public events; it does not import Topos internals.

`Start` creates the durable adapter state row and Cella sandbox, bootstraps it, persists revision 1, then launches a supervised turn. `Poll` reads adapter state; it does not make an unbounded model call. A process supervisor reclaims non-terminal rows after restart and resumes from the persisted transcript and sandbox.

## Durable journal and the blocking prerequisite

One adapter-owned state table, keyed by immutable session id, stores:

- locator revision and current stage/attempt;
- Cella sandbox id and owned-resource state;
- normalised Topos event log and replayable transcript checkpoints;
- current model/provider, cumulative usage/cost evidence, and Lux request ids;
- cancellation flag, evaluator result/revision count, terminal result, and cleanup state.

It contains task/transcript content and is therefore private evidence storage with the same authorisation/retention as session transcripts; unlike `SessionRef.Locator`, it is never returned as opaque browser data. Writes are append/CAS, and the ref locator carries only the state-row id plus current checkpoint revision.

Topos can resume a crashed loop from a replayed event transcript, and interruption returns a valid partial transcript. That is sufficient after a controlled cancel. It is not by itself sufficient for a process death during:

- a Lux generation whose response/usage was accepted upstream but not checkpointed locally; or
- a Cella tool command that changed the repository/filesystem but emitted no durable completion event.

Blind replay could double-spend a model call or repeat a side effect. `latere-topos` is therefore production-gated until one of these equivalent contracts exists and is tested end to end:

1. a durable Topos control-plane run API owns the loop and exposes retrieve/cancel/events after client death; or
2. Lux generation accepts a stable idempotency/retrieval key and Cella commands expose stable idempotent command ids/results, allowing the embedded adapter to recover the exact in-flight operation.

The adapter must probe/declare the contract version it relies on. “Resume usually works” is not an acceptance criterion. Before the prerequisite lands, `latere-topos` may run only with fake/local model+sandbox in tests or an explicitly non-detached development mode; the production catalog refuses it with `UnsupportedError{Capability: durable_replay}`.

## Identity and credentials

No OpenAI/Anthropic provider key enters replichai or the sandbox. Lux stores provider secrets and receives either a scoped `lux_*` virtual key or a refreshed Latere bearer. The Lux client uses the native `/lux/v1/generate` dialect through `luxsdk`, not `/compat/anthropic` or `/compat/openai`; compatibility surfaces are for wire-locked clients, while this is new code.

Background work cannot persist a user's browser bearer. Enqueue snapshots the actor subject and organization. A credential broker exchanges the service identity for short-lived, audience-scoped delegated tokens for Lux/Cella on demand; token sources refresh per request. If Auth cannot mint that delegation, the route is unavailable. Tokens/API keys never enter job JSON, the adapter journal, events, locators, command argv, or logs.

Cella secret inputs are vault entry references. Bootstrap/tool commands use `SecretEnv` or the platform's current egress-substitution contract; plaintext secrets are not materialised into prompts. Network access is compiled from `SessionSpec.Network` into an approved Cella policy/capability mapping. A policy that cannot exactly enforce `none|allowlist|open` is refused before sandbox creation.

## Sandbox, repository, artifacts, and Drive

Cella's exact exec API gives this runtime a stronger bootstrap than hosted shell through a model:

- package installs and bootstrap argv run directly, in order, with recorded exit status;
- attachments are written with digest verification and bounded size;
- repository clone/checkout uses a credential reference and exact revision;
- read-write push authority is command-scoped and repository-scoped;
- a nonzero/lost bootstrap command prevents the worker turn.

The Topos built-in tools operate only inside the owned sandbox and requested workdir. Tool scopes are the minimum needed for the worker; evaluator tools are read-only. Any sub-agent/delegation feature is disabled in v1 so one replichai session remains one bounded worker/evaluator trace.

Artifacts are discovered and fetched through the sandbox provider with path/size containment. Repository publication still verifies the GitHub tree and commit history. `Cancel` cancels the active Topos context and records the valid partial transcript; it then stops outstanding Cella execution through the durable command contract. `Release` destroys the owned sandbox only after evidence retention.

Drive is not reimplemented in this adapter. When Cella's Drive mount client is production-ready, a `DriveWorkspaceResource` may map to Cella's attach/materialize/periodic-sync/final-sync/release lifecycle and delegated Auth identity. Until then the adapter does not advertise that resource; persistent Cella disk is session-owned scratch, and Git remains the durable publication path. A failed final Drive sync must surface before release rather than claim durability.

## Lux routing, usage, and reconciliation

Each model request uses the exact snapshotted namespaced target and cost tags such as `product=replichai`, `job=<id>`, and `project=<id>` (non-personal identifiers only). Lux's effective provider/model becomes `ModelRef`; a mismatch from the snapshot is a contract failure before another turn.

Topos/Lux normalised usage maps once into `managed.Usage`: fresh input, cached input, generic cache write, output, and reported details. The adapter retains Lux request ids and reported micro-USD cost for reconciliation. Replichai still settles the project job exactly once from its enqueue snapshot; Lux's request log is compared by job cost tag and must reconcile within defined token/cost tolerance. Crash-replayed requests, once durable replay exists, must resolve to one logical Lux request and one charge.

Topos' own `BudgetUSD` receives the maximum spend authorised for the session from the held project credit and a cost source consistent with the snapshot/Lux evidence. A budget breach returns partial evidence and terminal failure; it never overruns silently. Lux spend caps are an upstream guard, not the project wallet.

The Lux native dialect reports fields it cannot preserve. Any loss affecting tool calls, system instructions, structured evaluator output, reasoning budget promised by the selected route, or usage is fatal. Cosmetic/non-semantic loss may be retained as an event warning only when enumerated in route policy.

## Acceptance criteria

1. Fake Lux + local sandbox and fake Lux + fake Cella run the full managed conformance suite and exceed 90% statement coverage for the adapter/journal.
2. The same `SessionSpec` runs through memory and Latere fakes with equivalent task/instruction roles, graded/ungraded terminal semantics, event usage, artifact bytes, cancellation, and retention-before-release.
3. Exact bootstrap tests cover package argv, attachment digest, repository revision, command-scoped secret refs, nonzero/lost command, path containment, and zero plaintext secret in requests/journal/events/logs.
4. Auth tests prove asynchronous token refresh/delegation by subject+audience, expiry recovery, tenant separation, and refusal when delegation is unavailable; no browser bearer is stored.
5. Lux tests prove native dialect use, exact namespaced target, single-target route, cost tags, effective provider/model capture, compatibility-loss policy, normalized usage, and project-ledger/Lux reconciliation.
6. Topos tests persist events/transcript checkpoints, resume after every safe event boundary, retain partial transcript on cancellation, separate evaluator context/tools, and enforce revision/budget limits.
7. Production registration has a hard capability test for durable model and command replay. Without it, startup refuses `latere-topos`; with it, crash tests at every in-flight boundary recover the same Lux request/command/result with no duplicate side effect or charge.
8. Cella lifecycle tests prove create, health, execution, artifact read, cancel, evidence retention, and idempotent destroy; a release failure remains cleanup-pending and resumable.
9. A provider-table end-to-end test routes one Lux target to OpenAI and one to Anthropic through the **same** Latere runtime and proves runtime identity stays `latere-topos` while model/cost provenance changes correctly.
10. An opt-in hosted smoke, enabled only after criterion 7, survives worker-process termination and restart, then retrieves evidence, reconciles Lux usage by cost tag, and releases the Cella sandbox.

## Out of scope

- Using the not-yet-stable Topos cloud `/v1` merely because the console exists; only a documented durable run contract satisfies the gate.
- Multi-agent delegation, mesh regions, or graphs. Replichai's first platform path is one worker plus separate evaluator turns.
- Persisting provider keys or user bearers in replichai.
- Reimplementing Drive mount synchronisation or Cella network/secret policy in replichai.
- Replacing GitHub as the published reproduction or Lux as the model credential/cost boundary.
