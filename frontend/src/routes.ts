import { useCallback, useEffect, useRef, useState } from 'react'

// Where the operator is, and the address that says so. The two are one mapping
// in both directions — `pathOf` writes it, `viewOf` reads it — so a screen
// cannot exist without a URL, and a reload, a Back, and a shared link all resolve
// through the same code (013).
//
// The optional fields are display hints, never identity: a project's title and a
// session's paper reference are records that can change, so they are filled in by
// the screen that fetched them and are absent from the path. Only ids travel in
// the URL.
export type ProjectTab = 'sessions' | 'verdict' | 'artifacts' | 'people'

export type View =
  | { name: 'projects' }
  | { name: 'public' }
  | { name: 'submit' }
  | { name: 'settings' }
  | { name: 'admin' }
  | { name: 'profile'; handle: string }
  | { name: 'project'; id: string; title?: string; tab?: ProjectTab }
  | { name: 'session'; jobId: string; projectId: string; ref?: string; projectTitle?: string }

// The registry and the console are not at /public and /admin because the API
// owns both roots: Go's mux matches them before the SPA fallback, so a browser
// sent there is answered with JSON. The release smoke asserts exactly that for
// /projects. routes.test.ts holds the whole no-collision rule.
const SCREENS: Record<string, View> = {
  '/': { name: 'projects' },
  '/registry': { name: 'public' },
  '/new': { name: 'submit' },
  '/settings': { name: 'settings' },
  '/console': { name: 'admin' },
}

const TABS: ProjectTab[] = ['sessions', 'verdict', 'artifacts', 'people']

const isTab = (s: string): s is ProjectTab => (TABS as string[]).includes(s)

// pathOf is the address a view lives at. The sessions tab is the project's own
// path rather than /p/{id}/sessions, so the link somebody shares of a project is
// the same one an invitation mail already carries.
export function pathOf(view: View): string {
  switch (view.name) {
    case 'project':
      return `/p/${view.id}` + (view.tab && view.tab !== 'sessions' ? `/${view.tab}` : '')
    case 'session':
      return `/p/${view.projectId}/s/${view.jobId}`
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

// viewOf is what an address means. Anything unrecognised is the dashboard: every
// path here is either one of ours or a typo in a hand-edited URL, and the useful
// answer to a typo is the app. The caller corrects the address.
export function viewOf(pathname: string): View {
  const path = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname
  const fixed = SCREENS[path || '/']
  if (fixed) return fixed

  // A handle is compared and stored lowercased, and it arrives percent-encoded
  // from a browser that encoded it. Both are the profile page's rules (006r),
  // kept here now that the address bar is read in one place.
  const profile = /^\/u\/([^/?#]+)$/.exec(path)
  if (profile) return { name: 'profile', handle: decodeURIComponent(profile[1]).toLowerCase() }

  const session = /^\/p\/([A-Za-z0-9_-]+)\/s\/([A-Za-z0-9_-]+)$/.exec(path)
  if (session) return { name: 'session', projectId: session[1], jobId: session[2] }

  // A project, and a tab if the segment after it names one. A segment that names
  // no tab — a typo, or a session link cut short at /p/{id}/s — still identifies
  // the project, so the reader lands on it rather than back at the dashboard, and
  // the address is corrected to the project's own.
  const project = /^\/p\/([A-Za-z0-9_-]+)(?:\/([a-z]+))?$/.exec(path)
  if (project) {
    const tab = project[2]
    return tab && isTab(tab) ? { name: 'project', id: project[1], tab } : { name: 'project', id: project[1] }
  }
  return { name: 'projects' }
}

// Navigate takes a view, or a function of the current one for the cases that
// fill a detail in on top of wherever the reader now is. `replace` marks a
// correction — an address being fixed rather than a place being visited.
export type Navigate = (next: View | ((current: View) => View), opts?: { replace?: boolean }) => void

// useRouter holds the current view in the address bar rather than beside it.
//
// History follows one default: push when the path changes, replace when it does
// not. Opening a screen adds an entry, switching a project tab adds one, and
// writing back a title the app had not fetched yet adds none. A correction passes
// `replace` explicitly, because a corrected address must not be a place Back can
// return to — it would be corrected again, and Back would never get past it.
export function useRouter() {
  const [view, setView] = useState<View>(() => viewOf(window.location.pathname))
  // The current view, readable outside a render. A history write must happen
  // exactly once per navigation, so it cannot live inside a state updater —
  // React is free to call one twice.
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

  // An address we do not recognise is corrected in place on arrival, so a reload
  // of a typo does not keep resolving to the dashboard at the wrong URL.
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
