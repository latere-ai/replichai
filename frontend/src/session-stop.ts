// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Stopping a session is not one instant act. The request signals the worker,
// which closes the backend run and only then writes the terminal phase. The
// control has to hold the whole of that wait, or a second click fires a second
// cancel at a run that is already coming down.

// terminal reports a settled session: one that will not change phase again.
export const terminal = (phase: string) => phase === 'done' || phase === 'failed'

// stopPending reports whether a stop is in flight: the viewer asked to stop, and
// the session has not settled yet. It stays true after the request returns —
// which is the case the button used to get wrong, snapping back to a live "Stop"
// on a session still shown as running.
export const stopPending = (phase: string, requested: boolean) => requested && !terminal(phase)

// freshest picks which snapshot of a session a row should show when the list
// hands it a new one. A list read taken before the session settled is stale: it
// would walk a stopped session back to running and offer Stop again. Same
// session, settled here, unsettled there — keep the settled one.
export const freshest = <T extends { id: string; phase: string }>(shown: T, incoming: T): T =>
  shown.id === incoming.id && terminal(shown.phase) && !terminal(incoming.phase) ? shown : incoming
