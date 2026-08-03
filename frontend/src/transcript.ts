// The arithmetic behind the session screen (006s), kept out of the components
// so it can be tested without a browser.
//
// Every function here reads the run's own record and nothing else. Where the
// record is silent — an event with no timestamp, a session with no usage
// anywhere — the answer is an explicit absence, never a filled-in zero.
import type { Event, Usage } from './api'

// A run's real elapsed span, in milliseconds since the epoch. Null when no event
// carries a usable timestamp: there is then no clock to draw, and the caller
// falls back to the plain list rather than inventing one.
export type Span = { from: number; to: number }

// timeOf reads an event's timestamp as a number. NaN for an absent or unparsable
// one, which every caller filters out rather than treating as the epoch.
export function timeOf(e: Event): number {
  return e.time ? new Date(e.time).getTime() : NaN
}

// bounds is the raw extent of the events, before any widening — so a caller that
// unions it with something else does not carry a padded end into the union.
function bounds(events: Event[]): Span | null {
  let from = Infinity
  let to = -Infinity
  for (const e of events) {
    const t = timeOf(e)
    if (Number.isNaN(t)) continue
    if (t < from) from = t
    if (t > to) to = t
  }
  return from === Infinity ? null : { from, to }
}

// widen guarantees a span has width, so every position calculation downstream
// divides by something.
function widen(s: Span): Span {
  return { from: s.from, to: s.to > s.from ? s.to : s.from + 1 }
}

// runSpan is the interval the scrubber draws: the session's own extent, taken
// from the phases it recorded, widened to cover any event outside it.
//
// The events alone are the wrong span. Every event is emitted inside `running`,
// so a bar drawn to fit them would show one phase at full width and hide that
// resolving took seven seconds and publishing seventeen — which is exactly the
// proportion a reader came for. The phases alone are also wrong, because a live
// session has not recorded its last phase yet and its events run past the end.
export function runSpan(events: Event[], phaseAt: Record<string, string> | undefined): Span | null {
  const ev = bounds(events)
  const stamps = Object.values(phaseAt ?? {})
    .map((s) => new Date(s).getTime())
    .filter((t) => !Number.isNaN(t))
  if (stamps.length === 0) return ev && widen(ev)
  return widen({
    from: Math.min(...stamps, ev?.from ?? Infinity),
    to: Math.max(...stamps, ev?.to ?? -Infinity),
  })
}

// A phase band: one lifecycle phase, positioned and sized by the time it really
// took. Width is a fraction of the span, not of the phase count — a `running`
// phase that ate 99% of the session is 99% of the bar, which is the whole point.
export type Band = { phase: string; left: number; width: number }

// PHASE_ORDER is the lifecycle spine, duplicated from the timeline strip so this
// module stays free of component imports.
const PHASE_ORDER = ['submitted', 'resolving', 'running', 'publishing', 'done', 'failed']

// phaseBands lays the session's phases across a span. A phase that started after
// the span ends, or one with no recorded start, is simply absent — the bar never
// fabricates a step the session did not take. The last phase runs to the end of
// the span, since nothing recorded when it stopped.
export function phaseBands(phaseAt: Record<string, string> | undefined, span: Span): Band[] {
  if (!phaseAt) return []
  const total = span.to - span.from
  const starts = PHASE_ORDER.filter((p) => phaseAt[p])
    .map((p) => ({ phase: p, at: new Date(phaseAt[p]).getTime() }))
    .filter((s) => !Number.isNaN(s.at))
    .sort((a, b) => a.at - b.at)
  const bands: Band[] = []
  for (let i = 0; i < starts.length; i++) {
    const start = Math.max(starts[i].at, span.from)
    const end = i + 1 < starts.length ? Math.min(starts[i + 1].at, span.to) : span.to
    if (end <= start) continue // a phase that left no room on this span
    bands.push({ phase: starts[i].phase, left: (start - span.from) / total, width: (end - start) / total })
  }
  return bands
}

// A histogram of where the run spent itself. `weighted` says which question the
// bars answer: true when at least one event carried a token charge and the bars
// are token-weighted, false when they count events instead. The distinction is
// surfaced to the reader — "how much did this cost" and "how busy was it" are
// different questions and the bars must not silently switch between them.
export type Histogram = { bars: number[]; weighted: boolean }

