// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useRef, useState } from 'react'

// Where the operator is, and the address that says so. The two are one mapping
// in both directions, written by `pathOf` and read by `viewOf`, so a screen
// cannot exist without a URL, and a reload, a Back and a shared link all resolve
// through the same code.
//
// The optional fields are display hints, never identity: a project's title and
// a session's paper reference can change, so they are filled in by the screen
// that fetched them and are absent from the path. Only ids travel in the URL.
export type ProjectTab = 'sessions' | 'verdict' | 'people'

// A verdict and a repository belong to the session that produced them, so both
// are addressed under the session rather than the project. A project holds
// papers and a paper has sessions; there is no "project repository" to browse.
export type SessionTab = 'transcript' | 'verdict' | 'artifacts'

// The settings screen is one view with two sub-tabs, each a real path,
// /settings and /settings/profile. 'account' is the default and omitted from the
// address, exactly as 'sessions' is for a project. Credits live on /wallet.
export type SettingsTab = 'account' | 'profile'

export type View =
  | { name: 'projects' }
  // An address this app does not have. It carries the path it was asked for, so
  // `pathOf` stays total and the address bar keeps what was typed rather than
  // being rewritten to `/`.
  | { name: 'notfound'; path: string }
  | { name: 'trash' }
  | { name: 'public' }
  | { name: 'submit' }
  | { name: 'wallet' }
  | { name: 'settings'; tab?: SettingsTab }
  | { name: 'admin' }
  | { name: 'profile'; handle: string }
  | { name: 'project'; id: string; title?: string; tab?: ProjectTab }
  | { name: 'session'; jobId: string; projectId: string; ref?: string; projectTitle?: string; tab?: SessionTab }

// The registry and the console are not at /public and /admin because the API
// owns both roots: Go's mux matches them before the SPA fallback, so a browser
// sent there is answered with JSON. routes.test.ts holds the no-collision rule.
const SCREENS: Record<string, View> = {
  '/': { name: 'projects' },
  '/registry': { name: 'public' },
  '/trash': { name: 'trash' },
  '/new': { name: 'submit' },
  '/wallet': { name: 'wallet' },
  '/settings': { name: 'settings' },
  '/console': { name: 'admin' },
}

const TABS: ProjectTab[] = ['sessions', 'verdict', 'people']

const isTab = (s: string): s is ProjectTab => (TABS as string[]).includes(s)

const SESSION_TABS: SessionTab[] = ['transcript', 'verdict', 'artifacts']

const isSessionTab = (s: string): s is SessionTab => (SESSION_TABS as string[]).includes(s)

// The non-default settings tabs, the ones that earn a path segment. 'account' is
// the bare /settings and never appears here, so the reverse-lookup stays single.
const SETTINGS_TABS: SettingsTab[] = ['profile']

const isSettingsTab = (s: string): s is SettingsTab => (SETTINGS_TABS as string[]).includes(s)

// isPublicView reports whether a signed-out reader can be shown this view: the
// registry, a project in it, a session under that project, a researcher's page,
// and the not-found page. Each renders whatever the reader may read; a private
// project reads as not public. Everything else is somebody's own workspace, so
// the landing stands in for it.
export function isPublicView(v: View): boolean {
  return v.name === 'public' || v.name === 'project' || v.name === 'session' || v.name === 'profile' || v.name === 'notfound'
}

// pathOf is the address a view lives at. The sessions tab is the project's own
// path rather than /p/{id}/sessions, so the link somebody shares of a project is
// the same one an invitation mail already carries. A session's transcript
// follows the same rule for the same reason: it is where a session opens, and
// the links to one already sent point at the bare path.
export function pathOf(view: View): string {
  switch (view.name) {
    case 'notfound':
      return view.path
    case 'project':
      return `/p/${view.id}` + (view.tab && view.tab !== 'sessions' ? `/${view.tab}` : '')
    case 'session':
      return `/p/${view.projectId}/s/${view.jobId}` + (view.tab && view.tab !== 'transcript' ? `/${view.tab}` : '')
    case 'settings':
      // 'account' is the default and stays the bare /settings, so SCREENS holds
      // one path for the name and the reverse-lookup below never sees a tab.
      return '/settings' + (view.tab && view.tab !== 'account' ? `/${view.tab}` : '')
    case 'profile':
      return `/u/${view.handle}`
    default: {
      const hit = Object.entries(SCREENS).find(([, v]) => v.name === view.name)
      // Unreachable: SCREENS covers every remaining variant, and the compiler
      // checks that because View is closed. The fallback keeps the function
      // total rather than throwing at a reader.
      return hit ? hit[0] : '/'
    }
  }
}

