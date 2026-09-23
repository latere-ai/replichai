---
title: "One managed-session contract"
status: implemented
---

# 041a — One managed-session contract

**Status:** implemented.

**Scope:** replace the Anthropic resource graph exposed by `internal/provider` with a semantic managed-session contract. Define the request, durable reference, capabilities, errors, state, evidence, and usage invariants. No consumer migration and no new vendor adapter in this leaf.

**Depends on:** [041](041-the-runtime-is-not-the-model-vendor.md), [004](004-go-port.md), [005](005-transcript.md), [031](031-the-model-is-a-choice.md).

## Problem

The existing neutral-looking API requires `ResolveModel → EnsureWorkspace → EnsureWorker → StartRun`. `WorkspaceRef`, `WorkerRef.Revision`, and `RunHandle.Worker` are Anthropic concepts. The names also promise idempotent “ensure” operations while the implementation always creates remote resources. Three consumers assemble those resources themselves, so every backend mismatch is duplicated.

The contract is underspecified where portability needs precision. `Packages map[string][]string` accepts arbitrary managers while Anthropic only acts on `pip`; `Network` cannot express an allowlist or credential-bound host; a repository mount assumes every backend has a native mount; rubric zero values cannot distinguish an ungraded run from an empty evaluation. Unsupported fields are silently ignored.

## Package boundary

The neutral package becomes `internal/managed`. It imports no adapter and contains no default runtime, registry, environment lookup, SDK type, vendor model name, or vendor resource vocabulary. Concrete implementations live below it (`internal/managed/anthropic`, later `openai` and `latere`) and may use vendor types internally.

Application packages receive a higher-level controller in 041b. They do not call adapter methods directly. The adapter contract remains small enough to fake:

```go
type Runtime interface {
    Name() RuntimeName
    Capabilities(context.Context) (Capabilities, error)
    Models(context.Context) ([]Model, error)
    Validate(SessionSpec) error
    Start(context.Context, SessionSpec) (SessionRef, error)
    Poll(context.Context, SessionRef) (Status, error)
    Events(context.Context, SessionRef) ([]Event, error)
    ListArtifacts(context.Context, SessionRef) ([]Artifact, error)
    FetchArtifact(context.Context, SessionRef, Artifact) ([]byte, error)
    Cancel(context.Context, SessionRef) error
    Release(context.Context, SessionRef) error
}
```

`Validate` is pure and mandatory. `Start` repeats validation defensively before any remote write. `Models` serves tier resolution and the `/models` surface; it is not part of constructing a session. `Events` is the canonical transcript input and may return a snapshot repeatedly. Raw vendor payloads may be attached to each normalized event for evidence.

## Session request

One `SessionSpec` describes product intent:

```go
type SessionSpec struct {
    Model        ModelSelector
    Instructions string
    Task         string
    Sandbox      SandboxSpec
    Resources    []Resource
    Evaluation   *EvaluationSpec
}
```

- `ModelSelector` carries either the closed `best|balanced|fast` preference or one exact, provider-qualified model reference. Exactly one form is set. A runtime resolves a preference and reports the exact `ModelRef` in status.
- `Instructions` defines the worker's durable role; `Task` is this session's request. An adapter may combine them on a wire that has no persistent worker, but it may not change their ordering or meaning.
- `Evaluation == nil` is the only representation of an ungraded session. A non-nil evaluation contains rubric text and a positive revision budget. Whether a vendor has a native grader or an adapter runs a portable evaluation loop is internal, but the terminal semantics are identical.
- `Resources` is a tagged union. The first set is `AttachmentResource` and `RepositoryResource`; unknown kinds fail validation.

`SandboxSpec` is explicit and validated:

```go
type SandboxSpec struct {
    Packages  []Package
    Bootstrap [][]string
    Network   NetworkPolicy
}

type Package struct { Manager, Name, Version string }
type NetworkPolicy struct {
    Mode           NetworkMode // none, allowlist, open
    AllowedDomains []string
    SecretRefs     map[string]string // environment name -> vault reference
}
```

Bootstrap entries are argv arrays, never shell fragments. Package managers are a closed documented set. An adapter declares the subset it supports. Domain names are canonicalized; `none` rejects domains and secrets, `allowlist` requires at least one domain, and `open` does not accept host-bound secrets. Secrets are references only and are forbidden from `SessionRef`, events, logs, and errors.

`RepositoryResource` names URL, revision, destination, access mode (`ro|rw`), and a credential reference. A runtime may mount, clone, or materialize it, but the resulting filesystem and write authority must match. `AttachmentResource` names a stable logical filename, media type, content source, and digest; duplicate destinations or a digest mismatch fail before start.

## Durable identity

```go
type SessionRef struct {
    Runtime        RuntimeName     `json:"runtime"`
    ID             string          `json:"id"`
    Revision       uint64          `json:"revision"`
    LocatorVersion int             `json:"locator_version"`
    Locator        json.RawMessage `json:"locator,omitempty"`
}
```

