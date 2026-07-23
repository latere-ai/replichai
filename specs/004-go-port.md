# 004 — Port the core to Go

**Status:** draft
**Scope:** moving the orchestrator, provider contract, and CLI from Python to Go, before specs 001–003 are built.

## Why now

Specs 001–003 are unbuilt. Porting the existing ~400 statements once is cheap; building them in Python and porting them afterwards is paying twice. Go is also the better fit for where this is going: real concurrency for parallel audits, a single static binary to deploy, no interpreter or virtualenv on a server.

The valuable part of what exists is language-neutral already — `method/`, `evals/`, and the nine-method provider contract. Only the plumbing moves.

## Decisions

**Go owns the repository root.** `go.mod`, `cmd/`, `internal/`. The Python moves to `reference/python/` and stays as the reference implementation, not as dead code: it is what the Go port is checked against.

**Layout**

```
go.mod                        module github.com/latere-ai/replichai
cmd/replichai/                the CLI — argument parsing and output only
internal/method/              loading instructions, rubric, task template
internal/audit/               the orchestrator
internal/provider/            the contract, the domain types, the registry
internal/provider/hosted/     the backend adapter — the only vendor-aware package
internal/provider/memory/     in-process double
method/  evals/               unchanged, shared by both implementations
reference/python/             the Python implementation
```

**Service-shaped from the start, without building the service.** Every call that can block takes a `context.Context` as its first parameter and honours cancellation. The audit core returns values and errors; it never prints, never reads flags, never calls `os.Exit`. An HTTP handler must be able to wrap it with no changes to it. Auth, persistence, and multi-tenancy are explicitly not in this spec.

**Standard library only.** The Python deliberately has zero runtime dependencies; the Go port keeps that. `net/http`, `encoding/json`, `log/slog`.

**The contract maps one-to-one.** Same nine operations, Go naming, `context` and `error` added:

| Python | Go |
|---|---|
| `resolve_model(preference)` | `ResolveModel(ctx, pref) (string, error)` |
| `ensure_workspace(spec)` | `EnsureWorkspace(ctx, WorkspaceSpec) (WorkspaceRef, error)` |
| `ensure_worker(spec)` | `EnsureWorker(ctx, WorkerSpec) (WorkerRef, error)` |
| `start_run(...)` | `StartRun(ctx, WorkerRef, WorkspaceRef, RunSpec) (RunHandle, error)` |
| `poll(handle)` | `Poll(ctx, RunHandle) (RunStatus, error)` |
| `list_artifacts(handle)` | `ListArtifacts(ctx, RunHandle) ([]Artifact, error)` |
| `fetch_artifact(...)` | `FetchArtifact(ctx, RunHandle, Artifact) ([]byte, error)` |
| `close_run(handle)` | `CloseRun(ctx, RunHandle) error` |
| `output_dir` property | `OutputDir() string` |

**Behaviour that must survive the port.** These are properties, not implementation details, and each already has a Python test that must have a Go counterpart:

1. An artifact path resolving outside the destination directory is refused — sandbox output is untrusted.
2. Compiled residue is never collected.
3. Silence from the grader never reads as success.
4. A run that has not started is not reported as finished (the race fixed in `fb128f7`).
5. A credential never appears in a URL.
6. A finished run is released even when the audit fails.
7. Responses containing raw control characters still parse.

## Conformance

`method/` and `evals/` are shared. The two implementations conform when, given the same method and the same eval case, they produce the same verdict. That is the acceptance test for the port, and it is why the Python stays.

Cheaper checks that run in CI without a backend: both implementations must reject the same hostile artifact paths, produce the same rendered task text for a given paper, and map the same backend states onto the same neutral states — driven by a shared table of cases in `testdata/`.

## Acceptance criteria

1. `go test ./...` passes with no network access and no credential.
2. Every property in the list above has a named Go test.
3. `go vet ./...` and `gofmt -l` are clean.
4. `internal/provider/hosted` is the only package naming any backend; a test asserts no other package does.
5. `replichai audit <paper>` and `replichai providers` behave as the Python CLI does.
6. Cancelling the context stops a run in flight and still releases it.
7. Coverage of `internal/` is above 90%.

## Out of scope

The HTTP server, auth, persistence, queues, and specs 001–003. This spec ends when Go can do everything Python does today.
