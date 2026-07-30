import { expect, test } from 'bun:test'
import { showsPicture } from './App'

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
