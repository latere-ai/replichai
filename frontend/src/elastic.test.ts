// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'

import { chromium, REACH, rubber } from './elastic'

// The stretch has to give at first, stiffen as the pull goes on, and never pass
// its reach however hard the wheel is pushed: a pane that follows the wheel one
// for one would scroll away from its frame instead of stretching.
test('a pull stretches less than it is pulled, and never past its reach', () => {
  expect(rubber(0)).toBe(0)
  const small = rubber(20)
  const large = rubber(2000)
  expect(small).toBeGreaterThan(0)
  expect(small).toBeLessThan(20)
  expect(large).toBeLessThan(REACH)
  expect(rubber(400) - rubber(200)).toBeLessThan(rubber(200) - rubber(0))
})

// A pull past the end stretches the other way, by the same curve.
test('a pull past the end stretches upward by the same amount', () => {
  expect(rubber(-120)).toBe(-rubber(120))
})

// Chromium stretches only the page, so the panes are drawn there and only
// there; Safari and Firefox stretch a pane on their own.
test('the panes are stretched by the app only in Chromium', () => {
  expect(chromium('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36')).toBe(true)
  expect(chromium('Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Safari/605.1.15')).toBe(false)
  expect(chromium('Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:142.0) Gecko/20100101 Firefox/142.0')).toBe(false)
})
