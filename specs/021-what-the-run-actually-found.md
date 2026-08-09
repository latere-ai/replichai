# 021 — What the run actually found

**Status:** designed. Parent of [021a](021a-the-findings-as-data.md), [021b](021b-a-verdict-that-reads-like-one.md), [021c](021c-what-the-authors-released.md), [021d](021d-watching-a-session-run.md), [021e](021e-the-project-rail.md).

**Scope:** turn the reproduction's findings from prose into data, and build the screens that read it — the operator design (`Replichai Operator.dc.html`). Five tightly-scoped children; this page holds the decisions they share and the order they land in.

**Depends on:** [002](002-publishing-a-reproduction.md) (the manifest and the redistribution gate), [005](005-the-transcript-is-the-evidence.md) (the transcript), [007a](007a-the-study-protocol-as-an-artifact.md) (`instrument.json`, the precedent for an agent-authored schema), [011](011-what-a-session-costs.md) (what a session costs), [015](015-a-verdict-belongs-to-a-session.md) (a verdict belongs to the session that produced it), [018](018-one-list-of-projects.md) (the projects list this adds a tile to).

## Problem

The product's whole claim is that it tells you *what* reproduced and what did not, claim by claim, with the boundary drawn and the unwritten decisions named. `method/rubric.md` demands exactly that: a per-component reproduction table (criterion 1), every component classified inner or outer with a citation (2), at least five hidden decisions with their sensitivity (3), and code/data/license/preregistration/supplement each scored with a fetched URL or an explicit "none found" naming where it looked (4).

None of it reaches the screen, because none of it is data. `Manifest` carries four things — verdict, rate, redistribution, method digest (`internal/publish/manifest.go:25-64`). `instrument.json` covers the *outer* loop only: protocol, analysis, and per-component servability (`internal/instrument/instrument.go:32-35`). Everything the rubric demands about the *inner* loop exists once, as prose the agent wrote into `REPRODUCIBILITY.md`, `SOURCES.md`, and `UNVERIFIED.md`.

So the Verdict pane can render a grade and a rate and nothing else (`App.tsx:1660`), and the Artifacts pane is a file browser over the reproduction repository — it cannot say what the *authors* released, which is the first thing an auditor wants and the thing `SOURCES.md` already establishes. A reader who wants the finding has to open a markdown file on GitHub and read it. The rate, the one number that does reach the screen, is the summary the rubric explicitly forbids reading on its own.

A second gap, unrelated in cause and identical in effect: a session that is running is a phase name and a spinner. `Job.Status` is a `provider.RunStatus` snapshot and the transcript is read from the forge, which has nothing in it until finalize. So the answer to "what is it doing right now" is "wait an hour and read it".

## Decisions

**The agent emits its findings as data, and the system verifies them.** A new `verdict.json`, written into the reproduction repository by the agent under a new rubric criterion, holding the boundary table, the decisions, the scorecard, and what the authors released. A new `internal/verdict` package owns the schema and validates it, exactly as `internal/instrument` does for the protocol — that package is the precedent, and it settled the argument already: an agent can be asked for structured output and the system can refuse it if it does not validate.

The alternative was parsing the markdown at finalize. It was rejected on the attestation model, not on effort: the manifest is *system-authored*, and a parser that decides what "## Boundary" meant makes the renderer the author of the verdict. A heading the agent worded differently would produce an empty table that reads as a paper with no boundary — a false claim, silently.

The cost is honest and stated: **a session that ran before this lands has no `verdict.json`, and its panes keep showing what they show today** — the grade and the rate. Nothing is backfilled, because there is nothing to backfill from that the system may sign.

**`verdict.json` never blocks publishing.** A run whose file is missing or invalid still finalizes, with the failure recorded in the manifest and in `UNVERIFIED.md`. The findings are what the agent claims; the transcript is the evidence; the manifest is the system's word. Making the system's word depend on the agent's JSON parsing correctly would invert all three.

**A verdict and its findings belong to the session.** [015](015-a-verdict-belongs-to-a-session.md) established this after the project-level panes showed one paper's reproduction under another paper's name. So the rich boundary view lives at `/p/{id}/s/{jobId}/verdict` and the released-artifacts view at `/p/{id}/s/{jobId}/artifacts`. The project keeps its cross-paper read and gains a rail that *summarises the latest session per paper* — labelled as that, never as the project's own result.

**The design's project is one paper; ours holds many.** The operator design says "a project holds one paper". [012](012-a-project-holds-papers.md) made that false and [015](015-a-verdict-belongs-to-a-session.md) fixed the screens that had not noticed. Where the design puts a per-paper fact on the project header, we put it on the paper group inside the Sessions tab. The design's four tabs map to: Sessions (ours), Verdict & boundary (ours, the cross-paper read, each row linking to that session's rich pane), Collaborators (ours) — and Artifacts stays off the project, where 015 put it.

**The semantic ramp is retuned to the design.** `--ok --warn --err --run` move from saturated to the design's muted values, which is what the stylesheet's own header advertises as a two-line retint. The design is dark-only, so each dark value gets a light twin at usable contrast on paper. This is app-wide by construction — every phase chip, verdict tone, and progress bar moves — and lands first, alone, so a regression in it is not confused with a regression in a new pane.

**Live is a poll relayed as a stream, not a new provider method.** `Provider.Transcript` returns the run's events and is documented as needing to be fetched *before* `CloseRun` — not after the run is terminal, unlike `ListArtifacts`. So the server polls it for a running session and relays new events onto the SSE stream the frontend already consumes. No adapter gains a method, and the `memory` provider keeps the whole thing testable offline.

## The children, in order

| Spec | What it lands | Why here |
|---|---|---|
| [021a](021a-the-findings-as-data.md) | `internal/verdict`, the rubric criterion, finalize verification, `GET /jobs/{id}/verdict` | Everything else reads it |
| [021b](021b-a-verdict-that-reads-like-one.md) | The session's Verdict & boundary pane: verdict card, boundary table, mismatches and unresolved, decisions left unwritten | The largest single gap |
| [021c](021c-what-the-authors-released.md) | The session's Artifacts pane gains *what the authors released* above the reproduction repository | Same data, second pane |
| [021d](021d-watching-a-session-run.md) | Live events relayed onto SSE; a running session opens a live transcript | Independent of 021a |
| [021e](021e-the-project-rail.md) | The Sessions tab's rail, Publish… on the header, and `SPEND · LAST 30 DAYS` on the projects screen | Reads 021a and 021d |

The palette retune lands with 021b, as its first commit, alone.

## Not in scope

Comparing sessions (the design's `COMPARE SESSIONS` button); the metrics screen; the paper picker and the cross-paper registry views; a public, signed-out registry; changing what the agent is *asked* to do beyond emitting the file it already had to write in prose.
