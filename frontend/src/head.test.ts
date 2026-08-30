// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { canonicalOf, titleOf } from './head'
import type { View } from './routes'

// A minimal translator: the hook takes one so a title is written in the
// reader's language, and these assertions do not depend on the English copy.
const t = ((k: string) => k) as never

test('the dashboard is titled only once the reader is known to be signed in', () => {
  const dashboard: View = { name: 'projects' }
  // Signed out, `/` is the landing, whose title the server wrote and this must
  // not overwrite. Before /me answers, nothing is known and nothing is written.
  expect(titleOf(dashboard, t, false)).toBe(null)
  expect(titleOf(dashboard, t, null)).toBe(null)
  expect(titleOf(dashboard, t, true)).toBe('navProjects · ReplicHAI')
})

test('a record is titled by its name, and never by a placeholder', () => {
  expect(titleOf({ name: 'project', id: 'p1' }, t, true)).toBe(null)
  expect(titleOf({ name: 'project', id: 'p1', title: 'A paper' }, t, true)).toBe('A paper · ReplicHAI')
  expect(titleOf({ name: 'session', jobId: 's1', projectId: 'p1' }, t, true)).toBe(null)
  expect(titleOf({ name: 'session', jobId: 's1', projectId: 'p1', ref: 'A paper' }, t, true)).toBe('A paper · ReplicHAI')
  expect(titleOf({ name: 'profile', handle: 'ada' }, t, false)).toBe('@ada · ReplicHAI')
})

test('a tab canonicalises to the record it is a view of', () => {
  expect(canonicalOf({ name: 'project', id: 'p1', tab: 'verdict' })).toBe('/p/p1')
  expect(canonicalOf({ name: 'project', id: 'p1' })).toBe('/p/p1')
  expect(canonicalOf({ name: 'session', jobId: 's1', projectId: 'p1', tab: 'artifacts' })).toBe('/p/p1/s/s1')
  expect(canonicalOf({ name: 'public' })).toBe('/registry')
  expect(canonicalOf({ name: 'projects' })).toBe('/')
})
