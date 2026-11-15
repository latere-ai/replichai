// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import type { ProjectView } from './api'
import { createProjectsStore } from './projects-store'

const row = (id: string) => ({ id }) as ProjectView

// A read the test finishes by hand.
function gate() {
  const reads: Array<{ ok: (p: ProjectView[]) => void; fail: () => void }> = []
  const read = () =>
    new Promise<ProjectView[] | null>((ok, fail) => {
      reads.push({ ok, fail: () => fail(new Error('offline')) })
    })
  return { reads, read }
}
const flush = () => new Promise((r) => setTimeout(r, 0))

test('a write during an in-flight read triggers another read after it', async () => {
  const g = gate()
  const s = createProjectsStore(g.read)
  s.subscribe(() => {})
  const first = s.load()
  s.invalidate()
  expect(g.reads.length).toBe(1)
  g.reads[0].ok([row('old')])
  await flush()
  expect(g.reads.length).toBe(2)
  g.reads[1].ok([row('old'), row('new')])
  await first
  await flush()
  expect(s.snapshot()?.map((p) => p.id)).toEqual(['old', 'new'])
})

test('a failed read is reported and is not an empty list', async () => {
  const g = gate()
  const s = createProjectsStore(g.read)
  const p = s.load()
  g.reads[0].fail()
  await p
  expect(s.failedSnapshot()).toBe(true)
  expect(s.snapshot()).toBeNull()
})

test('a failed read keeps the rows already shown and a later read clears the failure', async () => {
  const g = gate()
  const s = createProjectsStore(g.read)
  let p = s.load()
  g.reads[0].ok([row('a')])
  await p
  p = s.load()
  g.reads[1].fail()
  await p
  expect(s.failedSnapshot()).toBe(true)
  expect(s.snapshot()?.length).toBe(1)
  p = s.load()
  g.reads[2].ok([row('a'), row('b')])
  await p
  expect(s.failedSnapshot()).toBe(false)
  expect(s.snapshot()?.length).toBe(2)
})
