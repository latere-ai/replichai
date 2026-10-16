// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { CRITERIA, inner, rate, releaseTone, scoreOf, unresolved, type Claim, type Findings } from './findings'

const claim = (over: Partial<Claim>): Claim => ({
  claim: 'c',
  loop: 'inner',
  outcome: 'verified',
  evidence: 'e',
  citation: '§1',
  ...over,
})

const findings = (over: Partial<Findings> = {}): Findings => ({
  schema: 1,
  verdict: 'partial',
  summary: 's',
  graded_at: '2026-07-14T10:41:00Z',
  components: { verified: 2, partial: 1, blocked: 1, total: 4 },
  boundary: [
    claim({ claim: 'accuracy' }),
    claim({ claim: 'ordering', outcome: 'partial' }),
    claim({ claim: 'latency', outcome: 'blocked' }),
    claim({ claim: 'preferred', loop: 'outer', outcome: 'not_scored' }),
  ],
  ...over,
})

test('inner keeps only the loop an outcome means something for', () => {
  const f = findings()
  expect(inner(f).map((c) => c.claim)).toEqual(['accuracy', 'ordering', 'latency'])
})

test('unresolved is what did not cleanly reproduce', () => {
  expect(unresolved(findings()).map((c) => c.claim)).toEqual(['ordering', 'latency'])
})

// The case a table of outcomes hides: the run verified the claim and still
// found a number the paper got wrong.
test('a verified claim that disagrees with the paper is unresolved', () => {
  const f = findings({
    boundary: [
      claim({
        claim: 'accuracy',
        mismatches: [
          { kind: 'numeric', what: 'held-out accuracy', reported: '91.4%', observed: '89.6%', reported_value: 91.4, observed_value: 89.6, unit: '%', note: '-1.8 pp' },
        ],
      }),
    ],
  })
  expect(unresolved(f).map((c) => c.claim)).toEqual(['accuracy'])
})

// The list is how the server sends a claim's disagreements, and an empty list
// is a claim that agreed with the paper.
test('an empty list of mismatches leaves a verified claim resolved', () => {
  const f = findings({ boundary: [claim({ claim: 'accuracy', mismatches: [] })] })
  expect(unresolved(f)).toEqual([])
})

// An outer claim is not scored, so there is nothing about it to resolve.
// Listing it under a heading about what failed would read as an accusation.
test('outer claims are never unresolved', () => {
  const f = findings({ boundary: [claim({ claim: 'preferred', loop: 'outer', outcome: 'not_scored' })] })
  expect(unresolved(f)).toEqual([])
})

test('a run where everything reproduced has nothing unresolved', () => {
  const f = findings({ boundary: [claim({}), claim({ claim: 'b' })] })
  expect(unresolved(f)).toEqual([])
})

test('rate counts a partial as half', () => {
  expect(rate({ verified: 2, partial: 1, blocked: 1, total: 4 })).toBeCloseTo(0.625)
  expect(rate({ verified: 4, partial: 0, blocked: 0, total: 4 })).toBe(1)
})

// A run with no components has no rate. Printing 0.00 would state a finding it
// does not have.
test('rate is null rather than zero when there is nothing to divide', () => {
  expect(rate({ verified: 0, partial: 0, blocked: 0, total: 0 })).toBeNull()
  expect(rate(undefined)).toBeNull()
})

test('scoreOf finds a row and reports nothing when the run scored nothing', () => {
  const f = findings({ scorecard: [{ criterion: 'code', found: true, url: 'https://x' }] })
  expect(scoreOf(f, 'code')?.url).toBe('https://x')
  expect(scoreOf(f, 'data')).toBeUndefined()
  expect(scoreOf(findings(), 'code')).toBeUndefined()
})

test('the scorecard order is the five the rubric scores', () => {
  expect(CRITERIA).toEqual(['code', 'data', 'license', 'preregistration', 'supplement'])
})

// A dead link is something an author can still fix; nothing published is not.
// They must never share a tone.
test('a dead link and nothing published are toned differently', () => {
  expect(releaseTone('dead')).not.toBe(releaseTone('none'))
  expect(releaseTone('found')).toBe('var(--ok)')
})

// A page the run was not allowed to fetch says nothing about the authors. It
// is neutral, since nothing is known to be wrong, and never the tone of
// nothing published, which is a finding about the authors.
test('a page the run could not fetch is toned apart from every finding', () => {
  const tone = releaseTone('not_fetched')
  for (const other of ['found', 'partial', 'none', 'dead'] as const) expect(tone).not.toBe(releaseTone(other))
  expect(tone).toBe('var(--fg-2)')
})

// A boundary the server has not sent yet must not throw on the way in.
test('a missing boundary is empty, not a crash', () => {
  const f = { ...findings(), boundary: undefined as unknown as Claim[] }
  expect(inner(f)).toEqual([])
  expect(unresolved(f)).toEqual([])
})
