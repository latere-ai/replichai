// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useSyncExternalStore } from 'react'
import { listProjects, type ProjectView } from './api'

// The one list of projects, shared by everything that shows it.
//
// Three places used to read /projects for themselves: the rail, for the count
// on its Projects row, on every change of screen; the projects screen, for its
// rows; and the move-paper dialog, for its targets. Three reads of one list
// that could disagree with each other, and a rail count that went stale the
// moment a project was thrown away from the list beside it, because nothing
// told the rail. The list is read here once, handed to every subscriber, and
// re-read when something changes it.

let projects: ProjectView[] | null = null
let inflight: Promise<void> | null = null
const listeners = new Set<() => void>()

const emit = () => listeners.forEach((l) => l())

// load re-reads the list. Concurrent callers share the one request; a failure
// leaves whatever was shown standing, or an empty list when nothing was.
function load(): Promise<void> {
  if (inflight) return inflight
  inflight = listProjects()
    .then((p) => {
      projects = p ?? []
    })
    .catch(() => {
      projects ??= []
    })
    .finally(() => {
      inflight = null
      emit()
    })
  return inflight
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => {
    listeners.delete(l)
  }
}

const snapshot = () => projects

// useProjects is the list as this component sees it: what is cached at once,
// null until the first read has landed, and every later read as it arrives.
// Mounting revalidates, so a screen opened afresh shows the cached rows now and
// the server's rows a moment later. `enabled` is for a reader with no account,
// for whom the list is a route that refuses them.
export function useProjects(enabled = true): ProjectView[] | null {
  const list = useSyncExternalStore(subscribe, snapshot, snapshot)
  useEffect(() => {
    if (enabled) void load()
  }, [enabled])
  return list
}

// invalidateProjects is called after anything that changes the list, such as a
// project created, renamed, described, thrown away, restored, deleted or made
// public, so every subscriber is handed the new list without asking.
export function invalidateProjects(): void {
  if (listeners.size) void load()
  else projects = null
}
