# 041d — Models and meters name their source

**Status:** designed, not built.

**Scope:** make model identity, event usage, session usage, and rate lookup provider-neutral and provider-qualified. Preserve project holds, enqueue-time rate snapshots, and exactly-once settlement while preventing cross-provider fallback or silent omission of hosted-tool cost.

**Depends on:** [041a](041a-one-managed-session-contract.md), [041c](041c-a-session-remembers-its-runtime.md), [011](011-what-a-session-costs.md), [031](031-the-model-is-a-choice.md), [006s](006s-reading-a-session-in-time.md), [039](039-a-description-can-be-drafted.md).

## Problem

The current model and price keys are bare strings. The default card contains only Claude models, and an unknown model is charged at the card's most expensive rates. Once OpenAI exists, a missing OpenAI price could therefore be charged with an Anthropic rate. A response can also report an applied service/billing tier different from the requested one.

`provider.Usage` has Anthropic's 5-minute and 1-hour cache-write classes. It has one generic cache-write bucket but no generic cache-write rate, so pricing falls back to the greater Anthropic lifetime rate. OpenAI's cached input is included in total input while Anthropic's cache tokens are adjacent; treating both the same double-counts one of them. Hosted containers and tools may introduce billable units that are not tokens.

At event granularity the boundary is already broken: HTTP parses `raw.model_usage.cache_*` from Anthropic transcript JSON. An OpenAI or Lux event would display and aggregate zero unless UI code learned another vendor wire format.

## Model identity

Every resolved model is:

```go
type ModelRef struct {
    Provider       string `json:"provider"`
    ID             string `json:"id"`
    BillingVariant string `json:"billing_variant,omitempty"`
}
```

`Provider` is the inference/cost source (`anthropic`, `openai`, `openrouter`, `local`, and so on), not the session runtime. `ID` is the exact provider model id. `BillingVariant` records a price-affecting applied class such as an OpenAI service tier; it stays empty when the provider has one price.

`ModelRef` is canonical, comparable, and required in resolved status, usage, rates, and job settlement. A Latere runtime can therefore be `latere-topos` while its model is `openai/gpt-…`; direct Anthropic can be `anthropic-managed` plus `anthropic/claude-…`.

The user-facing `best|balanced|fast` remains a preference. Runtime `Models` returns provider-qualified candidates and the route policy deterministically maps each preference to one `ModelRef`. There is no “first model in the catalogue” fallback. Exact model requests remain operator-only and require a fully qualified reference.

`GET /models` reports the selected runtime and exact provider/id/variant behind each preference, along with the snapshotted price summary. Cache keys include route-policy version and rate-card revision; changing either invalidates the answer.

## Normalized usage

`managed.Usage` retains explicit token fields for compatibility and adds named non-token meters:

```go
type Usage struct {
    Model         ModelRef
    InputFresh    int64
    InputCached   int64
    CacheWrite    int64
    CacheWrite5m  int64
    CacheWrite1h  int64
    Output        int64
    Extra         map[Meter]int64
    Details       map[string]int64
}
```

- The six token buckets are mutually exclusive billable quantities. `InputFresh + InputCached + CacheWrite + CacheWrite5m + CacheWrite1h` is normalized input; `Output` is normalized output.
- `CacheWrite` is a real generic class with its own rate. It is not mapped to Anthropic's 5m/1h classes. A legacy unattributed Anthropic write may use the old fail-expensive lifetime fallback only when its source is explicitly Anthropic.
- `Extra` carries typed, non-negative billable meters such as `container.second` or `hosted_tool.call`. Each runtime declares the meters it can emit in capabilities.
- `Details` is non-billable decomposition, for example reasoning tokens already included in `Output`. Pricing never iterates it.
- Status usage is cumulative and component-wise monotonic. `Event.Usage` is an optional per-event delta. Summing event deltas may lag the status total while a runtime has not emitted detailed events, but no bucket may be interpreted with vendor wire rules outside its adapter.

Adapter normalization rules are contractual:

- Anthropic's input, cache-read, and cache-creation fields are additive; cache creation remains split into 5m/1h when reported.
- OpenAI's cached tokens are part of input, so `InputFresh = input - cached`; negative results are a contract error. Generic cache writes remain `CacheWrite` if reported. Reasoning is a detail of output, not a second billable output.
- Lux native usage treats cached input as a subset of input and applies the same subtraction. Its namespaced effective provider/model becomes `ModelRef` even when the caller used an alias.
- Missing provider usage remains zero and carries an “unreported” detail/diagnostic; an adapter may not estimate and label it reported.

