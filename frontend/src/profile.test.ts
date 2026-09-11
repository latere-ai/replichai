// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { isAdmin, isMine, showsPicture } from './account'

// 006k AC7: the Platform group renders only for an admin. Hiding it is a
// courtesy — the routes are refused server-side either way.
test('isAdmin gates the Platform rail group on the admin tier alone', () => {
  expect(isAdmin({ authenticated: true, tier: 'admin' })).toBe(true)
  expect(isAdmin({ authenticated: true, tier: 'user' })).toBe(false)
  expect(isAdmin({ authenticated: true, tier: 'none' })).toBe(false)
  expect(isAdmin({ authenticated: true })).toBe(false)
  // No almost-matches: the tier string is compared exactly.
  expect(isAdmin({ authenticated: true, tier: 'Admin' })).toBe(false)
  expect(isAdmin({ authenticated: true, tier: 'platform-admin' })).toBe(false)
})

// 006m AC4: the avatar degrades to initials rather than to a broken image.
test('showsPicture falls back to initials for anything but a loadable https URL', () => {
  expect(showsPicture('https://auth.latere.ai/a.png', false)).toBe(true)
  // Failed to load: the fallback is the whole point of tracking `broken`.
  expect(showsPicture('https://auth.latere.ai/a.png', true)).toBe(false)
  // No picture at all — the common case for a fresh account.
  expect(showsPicture(undefined, false)).toBe(false)
  expect(showsPicture('', false)).toBe(false)
  // The server filters these (safePicture), and the client does not trust that
  // it did — a javascript: or data: value must never reach an <img src>.
  for (const p of ['javascript:alert(1)', 'data:image/png;base64,AAAA', 'http://auth.latere.ai/a.png', '/relative.png']) {
    expect(showsPicture(p, false)).toBe(false)
  }
})

// 028: signed out, `me.principal` is undefined and the server withholds `owner`
// from a reader with no stake — so a bare `===` would report every registry row
// as the reader's own, and paint a stranger's public project as editable.
test('isMine never matches two absent principals', () => {
  expect(isMine({ authenticated: false }, undefined)).toBe(false)
  expect(isMine({ authenticated: false }, 'someone')).toBe(false)
  expect(isMine({ authenticated: true, principal: 'me' }, undefined)).toBe(false)
  expect(isMine({ authenticated: true, principal: 'me' }, '')).toBe(false)
  expect(isMine({ authenticated: true, principal: 'me' }, 'someone')).toBe(false)
  expect(isMine({ authenticated: true, principal: 'me' }, 'me')).toBe(true)
})
