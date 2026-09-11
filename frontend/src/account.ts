// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Who the reader is, and how they are drawn: the tier and ownership checks the
// screens gate on, and the avatar's fallbacks — initials and a stable hue — for
// an account with no picture to show. Pure functions, so the tests read them
// without a browser.

import { type Me } from './api'

// isAdmin decides whether the Platform group renders. It decides nothing else:
// every admin route is refused server-side by the tier guard and again by the
// store, so hiding the tab is a courtesy, not the guard.
export function isAdmin(me: Me): boolean {
  return me.tier === 'admin'
}

// isMine reports whether a project's owning principal is this reader's.
//
// Both sides are optional and both are absent at once on the screen this exists
// for: signed out, `me.principal` is undefined and the server withholds `owner`
// from a reader with no stake in the project (028) — so a bare `===` would make
// every registry row the reader's own, and every public project look editable.
// Two unknowns are not a match.
export function isMine(me: Me, owner?: string): boolean {
  return !!me.principal && !!owner && owner === me.principal
}

export function initials(email?: string) {
  if (!email) return '·'
  return email.slice(0, 2).toUpperCase()
}

// hueOf turns an address into a stable hue. It is only decoration, but a stable
// one: the same person is the same colour in every list they appear in, which
// is what makes a members table scannable.
export function hueOf(email?: string): number {
  if (!email) return 240
  let h = 0
  for (const ch of email.toLowerCase()) h = (h * 31 + ch.charCodeAt(0)) % 360
  return h
}

// showsPicture decides between the IdP's picture and the initials fallback. It
// is the whole rule of 006m AC4, kept pure so it is testable: no picture, a
// picture that is not absolute https (the server already filters those, and the
// client does not trust that it did), or one that failed to load, all fall back.
export function showsPicture(picture: string | undefined, broken: boolean): boolean {
  return !broken && !!picture && picture.startsWith('https://')
}
