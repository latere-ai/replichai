---
title: "Reading a session in time"
status: built
---

# 006s — Reading a session in time

**Status:** built. `TranscriptEvent` wraps `provider.Event` with the usage the backend charged that one request, derived in the same pass that sums the session total, so the parts add up to the whole by construction rather than by agreement, and the committed `transcript.jsonl` schema is untouched. `stripRaw` is gone; `attachUsage` drops the raw payloads on its way through. The client gained `transcript.ts` (the run's span, proportional phase bands, the histogram, the search predicate, the title split, seek), all pure and unit-tested, with the screen a thin layer over it. Two things moved during the build. The span is the *session's*, not the events': every event is emitted inside `running`, so a bar fitted to them showed one phase at full width and hid that resolving took seven seconds. And the histogram weighs **fresh** tokens (input + output), not the total: on a real session cache reads are 99% of the total and grow with the conversation, so a total-weighted bar measures length rather than work, and every card read the same figure. The per-card cost carries the same definition, stated once in the legend above the stream rather than in a tooltip, because a card clips its own overflow, so an overlay anchored inside one is cut off. Covered by two handler tests (committed and live paths, per-event figures summing to the session total), a unit-test suite over `transcript.ts` (sixteen at the time of writing, twenty-two now; the module has grown with 011's token classes and the cursor correction below), and a browser test that asserts the running band owns >90% of the bar, that a scrub scrolls the stream while rewriting *zero* per-card styles, that search narrows the stream and the rail counts together and clearing restores them, and that no raw identifier reaches the filter rail.
**Corrected after the build.** The bar reported two different quantities as one number: where you clicked, and where you ended up. A session that finishes its work early and then sits in a phase for hours leaves a stretch of bar with no events under it (an overnight run put every event in the first quarter), and every position in that tail resolves to the last event, which is right. The cursor stayed under the pointer, so it read 09:00 while the stream sat at 18:45, and clicking the middle of the bar looked like a jump to the bottom.

The span is not the bug and is unchanged; the decision below stands. What changed is that the cursor now follows the event the scrub landed on. Scrubbing into the dead tail snaps back to the last event, which is both where the stream went and a visible answer to *why can I not scrub further*. The arithmetic is `seekTarget` in `transcript.ts`, beside the rest of the bar's maths, with its own test.

**Scope:** make a session's transcript *navigable*. Put the run's own clock on the screen (a per-event timestamp, a scrubber over the real elapsed span with the phases drawn to scale and event density under them), add search and a selected-event inspector, and serve the per-event token usage the server already parses and then discards. A revision of [006h](006h-session-transcript-viewer.md), which made the transcript visible; this makes it readable.
**Depends on:** [006h](006h-session-transcript-viewer.md) (the endpoint, the event model, the usage accuracy rule), [006g](006g-projects-and-replication-sessions.md) (the session), [005](005-transcript.md) (what a transcript is).

## Problem

A real session is 771 events. [006h](006h-session-transcript-viewer.md) renders them as one flat list in emission order, and that is all: no clock, no way to reach a moment, no way to find a string, no way to see an event's full content when the card truncates it. Two hours of work arrive as an undifferentiated scroll.

The data to do better is already on the wire and discarded twice over. Every event carries `time`: served today, rendered nowhere. Every event that closes a model request carries a `model_usage` object in its backend-original `raw`; `deriveUsage` reads it to reconstruct a session total and `stripRaw` then deletes it, so the one place where cost is attributable to a *decision* is discarded on the way out. The session already knows when it entered each phase (`phase_at`, drawn today as a flat strip of labels with no width).

So the run has a shape (where the time went, where the tokens went, which phase was expensive) and none of it reaches the reader.

## Decisions

**Time is the primary axis, and it is the run's real clock.** Each card gets its own timestamp in the gutter. Above the stream, a scrubber spans the session, first recorded phase to last, widened to cover any event outside it (the build corrected this from *first event to last*, for the reason the status gives): the phases from `phase_at` drawn *proportionally* (a `running` phase that took 5h53m of a 5h54m session occupies the width it earned), and beneath them a density histogram bucketed over that span. Dragging or clicking the scrubber scrolls the stream to the first event at or after that instant. Rejected: a timeline laid out by event index. It looks the same and misleads, because it draws a thirty-second burst of tool calls and a twenty-minute training run at the same width, which is exactly the information the reader came for.

**Per-event usage is served, not derived twice and dropped.** The transcript response gains a per-event `usage`, read from `raw.model_usage` in the same pass `deriveUsage` already makes and with the same field selection, before `stripRaw` runs. An event that carries no usage gets none: the card shows nothing there, never a zero. This is [006h](006h-session-transcript-viewer.md)'s rule applied one level down: the absence of a number is a fact about the run, not a gap to fill. It holds on both read paths, because live and committed events are both built by `translateEvent`, which sets `raw` on every event. Rejected: summing tokens per phase or per tool on the server. The client has the events and the arithmetic is the client's.

**The histogram is weighted by *fresh* tokens where tokens are known, and by count where they are not.** A bar's height should say where the run spent its work. Event count says only how chatty the backend was, so token weight is right, but the *total* is the wrong token figure. On a real session cache reads are 99% of it (11.6M of 11.7M), and a cache read scales with how long the conversation has grown rather than with what the agent did in that step; weighting by the total draws a monotonic ramp and calls it spend, and every card renders the same number. So the weight is input + output, cache reads excluded, and the per-card figure carries the same definition. The full breakdown, cache included, is one click away in the inspector, and the total remains the total everywhere it is named. A session whose events carry no usage at all still gets a histogram, drawn by count, and the legend says which of the two it is. Rejected: leaving the histogram out when usage is missing. The time distribution is worth seeing on its own.

