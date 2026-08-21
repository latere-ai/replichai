# 041c — A session remembers its runtime

**Status:** designed, not built.

**Scope:** replace init-time global provider registration with an instance-scoped runtime catalog/router; snapshot the runtime on every job; persist the complete `SessionRef`; route restart, transcript, artifact, cancel, and release operations by that reference; make runtime credentials explicit and provider-scoped.

**Depends on:** [041a](041a-one-managed-session-contract.md), [041b](041b-one-managed-session-lifecycle.md), [006b](006b-job-model-and-persistence.md), [031](031-the-model-is-a-choice.md), [039](039-a-description-can-be-drafted.md).

## Problem

`internal/provider` owns a mutable package-global registry, adapters register themselves from `init`, and both commands blank-import the Anthropic package to make it exist. The contract itself sets `DefaultName = "anthropic"`. The server creates one global provider from `PROVIDER`; the CLI uses a different hard-coded default. Help text manually lists `memory | anthropic`.

The selected process provider is then used for every operation. Jobs persist a bare `RunID`; resume, live transcript, publication, cancellation, and cleanup reconstruct `RunHandle{ID: runID}`. A deploy changing the default can send an old Anthropic session to OpenAI. Even without a provider change, the current row discards workspace/worker identity; a response/container backend cannot recover its artifacts from the response id alone.

One generic `REPLICHAI_API_KEY` is also ambiguous once runtimes coexist. The server currently lets the Anthropic drafting client fall back to it regardless of the managed runtime selected, so an OpenAI runtime key could be sent to Anthropic.

## Catalog and router

`internal/managed` remains types and interfaces only. `internal/runtimes` owns two instance-scoped objects:

```go
type Catalog struct { /* immutable factories by RuntimeName */ }
func NewCatalog(entries ...Entry) (*Catalog, error)
func (c *Catalog) Names() []RuntimeName
func (c *Catalog) Build(ctx context.Context, name RuntimeName, cfg Config) (managed.Runtime, error)

type Router struct { /* built runtimes + route policy */ }
func (r *Router) ForNew(Route) (managed.Runtime, error)
func (r *Router) ForRef(managed.SessionRef) (managed.Runtime, error)
```

Catalog construction is explicit. `internal/runtimes/builtin` is the single composition package allowed to import concrete adapters and registers memory plus adapters enabled in the binary. Commands import `builtin`, not adapter packages, and pass explicit configuration. Adapters do not self-register or read process environment variables.

Runtime names are trimmed, lowercase, non-empty, and stable persistence values: `memory`, `anthropic-managed`, `openai-responses`, `latere-topos`. Aliases such as legacy `anthropic` are accepted only at configuration input and canonicalized before persistence. Duplicate canonical names fail catalog construction. Names are returned sorted; unknown-runtime errors list them.

`ForNew` applies an immutable route policy snapshotted when a job is enqueued. Version one supports a deployment-wide runtime plus the test-only memory runtime. The route record still has a version and the selected runtime so a future tier/project policy can be added without reinterpreting old jobs. `ForRef` ignores the current default and dispatches solely by `SessionRef.Runtime`; a missing adapter is an actionable `RuntimeUnavailableError`, never fallback.

The router tags/validates every returned reference and status. It rejects a runtime that returns another name, a status whose ref changes, and any cross-runtime resource/reference before another call.

## Durable job schema

One migration adds:

- `jobs.runtime TEXT`: canonical runtime selected at enqueue;
- `jobs.route JSONB`: versioned snapshot of the new-session route decision;
- `jobs.session_ref JSONB`: complete `managed.SessionRef`, null until remote start succeeds;
- `jobs.cleanup_state TEXT`: empty, `pending`, or `released`, so evidence-retention/release recovery is explicit.

The existing public `status.run_id` remains during compatibility but is derived from `session_ref.id` on writes and ignored for routing. New code never reconstructs a reference from it.

Enqueue chooses and persists `runtime`/`route` in the same transaction as the job and rate-card snapshot. The worker may start on another process after configuration changes and still uses that route. `Hooks.Started` persists `session_ref` before the controller's first poll. `Hooks.Checkpoint` may compare-and-swap it to a higher revision with the same runtime/id as adapter orchestration advances. Attempts to change identity, skip/decrease a revision, or overwrite a newer checkpoint fail.

MemStore and Postgres store round-trip the full reference and route. Reads validate the persisted JSON, but retain unknown locator fields for forward compatibility. API responses expose runtime and public id; the opaque locator is not returned to browsers, logs, SSE, or public registry records.

## Legacy migration

