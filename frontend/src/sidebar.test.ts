// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { readFolded } from './sidebar'

// The rail a new operator meets names its destinations. Folding is something you
// ask for, so only the value the app writes for it counts as having asked.
test('a first visit, or a corrupted value, leaves the rail open', () => {
  expect(readFolded(null)).toBe(false)
  expect(readFolded('')).toBe(false)
  expect(readFolded('true')).toBe(false)
  expect(readFolded('1')).toBe(false)
  expect(readFolded('collapsed')).toBe(false)
})

test('the stored fold is honoured', () => {
  expect(readFolded('folded')).toBe(true)
})

// 'open' is written rather than the key being cleared, so a rail deliberately
// unfolded is distinguishable from one never touched. Both read as open, but a
// future default that flips would need to tell them apart.
test('an unfolded rail is stored, not forgotten', () => {
  expect(readFolded('open')).toBe(false)
})