**A card's title is the event's own first line.** The card gains a title above its body: the first line of `content`, with the remainder as the body. Nothing is generated, summarized, or inferred; the split is typographic, and an event whose content is a single line has no body. Rejected: an LLM-written or heuristic summary. The transcript is evidence, and evidence is not narrated.

**Search and the kind filters narrow the list; the scrubber moves within it.** A search term filters the stream to matching events, the way the kind toggles already do, and the counts update to match. The scrubber never filters; it scrolls. Rejected: dimming the events outside the cursor's window, as the design draws it. At 771 cards a per-card `filter` is a compositing layer per card recomputed on every pointer frame; the design was drawn against twelve events and the technique does not survive the real number. Scrolling to the moment is also better: the reader wanted to *go* there.

**The third column becomes the selected event.** Clicking a card selects it, and the right rail shows that event in full: its exact timestamp and offset into the run, its kind and tool, its untruncated content and tool input or output, and its usage when it has one, with the session's usage summary below. This is what makes a 771-event stream workable: the cards stay scannable because the detail has somewhere to live. Rejected (for now): the design's claim ↔ evidence panel. Nothing in the system decomposes a paper into per-claim verdicts (the manifest carries a scalar `Verdict` and an optional `Rate`), so the panel could only be filled by inventing the claims. It needs the worker to emit a decomposition and the manifest to carry it; that is its own spec.

**Every event kind gets a name.** `outcome_evaluation_start` and `outcome_evaluation_ongoing` currently reach the filter rail as raw snake_case, because the grade family in the client lists only `outcome_evaluation_end`. The rail is rebuilt here, so the family is completed and an unrecognised kind falls back to a readable form of its own identifier rather than the identifier itself.

## Interface (sketch)

```go
// The transcript response (006h) gains per-event usage. Populated from
// raw.model_usage in the same pass deriveUsage makes, before stripRaw; nil for
// an event that carries none.
type Event struct {
    // … role, kind, tool, content, time as today
    Usage *provider.Usage `json:"usage,omitempty"`
}
```

Frontend ([006e](006e-frontend.md) revision), all within the existing session screen:

- **Scrubber**: proportional phase bands from `phase_at`, a density histogram over the event span, and a draggable cursor that scrolls the stream and then comes to rest on the event it reached rather than under the pointer. Start and end clock labels.
- **Stream**: timestamp and kind badge in the gutter; a title line (first line of content) and body; the tool chip and I/O block as today; per-event usage when present; selected state.
- **Left rail**: search input, kind toggles with live counts, tools used, hide-lifecycle toggle (as today, plus search).
- **Right rail**: the selected event in full, then the session usage summary. With nothing selected, the usage summary alone.

Strings are `en` only, matching the current `i18n.Lang` union.

## Acceptance criteria

1. `GET /jobs/{id}/transcript` returns a per-event `usage` for every event whose backend-original payload carries `model_usage`, and omits it for every event that does not, asserted on both the committed and the live read path.
2. The per-event usage figures sum to the session usage the same endpoint derives, so the two can never disagree.
3. The scrubber's phase bands are proportional to real elapsed time from `phase_at`, and its histogram buckets real event timestamps: a session whose `running` phase is 99% of the elapsed span shows a `running` band that is 99% of the width. The span drawn is the session's, widened to cover any event outside it, not the events' alone.
3b. The histogram ranks a step by the tokens it pushed through the model, not by the conversation's accumulated cache: two steps whose cache reads differ tenfold but whose work differs the other way round rank by the work.
4. Dragging or clicking the scrubber scrolls the stream to the first event at or after that instant, **and the cursor comes to rest on that event rather than under the pointer**. The two differ whenever the session outlives its events: a scrub into a stretch of bar with nothing under it snaps back to the last event, so the cursor never reports a time the stream is nowhere near.
5. Searching filters the stream to events whose content, tool, or kind matches, and the kind counts reflect the filtered set; clearing the search restores every event.
6. Selecting an event shows it in full in the right rail (untruncated content, exact time, offset into the run, and its usage when it has one); deselecting restores the usage summary.
7. Every event kind rendered in the filter rail has a human label; no raw snake_case identifier reaches the screen.
8. A card's title is the first line of its content and its body is the remainder; an event with a single-line content has no body element.
9. The stream still renders 771 events without clipping or overlap, and scrubbing does not degrade. The [browser layout test](../frontend/test/transcript-layout.test.mjs) is extended to cover the new card shape and to assert the scrub interaction touches no per-card style.

## Out of scope

Claim ↔ evidence (needs a per-component decomposition from the worker and a manifest schema that carries it; its own spec). Money: there is no pricing table and no sandbox accounting in the system, so a dollar cost or a sandbox-minutes figure could only be fabricated. Exporting a transcript. Diffing two sessions. Virtualizing the stream: a plain list renders 771 cards fine, and virtualization would silently exempt off-screen rows from the layout assertions. The rest of the operator redesign (project detail, run queue, replication diff, the outer-loop screens); this spec is the session screen only.
