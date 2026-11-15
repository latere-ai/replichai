// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useSyncExternalStore } from 'react'
import { listProjects, type ProjectView } from './api'

// The one list of projects, shared by everything that shows it: the rail's
// count, the projects screen and the move-paper dialog. It is read once, handed
// to every subscriber, and re-read when something changes it, so the views never
// disagree and the rail's count never goes stale.

type Read = () => Promise<ProjectView[] | null>

// createProjectsStore holds the list and the one read of it. The read is a
// parameter so a test can drive it without a network.
export function createProjectsStore(read: Read) {
  let projects: ProjectView[] | null = null
  // Whether the last read failed. A failed read is not an empty list: the rows
  // already shown stand, and a screen with none says the load failed.
  let failed = false
  let inflight: Promise<void> | null = null
  // A write landed while a read was in flight, so that read may predate it and
  // another is due once it ends.
  let again = false
  const listeners = new Set<() => void>()
  const emit = () => listeners.forEach((l) => l())

  // load re-reads the list. Concurrent callers share the one request.
  function load(): Promise<void> {
    if (inflight) return inflight
    inflight = read()
      .then((p) => {
        projects = p ?? []
        failed = false
      })
      .catch(() => {
        failed = true
      })
      .finally(() => {
        inflight = null
        emit()
      })
      .then(() => {
        if (again) {
          again = false
          return load()
        }
      })
    return inflight
  }

  return {
    subscribe(l: () => void) {
      listeners.add(l)
      return () => {
        listeners.delete(l)
      }
    },
    snapshot: () => projects,
    failedSnapshot: () => failed,
    load,
    // invalidate is called after anything that changes the list. A write during
    // an in-flight read queues one more read after it.
    invalidate(): void {
      if (inflight) again = true
      else if (listeners.size) void load()
      else projects = null
    },
  }
}

const store = createProjectsStore(() => listProjects())

// useProjects is the list as this component sees it: what is cached at once,
// null until the first read has landed, and every later read as it arrives.
// Mounting revalidates, so a screen opened afresh shows the cached rows now and
// the server's rows a moment later. `enabled` is for a reader with no account,
// for whom the list is a route that refuses them.
export function useProjects(enabled = true): ProjectView[] | null {
  const list = useSyncExternalStore(store.subscribe, store.snapshot, store.snapshot)
  useEffect(() => {
    if (enabled) void store.load()
  }, [enabled])
  return list
}

// useProjectsFailed reports that the last read of the list failed, and retryProjects
// reads it again.
export const useProjectsFailed = (): boolean => useSyncExternalStore(store.subscribe, store.failedSnapshot, store.failedSnapshot)
export const retryProjects = (): void => void store.load()

// invalidateProjects is called after anything that changes the list, such as a
// project created, renamed, described, thrown away, restored, deleted or made
// public, so every subscriber is handed the new list without asking.
export const invalidateProjects = (): void => store.invalidate()
