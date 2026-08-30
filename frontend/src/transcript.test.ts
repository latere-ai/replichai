// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import type { Event } from './api'
import { cacheWrittenOf, clockOf, compactTokens, firstAtOrAfter, freshOf, histogram, matches, offsetOf, phaseBands, runSpan, seekTarget, splitTitle, totalOf } from './transcript'

const at = (iso: string, over: Partial<Event> = {}): Event => ({ role: 'agent', kind: 'message', time: iso, ...over })

test('a run with no timestamps anywhere has no span to draw', () => {
  expect(runSpan([{ role: 'agent', kind: 'message' }], undefined)).toBeNull()
  expect(runSpan([], undefined)).toBeNull()
})

test('with no phases recorded the span covers the events, and is never zero-width', () => {
  const s = runSpan([at('2026-07-14T09:12:00Z'), at('2026-07-14T10:59:00Z')], undefined)!
  expect(s.to - s.from).toBe(107 * 60 * 1000)
  // One event, or many in the same instant, still leaves something to divide by.
  const point = runSpan([at('2026-07-14T09:12:00Z')], undefined)!
  expect(point.to).toBeGreaterThan(point.from)
})

test('the drawn span is the session, not just the events inside it', () => {
  // The real shape: resolving takes seconds, then every event lands inside a
  // multi-hour running phase. Fitting the bar to the events would hide that.
  const phaseAt = {
    submitted: '2026-07-14T09:12:00Z',
    resolving: '2026-07-14T09:12:07Z',
    running: '2026-07-14T09:12:14Z',
    done: '2026-07-14T10:59:00Z',
  }
  const events = [at('2026-07-14T09:20:00Z'), at('2026-07-14T10:50:00Z')]
  const s = runSpan(events, phaseAt)!
  expect(s.from).toBe(Date.parse('2026-07-14T09:12:00Z'))
  expect(s.to).toBe(Date.parse('2026-07-14T10:59:00Z'))
})

test('a live session whose events run past its last recorded phase still fits on the bar', () => {
  const s = runSpan([at('2026-07-14T11:30:00Z')], { submitted: '2026-07-14T09:12:00Z', running: '2026-07-14T09:12:14Z' })!
  expect(s.to).toBe(Date.parse('2026-07-14T11:30:00Z'))
})

test('with neither phases nor timestamps there is no clock to draw', () => {
  expect(runSpan([{ role: 'agent', kind: 'message' }], undefined)).toBeNull()
  expect(runSpan([], {})).toBeNull()
})

test('phase bands are proportional to elapsed time, not to the number of phases', () => {
  // The shape of a real session: resolving is seconds, running is hours.
  const span = { from: Date.parse('2026-07-14T09:00:00Z'), to: Date.parse('2026-07-14T11:00:00Z') }
  const bands = phaseBands(
    {
      submitted: '2026-07-14T09:00:00Z',
      resolving: '2026-07-14T09:00:36Z',
      running: '2026-07-14T09:01:00Z',
      done: '2026-07-14T11:00:00Z',
    },
    span,
  )
  expect(bands.map((b) => b.phase)).toEqual(['submitted', 'resolving', 'running'])
  // `done` opens at the very end of the span and so has no room; it is dropped
  // rather than drawn at zero width.
  expect(bands[2].width).toBeCloseTo(119 / 120, 5)
  expect(bands[0].left).toBe(0)
  // The bands tile the span without gaps.
  expect(bands[1].left).toBeCloseTo(bands[0].left + bands[0].width, 9)
})

test('a session that never recorded a phase gets no bands rather than invented ones', () => {
  const span = { from: 0, to: 1000 }
  expect(phaseBands(undefined, span)).toEqual([])
  expect(phaseBands({}, span)).toEqual([])
})

