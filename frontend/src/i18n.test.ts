// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { countOf, readLang, translator } from './i18n'

// The language is remembered in localStorage, and localStorage holds whatever
// was last written there, by an older build, by another tab, or by hand. It used
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

test('countOf uses the singular for exactly one and the count otherwise', () => {
  const { t } = translator('en')
  expect(countOf(t, 'sessionCount', 1)).toBe('1 session')
  expect(countOf(t, 'sessionCount', 0)).toBe('0 sessions')
  expect(countOf(t, 'sessionCount', 3)).toBe('3 sessions')
  expect(countOf(t, 'morePapers', 1)).toBe('1 more paper')
  // A key with no `One` form does not compile, so it can never print its name.
  // @ts-expect-error workspace has no workspaceOne
  countOf(t, 'workspace', 1)
})
