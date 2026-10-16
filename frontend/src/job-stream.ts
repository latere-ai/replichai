// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef } from 'react'
import type { Job } from './api'
import { terminal } from './session-stop'

// A running session is watched over the server's event stream, which sends a
// snapshot of the job when it connects and a frame on every change after (see
// internal/httpapi/events.go). The stream's lifetime depends only on which
// session and whether it is still worth watching, never on the job it
// delivers. An effect keyed on the job would reconnect on every frame.

// watchJob opens the stream, hands each frame to onJob, and closes once the job
// settles; the server closes its end then too. It returns the close.
function watchJob(id: string, onJob: (j: Job) => void): () => void {
  const es = new EventSource(`/jobs/${id}/events`)
  es.addEventListener('status', (e) => {
    try {
      const j = JSON.parse((e as MessageEvent).data) as Job
      onJob(j)
      if (terminal(j.phase)) es.close()
    } catch {
      /* ignore a malformed frame */
    }
  })
  return () => es.close()
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
