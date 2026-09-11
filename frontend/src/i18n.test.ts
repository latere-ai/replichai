// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { readLang } from './i18n'

// The language is remembered in localStorage, and localStorage holds whatever
// was last written there — by an older build, by another tab, by hand. It used
// to be trusted as it was, so a value naming no locale ('fr') reached the string
// table as an undefined block and the first t() threw. Anything unrecognised is
// English, the same rule the theme follows for its stored mode.
test('a stored language the app does not have reads as English', () => {
  expect(readLang('fr')).toBe('en')
  expect(readLang('')).toBe('en')
  expect(readLang(null)).toBe('en')
  expect(readLang('EN')).toBe('en')
})

test('a stored language the app has is kept', () => {
  expect(readLang('en')).toBe('en')
})
