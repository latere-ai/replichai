---
title: "Watching a session run"
status: built
---

# 021d — Watching a session run

**Status:** built as a revision of this spec, not as it was written. **Most of what it specifies already existed and was not built here.**

The premise was wrong. `getSessionTranscript` (`internal/httpapi/handlers.go`) already reads the backend's live event stream for a non-terminal session by its observed run id, returns `live: true`, and the viewer already polls on that flag, marks the stream live, and counts its events (006h/006s). Criteria 1, 2, 3, 5 and the Go half of 7 describe a relay that ships today. Writing them here was a failure to read the code before designing over it.

What was missing, and what landed: **a running row said nothing about how long it had been running.** It showed a pulsing phase pill and a cost and no duration, so the screen could not answer how long an in-flight run had been spending. `frontend/src/when.ts` gained `elapsed`, with tests, and `SessionRow` renders it while the session is live, ticking once a second and holding no timer when the row is terminal.

`elapsed` is deliberately not `relativeTime` with different words. An age rounds down to one unit, so a run 89 minutes in would read "an hour ago", which is imprecise on a row that is still charging.

**Scope:** make a running session watchable. The server polls the provider's transcript for a live run and relays new events onto the SSE stream the frontend already consumes; the session screen renders them through the existing viewer and converges on the committed transcript when the run finalizes; the Sessions list shows a running session's elapsed time, spend, and event count, and opens the live view when clicked.

**Depends on:** [006a](006a-http-api-and-streaming.md) (the SSE stream), [006s](006s-reading-a-session-in-time.md) (the transcript viewer this feeds), [005](005-transcript.md) (the committed transcript it converges on), [011](011-what-a-session-costs.md) (the spend it shows).

## Problem

A session runs for an hour or more. For that hour the app says `RUNNING` and shows a phase name. `Job.Status` is a `provider.RunStatus` snapshot, and `GET /jobs/{id}/transcript` reads the file from the forge, which does not exist until finalize commits it. So the only answer the product can give about a run in flight is "wait".

That is more than an inconvenience. A run that has gone wrong (stuck on a download, looping on a failing build, spending the project's credits) is indistinguishable from one that is working until it is over and the money is spent.

## Decisions

**Poll the provider, relay as a stream.** `Provider.Transcript` returns the run's events, and its contract says to fetch it *before* `CloseRun`, not after the run is terminal, which is what `ListArtifacts` says and this deliberately does not. So the worker, which already polls `Poll` for a running job, also fetches the transcript on that tick and publishes events it has not published before onto the job's existing SSE topic. No adapter gains a method; the `memory` provider makes the whole path testable offline.

**The relay is stateless about content and stateful only about position.** It remembers how many events it has already emitted for a run and sends the tail. A backend that renumbers or truncates its own event list therefore cannot make the app invent events; the worst case is a gap, which the viewer shows as a gap.

**Live events are the same shape as committed ones.** The frontend's transcript module (006s) already parses the committed file into its event model. The relayed events go through the same parser, so the live view and the post-hoc view are one component, and when the committed transcript lands the screen keeps rendering without a reload changing what it means.

**A running session's tab is the transcript, and it says it is live.** The session screen's default tab for a running job is the transcript, headed by a live strip (elapsed, spend so far, events so far) rather than the phase-band timeline, which needs a finished run to be proportional. When the run finalizes the strip becomes the timeline and the scrubber appears.

**The Sessions list shows the run, not just its name.** A running row carries elapsed time, spend so far, and the current phase; the pulse stays. Clicking it opens the live view. This is the design's `RUNNING · today, 11:40 · running for 34m · $14.75` row, with the numbers coming from what 011 already meters.

**Nothing here is spent unless the reader is looking.** The relay polls only while at least one SSE subscriber is attached to that job, and falls back to the existing phase-only stream when none is. A watched run costs an extra transcript fetch per tick; an unwatched one costs exactly what it costs today.

## Acceptance criteria

Struck through are the ones 006h/006s already satisfied before this spec was written. They are kept rather than deleted, because the record of a spec that specified existing behavior is worth keeping.

1. ~~While a job runs, new provider events reach the client.~~ Already true: the transcript route reads the live stream by run id.
2. ~~No fetch is issued for an unwatched job.~~ Already true: the read is request-driven, so nothing polls unless a reader is looking.
3. ~~A shorter event list does not re-emit or panic.~~ Not applicable: there is no relay holding a position, because there is no relay.
4. ~~The live view renders through the same parser as the committed transcript.~~ Already true, with a live marker and an event count.
5. ~~The screen converges on the committed transcript without a reload.~~ Already true: the viewer stops polling when `live` goes false.
6. **A running row in the Sessions list shows how long it has been running, and opens the live view.** The second half was already true; the first is what landed here.
7. `elapsed` is unit-tested at its edges (under a minute, under an hour, over an hour, an unparseable start, and a clock that runs backwards) and does not round the minutes away where `relativeTime` would.

## Not in scope

Streaming from the provider natively (no adapter exposes it). Stopping a run from the live view; `session-stop` already does that. Backfilling live events for a run that started before this lands.
