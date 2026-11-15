// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { awaitSettled, freshest, stopPending, stoppedByHand, terminal } from './session-stop'
import { phaseClass, phaseLive, phaseTone } from './phase'
import type { Job } from './api'

const job = (id: string, phase: string): Job => ({ id, kind: 'replicate', ref: 'r', phase })

test('a session nobody asked to stop offers a live Stop', () => {
  expect(stopPending('running', false)).toBe(false)
  expect(stopPending('submitted', false)).toBe(false)
})

// The cancel request returns as soon as the worker has been signaled,
// while the session is still running. The control has to stay pending across
// that gap; otherwise it re-arms and a second click sends a second cancel.
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

test('a phase this build does not know is not settled, not running and not failed', () => {
  expect(terminal('reconciling')).toBe(false)
  expect(phaseLive('reconciling')).toBe(false)
  expect(phaseTone('reconciling')).toBe('neutral')
  expect(phaseClass('reconciling')).toBe('unknown')
  expect(phaseLive('running')).toBe(true)
  expect(phaseTone('failed')).toBe('err')
})

test('a stop by hand is read from the stop reason, not from the error text', () => {
  expect(stoppedByHand('failed', 'canceled')).toBe(true)
  expect(stoppedByHand('failed', 'interrupted')).toBe(true)
  expect(stoppedByHand('failed', 'error')).toBe(false)
  expect(stoppedByHand('failed', undefined)).toBe(false)
  expect(stoppedByHand('running', 'canceled')).toBe(false)
})

test('awaitSettled returns the settled session', async () => {
  const seen = ['running', 'running', 'failed']
  const r = await awaitSettled(async () => ({ phase: seen.shift() ?? 'failed' }), async () => {})
  expect(r.settled).toBe(true)
  expect(r.job?.phase).toBe('failed')
})

test('awaitSettled gives up after its budget and says it did not settle', async () => {
  let slept = 0
  const r = await awaitSettled(
    async () => ({ phase: 'running' }),
    async (ms) => {
      slept += ms
    },
    5000,
  )
  expect(r.settled).toBe(false)
  expect(r.job?.phase).toBe('running')
  expect(slept).toBeGreaterThanOrEqual(5000)
})

test('awaitSettled survives a failed read', async () => {
  let n = 0
  const r = await awaitSettled(
    async () => {
      if (n++ === 0) throw new Error('offline')
      return { phase: 'done' }
    },
    async () => {},
  )
  expect(r.settled).toBe(true)
})