// histogram buckets events across the span. Heights are normalised to the
// tallest bucket, so a bar is a fraction of the peak in [0, 1].
export function histogram(events: Event[], span: Span, buckets: number): Histogram {
  const weighted = events.some((e) => e.usage)
  const raw = new Array(buckets).fill(0)
  const total = span.to - span.from
  for (const e of events) {
    const t = timeOf(e)
    if (Number.isNaN(t)) continue
    const i = Math.min(buckets - 1, Math.max(0, Math.floor(((t - span.from) / total) * buckets)))
    raw[i] += weighted ? (e.usage ? freshOf(e.usage) : 0) : 1
  }
  const peak = Math.max(...raw)
  return { bars: peak > 0 ? raw.map((v) => v / peak) : raw, weighted }
}

// totalOf is a usage record's total, the one definition the whole client uses.
export function totalOf(u: Usage): number {
  return u.InputTokens + u.OutputTokens + u.CachedInputTokens + cacheWrittenOf(u)
}

// cacheWrittenOf is every cache-write token, whatever lifetime it bought. The
// split is priced, not read: a viewer wants one number for "what this run wrote
// to the cache".
export function cacheWrittenOf(u: Usage): number {
  return (u.CacheWrite5mTokens ?? 0) + (u.CacheWrite1hTokens ?? 0) + (u.CacheWriteTokens ?? 0)
}

// freshOf is the tokens a request actually pushed through the model: the new
// input and the output, with the cache reads left out.
//
// The total is the wrong measure of a *step*. On a real session it is 99% cache
// reads (11.6M of 11.7M), and a cache read scales with how long the conversation
// has grown rather than with what the agent did — so ranking steps by total
// draws a monotonic ramp and calls it spend. Fresh tokens vary with the work,
// which is the question the histogram and the per-card figure are asking. The
// full breakdown, cache included, is always one click away in the inspector.
export function freshOf(u: Usage): number {
  return u.InputTokens + u.OutputTokens
}

// firstAtOrAfter is the index of the first event at or after an instant — where
// the scrubber scrolls to. Events are in emission order, which is time order.
// It returns the last index when the instant is past the final event, so
// scrubbing to the far right lands on the end of the run rather than nowhere.
export function firstAtOrAfter(events: Event[], t: number): number {
  let last = -1
  for (let i = 0; i < events.length; i++) {
    const at = timeOf(events[i])
    if (Number.isNaN(at)) continue
    last = i
    if (at >= t) return i
  }
  return last
}

// matches is the transcript search predicate: an event matches when the query
// appears in what the reader can see of it — its content, its tool, or its kind.
// An empty query matches everything, so clearing the box restores the run.
export function matches(e: Event, q: string): boolean {
  const needle = q.trim().toLowerCase()
  if (!needle) return true
  return (
    (e.content ?? '').toLowerCase().includes(needle) ||
    (e.tool ?? '').toLowerCase().includes(needle) ||
    e.kind.toLowerCase().includes(needle)
  )
}

// splitTitle divides an event's content into the line a reader scans and the
// rest. The split is typographic and nothing else: the title is the event's own
// first line, verbatim. A transcript is evidence, and evidence does not get a
// narrator — so nothing here summarises, rewrites, or infers.
//
// A first line long enough to be a paragraph is left as the body with no title,
// because a title that wraps to four lines is not a title.
const maxTitleChars = 120
export function splitTitle(content: string | undefined): { title: string; body: string } {
  const text = (content ?? '').trim()
  if (!text) return { title: '', body: '' }
  const brk = text.indexOf('\n')
  const first = brk === -1 ? text : text.slice(0, brk).trim()
  if (first.length > maxTitleChars) return { title: '', body: text }
  return { title: first, body: brk === -1 ? '' : text.slice(brk + 1).trim() }
}

// compactTokens renders a token count the way it appears beside an event: whole
// thousands past a thousand, so a column of them stays scannable.
export function compactTokens(n: number): string {
  if (n < 1000) return String(n)
  if (n < 10_000) return (n / 1000).toFixed(1).replace(/\.0$/, '') + 'k'
  if (n < 1_000_000) return Math.round(n / 1000) + 'k'
  return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M'
}

// clockOf is an event's wall-clock time in the reader's own zone, to the minute
// — the granularity the gutter has room for.
export function clockOf(e: Event): string {
  const t = timeOf(e)
  if (Number.isNaN(t)) return ''
  return new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false })
}

// offsetOf is how far into the run an event happened, which is what makes two
// events comparable when the absolute clock means nothing to the reader.
export function offsetOf(e: Event, span: Span): string {
  const t = timeOf(e)
  if (Number.isNaN(t)) return ''
  const s = Math.max(0, Math.round((t - span.from) / 1000))
  if (s < 60) return `+${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `+${m}m ${s % 60}s`
  return `+${Math.floor(m / 60)}h ${m % 60}m`
}
