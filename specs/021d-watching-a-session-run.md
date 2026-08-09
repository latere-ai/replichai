# 021d — Watching a session run

**Status:** designed.

**Scope:** make a running session watchable. The server polls the provider's transcript for a live run and relays new events onto the SSE stream the frontend already consumes; the session screen renders them through the existing viewer and converges on the committed transcript when the run finalizes; the Sessions list shows a running session's elapsed time, spend, and event count, and opens the live view when clicked.

**Depends on:** [006a](006a-http-api-and-streaming.md) (the SSE stream), [006s](006s-reading-a-session-in-time.md) (the transcript viewer this feeds), [005](005-the-transcript-is-the-evidence.md) (the committed transcript it converges on), [011](011-what-a-session-costs.md) (the spend it shows).

## Problem

A session runs for an hour or more. For that hour the app says `RUNNING` and shows a phase name. `Job.Status` is a `provider.RunStatus` snapshot, and `GET /jobs/{id}/transcript` reads the file from the forge — which does not exist until finalize commits it. So the only honest answer the product can give about a run in flight is "wait".

That is worse than an inconvenience. A run that has gone wrong — stuck on a download, looping on a failing build, burning the project's credits — is indistinguishable from one that is working, until it is over and the money is spent.

## Decisions

**Poll the provider, relay as a stream.** `Provider.Transcript` returns the run's events and its contract says to fetch it *before* `CloseRun` — not after the run is terminal, which is what `ListArtifacts` says and this deliberately does not. So the worker, which already polls `Poll` for a running job, also fetches the transcript on that tick and publishes events it has not published before onto the job's existing SSE topic. No adapter gains a method; the `memory` provider makes the whole path testable offline.

**The relay is stateless about content and stateful only about position.** It remembers how many events it has already emitted for a run and sends the tail. A backend that renumbers or truncates its own event list therefore cannot make the app invent events — the worst case is a gap, which the viewer shows as a gap.

**Live events are the same shape as committed ones.** The frontend's transcript module (006s) already parses the committed file into its event model. The relayed events go through the same parser, so the live view and the post-hoc view are one component, and the moment the committed transcript lands the screen keeps rendering without a reload having changed what it means.

**A running session's tab is the transcript, and it says it is live.** The session screen's default tab for a running job is the transcript, headed by a live strip — elapsed, spend so far, events so far — rather than the phase-band timeline, which needs a finished run to be proportional. When the run finalizes the strip becomes the timeline and the scrubber appears.

**The Sessions list shows the run, not just its name.** A running row carries elapsed time, spend so far, and the current phase; the pulse stays. Clicking it opens the live view. This is the design's `RUNNING · today, 11:40 · running for 34m · $14.75` row, with the numbers coming from what 011 already meters.

**Nothing here is spent unless the reader is looking.** The relay polls only while at least one SSE subscriber is attached to that job, and falls back to the existing phase-only stream when none is. A watched run costs an extra transcript fetch per tick; an unwatched one costs exactly what it costs today.

## Acceptance criteria

1. While a job runs and a subscriber is attached, new provider events reach the SSE stream, each exactly once, in order.
2. With no subscriber, no transcript fetch is issued for that job.
3. A backend that returns a shorter list than before does not cause a re-emit or a panic.
4. The session screen renders relayed events through the same parser as the committed transcript, with a live strip carrying elapsed, spend, and event count.
5. When the run finalizes, the screen shows the committed transcript and its timeline without the reader reloading.
6. A running row in the Sessions list shows elapsed and spend and opens the live view.
7. A browser test drives 4–6 against a mocked stream, and fails without the change. Go tests cover 1–3 against the `memory` provider.

## Not in scope

Streaming from the provider natively (no adapter exposes it). Stopping a run from the live view — `session-stop` already does that. Backfilling live events for a run that started before this lands.
