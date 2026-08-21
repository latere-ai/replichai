# 041 — The runtime is not the model vendor

**Status:** normalization implemented; alternative adapters remain designed. Children 041a–041e refactor the existing Anthropic implementation without changing product behaviour. 041f and 041g remain unimplemented by design.

**Scope:** managed reproduction and experiment sessions only. Replace the Anthropic-shaped resource choreography exposed to the application with a durable, provider-neutral session contract; route every operation by the runtime recorded on that session; normalize usage and pricing; preserve the existing Anthropic path; then make OpenAI and the Latere platform possible adapters.

**Depends on:** [004](004-go-port.md) (the first provider seam), [005](005-transcript.md) (the transcript contract), [006b](006b-job-model-and-persistence.md) (durable jobs and restart), [011](011-what-a-session-costs.md) (rate snapshots and settlement), [014a](014a-managed-runs-close-truthfully.md) (terminal truth), [031](031-the-model-is-a-choice.md) (the user-facing tier). [039](039-a-description-can-be-drafted.md) is adjacent but deliberately separate: drafting is a single model call, not a managed session.

## Problem

The package is named `provider`, but the public lifecycle is Anthropic Managed Agents' resource graph: resolve a model, create an environment, create an agent revision, create a session, poll it, then interrupt and archive it. `audit`, the experiment runner, and the model cohort all repeat that choreography. OpenAI Responses with hosted shell has a response and a container, not a persistent agent revision; Topos with Cella and Lux has an orchestrated run, one or more sandboxes, and a model route. An adapter for either would have to invent Anthropic resources merely to satisfy the interface.

The more serious leak is durable. A job stores only a session id. Resume, transcript, publication, cancellation, and cleanup send that id to whichever single provider the new process selected. Switching a deployment from Anthropic to OpenAI while a run is live would route an Anthropic id to OpenAI, and the current record has nowhere to retain an OpenAI container id or a Latere trace locator.

Usage looks normalized at the run boundary but is decoded from Anthropic's raw transcript again in the HTTP layer. Models and prices are keyed by a bare string. Two providers may use the same model id, OpenAI cache accounting is inclusive where Anthropic's is additive, and hosted tools can carry non-token meters. A provider-neutral runtime that misprices the result is not neutral.

Finally, “provider” now names two independent choices:

- the **session runtime** that owns lifecycle, sandbox, events, artifacts, cancellation, and release; and
- the **model route** that supplies inference and determines model usage and price.

They happen to be the same vendor today. They are not the same in the Latere platform: Topos owns orchestration, Cella owns compute and mounts, and Lux routes an OpenAI, Anthropic, OpenRouter, Gemini, or local model.

## Architectural decision

The application describes one session and receives one durable reference. It never constructs vendor resources.

```
audit / experiment / cohort
        |
        v
session.Controller ---- RuntimeRouter ---- anthropic-managed
        |                      |---------- openai-responses
        |                      `---------- latere-topos
        |
        `---- durable SessionRef { runtime, id, locator_version, locator }

latere-topos ---- Topos (orchestration)
              + Cella (sandbox and mounts)
              + Lux (model route: openai/*, anthropic/*, ...)
```

`runtime` is the routing key for lifecycle operations. `ModelRef.Provider` is the billing/model provenance. Neither is inferred from the other, from a model name, from an id prefix, or from the deployment's current default.

The normalized contract is intentionally semantic rather than lowest-common-denominator. An adapter must implement the requested evaluation, repository, network, artifact-retention, and cancellation guarantees or return a typed unsupported error **before its first remote write**. It may not silently omit a field or report a weaker run as equivalent.

## Sequence

| Child | Scope | Depends on |
|---|---|---|
| [041a](041a-one-managed-session-contract.md) | One request, one durable reference, explicit capabilities and invariants | this umbrella |
| [041b](041b-one-managed-session-lifecycle.md) | Shared wait/evidence/cancel/release controller and transactional start | 041a |
| [041c](041c-a-session-remembers-its-runtime.md) | Instance-scoped catalog/router, persistence, restart routing, credentials | 041a, 041b |
| [041d](041d-models-and-meters-name-their-source.md) | Provider-qualified models, neutral event usage, extensible metering and rates | 041a, 041c |
| [041e](041e-anthropic-conforms-to-the-session-contract.md) | Migrate Anthropic and memory behind the seam; conformance and regression proof | 041a–041d |
| [041f](041f-openai-responses-runs-a-session.md) | Direct OpenAI Responses plus hosted-shell/container adapter | 041e |
| [041g](041g-latere-runs-a-session.md) | Platform-native Topos + Cella + Lux adapter | 041e |

