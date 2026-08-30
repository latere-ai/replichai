// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { freshest, stopPending, terminal } from './session-stop'
import type { Job } from './api'

const job = (id: string, phase: string): Job => ({ id, kind: 'replicate', ref: 'r', phase })

test('a session nobody asked to stop offers a live Stop', () => {
  expect(stopPending('running', false)).toBe(false)
  expect(stopPending('submitted', false)).toBe(false)
})

// The bug: the cancel request returns as soon as the worker has been signalled,
// while the session is still running. The control has to stay pending across
// that gap — otherwise it re-arms and a second click sends a second cancel.
test('a stop stays pending after the request returns, while the session still runs', () => {
  for (const phase of ['submitted', 'resolving', 'running', 'publishing']) {
    expect(stopPending(phase, true)).toBe(true)
  }
})

test('a stop stops pending once the session settles', () => {
  expect(stopPending('failed', true)).toBe(false)
  expect(stopPending('done', true)).toBe(false)
})

test('terminal names the settled phases', () => {
  expect(terminal('done')).toBe(true)
  expect(terminal('failed')).toBe(true)
  expect(terminal('running')).toBe(false)
})

// A project reload racing the stop would otherwise hand the row a snapshot taken
// before the session settled, reviving a Stop button on a stopped session.
test('a stale list snapshot cannot revive a stopped session', () => {
  const stopped = job('a', 'failed')
  expect(freshest(stopped, job('a', 'running'))).toBe(stopped)
})

test('a fresh snapshot of the same session is taken', () => {
  const running = job('a', 'running')
  const settled = job('a', 'done')
  expect(freshest(running, settled)).toBe(settled)
  const later = job('a', 'running')
  expect(freshest(running, later)).toBe(later)
})

test('a different session always replaces what is shown', () => {
  const other = job('b', 'running')
  expect(freshest(job('a', 'failed'), other)).toBe(other)
})