// decodeHandle reads a handle out of its address segment, or null when the
// encoding is malformed. decodeURIComponent throws on an escape cut short, such
// as '%E0%A4%A', and a throw inside the router's initial state would take the
// whole app down to a white screen.
function decodeHandle(raw: string): string | null {
  try {
    return decodeURIComponent(raw).toLowerCase()
  } catch {
    return null
  }
}

// viewOf is what an address means. Anything unrecognized resolves to the
// not-found screen at the address that was typed, not to the dashboard, which
// would be a soft 404 to a crawler and a substituted page to a person.
export function viewOf(pathname: string): View {
  const path = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname
  const fixed = SCREENS[path || '/']
  if (fixed) return fixed

  // A handle is compared and stored lowercased, and it arrives percent-encoded
  // from a browser that encoded it.
  const profile = /^\/u\/([^/?#]+)$/.exec(path)
  if (profile) {
    const handle = decodeHandle(profile[1])
    return handle ? { name: 'profile', handle } : { name: 'notfound', path }
  }

  // Settings, and a tab if the segment after it names one. A segment that names
  // no tab, whether 'account' (the bare path's own tab) or a typo, resolves to
  // the bare settings view and useRouter corrects the address, the same rule a
  // project's unknown tab follows.
  const settings = /^\/settings\/([a-z]+)$/.exec(path)
  if (settings) {
    const tab = settings[1]
    return isSettingsTab(tab) ? { name: 'settings', tab } : { name: 'settings' }
  }

  // A session, and a tab if the segment after it names one. A segment that names
  // none is dropped the same way a project's is, keeping the session.
  const session = /^\/p\/([A-Za-z0-9_-]+)\/s\/([A-Za-z0-9_-]+)(?:\/([a-z]+))?$/.exec(path)
  if (session) {
    const tab = session[3]
    const at = { name: 'session', projectId: session[1], jobId: session[2] } as const
    return tab && isSessionTab(tab) ? { ...at, tab } : at
  }

  // A project, and a tab if the segment after it names one. A segment that names
  // no tab, such as a typo or a session link cut short at /p/{id}/s, still
  // identifies the project, so the reader lands on it and the address is
  // corrected to the project's own.
  const project = /^\/p\/([A-Za-z0-9_-]+)(?:\/([a-z]+))?$/.exec(path)
  if (project) {
    const tab = project[2]
    return tab && isTab(tab) ? { name: 'project', id: project[1], tab } : { name: 'project', id: project[1] }
  }
  return { name: 'notfound', path }
}

// Navigate takes a view, or a function of the current one for the cases that
// fill a detail in on top of wherever the reader now is. `replace` marks a
// correction: an address being fixed rather than a place being visited.
export type Navigate = (next: View | ((current: View) => View), opts?: { replace?: boolean }) => void

// useRouter holds the current view in the address bar rather than beside it.
//
// History follows one default: push when the path changes, replace when it does
// not. Opening a screen adds an entry, switching a project tab adds one, and
// writing back a title the app had not fetched yet adds none. A correction passes
// `replace` explicitly, because a corrected address must not be a place Back can
// return to; it would be corrected again, and Back would never get past it.
export function useRouter() {
  const [view, setView] = useState<View>(() => viewOf(window.location.pathname))
  // The current view, readable outside a render. A history write must happen
  // exactly once per navigation, so it cannot live inside a state updater,
  // because React is free to call one twice.
  const current = useRef(view)

  const navigate = useCallback<Navigate>((next, opts) => {
    const to = typeof next === 'function' ? next(current.current) : next
    // An updater that had nothing to add returns the view it was given. That is
    // not a navigation, and writing history for it would be one write per render
    // of whichever screen keeps offering the same detail.
    if (to === current.current) return
    const path = pathOf(to)
    if (opts?.replace || path === window.location.pathname) window.history.replaceState(null, '', path)
    else window.history.pushState(null, '', path)
    current.current = to
    setView(to)
  }, [])

  // On arrival the address is corrected in place to the path its view maps back
  // to, dropping a trailing slash or a segment that names no tab.
  useEffect(() => {
    const want = pathOf(current.current)
    if (window.location.pathname !== want) window.history.replaceState(null, '', want)
  }, [])

  // Back and Forward are the browser's, so the view follows the address rather
  // than the other way round.
  useEffect(() => {
    const pop = () => {
      const to = viewOf(window.location.pathname)
      current.current = to
      setView(to)
    }
    window.addEventListener('popstate', pop)
    return () => window.removeEventListener('popstate', pop)
  }, [])

  return { view, navigate }
}
