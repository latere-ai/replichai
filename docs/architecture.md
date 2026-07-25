# Architecture

## Shape

```
paper reference
      │
      ▼
┌──────────────┐  DOI, else arXiv upgraded to its DOI, else a content digest
│ paper        │  a stable identity, plus a slug that is presentation only
└──────────────┘
      │
      ▼
┌──────────────┐  reads the manifests of everything already published
│ registry     │  warns about prior reproductions, allocates <slug>-<suffix>
└──────────────┘
      │
      ▼
┌──────────────┐  creates the private repository the agent will commit into
│ github       │
└──────────────┘
      │  RepoMount
      ▼
┌──────────────┐   reads    ┌──────────┐
│ orchestrator │ ─────────► │ method/  │  instructions · rubric · task template
└──────────────┘            └──────────┘
      │
      │ speaks only the Provider contract
      ▼
┌────────────────────────────────────────────┐
│ provider adapter                           │  the only backend-aware code
└────────────────────────────────────────────┘
      │
      ▼
┌────────────────────────────────────────────┐
│ agent runtime                              │  sandboxed worker + tools + grader
│   • network access for the artifact hunt   │
│   • a shell for building and running       │
│   • the repository mounted at              │
│     /workspace/repro, committed live       │
│   • grades output against the rubric       │
└────────────────────────────────────────────┘
      │
      ▼
┌────────────────────────────────────────────┐
│ finalize — outside the sandbox             │  verify the committed tree,
│                                            │  write replication.json,
│                                            │  commit the transcript, invite
└────────────────────────────────────────────┘
```

An audit without `--publish` stops at the agent runtime and collects artifacts into a local directory. Everything above and below the runtime is the publishing path, and it is optional per run — not a separate program.

## Layers

**Paper identity** (`internal/paper/`) answers "is this the same paper we already audited?". Identity is a resolved external identifier — a DOI, else an arXiv ID upgraded to its published DOI, else a digest of the fetched content — never the title, which changes between preprint and camera-ready. The slug answers "what should the repository be called?" and never feeds identity. An arXiv lookup that cannot complete errors rather than guessing, so identity does not drift with connectivity. The network is reached through a `Fetcher` seam the CLI supplies, which keeps the package testable without one.

**Orchestrator** (`internal/audit/`) knows the shape of an audit: resolve a model, prepare a sandbox, define the worker, start a run, wait for it, collect what it produced, release it. It knows nothing about any backend. Every call it makes goes through the provider contract. It returns values rather than printing, so an HTTP layer can wrap it without changes, and it exposes `Start` separately from `Run` so a caller can kick off a run and reattach later.

**Provider contract** (`internal/provider/provider.go`) is the seam: name the adapter, say where in the sandbox output belongs, resolve a model preference, ensure a workspace, ensure a worker, start a run, poll it, list artifacts, fetch one, fetch the run's transcript, close the run. Any backend that can run a sandboxed, tool-using worker and grade its output can sit behind it.

**Provider adapters** (`internal/provider/`) translate. Each one is the sole place its backend's vocabulary, endpoints, and payload shapes appear — and, because the adapter is that one place, it also chooses the public name it registers under. Two ship today: `anthropic` (package `internal/provider/anthropic`), which talks to the managed-agents runtime in use, and `memory`, which runs nothing and exists so the orchestrator can be tested and the contract demonstrated. The seam itself names neither: it learns which adapter is the default when that adapter registers, so `provider.go` holds no backend name.

Every operation that can block takes a `context.Context` and honours cancellation. Porting to a different runtime is one new adapter package plus one changed default; nothing above the seam moves. The `anthropic` adapter talks to its backend through that vendor's official SDK rather than hand-rolled HTTP, which buys typed payloads, retry on rate limits and server errors, and correct protocol headers.

**Publishing** (`internal/publish/`) owns the manifest, the redistribution gate, repository naming, and the transcript rendering. It is split so that everything decidable without a network is pure: `Classify` and `PlanPublication` touch no repository and no clock; `Publish` is the only part that creates anything, and it does so through `Forge`, a seam over repository hosting that mirrors the provider seam.

**The registry** (`internal/registry/`) derives the set of published reproductions by reading their manifests, so there is no second source of truth to drift. It answers three questions: has this paper been reproduced before, what slug did its first reproduction fix, and what unique name should the next one get. Reproductions of one paper are related by identity, never by slug or title. It reads through a `Source` — an organisation on GitHub, or a directory on disk.

**GitHub** (`internal/github/`) is a small hand-rolled client for the handful of operations publishing needs: create a private repository, read the head commit and its tree, put a file, invite a collaborator, flip visibility. It runs server-side with the organisation's token, and none of it belongs inside the sandbox.

**Method** (`method/`) is data, not code. Three documents, loaded at run time:

| File | Role |
|---|---|
| `instructions.md` | The worker's standing instructions: the inner/outer doctrine, the search procedure, the never-dos |
| `rubric.md` | Six binary criteria. The grader scores every run against these |
| `task-template.md` | The per-run task, with `{{PAPER}}` substituted |

Keeping the rubric out of the worker definition is deliberate: the rubric travels with each run, so sharpening it costs a text edit rather than a redefinition of the worker. Iterating on the bar is the cheapest operation in the system, and it should stay that way.

