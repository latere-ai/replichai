// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, expect, test } from 'bun:test'
import { listProjects, setUnauthorizedHandler, topUp } from './api'

const realFetch = globalThis.fetch
afterEach(() => {
  globalThis.fetch = realFetch
  setUnauthorizedHandler(null)
})

test('a 401 from any call reaches the one handler', async () => {
  let told = 0
  setUnauthorizedHandler(() => told++)
  globalThis.fetch = (async () => new Response('{}', { status: 401 })) as unknown as typeof fetch
  await expect(listProjects()).rejects.toMatchObject({ status: 401 })
  expect(told).toBe(1)
})

test('another refusal does not', async () => {
  let told = 0
  setUnauthorizedHandler(() => told++)
  globalThis.fetch = (async () => new Response('{}', { status: 403 })) as unknown as typeof fetch
  await expect(listProjects()).rejects.toMatchObject({ status: 403 })
  expect(told).toBe(0)
})

test('a top-up sends its idempotency key', async () => {
  let sent: Headers | undefined
  globalThis.fetch = (async (_u: unknown, init?: RequestInit) => {
    sent = new Headers(init?.headers)
    return new Response('{"email":"a@x.org","wallet":1}', { status: 200 })
  }) as unknown as typeof fetch
  await topUp('a@x.org', 5, 'k-1')
  expect(sent?.get('Idempotency-Key')).toBe('k-1')
})
