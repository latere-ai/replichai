// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef } from 'react'
import type { Job } from './api'
import { terminal } from './session-stop'

// A running session is watched over the server's event stream, which sends a
// snapshot of the job the moment it connects and a frame on every change after,
// see internal/httpapi/events.go. Both screens that show a session used to open
// the stream from an effect that depended on the job itself, so the snapshot
// the connect produced replaced the job, the replaced job re-ran the effect,
// and the effect reconnected: one stream per frame, for as long as the page was
// open. The row in a project list had the same bug and reopened its stream on
// every refresh of the list around it.
//
// The stream's lifetime is now decided by two facts only: which session, and
// whether it is still worth watching. Nothing the stream delivers can reach
// either, so nothing it delivers can reopen it.

// watchJob opens the stream, hands each frame to onJob, and closes once the job
// settles; the server closes its end then too. It returns the close.
export function watchJob(id: string, onJob: (j: Job) => void): () => void {
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