Raw event JSON remains lossless evidence but is never read by HTTP, job, credit, or frontend code. `internal/httpapi` renders `Event.Usage` only.

## Rate card and settlement

`credit.Rates` is keyed by `ModelRef` and adds `CacheWrite` plus named extra-meter rates. A rate is integer micro-USD per declared unit scale; token rates keep per-million units. `Card.For(ModelRef)` resolves in this order:

1. exact provider + model + billing variant;
2. provider + model with an empty variant, only when policy explicitly permits variant fallback;
3. the most expensive matching rates **within the same provider and meter set**, as the existing fail-expensive rule.

There is no cross-provider fallback. A provider with no rate coverage is refused before the remote start and the hold is released. A runtime declares its possible billable meters; enqueue succeeds only when the snapshotted card prices all of them. If an undeclared/unpriced meter nevertheless appears, settlement records `billing_review`, retains the hold and complete usage evidence, and alerts the operator; it never settles that meter at zero.

`Rates.Price` checks overflow, negative quantities, model equality, and meter coverage. It prices each mutually exclusive token bucket once and each `Extra` meter once. Unknown non-billable `Details` are ignored by construction, not by name matching.

The job continues to snapshot a card at enqueue and settlement remains in the same transaction that makes the job terminal. The snapshot now includes provider-qualified entries, route revision, and meter schema version. Runtime status reports the applied `BillingVariant`; if it differs from the route's assumption, settlement chooses the matching entry from the **snapshotted** card, never today's card. A route whose provider can change the billed variant must snapshot all allowed variants or force one.

Lux's own request log is upstream reconciliation evidence, not a replacement for replichai's project ledger. A Latere adapter tags model calls with the replichai job id and may retain Lux request/cost ids, but the project is charged once through the existing job settlement.

## Legacy data

Old `Rates` JSON and job rate snapshots have model-only keys. The explicit v1 decoder assigns provider `anthropic`, empty billing variant, and the current token-meter schema. Existing generic cache writes preserve their historical Anthropic fail-expensive calculation; known 5m/1h writes remain unchanged.

Platform settings holding the default card migrate the same way. The new shipped default card is provider-scoped: it may contain Anthropic entries initially, but an OpenAI route is not available until OpenAI entries are configured. Memory has an explicit zero-cost `memory` model entry rather than borrowing Claude rates.

Historical settlement fixture tests calculate the old and migrated price and require exact micro-USD equality. Missing provider is accepted only by the v1 decoder; new JSON validation rejects it.

## Acceptance criteria

1. `ModelRef` is present in runtime model lists, status, normalized usage, rates, job snapshots, settlement, and `/models`; session runtime remains a separate field.
2. Two providers with the same model id resolve distinct rates. Unknown-model fallback stays within its provider; a provider with no coverage refuses before start.
3. Table tests prove Anthropic additive cache classes, OpenAI/Lux inclusive cached-input subtraction, generic cache-write pricing, and reasoning-as-output-detail without double counting.
4. Named extra-meter tests prove every declared meter is preflight-priced and charged once; an undeclared or unpriced reported meter enters `billing_review` and is never silently zero.
5. `Event.Usage` drives HTTP per-event and total displays without `Raw`. An architecture test fails if any package outside an adapter parses vendor usage fields.
6. Cumulative status usage is monotonic; event deltas use the same buckets; malformed negative/overflow/inclusive counts fail with typed errors.
7. Legacy settings and job snapshot fixtures decode as Anthropic and preserve exact previous costs, including cache lifetime classes and unattributed legacy writes.
8. Enqueue card snapshot, terminal exactly-once settlement, cancellation settlement, and restart settlement pass for synthetic Anthropic and OpenAI model refs.
9. `/models` tests cover provider/runtime display, route/card cache invalidation, deterministic tier mapping, and absence of catalogue-first fallback.
10. New/changed model, usage, and pricing code exceeds 90% statement coverage.

## Out of scope

- Changing the reserve amount or wallet semantics.
- Billing a researcher directly from Lux/Cella records.
- Guessing prices by scraping provider model-list endpoints.
- User-entered exact model ids; [031](031-the-model-is-a-choice.md)'s three preferences remain the product surface.