041a–041e are the requested refactor. They leave Anthropic as the only production runtime but make that fact composition configuration instead of an application assumption. 041f and 041g are independent after the seam: either may land first, and installing both is not required to prove the normalization.

## Global invariants

1. Vendor SDK types and wire fields exist only inside their adapter. Core, HTTP, jobs, credit, audit, experiment, and publication packages do not import a concrete runtime.
2. Every started session is represented by a runtime-tagged, versioned `SessionRef` persisted before the first poll. Adapter orchestration may advance its opaque locator through CAS-persisted checkpoints, but runtime and session id never change. Every later operation routes by that reference.
3. A session's runtime, id, and resolved model identity are immutable. Changing a deployment default affects new sessions only.
4. `Evaluation == nil` means ungraded. A requested evaluation is either implemented with equivalent semantics or refused; zero values never create an empty vendor evaluator.
5. Startup owns rollback of every partial remote resource. Terminal evidence is retained before release. Cancellation and release are separate, idempotent operations.
6. Usage is cumulative, monotonic, and normalized at the adapter boundary. Raw event JSON is evidence only, never a billing or UI input.
7. Prices are selected by provider-qualified model and billing variant. No fallback crosses provider boundaries; no reported billable meter is silently discarded.
8. Existing production records with no runtime/provider provenance decode explicitly as Anthropic, because that is the historical production invariant. An empty field never means “today's default.”
9. The current memory and Anthropic end-to-end flows remain behaviourally unchanged through 041e, including live publication, transcript retention, verdicts, restart, cancellation, and exactly-once settlement.

## Latere platform boundary

The platform is considered as a composition, not another spelling of OpenAI:

- **Lux** is the model gateway and credential/cost boundary. New model code should use its native dialect, namespaced `provider/model` identity, explicit compatibility-loss reporting, virtual keys, and cost tags. A stock vendor SDK compatibility surface is for an already wire-locked client, not the new internal contract.
- **Topos** is the embeddable orchestration runtime. Its transcript/trace is run state; its model client is Lux-native; its sandbox backend is injectable.
- **Cella** is the hosted sandbox and secret/network/mount authority. Replichai passes references and delegated identity, never provider secrets in sandbox environment text.
- **Drive** is the durable workspace boundary when a Latere run needs files to survive an ephemeral sandbox. Its attach/materialize/sync/release ordering remains owned by Cella/Drive, not reimplemented in replichai.

The direct OpenAI adapter remains useful for a deployment that does not run the Latere control planes. The Latere adapter is preferred when platform identity, governed model routing, durable workspace, and sandbox policy are required.

## Out of scope

- Merging `internal/draft` into the managed-session contract. Its one-call shape is correct.
- Making OpenAI imitate Anthropic's private Managed Agents API. 041f defines equivalent product semantics over public Responses/container primitives.
- Moving replichai's project wallet or exactly-once settlement into Lux. Lux usage is upstream evidence; replichai still owns the prepaid project ledger.
- Exposing runtime choice to researchers. They continue to choose `best`, `balanced`, or `fast`; deployment routing maps that preference to a runtime and model.
- Replacing GitHub publication with Drive. A repository remains the public reproduction artifact.

## Outcome

The application now describes one managed session, executes it through one controller, persists its runtime-tagged opaque reference before polling, and routes later operations by that reference. Audit, experiments, cohorts, server, and CLI no longer assemble Anthropic environments/agents/sessions. Model and rate identities are provider-qualified; transcript usage is normalized inside the adapter; runtime construction is explicit and credential-scoped. The existing Anthropic protocol implementation remains isolated behind a compatibility adapter while its startup and release paths are transactional. No OpenAI or Latere network adapter was added.
