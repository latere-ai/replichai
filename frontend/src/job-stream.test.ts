// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { ApiError, type Job } from './api'
import { watchJob, type WatchDeps } from './job-stream'

const job = (phase: string): Job => ({ id: 'a', kind: 'replicate', ref: 'r', phase })

// A harness with a fake stream and a clock the test advances by hand.
function harness(reads: Array<Job | Error>) {
  const listeners: Record<string, (e: Event) => void> = {}
  const es = {
    readyState: 1,
    onopen: null as ((e: Event) => void) | null,
    onerror: null as ((e: Event) => void) | null,
    closed: false,
    addEventListener(t: string, fn: (e: Event) => void) {
      listeners[t] = fn
    },
    close() {
      es.closed = true
    },
  }
  const timers: Array<() => void> = []
  const deps: WatchDeps = {
    open: () => es,
    read: async () => {
      const r = reads.shift()
      if (!r) throw new Error('offline')
      if (r instanceof Error) throw r
      return r
    },
    setTimer: (fn) => {
      timers.push(fn)
      return timers.length
    },
    clearTimer: () => {
      timers.length = 0
    },
  }
  const seen: Job[] = []
  const gone = { n: 0 }
  const stop = watchJob('a', (j) => seen.push(j), deps, () => gone.n++)
  const tick = async () => {
    const fn = timers.shift()
    fn?.()
    await new Promise((r) => setTimeout(r, 0))
  }
  const frame = (j: Job) => listeners['status']({ data: JSON.stringify(j) } as unknown as Event)
  const end = (reason: string) => listeners['end']({ data: JSON.stringify({ reason }) } as unknown as Event)
  return { es, seen, tick, frame, end, gone, stop, timers }
}

test('an error on the stream falls back to reading the session until it settles', async () => {
  const h = harness([job('running'), job('failed')])
  h.es.onerror?.(new Event('error'))
  await h.tick()
  expect(h.seen.map((j) => j.phase)).toEqual(['running'])
  await h.tick()
  expect(h.seen.map((j) => j.phase)).toEqual(['running', 'failed'])
  expect(h.es.closed).toBe(true)
  expect(h.timers.length).toBe(0)
})

test('a failed read is retried', async () => {
  const h = harness([new ApiError(500), job('done')])
  h.es.onerror?.(new Event('error'))
  await h.tick()
  expect(h.seen.length).toBe(0)
  expect(h.timers.length).toBe(1)
  await h.tick()
  expect(h.seen.map((j) => j.phase)).toEqual(['done'])
})

test('a session that cannot be read ends the fallback', async () => {
  const h = harness([new ApiError(404)])
  h.es.onerror?.(new Event('error'))
  await h.tick()
  expect(h.timers.length).toBe(0)
})

test('the stream coming back stops the fallback', async () => {
  const h = harness([job('running')])
  h.es.onerror?.(new Event('error'))
  h.es.onopen?.(new Event('open'))
  expect(h.timers.length).toBe(0)
  h.frame(job('running'))
  expect(h.seen.length).toBe(1)
})

test('a settled frame closes the stream', () => {
  const h = harness([])
  h.frame(job('done'))
  expect(h.es.closed).toBe(true)
})

test('an end for a session that is gone closes the stream and tells the screen', () => {
  const h = harness([])
  h.end('not_found')
  expect(h.es.closed).toBe(true)
  expect(h.gone.n).toBe(1)
  expect(h.timers.length).toBe(0)
})

test('an end for a session that could not be read falls back to reading it', async () => {
  const h = harness([job('running'), job('done')])
  h.end('unavailable')
  expect(h.es.closed).toBe(true)
  expect(h.gone.n).toBe(0)
  await h.tick()
  expect(h.seen.map((j) => j.phase)).toEqual(['running'])
  await h.tick()
  expect(h.seen.map((j) => j.phase)).toEqual(['running', 'done'])
})
