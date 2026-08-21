import { useEffect } from 'react'
import type { I18n } from './i18n'
import { pathOf, type View } from './routes'

// What the document says about itself, after the first paint (042a).
//
// The server writes the head for the address a reader arrives at, because a
// crawler and a link unfurler never run this file. From the second screen
// onwards nothing on the server is involved: the reader is navigating inside one
// document, and without this the tab keeps the title of wherever they came in —
// the landing's question on the wallet screen, one project's name on another's.
//
// It writes only what the client actually knows. A project whose title has not
// been fetched yet leaves the title alone rather than replacing it with the
// site's name, because on a first load that would overwrite the *correct* title
// the server just wrote with a worse one.

const SITE = 'ReplicHAI'

// named renders a screen's own name into a tab title.
const named = (name: string) => `${name} · ${SITE}`

// titleOf is the tab title for a view, or null when the client cannot say
// anything truer than what is already there.
//
// `authed` is what tells the two meanings of `/` apart: signed out it is the
// landing, whose title is the product's claim and is the server's to write;
// signed in it is the dashboard. Null is "not known yet", which is every render
// before /me comes back.
export function titleOf(view: View, t: I18n['t'], authed: boolean | null): string | null {
  switch (view.name) {
    case 'projects':
      return authed ? named(t('navProjects')) : null
    case 'public':
      return named(t('navPublic'))
    case 'trash':
      return named(t('navTrash'))
    case 'submit':
      return named(t('newProject'))
    case 'wallet':
      return named(t('walletHeading'))
    case 'settings':
      return named(t('settings'))
    case 'admin':
      return named(t('navAdmin'))
    case 'profile':
      // The handle is in the address, so it is known before anything is
      // fetched; the display name that replaces it is not this hook's to wait
      // for — the screen writes it once it has it.
      return named(`@${view.handle}`)
    case 'project':
      return view.title ? named(view.title) : null
    case 'session':
      return view.ref || view.projectTitle ? named(view.ref || (view.projectTitle as string)) : null
    default:
      return null
  }
}

// canonicalOf is the one address a screen is at. A tab is a view of a record
// rather than a second record, so it folds to the record's own path — the same
// rule the server applies, and the reason both are written down twice rather
// than derived from `pathOf`, which deliberately keeps the tab.
export function canonicalOf(view: View): string {
  switch (view.name) {
    case 'project':
      return `/p/${view.id}`
    case 'session':
      return `/p/${view.projectId}/s/${view.jobId}`
    default:
      return pathOf(view)
  }
}

// useHead keeps the tab title and the canonical link on the current screen.
export function useHead(view: View, t: I18n['t'], authed: boolean | null) {
  useEffect(() => {
    const title = titleOf(view, t, authed)
    if (title && document.title !== title) document.title = title

    const href = window.location.origin + canonicalOf(view)
    let link = document.querySelector<HTMLLinkElement>('link[rel="canonical"]')
    if (!link) {
      link = document.createElement('link')
      link.rel = 'canonical'
      document.head.appendChild(link)
    }
    if (link.href !== href) link.href = href
  }, [view, t, authed])
}
