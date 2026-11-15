// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Stopping a session is not one instant act. The request signals the worker,
// which closes the backend run and only then writes the terminal phase. The
// control has to hold the whole of that wait, or a second click fires a second
// cancel at a run that is already coming down.

import { isTerminal } from './generated/job-phase'
import type { StopReason } from './api'

// terminal reports a settled session: one that will not change phase again. The
// phase table is the server's; a phase this build does not know is not settled.
export const terminal = (phase: string) => isTerminal(phase)

// stopPending reports whether a stop is in flight: the viewer asked to stop, and
// the session has not settled yet. It stays true after the request returns, so
// the button does not offer a live "Stop" on a session still shown as running.
export const stopPending = (phase: string, requested: boolean) => requested && !terminal(phase)

// freshest picks which snapshot of a session a row should show when the list
// hands it a new one. A list read taken before the session settled is stale: it
// would walk a stopped session back to running and offer Stop again. When the
// same session is settled in one snapshot and unsettled in the other, the
// settled one is kept.
export const freshest = <T extends { id: string; phase: string }>(shown: T, incoming: T): T =>
  shown.id === incoming.id && terminal(shown.phase) && !terminal(incoming.phase) ? shown : incoming

// stoppedByHand reports a failed session that was stopped rather than broken:
// a person canceled it, or the server restarted under it. It reads in the
// neutral tone, and what it recorded is not a fault to report.
export const stoppedByHand = (phase: string, reason?: StopReason) => phase === 'failed' && (reason === 'canceled' || reason === 'interrupted')

// How long the control waits for a stop to settle before it says the stop is
// still in progress, and how often it looks meanwhile.
export const STOP_WAIT_MS = 20_000
const STOP_LOOK_MS = [500, 1000, 2000, 3000]

// awaitSettled reads the session until it settles or the wait runs out. It
// returns the last snapshot it read and whether that one is settled; a read that
// fails counts as not settled, since the worker is still coming down. `sleep` is
// a parameter so a test does not wait.
export async function awaitSettled<T extends { phase: string }>(
  read: () => Promise<T>,
  sleep: (ms: number) => Promise<void> = (ms) => new Promise((r) => setTimeout(r, ms)),
  budget = STOP_WAIT_MS,
): Promise<{ job: T | null; settled: boolean }> {
  let spent = 0
  let job: T | null = null
  for (let i = 0; ; i++) {
    try {
      job = await read()
      if (terminal(job.phase)) return { job, settled: true }
    } catch {
      /* look again until the wait runs out */
    }
    if (spent >= budget) return { job, settled: false }
    const ms = STOP_LOOK_MS[Math.min(i, STOP_LOOK_MS.length - 1)]
    await sleep(ms)
    spent += ms
  }
}
