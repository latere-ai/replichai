import { expect, test } from 'bun:test'
import { gradeable, groupByPaper, isGradeable } from './papers'
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
// not a reference — so a run can outlive the row it started from. Dropping it
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

// The defect this module exists for. repo_name is set as the publishing phase
// *opens*, and publishing → failed is a legal transition, so a run that fell
// over mid-publish carries a repository name. Testing the repository alone —
// which is what the project's artifacts and verdict tabs did — presented that
// run as the project's result, ahead of an older session that finished.
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
