# 006s — Reading a session in time

**Status:** designed
**Scope:** make a session's transcript *navigable*. Put the run's own clock on the screen — a per-event timestamp, a scrubber over the real elapsed span with the phases drawn to scale and event density under them — add search and a selected-event inspector, and serve the per-event token usage the server already parses and then discards. A revision of [006h](006h-session-transcript-viewer.md), which made the transcript visible; this makes it readable.
**Depends on:** [006h](006h-session-transcript-viewer.md) (the endpoint, the event model, the usage honesty rule), [006g](006g-projects-and-replication-sessions.md) (the session), [005](005-transcript.md) (what a transcript is).

## Problem

A real session is 771 events. [006h](006h-session-transcript-viewer.md) renders them as one flat list in emission order, and that is the whole of it: no clock, no way to reach a moment, no way to find a string, no way to see an event's full content when the card truncates it. Two hours of work arrive as an undifferentiated scroll.

The data to do better is already on the wire and thrown away twice over. Every event carries `time` — served today, rendered nowhere. Every event that closes a model request carries a `model_usage` object in its backend-original `raw`; `deriveUsage` reads it to reconstruct a session total and `stripRaw` then deletes it, so the one place where cost is attributable to a *decision* is discarded on the way out. The session already knows when it entered each phase (`phase_at`, drawn today as a flat strip of labels with no width).

So the run has a shape — where the time went, where the tokens went, which phase was expensive — and none of it reaches the reader.

## Decisions

**Time is the spine, and it is the run's real clock.** Each card gets its own timestamp in the gutter. Above the stream, a scrubber spans first event to last: the phases from `phase_at` drawn *proportionally* (a `running` phase that took 5h53m of a 5h54m session occupies the width it earned), and beneath them a density histogram bucketed over that span. Dragging or clicking the scrubber scrolls the stream to the first event at or after that instant. Rejected: a timeline laid out by event index — it looks the same and lies, because it draws a thirty-second flurry of tool calls and a twenty-minute training run at the same width, which is exactly the information the reader came for.

**Per-event usage is served, not derived twice and dropped.** The transcript response gains a per-event `usage`, read from `raw.model_usage` in the same pass `deriveUsage` already makes and with the same field selection, before `stripRaw` runs. An event that carries no usage gets none — the card shows nothing there, never a zero. This is [006h](006h-session-transcript-viewer.md)'s rule applied one level down: the absence of a number is a fact about the run, not a gap to fill. It holds on both read paths, because live and committed events are both built by `translateEvent`, which sets `raw` on every event. Rejected: summing tokens per phase or per tool on the server — the client has the events and the arithmetic is the client's.

**The histogram is weighted by tokens where tokens are known, and by count where they are not.** A bar's height should say "this is where the run spent itself". Token weight says that; event count says only how chatty the backend was. A session whose events carry no usage at all still gets a histogram, drawn by count, and the legend says which one it is. Rejected: leaving the histogram out when usage is missing — the time distribution is worth seeing on its own.

**A card's title is the event's own first line.** The card grows a title above its body: the first line of `content`, with the remainder as the body. Nothing is generated, summarised, or inferred — the split is typographic, and an event whose content is a single line simply has no body. Rejected: an LLM-written or heuristic summary; the transcript is evidence, and evidence does not get a narrator.

**Search and the kind filters narrow the list; the scrubber moves within it.** A search term filters the stream to matching events, the way the kind toggles already do, and the counts update to match. The scrubber never filters — it scrolls. Rejected: dimming the events outside the cursor's window, as the design draws it. At 771 cards a per-card `filter` is a compositing layer per card recomputed on every pointer frame; the design was drawn against twelve events and the technique does not survive the real number. Scrolling to the moment is also simply better: the reader wanted to *go* there.

**The third column becomes the selected event.** Clicking a card selects it, and the right rail shows that event in full — its exact timestamp and offset into the run, its kind and tool, its untruncated content and tool input or output, and its usage when it has one — with the session's usage summary below. This is what makes a 771-event stream workable: the cards stay scannable because the detail has somewhere to live. Rejected (for now): the design's claim ↔ evidence panel. Nothing in the system decomposes a paper into per-claim verdicts — the manifest carries a scalar `Verdict` and an optional `Rate` — so the panel could only be filled by inventing the claims. It needs the worker to emit a decomposition and the manifest to carry it; that is its own spec.

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

- **Scrubber** — proportional phase bands from `phase_at`, a density histogram over the event span, a draggable cursor that scrolls the stream. Start and end clock labels.
- **Stream** — timestamp + kind badge in the gutter; a title line (first line of content) and body; the tool chip and I/O block as today; per-event usage when present; selected state.
- **Left rail** — search input, kind toggles with live counts, tools used, hide-lifecycle toggle (as today, plus search).
- **Right rail** — the selected event in full, then the session usage summary. With nothing selected, the usage summary alone.

Strings are `en` only, matching the current `i18n.Lang` union.

## Acceptance criteria

1. `GET /jobs/{id}/transcript` returns a per-event `usage` for every event whose backend-original payload carries `model_usage`, and omits it for every event that does not — asserted on both the committed and the live read path.
2. The per-event usage figures sum to the session usage the same endpoint derives, so the two can never disagree.
3. The scrubber's phase bands are proportional to real elapsed time from `phase_at`, and its histogram buckets real event timestamps — a session whose `running` phase is 99% of the elapsed span shows a `running` band that is 99% of the width.
4. Dragging or clicking the scrubber scrolls the stream to the first event at or after that instant.
5. Searching filters the stream to events whose content, tool, or kind matches, and the kind counts reflect the filtered set; clearing the search restores every event.
6. Selecting an event shows it in full in the right rail — untruncated content, exact time, offset into the run, and its usage when it has one; deselecting restores the usage summary.
7. Every event kind rendered in the filter rail has a human label; no raw snake_case identifier reaches the screen.
8. A card's title is the first line of its content and its body is the remainder; an event with a single-line content has no body element.
9. The stream still renders 771 events without clipping or overlap, and scrubbing does not degrade — the [browser layout test](../frontend/test/transcript-layout.test.mjs) is extended to cover the new card shape and to assert the scrub interaction touches no per-card style.

## Out of scope

Claim ↔ evidence (needs a per-component decomposition from the worker and a manifest schema that carries it — its own spec). Money: there is no pricing table and no sandbox accounting in the system, so a dollar cost or a sandbox-minutes figure could only be fabricated. Exporting a transcript. Diffing two sessions. Virtualising the stream — a plain list renders 771 cards fine, and virtualisation would silently exempt off-screen rows from the layout assertions. The rest of the operator redesign (project detail, run queue, replication diff, the outer-loop screens) — this spec is the session screen only.
