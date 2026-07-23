# Architecture

## Shape

```
paper reference
      │
      ▼
┌─────────────┐   reads    ┌──────────┐
│ orchestrator│ ─────────► │ method/  │  instructions · rubric · task template
└─────────────┘            └──────────┘
      │
      │ speaks only the Provider contract
      ▼
┌──────────────────────────────────────┐
│ provider adapter                     │  the only backend-aware code
└──────────────────────────────────────┘
      │
      ▼
┌──────────────────────────────────────┐
│ agent runtime                        │  sandboxed worker + tools + grader
│   • network access for artifact hunt │
│   • a shell for building and running │
│   • grades output against the rubric │
└──────────────────────────────────────┘
      │
      ▼
  artifacts ──► local run directory ──► (future) reproduction repository
```

## Layers

**Orchestrator** (`internal/audit/`) knows the shape of an audit: resolve a model, prepare a sandbox, define the worker, start a run, wait for it, collect what it produced, release it. It knows nothing about any backend. Every call it makes goes through the provider contract.

**Provider contract** (`internal/provider/provider.go`) is the seam. Nine methods: resolve a model, ensure a workspace, ensure a worker, start a run, poll it, list artifacts, fetch one, close the run, and report where in the sandbox output should be written. Any backend that can run a sandboxed, tool-using worker and grade its output can sit behind it.

**Provider adapters** (`internal/provider/`) translate. Each one is the sole place its backend's vocabulary, endpoints, and payload shapes appear — and, because the adapter is that one place, it also chooses the public name it registers under. Two ship today: `anthropic` (package `internal/provider/hosted`), which talks to the managed-agents runtime in use, and `memory`, which runs nothing and exists so the orchestrator can be tested and the contract demonstrated. The seam itself names neither: it learns which adapter is the default when that adapter registers, so `provider.go` holds no backend name.

Every operation that can block takes a `context.Context` and honours cancellation, and the orchestrator returns values rather than printing, so an HTTP layer can wrap it without changes. Porting to a different runtime is one new package plus one changed setting; nothing above the seam moves.

The `anthropic` adapter talks to its backend through that vendor's official SDK rather than hand-rolled HTTP, which buys typed payloads, retry on rate limits and server errors, and correct protocol headers. The tradeoff is honest: adopting the SDK puts the vendor's name in `go.mod`, where a reader of the dependency list will see it. The adapter is the one place above `go.mod` that names the backend — in its SDK calls and in the public provider name it registers, `anthropic`. Everything else — the method, the evaluation cases, the seam, and every package above it — names only roles. A test (`TestOnlyTheAdapterNamesTheBackend`) enforces this against a small allowlist: the adapter and its test, `go.mod`/`go.sum`, and the two documents (this one and the port spec) that record the tradeoff.

A second implementation of the same design lives in `reference/python/`. It is not dead code: `method/` and `evals/` are shared, and the two implementations conform when they produce the same verdict for the same case.

**Method** (`method/`) is data, not code. Three documents, loaded at run time:

| File | Role |
|---|---|
| `instructions.md` | The worker's standing instructions: the inner/outer doctrine, the search procedure, the never-dos |
| `rubric.md` | Six binary criteria. The grader scores every run against these |
| `task-template.md` | The per-run task, with `{{PAPER}}` substituted |

Keeping the rubric out of the worker definition is deliberate: the rubric travels with each run, so sharpening it costs a text edit rather than a redefinition of the worker. Iterating on the bar is the cheapest operation in the system, and it should stay that way.

`{{OUTPUT_DIR}}` in the instructions is filled in by the provider, so the method never hard-codes one runtime's filesystem layout.

## Grading

Grading is not a post-processing step; it runs inside the loop. Each run carries its rubric and a revision budget. The runtime grades the worker's output, and if it falls short the worker revises and is graded again, up to the budget. What comes back is a verdict, not just a deliverable.

This is why the rubric criteria are written as binary checks with stated evidence requirements. "A useful report" cannot be graded. "A rate derived from a stated denominator, or it fails" can.

## Trust boundaries

Artifact paths and file contents come out of a sandbox that has just executed code found on the internet. They are untrusted input:

- collection refuses any path that resolves outside the destination directory (absolute paths, `..` traversal);
- compiled residue (`.pyc`, `__pycache__/`) is dropped — machine-specific, and a reproduction repository should hold sources;
- silence from the grader is never success. The backend rests in the same state both before a run starts and after it ends, so resting with no grader record means **pending**, not finished — calling it terminal would abort a healthy run polled between kickoff and pickup. Either way it does not pass.

The credential is read from the environment and travels only in a request header. It is never placed in a URL, a log line, or a payload, and there is a test asserting that.

## What is not here

Reproduction artifacts are not committed to this repository. An audit's output is the paper's reproduction, not this tool's source, and the two have different lifecycles, different licences, and different audiences. Publishing artifacts to their own repositories is designed in [`specs/`](../specs/) and not yet built.
