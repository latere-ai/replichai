// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { citationBits, gradeable, groupByPaper, isGradeable, listCitation } from './papers'
import type { Job, ProjectPaper } from './api'

const paper = (key: string, title: string): ProjectPaper => ({
  key,
  ref: key,
  title,
  added_by: 'u',
  added_at: '2026-01-01T00:00:00Z',
  sessions: 0,
})

const job = (id: string, paperKey: string, phase: string, repo?: string): Job => ({
  id,
  kind: 'replicate',
  ref: paperKey,
  phase,
  paper_key: paperKey,
  repo_name: repo,
})

const A = paper('arxiv:1111.1', 'Paper A')
const B = paper('arxiv:2222.2', 'Paper B')

test('every session lands under the paper it targeted', () => {
  const g = groupByPaper([A, B], [job('s2', B.key, 'done', 'open-hai/b'), job('s1', A.key, 'done', 'open-hai/a')])
  expect(g.papers.map((p) => p.paper.key)).toEqual([A.key, B.key])
  expect(g.papers[0].sessions.map((s) => s.id)).toEqual(['s1'])
  expect(g.papers[1].sessions.map((s) => s.id)).toEqual(['s2'])
  expect(g.other).toEqual([])
})

test('a paper nobody has run keeps its row', () => {
  const g = groupByPaper([A, B], [job('s1', A.key, 'done', 'open-hai/a')])
  expect(g.papers[1].paper.key).toBe(B.key)
  expect(g.papers[1].sessions).toEqual([])
})

// A paper with no runs can be removed (012) and a job's paper_key is a string,
// not a reference, so a run can outlive the row it started from. Dropping it
// would lose a session the project paid for.
test('a session whose paper the project no longer holds is listed, not dropped', () => {
  const g = groupByPaper([A], [job('s1', A.key, 'done', 'open-hai/a'), job('s9', 'arxiv:9999.9', 'failed')])
  expect(g.papers[0].sessions.map((s) => s.id)).toEqual(['s1'])
  expect(g.other.map((s) => s.id)).toEqual(['s9'])
})

test('a session carrying no paper key at all is listed as other', () => {
  const g = groupByPaper([A], [{ id: 'old', kind: 'replicate', ref: 'r', phase: 'done' }])
  expect(g.other.map((s) => s.id)).toEqual(['old'])
})

test('sessions keep the order they were given, newest first', () => {
  const g = groupByPaper([A], [job('new', A.key, 'done', 'r'), job('old', A.key, 'done', 'r')])
  expect(g.papers[0].sessions.map((s) => s.id)).toEqual(['new', 'old'])
})

// The defect this module fixes. repo_name is set when the publishing phase
// opens, and publishing to failed is a legal transition, so a run that failed
// mid-publish carries a repository name. Testing the repository alone, as the
// project's artifacts and verdict tabs did, presented that run as the project's
// result, ahead of an older session that finished.
test('a session that failed while publishing is not a result', () => {
  expect(isGradeable(job('half', A.key, 'failed', 'open-hai/half'))).toBe(false)
})

test('a session is gradeable only when it finished and has a repository', () => {
  expect(isGradeable(job('ok', A.key, 'done', 'open-hai/ok'))).toBe(true)
  expect(isGradeable(job('norepo', A.key, 'done'))).toBe(false)
  expect(isGradeable(job('running', A.key, 'running', 'open-hai/x'))).toBe(false)
  expect(isGradeable(job('failed', A.key, 'failed'))).toBe(false)
})

// The bug as reported: two papers, each with a published session, and one view
// that showed only the newest repository. Both must be reachable, each under
// its own paper.
test('two papers each with a published session yield one row apiece', () => {
  const rows = gradeable(groupByPaper([A, B], [job('s2', B.key, 'done', 'open-hai/b'), job('s1', A.key, 'done', 'open-hai/a')]))
  expect(rows.map((r) => [r.paper.key, r.session.id])).toEqual([
    [A.key, 's1'],
    [B.key, 's2'],
  ])
})

test('the outcome rows exclude runs that produced nothing', () => {
  const rows = gradeable(groupByPaper([A], [job('half', A.key, 'failed', 'open-hai/half'), job('ok', A.key, 'done', 'open-hai/ok')]))
  expect(rows.map((r) => r.session.id)).toEqual(['ok'])
})

test('a citation reads authors, venue, year — and omits what no source declared', () => {
  expect(citationBits({ authors: ['Ada Lovelace'], venue: 'CHI', year: 2024 }, 'et al.')).toEqual(['Ada Lovelace', 'CHI', 2024].map(String))
  expect(citationBits({ authors: ['Ada Lovelace', 'Grace Hopper'] }, 'et al.')).toEqual(['Ada Lovelace, Grace Hopper'])
  expect(citationBits({ venue: 'CHI', year: 2024 }, 'et al.')).toEqual(['CHI', '2024'])
  expect(citationBits({ year: 2024 }, 'et al.')).toEqual(['2024'])
})

test('a fourth author becomes et al., and the label is the caller’s', () => {
  const four = { authors: ['Ada Lovelace', 'Grace Hopper', 'Alan Turing', 'Barbara Liskov'] }
  expect(citationBits(four, 'et al.')).toEqual(['Ada Lovelace, Grace Hopper, Alan Turing et al.'])
  expect(citationBits({ authors: four.authors.slice(0, 3) }, 'et al.')).toEqual(['Ada Lovelace, Grace Hopper, Alan Turing'])
})

test('a record nobody could fill in yields nothing to render', () => {
  expect(citationBits(undefined, 'et al.')).toEqual([])
  expect(citationBits({}, 'et al.')).toEqual([])
  // A year of zero is "no source declared one", not the year zero.
  expect(citationBits({ authors: [], venue: '', year: 0 }, 'et al.')).toEqual([])
})

test('a registry row cites the first paper and counts the rest', () => {
  const one = { title: 'Point of no Undo', authors: ['Ada Lovelace'], venue: 'CHI', year: 2024 }
  const two = { title: 'PinchCatcher', authors: ['Grace Hopper'], venue: 'UIST', year: 2023 }
  expect(listCitation([one], 'et al.')).toEqual({ bits: ['Ada Lovelace', 'CHI', '2024'], more: 0 })
  expect(listCitation([one, two], 'et al.')).toEqual({ bits: ['Ada Lovelace', 'CHI', '2024'], more: 1 })
  // The second paper's authors never join the first paper's line.
  expect(listCitation([one, two], 'et al.').bits.join(' ')).not.toContain('Grace Hopper')
})

test('a row with nothing to cite says nothing at all', () => {
  expect(listCitation(undefined, 'et al.')).toEqual({ bits: [], more: 0 })
  expect(listCitation([], 'et al.')).toEqual({ bits: [], more: 0 })
  // No line, so no dangling count under it either.
  expect(listCitation([{ title: 'Untitled' }, { title: 'Also untitled' }], 'et al.')).toEqual({ bits: [], more: 0 })
})