`{{OUTPUT_DIR}}` in the instructions is filled in by the provider, so the method never hard-codes one runtime's filesystem layout. When a repository is mounted, the mount path is the output directory — the reproduction is written where it will be committed from.

**CLI** (`cmd/replichai/`) is argument parsing, output, and the composition root: it is where the vendor adapter is imported, where the HTTP fetcher is supplied, and where the pieces above are wired into `audit`, `resolve`, `publish`, `finish`, and `providers`.

A second implementation of the core — orchestrator, backend seam, and method loading — lives in `reference/python/`. It is not dead code: `method/` and `evals/` are shared, and the two conform when they produce the same verdict for the same case. It has not followed the Go seam past 005, so its provider has no `Transcript`; identity, publishing, and the registry are Go-only.

## Grading

Grading is not a post-processing step; it runs inside the loop. Each run carries its rubric and a revision budget. The runtime grades the worker's output, and if it falls short the worker revises and is graded again, up to the budget. What comes back is a verdict, not just a deliverable.

This is why the rubric criteria are written as binary checks with stated evidence requirements. "A useful report" cannot be graded. "A rate derived from a stated denominator, or it fails" can.

## Claim, evidence, verdict

A published reproduction is three things, and who authors each is the point: the subject of an audit never writes its own grade.

| Layer | Author | Artifact |
|---|---|---|
| Claim | the agent, inside the sandbox | the reproduction — code, commit history, `REPRODUCIBILITY.md` |
| Evidence | the backend's own event log, fetched by the system | `transcript.jsonl` and `TRANSCRIPT.md` |
| Verdict | the system, outside the sandbox | `replication.json` |

The transcript is rendered from the same events into two forms. `transcript.jsonl` is canonical and machine-readable — one normalised event per line, typed fields up top and the backend's original event nested under `raw`, so it is both consistent to parse and lossless. The narrative is chunked: `TRANSCRIPT.md` is an index over `transcript/NNN.md` parts of forty events each, interlinked previous/index/next, with per-event content capped. A full run runs to many megabytes, and one file that size is neither readable nor commitable through an ordinary contents API.

The whole set is committed in a single commit through the Git Data API — blob, tree, commit, ref — so the evidence lands atomically rather than as a spray of per-file commits, with no inline size limit. It is idempotent by blob sha: re-finishing a reproduction whose transcript has not changed commits nothing.

## Two publish paths, on purpose

**The mount path** is the real one. The repository is created before the run and mounted into the sandbox, and the agent commits and pushes its own history as it works. Because nothing can be filtered before the push, the gate becomes a post-run verifier: `VerifyTree` reads what was actually committed and flags what is almost certainly third-party — the paper, a dataset, model weights, an archive, a leaked secret. It cannot allow-list, because an agent-authored repository is a real software project, not a fixed set of report files. So the method is the primary control (keep downloads out of the repository) and the verifier is the backstop.

**The filesystem path** stays as the offline way to exercise publishing with no backend at all: `Classify`'s allow-list, `PlanPublication`, `FSForge`, and the `publish` subcommand, which previews by default and writes only with `--live`. It is how the gate is tested and how the manifest shape is checked without touching GitHub.

## Trust boundaries

Artifact paths and file contents come out of a sandbox that has just executed code found on the internet. They are untrusted input:

- collection refuses any path that resolves outside the destination directory (absolute paths, `..` traversal);
- compiled residue (`.pyc`, `__pycache__/`) is dropped — machine-specific, and a reproduction repository should hold sources;
- silence from the grader is never success. The backend rests in the same state both before a run starts and after it ends, so resting with no grader record means **pending**, not finished — calling it terminal would abort a healthy run polled between kickoff and pickup. Either way it does not pass.

Publication fails closed. A repository is created private and stays private. A tree carrying third-party paths is recorded in the manifest as a redistribution that did not clear, and `EligibleForPublic` reads that manifest — so the decision to make a reproduction public has a fact behind it rather than an operator's memory. A transcript carrying a secret is reported at finalize; folding that into the eligibility decision, and wiring the visibility flip itself to a caller, is designed and not yet built ([009](../specs/009-secret-containment-when-the-agent-commits-live.md)). Nothing today makes a repository public, which is why the gap is not yet a hole — and 009 is what has to land before that changes.

Ownership is granted by invitation — the organisation invites a GitHub login as a collaborator — so the service never holds anyone's repository credentials.

The backend credential is read from the environment and travels only in a request header. It is never placed in a URL, a log line, or a payload, and there is a test asserting that. The GitHub token is read from `REPLICHAI_GITHUB_TOKEN` at the CLI edge; only the mount credential enters the sandbox, and it is meant to be scoped to the one repository being written.

## What is not here

Reproduction artifacts are not committed to this repository. An audit's output is the paper's reproduction, not this tool's source, and the two have different lifecycles, different licences, and different audiences — which is why each reproduction gets its own repository under its own organisation.

What is designed but not built is the service around the engine: the web application ([`specs/006-web-application.md`](../specs/006-web-application.md) and its children) and the replication track past the inner loop ([`specs/007-from-reproduction-to-replication.md`](../specs/007-from-reproduction-to-replication.md)). [`specs/README.md`](../specs/README.md) is the map of what is built and what is not.