test('the histogram measures work, not conversation length — cache reads do not move the bars', () => {
  // The shape of a real session: cache reads dwarf everything and grow with the
  // conversation, so weighting by the total would draw a monotonic ramp and call
  // it spend. Here the second step does a tenth of the work of the first while
  // its cache read is ten times larger.
  const span = { from: Date.parse('2026-07-14T09:00:00Z'), to: Date.parse('2026-07-14T09:04:00Z') }
  const h = histogram(
    [
      at('2026-07-14T09:00:30Z', { usage: { InputTokens: 500, OutputTokens: 500, CachedInputTokens: 1_000_000 } }),
      at('2026-07-14T09:03:30Z', { usage: { InputTokens: 50, OutputTokens: 50, CachedInputTokens: 10_000_000 } }),
    ],
    span,
    4,
  )
  expect(h.weighted).toBe(true)
  expect(h.bars).toEqual([1, 0, 0, 0.1])
})

test('the histogram is token-weighted when any event was charged, and counts events when none was', () => {
  const span = { from: Date.parse('2026-07-14T09:00:00Z'), to: Date.parse('2026-07-14T09:04:00Z') }
  const charged = [
    at('2026-07-14T09:00:30Z', { usage: { InputTokens: 10, OutputTokens: 0, CachedInputTokens: 0 } }),
    at('2026-07-14T09:03:30Z', { usage: { InputTokens: 100, OutputTokens: 0, CachedInputTokens: 0 } }),
  ]
  const w = histogram(charged, span, 4)
  expect(w.weighted).toBe(true)
  // Normalised to the peak: the cheap bucket is a tenth of the expensive one.
  expect(w.bars).toEqual([0.1, 0, 0, 1])

  // The same two moments, uncharged: the shape is now "how busy", and says so.
  const c = histogram([at('2026-07-14T09:00:30Z'), at('2026-07-14T09:03:30Z')], span, 4)
  expect(c.weighted).toBe(false)
  expect(c.bars).toEqual([1, 0, 0, 1])
})

test('the histogram survives a run where nothing is placeable', () => {
  const h = histogram([{ role: 'agent', kind: 'message' }], { from: 0, to: 10 }, 3)
  expect(h.bars).toEqual([0, 0, 0])
})

test('scrubbing lands on the first event at or after the instant, and clamps past the end', () => {
  const events = [at('2026-07-14T09:00:00Z'), at('2026-07-14T09:05:00Z'), at('2026-07-14T09:10:00Z')]
  expect(firstAtOrAfter(events, Date.parse('2026-07-14T08:00:00Z'))).toBe(0)
  expect(firstAtOrAfter(events, Date.parse('2026-07-14T09:01:00Z'))).toBe(1)
  expect(firstAtOrAfter(events, Date.parse('2026-07-14T09:05:00Z'))).toBe(1)
  // Past the last event, the far right of the bar is the end of the run.
  expect(firstAtOrAfter(events, Date.parse('2026-07-15T00:00:00Z'))).toBe(2)
})

test('search reads what the reader can see: content, tool, and kind', () => {
  const e = at('2026-07-14T09:00:00Z', { kind: 'tool_use', tool: 'sandbox_run', content: 'python train.py --folds loo' })
  expect(matches(e, 'train.py')).toBe(true)
  expect(matches(e, 'SANDBOX')).toBe(true) // case-insensitive
  expect(matches(e, 'tool_use')).toBe(true)
  expect(matches(e, 'nothing here')).toBe(false)
  // An empty or blank query restores the whole run.
  expect(matches(e, '')).toBe(true)
  expect(matches(e, '   ')).toBe(true)
})

test('a title is the event’s own first line, verbatim, and never a summary', () => {
  expect(splitTitle('Resolving the reference\nThe DOI points to CHI ’24.')).toEqual({
    title: 'Resolving the reference',
    body: 'The DOI points to CHI ’24.',
  })
  // A single line is a title with no body.
  expect(splitTitle('Clone and inspect the repository')).toEqual({ title: 'Clone and inspect the repository', body: '' })
  expect(splitTitle(undefined)).toEqual({ title: '', body: '' })
})

test('a first line too long to be a title stays the body', () => {
  const rambling = 'x'.repeat(200)
  expect(splitTitle(rambling)).toEqual({ title: '', body: rambling })
})

test('the figure beside an event is its work, and the total is still the total', () => {
  const u = { InputTokens: 1807, OutputTokens: 240, CachedInputTokens: 62_040 }
  expect(freshOf(u)).toBe(2047)
  expect(totalOf(u)).toBe(64_087)
})

