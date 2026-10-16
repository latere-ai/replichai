// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect } from 'react'
import type { I18n } from './i18n'
import { pathOf, type View } from './routes'

// The document's title and canonical link after the first paint. The server
// writes the head for the address a reader arrives at, because crawlers and link
// unfurlers never run this file; navigation after that stays in one document, so
// the client keeps the head in step with the screen. It writes only what it
// knows: a project whose title has not been fetched yet leaves the title alone
// rather than overwriting the server's correct one with the site's name.

const SITE = 'ReplicHAI'

// named renders a screen's own name into a tab title.
const named = (name: string) => `${name} · ${SITE}`

// titleOf is the tab title for a view, or null when the client cannot say
// anything truer than what is already there. `authed` tells the two meanings of
// `/` apart: signed out it is the landing, whose title the server writes; signed
// in it is the dashboard. Null means not known yet, before /me comes back.
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
      // for. The screen writes it once it has it.
      return named(`@${view.handle}`)
    case 'notfound':
      return named(t('notFoundTitle'))
    case 'project':
      return view.title ? named(view.title) : null
    case 'session':
      return view.ref || view.projectTitle ? named(view.ref || (view.projectTitle as string)) : null
    default:
      return null
  }
}

// canonicalOf is the canonical address of a screen. A tab is a view of a record
// rather than a second record, so it folds to the record's own path. The server
// applies the same rule, which is why both are written out here rather than
// derived from `pathOf`, which deliberately keeps the tab.
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
