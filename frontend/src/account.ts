// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The reader's identity and how it is drawn: the tier and ownership checks the
// screens gate on, and the avatar fallbacks, initials and a stable hue, for an
// account with no picture. Pure functions, so the tests run without a browser.

import { type Me } from './api'

// isAdmin decides whether the Platform group renders, and nothing else: every
// admin route is refused server-side by the tier guard and again by the store,
// so hiding the tab is a convenience, not the access control.
export function isAdmin(me: Me): boolean {
  return me.tier === 'admin'
}

// isMine reports whether a project's owning principal is this reader's.
// Both sides can be absent: signed out, `me.principal` is undefined, and the
// server withholds `owner` from a reader with no stake in the project. Two
// undefined values do not match, or every public project would look editable.
export function isMine(me: Me, owner?: string): boolean {
  return !!me.principal && !!owner && owner === me.principal
}

export function initials(email?: string) {
  if (!email) return '·'
  return email.slice(0, 2).toUpperCase()
}

// hueOf turns an address into a stable hue. It is decoration, but the same
// person gets the same color in every list, which makes a members table
// easier to scan.
export function hueOf(email?: string): number {
  if (!email) return 240
  let h = 0
  for (const ch of email.toLowerCase()) h = (h * 31 + ch.charCodeAt(0)) % 360
  return h
}

// showsPicture decides between the IdP's picture and the initials fallback. No
// picture, a picture that is not an absolute https URL, or one that failed to
// load all fall back. The server already filters non-https pictures; the client
// checks again rather than trusting that it did.
export function showsPicture(picture: string | undefined, broken: boolean): boolean {
  return !broken && !!picture && picture.startsWith('https://')
}