The historical invariant is explicit: every production managed session before 041 ran on Anthropic. Migration therefore maps every legacy job—not only active rows—to canonical `anthropic-managed`, and converts a non-empty legacy `status.run_id` into `SessionRef{Runtime: "anthropic-managed", ID: run_id, LocatorVersion: 1}`. It never maps an empty provider to the deployment's current default.

Legacy pending rows receive an Anthropic route snapshot. This preserves what they would have run before the deployment changed. Memory jobs are test/process-local and are not inferred from id prefixes; fixtures that need memory declare it after migration.

Old status/rate JSON decoders accept missing provenance and assign Anthropic through an explicit `DecodeLegacyV1` path. New encoders always write provenance. The compatibility decoder is measurable (one counter/log field) and removable only when no old rows remain.

## Configuration and credentials

`REPLICHAI_RUNTIME` is the canonical selector for new jobs. `PROVIDER` remains a deprecated alias for one release; setting both to different canonical values fails startup. Server and CLI use the same precedence and no contract-owned default. Product defaults live in composition: server keeps `memory` for local development unless production configuration selects a hosted runtime; CLI keeps `anthropic-managed` for backward compatibility.

Credentials are provider-scoped constructor input:

| Runtime/model path | Credential configuration |
|---|---|
| Anthropic managed | `ANTHROPIC_API_KEY`, optional `ANTHROPIC_BASE_URL` |
| OpenAI direct | `OPENAI_API_KEY`, optional `OPENAI_BASE_URL` |
| Lux model access | `LUX_API_KEY` or a request-scoped Latere bearer, plus `LUX_BASE_URL` |
| Cella sandbox | a token source / request-scoped delegated bearer, plus `CELLA_BASE_URL` |

`REPLICHAI_API_KEY` and `REPLICHAI_API_BASE` are accepted only by the legacy `anthropic` configuration alias during the transition and produce a deprecation warning. They are never shared between catalog entries.

Drafting remains separate. `DRAFT_API_KEY` wins. A configured draft may reuse a runtime credential only when the credential's model provider equals the draft provider (Anthropic-to-Anthropic or OpenAI-to-OpenAI); selecting an OpenAI runtime can never make its key an Anthropic fallback. Latere/Lux credentials are used by drafting only when drafting is explicitly configured for Lux.

Secrets stay outside persisted catalog, route, session ref, status, and settings JSON. Constructors receive secret values/token sources separately from public config and redact them from errors.

## Operational behavior

- `/models`, boot diagnostics, CLI `providers`, and flag help derive available names from the catalog; no manual vendor list remains.
- Deployment may continue pinning `anthropic-managed` while 041a–041e land. This is configuration, not a core default.
- If an active job references a runtime not installed in the new binary, reconciliation leaves it recoverable and reports `runtime_unavailable`; it does not mark the vendor session failed or try another runtime.
- Runtime changes are safe for new jobs immediately after 041c because old jobs retain their route. Removing an adapter is safe only after no unreleased references name it.

## Acceptance criteria

1. Catalog tests cover isolated instances, canonical names/aliases, sorted discovery, duplicate rejection, caller-supplied default, factory errors with runtime context, and actionable unknown names; new catalog/router code exceeds 90% statement coverage.
2. A two-fake router test exercises start, poll, events, artifact list/fetch, cancel, and release. After the new-session default flips from A to B, every operation on an A reference still reaches only A.
3. A mismatch returned by an adapter or supplied in a reference fails before another backend call. A missing adapter never falls back.
4. MemStore and Postgres tests round-trip route, runtime, complete opaque locator/revision, and cleanup state. The locator survives unknown fields and process restart byte-for-byte; CAS rejects a stale checkpoint.
5. A restart end-to-end test persists an in-flight A session, rebuilds the runner with B as the new default, then resumes, streams transcript, publishes, cancels or completes, and releases through A only.
6. The session reference is committed before the first poll; a simulated crash immediately after `Started` resumes without a duplicate remote start.
7. Migration fixtures containing legacy status and snapshotted job JSON backfill Anthropic explicitly and preserve the previous model and cost. Empty provenance never resolves to the current default.
8. Credential tests prove OpenAI runtime plus Anthropic drafting never reuses the OpenAI key; same-provider reuse and explicit `DRAFT_API_KEY` work; persisted/logged structures contain no secret.
9. Commands contain no blank adapter import, flags/help are catalog-derived, and server/CLI config precedence is identical. A dependency test permits concrete adapter imports only below adapters and `internal/runtimes/builtin`.
10. Existing server end-to-end and memory compose smoke tests remain green.

## Out of scope

- Routing a single active session across runtimes. A session never migrates mid-run.
- A researcher-visible runtime selector or arbitrary route rules. Version one is an operator policy.
- Provider-qualified model/rate migration: 041d.
- Implementing the direct OpenAI or Latere adapters: 041f/041g.
