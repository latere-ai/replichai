// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { grantKeys } from './grant-key'

const counter = () => {
  let n = 0
  return () => `key-${++n}`
}

test('a retried grant carries the same key', () => {
  const k = grantKeys(counter())
  const first = k.keyFor('a@x.org', 5_000_000)
  expect(k.keyFor('A@x.org', 5_000_000)).toBe(first)
})

test('a grant that went through frees its key for the next one', () => {
  const k = grantKeys(counter())
  const first = k.keyFor('a@x.org', 5_000_000)
  k.settled('a@x.org', 5_000_000)
  expect(k.keyFor('a@x.org', 5_000_000)).not.toBe(first)
})

test('a different grant gets a different key', () => {
  const k = grantKeys(counter())
  expect(k.keyFor('a@x.org', 5_000_000)).not.toBe(k.keyFor('a@x.org', 6_000_000))
  expect(k.keyFor('a@x.org', 5_000_000)).not.toBe(k.keyFor('b@x.org', 5_000_000))
})