`ID` is the runtime's human-operable stable session id. `Locator` is adapter-owned recovery metadata: for example, an OpenAI response chain and container id, or a Topos trace and Cella sandbox ids. `LocatorVersion` versions its schema; `Revision` is a monotonically increasing checkpoint. The locator is size-limited, JSON-valid, and contains no credential or user content. Core code persists and returns it unchanged but never parses it. The router rejects a reference whose `Runtime` differs from the selected adapter.

The reference is valid as soon as `Start` succeeds. A runtime may not return success until every id required to recover the current stage, poll, evidence, cancel, and release is present. Multi-stage orchestration may return a higher-revision locator in `Status`; runtime and id remain immutable. The controller persists that checkpoint with compare-and-swap before another adapter call. Any remote mutation that can precede a checkpoint uses a deterministic idempotency key derived from session id and stage, so replay after a crash resolves the same resource. If startup fails, it returns no reference and rolls back all partial writes as specified in 041b.

## Status and evidence invariants

`Status` carries the complete `SessionRef`, exact `ModelRef`, state, timestamps, cumulative `Usage`, optional terminal reason, optional error summary, and optional verdict.

- States remain `pending`, `running`, `done`, and `failed` for public compatibility. `TerminalReason` distinguishes success, evaluation failure, cancellation, timeout, runtime failure, and policy refusal.
- A status may advance only `SessionRef.Revision` and `Locator`; runtime/id never change and revisions never decrease. A terminal reference is stable.
- `pending → running → done|failed` is monotonic; a terminal observation never becomes non-terminal. Repeated terminal polls are stable.
- Usage is the cumulative total for the session and is component-wise monotonic. Unknown usage is zero, never an estimate disguised as provider data.
- An ungraded successful session is `done` with `Verdict == nil`. A graded `done` session has a decided verdict. A runtime may not report a graded success without its evaluation result.
- Events have stable ids, normalized kind/role/text/time/usage fields, and optional raw evidence. Re-reading events does not change an existing event's normalized meaning.
- Artifact paths are relative, slash-clean, and unique. Fetch must reject traversal and an artifact not returned by `ListArtifacts`.
- Events and artifacts remain readable after terminal state and cancellation until `Release` completes. A successful release may make them unreadable; this is why capture precedes release.

## Capabilities and errors

Capabilities are semantic flags and constrained values, not vendor product names: supported package managers, network modes, repository access modes, evaluation, durable event retention, artifact fetch, cancellation, and maximum attachment/locator sizes.

All unsupported behavior returns `UnsupportedError{Runtime, Field, Capability}`. Invalid combinations return `InvalidSpecError`. Reference/runtime disagreement returns `RuntimeMismatchError`. Missing remote state returns `NotFoundError`; retryable upstream failures implement a `Temporary() bool` marker. Errors wrap causes but redact credentials and attachment content.

An adapter must discover remote model availability without mutating state. Capability and validation errors occur before upload, container, workspace, agent, response, or session creation. There is no “best effort” mode.

## Acceptance criteria

1. `internal/managed` exposes the semantic interface above and contains no Anthropic/OpenAI/Topos/Cella SDK import or vendor resource name.
2. `SessionSpec.Validate` covers selector exclusivity, evaluation nil/positive budget, package manager syntax, argv bootstrap, network combinations, resource destinations/digests, and secret-reference redaction.
3. `SessionRef` JSON round-trips arbitrary versioned locator data byte-for-byte and rejects empty runtime/id, zero/decreasing revision, invalid JSON, oversized locators, and secret-bearing keys.
4. A reusable contract test runs against a strict fake: ungraded sessions have no verdict; graded sessions carry rubric and budget; state and usage are monotonic; terminal state is stable; evidence survives cancel until release.
5. Unsupported package, network, repository, evaluation, or attachment features fail before the fake records its first remote mutation.
6. Cross-runtime references fail with `RuntimeMismatchError` before any adapter call.
7. Event and artifact tests prove stable ids, neutral usage, path containment, deterministic order, and the documented retention point.
8. New/changed code in `internal/managed` exceeds 90% statement coverage.

## Out of scope

- Poll retry timing and resource rollback orchestration: 041b.
- Catalog selection, persistence migration, and credentials: 041c.
- The final usage meter/rate-card representation: 041d; this leaf establishes only cumulative normalized usage.
- Migrating the current Anthropic and memory adapters: 041e.
- Direct OpenAI or Latere network calls: 041f and 041g.

## Outcome

`internal/managed` now owns the semantic spec, durable reference, status, usage, evidence, capabilities, typed errors, validation, runtime contract, reusable conformance suite, and deterministic memory runtime. The package has no vendor dependency; its focused coverage exceeds 90%.
