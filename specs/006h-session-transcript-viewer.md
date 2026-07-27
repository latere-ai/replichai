# 006h — The session transcript viewer

**Status:** implemented
**Scope:** show a replication session's **transcript** in the app — the agent's trajectory (thinking, tool calls, tool results, the grade) and its token usage. The capture already exists at the provider layer and is published into the repository ([005](005-transcript.md)); this spec makes the web app *serve and render* it, replacing the hard-coded 404 stub. A child of [006](006-web-application.md).
**Depends on:** [006g](006g-projects-and-replication-sessions.md) (the session the transcript belongs to), [005](005-transcript.md) (what a transcript is and where it lives), [003](003-reproduction-registry.md) (the forge pass-through that reads it).

## Problem

The transcript is the evidence — "the backend's own event log, not the agent's say-so" ([005](005-transcript.md)) — and the whole point of a session is to be inspectable after the run. The engine fetches it from the managed-agents session (`provider.Transcript` over `Beta.Sessions.Events`) and [005](005-transcript.md) commits it into the repository as `transcript.jsonl`. But the web surface never wired it: the server's transcript reader is a stub that returns not-found for every request, so `GET …/transcript` always 404s and the UI has no trajectory view and no usage anywhere. A user looking at a finished session sees a link to GitHub and nothing else — the "terribly executed" gap. This spec serves the transcript the engine already produced and renders it as a trajectory.

## Decisions

**Read the committed transcript through the forge, not the live session.** A managed-agents session is released when its run closes (`CloseRun`), so for a finished session the durable transcript is the `transcript.jsonl` [005](005-transcript.md) committed to the repository. The server reads it by pass-through over the forge (003) — the same read-only path the reproduction list uses — parses the newline-delimited `provider.Event`s, and serves them. No transcript is copied into Postgres; the repository stays the source of truth, consistent with "derived, not stored". Rejected: re-fetching from `provider.Transcript` on every view — the session is usually gone, and it would put a managed-agents call on a read path.

**The transcript is served as structured events, and rendered as a trajectory.** The endpoint returns the parsed `provider.Event` sequence (`role`, `kind` ∈ message | thinking | tool_use | tool_result | grade, `tool`, `content`, `time`) as JSON, not a rendered blob — so the client renders a real trajectory: agent messages and thinking, each tool call paired with its result, and the final grade, in time order. The raw markdown `TRANSCRIPT.md` remains in the repo for a plain read; the app renders from the events. Rejected: serving the pre-rendered `TRANSCRIPT.md` — it throws away the structure the viewer needs to group a call with its result and to fold long tool output.

**Token usage is shown per session, truthfully or not at all.** Usage — input, output, cached, and total — comes first from the session's recorded `RunStatus.Usage` (006b observes it during the run). When it was never recorded (a historical CLI backfill), it is *derived from the transcript* itself: each `model_request_end` event carries a `model_usage` object, and summing them reconstructs the total, with the same field selection the anthropic adapter uses so a derived total agrees with a recorded one. Only when neither exists does the viewer show "not recorded", never a fabricated zero — the same honesty rule [004](004-go-port.md)/manifest `Rate` already follows. (Derivation reads each event's backend-original `raw`; the server drops `raw` from the response afterward, since a full transcript is megabytes of it.) The viewer shows the usage summary beside the trajectory.

**A running session streams; a finished one is static.** While a session runs, the existing SSE stream (006a) already narrates phase and live usage, so the viewer shows progress and the transcript becomes available once publishing has committed it. The endpoint answers `409 not_ready` (not 404) for a session that has not published its transcript yet, so the client can distinguish "no transcript exists" from "not written yet". Rejected: blocking the read until the run finishes — a viewer must never hold a request open on a run.

## Interface (sketch)

```go
// On the read surface (006a): a session-scoped transcript (the session is the
// job, so it hangs off the existing /jobs path — no API rename).
// GET /jobs/{id}/transcript -> { events: []provider.Event, usage: Usage|null }
type Transcript struct {
    Events []provider.Event `json:"events"`
    Usage  *provider.Usage  `json:"usage"` // nil == not recorded
}

// The server's transcript reader resolves the session -> its repository ->
// transcript.jsonl over the forge, parses the events, and pairs them with the
// session's recorded usage. ErrNotReady maps to 409; a genuine miss is
// ErrReproNotFound (404).
```

Frontend ([006e](006e-frontend.md) revision): a trajectory component under a session — thinking and message blocks, each `tool_use` folded together with its `tool_result` (long output collapsible), the `grade` called out, and a usage summary (input / output / cached / total, or "not recorded"). i18n `en` + `zh`.

## Acceptance criteria

1. `GET /jobs/{id}/transcript` returns the parsed event sequence and the session's usage for a published session; the three existing reproductions each render a non-empty trajectory read from their committed `transcript.jsonl`.
2. The events carry their kind (message | thinking | tool_use | tool_result | grade), tool name, content, and time, and are time-ordered, so the client pairs each tool call with its result and shows the final grade.
3. Usage is shown from the recorded `RunStatus.Usage`; a session with no recorded usage shows "not recorded", not a zero — asserted with a backfilled session.
4. A session that has not yet published a transcript answers `409 not_ready`, distinct from a `404` for a session that does not exist.
5. The reader is a forge pass-through — no transcript row is written to Postgres — and a forge/transport failure is a `500`, never disguised as a miss (the 003 read-error rule).
6. The trajectory and usage render in the frontend under a session, in `en` and `zh`.

## Out of scope

Live tailing of an in-flight managed-agents session (the SSE phase/usage stream already covers "what is happening now"; the trajectory appears once committed). Editing or annotating a transcript. Diffing two sessions' trajectories. Secret redaction beyond what [005](005-transcript.md)/[009](009-secret-containment-when-the-agent-commits-live.md) already enforce before a transcript is committed — the viewer shows what the gate already cleared. The project/session entity and routes ([006g](006g-projects-and-replication-sessions.md)).