test('token counts stay scannable in a column', () => {
  expect(compactTokens(0)).toBe('0')
  expect(compactTokens(999)).toBe('999')
  expect(compactTokens(2100)).toBe('2.1k')
  expect(compactTokens(9000)).toBe('9k')
  expect(compactTokens(22_700)).toBe('23k')
  expect(compactTokens(2_124_865)).toBe('2.1M')
})

test('an event with no timestamp shows no clock and no offset, rather than the epoch', () => {
  const none: Event = { role: 'agent', kind: 'message' }
  expect(clockOf(none)).toBe('')
  expect(offsetOf(none, { from: 0, to: 1 })).toBe('')
})

test('an offset says how far into the run something happened', () => {
  const span = { from: Date.parse('2026-07-14T09:00:00Z'), to: Date.parse('2026-07-14T12:00:00Z') }
  expect(offsetOf(at('2026-07-14T09:00:41Z'), span)).toBe('+41s')
  expect(offsetOf(at('2026-07-14T09:22:04Z'), span)).toBe('+22m 4s')
  expect(offsetOf(at('2026-07-14T10:47:00Z'), span)).toBe('+1h 47m')
})

test('cache writes count toward the total, whatever lifetime they bought', () => {
  // A cache write is billed above fresh input (011), so a total that drops it
  // under-reports the run. The 5m/1h split is a pricing detail; a reader wants
  // one number, and a backend that reports no lifetime still gets counted.
  const u = {
    InputTokens: 100,
    OutputTokens: 20,
    CachedInputTokens: 5_000,
    CacheWrite5mTokens: 700,
    CacheWrite1hTokens: 300,
    CacheWriteTokens: 40,
  }
  expect(cacheWrittenOf(u)).toBe(1_040)
  expect(totalOf(u)).toBe(6_160)
  // freshOf is deliberately untouched: it ranks a step by its work, and a cache
  // write scales with the conversation rather than with what the agent did.
  expect(freshOf(u)).toBe(120)
})

test('usage from a backend that reports no cache writes still totals', () => {
  const u = { InputTokens: 10, OutputTokens: 5, CachedInputTokens: 1 }
  expect(cacheWrittenOf(u)).toBe(0)
  expect(totalOf(u)).toBe(16)
})

// The scrubber's cursor follows the event it landed on, not the pointer.
//
// A session that finishes its work early and sits in a phase for hours leaves a
// stretch of bar with no events under it — an overnight run puts every event in
// the first quarter. Each position in that tail resolves to the last event, so a
// cursor drawn where the pointer was reported 09:00 while the transcript showed
// 18:45. Snapping it back is both honest and a visible answer to "why can I not
// scrub further".
test('a scrub into the dead tail snaps the cursor back to the last event', () => {
  // Events span the first quarter; the session runs four times as long.
  const events = [at('2026-08-03T18:00:00Z'), at('2026-08-03T18:30:00Z'), at('2026-08-03T19:00:00Z')]
  const span = { from: Date.parse('2026-08-03T18:00:00Z'), to: Date.parse('2026-08-04T02:00:00Z') }

  const far = seekTarget(events, span, 0.9)
  expect(far?.index).toBe(2)
  // 19:00 is one eighth of an eight-hour span, not the 0.9 that was clicked.
  expect(far?.cursor).toBeCloseTo(0.125, 3)

  // Inside the events the cursor still lands on the event, which is at or after
  // the click rather than exactly on it.
  const mid = seekTarget(events, span, 0.05)
  expect(mid?.index).toBe(1)
  expect(mid?.cursor).toBeCloseTo(0.0625, 3)

  // The start is the first event.
  expect(seekTarget(events, span, 0)?.index).toBe(0)
  expect(seekTarget(events, span, 0)?.cursor).toBeCloseTo(0, 3)
})

// Nothing to land on is not a cursor at zero: a filtered-out list leaves the
// scrubber where it was rather than jumping to the top.
test('a scrub with no events to land on reports nothing', () => {
  const span = { from: 0, to: 1000 }
  expect(seekTarget([], span, 0.5)).toBeNull()
  expect(seekTarget([{ role: 'agent', kind: 'message' }], span, 0.5)).toBeNull()
})
