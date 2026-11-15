// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef } from 'react'
import { ApiError, getJob, type Job } from './api'
import { terminal } from './session-stop'

// A running session is watched over the server's event stream, which sends a
// snapshot of the job when it connects and a frame on every change after (see
// internal/httpapi/events.go). The stream's lifetime depends only on which
// session and whether it is still worth watching, never on the job it
// delivers. An effect keyed on the job would reconnect on every frame.

// The slice of EventSource the watcher uses, and the clock and the read it
// leans on, so a test can drive it without a browser.
type Source = {
  readyState: number
  onopen: ((e: Event) => void) | null
  onerror: ((e: Event) => void) | null
  addEventListener(type: string, fn: (e: Event) => void): void
  close(): void
}
export type WatchDeps = {
  open: (url: string) => Source
  read: (id: string) => Promise<Job>
  setTimer: (fn: () => void, ms: number) => unknown
  clearTimer: (h: unknown) => void
}
const browserDeps: WatchDeps = {
  open: (url) => new EventSource(url) as unknown as Source,
  read: getJob,
  setTimer: (fn, ms) => setTimeout(fn, ms),
  clearTimer: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
}

// While the stream is down, the session is read on this schedule: first soon,
// then less often up to the cap.
export const FALLBACK_MS = [2000, 4000, 8000, 15000, 30000]

// watchJob opens the stream, hands each frame to onJob, and closes once the job
// settles; the server closes its end then too. When the stream fails, whether
// refused or dropped, the session is read on a schedule instead, until it
// settles or the stream is back, so the screen never freezes on its last phase.
// A read refused as unauthorized, forbidden or missing ends the watch, since
// nothing more can be learned. It returns the close.
export function watchJob(id: string, onJob: (j: Job) => void, deps: WatchDeps = browserDeps): () => void {
  let closed = false
  let timer: unknown
  let polling = false
  let misses = 0
  const es = deps.open(`/jobs/${id}/events`)

  const stopPolling = () => {
    polling = false
    misses = 0
    if (timer !== undefined) deps.clearTimer(timer)
    timer = undefined
  }
  const close = () => {
    closed = true
    stopPolling()
    es.close()
  }
  const deliver = (j: Job) => {
    onJob(j)
    if (terminal(j.phase)) close()
  }
  const poll = () => {
    timer = undefined
    if (closed || !polling) return
    deps
      .read(id)
      .then((j) => {
        if (closed || !polling) return
        misses = 0
        deliver(j)
      })
      .catch((e: unknown) => {
        if (closed || !polling) return
        if (e instanceof ApiError && [401, 403, 404].includes(e.status)) {
          stopPolling()
          return
        }
        misses++
      })
      .finally(() => {
        if (closed || !polling) return
        timer = deps.setTimer(poll, FALLBACK_MS[Math.min(misses, FALLBACK_MS.length - 1)])
      })
  }

  es.addEventListener('status', (e) => {
    stopPolling()
    try {
      deliver(JSON.parse((e as MessageEvent).data) as Job)
    } catch {
      /* ignore a malformed frame */
    }
  })
  es.onopen = () => stopPolling()
  es.onerror = () => {
    if (closed || polling) return
    polling = true
    timer = deps.setTimer(poll, FALLBACK_MS[0])
  }
  return close
}

// useJobStream watches one session for as long as `enabled` holds and hands
// every frame to onJob. The callback is read through a ref, so a caller may
// pass a fresh function each render without the stream noticing.
export function useJobStream(jobId: string, enabled: boolean, onJob: (j: Job) => void): void {
  const latest = useRef(onJob)
  useEffect(() => {
    latest.current = onJob
  })
  useEffect(() => {
    if (!enabled) return
    return watchJob(jobId, (j) => latest.current(j))
  }, [jobId, enabled])
}
