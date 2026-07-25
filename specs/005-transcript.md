# 005 — Capture and publish the reproduction transcript

**Status:** draft
**Scope:** capturing the agent's full session transcript and publishing it into the reproduction repository as first-class evidence.
**Depends on:** [002](002-publishing-reproductions.md) (the redistribution gate), [006f](006f-taking-a-reproduction-out.md) (the finalize step it hooks into).

## Problem

A published reproduction today carries the work (the agent's code and commit history) and a verdict (`replication.json`). It is missing the middle term: the **evidence** of how the reproduction was actually produced — the tool calls, the reasoning, the dead ends, the grading. For a *reproducibility* auditor that is the most valuable artifact of all, and the README already promises it: "records every decision the paper left unwritten." The transcript is that promise made concrete and checkable.

## The trust model this fits

A reproduction is a **claim / evidence / verdict** triad, and who authors each layer is a deliberate boundary — the subject of an audit never authors its own grade:

| Layer | Author | Artifact |
|---|---|---|
| **Claim** | the agent, inside the sandbox | the reproduction — code, `src/`, history, `REPRODUCIBILITY.md` |
| **Evidence** | the backend's event log, fetched by the system | **the transcript (this spec)** |
| **Verdict** | the system, outside the sandbox | `replication.json` |

The transcript belongs to the same system-authored layer as the manifest: it comes from the backend's own event log, not the agent's say-so, so the agent cannot rewrite its own record. That is what makes it evidence rather than another claim.

## Decisions

**Fetched from the backend, not written by the agent.** The transcript is read from the session's event stream (`Sessions.Events.ListAutoPaging` — confirmed present; events include agent messages, thinking, tool use and results, user inputs, and the outcome-evaluation spans). The provider seam gains a neutral `Transcript` method so the contract stays backend-agnostic and the anthropic adapter maps its events down. A mini-004-style contract addition.

**Captured before the session is released.** The adapter archives a session on `CloseRun`, so the transcript must be fetched before teardown, or by `finish` while the session is still readable. `audit.Result` (or the finalize step) carries it.

**Committed by the system in the finalize step.** The same `finish`/finalize moment that commits `replication.json` also commits the transcript — same author (the org token, outside the sandbox), same trust posture. Two files: `transcript.jsonl` (the canonical, greppable event stream) and a rendered `TRANSCRIPT.md` (a human-readable narrative).

**Neutral event model, with a raw escape hatch.** A portable `Event{Role, Kind, Content, Time}` for cross-backend fidelity, with the raw event JSON preserved in `transcript.jsonl` so nothing is lost.

**The content gate applies — this is 002's gate at the content level.** A transcript embeds third-party content inline: the paper's text pulled into context, tool outputs, possibly gated data. So before it can make a repository eligible for the public flip, its content must pass a redistribution check — the same reason a committed dataset bars going public. A transcript that carries gated content keeps the repository private. This extends the verifier ([006f](006f-taking-a-reproduction-out.md)) from file paths to inline content.

**Publishing raw agent thinking is deliberate but bounded.** The reasoning trail is the point — it shows *how* decisions were made. It is verbose and full of dead ends; that is acceptable for evidence. It is never made public without passing the content gate.

## Interface (sketch)

```go
// In internal/provider: the neutral event and the seam addition.
type Event struct {
	Role    string    // "agent" | "user" | "system"
	Kind    string    // "message" | "thinking" | "tool_use" | "tool_result" | "grade" | ...
	Content string
	Time    time.Time
	Raw     json.RawMessage // the backend's original event, losslessly
}

type Transcriber interface {
	Transcript(ctx context.Context, h RunHandle) ([]Event, error)
}

// In internal/publish: render and gate.
func RenderTranscript(events []provider.Event) (jsonl []byte, md []byte)
func TranscriptLeaks(events []provider.Event) []string // third-party content found inline
```

The anthropic adapter implements `Transcript`; `finish` fetches it, runs `TranscriptLeaks`, commits both files, and folds any leak into the same public-flip bar the tree verifier uses.

## Acceptance criteria

1. The provider seam gains a neutral `Transcript`; the anthropic adapter maps `Sessions.Events` to `[]Event`, tested against a canned event stream with no network.
2. The transcript is captured before the session is released.
3. `finish` commits `transcript.jsonl` and `TRANSCRIPT.md` in the same, system-authored moment as `replication.json`, and the commit is idempotent (no redundant commit on re-run, like the manifest).
4. `TranscriptLeaks` flags inline third-party content, and a transcript that leaks bars the repository from the public flip — asserted directly.
5. A reproduction with a transcript reads as claim + evidence + verdict: the repository contains the agent's history, the transcript, and the manifest, each from its proper author.

## Out of scope

Diffing two reproductions' transcripts; a web viewer for the transcript (that is 006e). Redacting *within* a transcript to make an otherwise-gated one publishable — a leak bars the public flip; it is not laundered.
