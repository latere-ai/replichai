// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { readMode, resolveTheme, THEME_MODES } from './theme'
import { hueOf } from './App'

// The mode is what persists; the resolved theme is what the stylesheet reads.
// Every dark rule hangs off [data-theme='dark'], so "auto" must never reach the
// DOM — resolving it here is the whole reason this function exists.
test('auto follows the system preference', () => {
  expect(resolveTheme('auto', true)).toBe('dark')
  expect(resolveTheme('auto', false)).toBe('light')
})

test('a pinned mode ignores the system preference', () => {
  expect(resolveTheme('dark', false)).toBe('dark')
  expect(resolveTheme('light', true)).toBe('light')
})

// The app stored a bare 'dark'/'light' before there was an auto mode, so a
// returning operator must keep the appearance they picked.
test('a stored legacy choice survives', () => {
  expect(readMode('dark')).toBe('dark')
  expect(readMode('light')).toBe('light')
})

test('a first visit, or a corrupted value, defaults to auto', () => {
  expect(readMode(null)).toBe('auto')
  expect(readMode('')).toBe('auto')
  expect(readMode('sepia')).toBe('auto')
})

// The switcher renders one control per mode, so a mode missing from this list
// would be unreachable in the UI even though resolveTheme still handles it.
test('every mode is offered by the switcher, and each resolves', () => {
  expect(THEME_MODES).toEqual(['auto', 'light', 'dark'])
  for (const mode of THEME_MODES) {
    expect(readMode(mode)).toBe(mode)
    expect(['light', 'dark']).toContain(resolveTheme(mode, true))
    expect(['light', 'dark']).toContain(resolveTheme(mode, false))
  }
})

// The avatar hue is decoration, but a stable one: the same address is the same
// colour everywhere it appears, and case is not part of the identity.
test('an avatar hue is stable, case-insensitive, and in range', () => {
  expect(hueOf('ada@example.edu')).toBe(hueOf('ADA@Example.edu'))
  expect(hueOf('ada@example.edu')).not.toBe(hueOf('grace@example.edu'))
  for (const email of ['a@b.c', 'ada@example.edu', 'a-very-long-address@some.university.example']) {
    const h = hueOf(email)
    expect(h).toBeGreaterThanOrEqual(0)
    expect(h).toBeLessThan(360)
  }
})

test('an absent address still gets a hue', () => {
  expect(hueOf(undefined)).toBe(240)
  expect(hueOf('')).toBe(240)
})
