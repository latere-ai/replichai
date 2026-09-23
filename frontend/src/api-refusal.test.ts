// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { ApiError, getMe, makeProjectPublic } from './api'
import { refusal } from './errors'

// The client used to read only the `error` field off a refusal and drop the
// rest of the body. A publish refusal now carries the blocking sessions, and the
// screen cannot show them if they are dropped here, so the parse is asserted at
// the client boundary through a real call.
test('a refusal keeps the blockers the server sent', async () => {
  const real = globalThis.fetch
  globalThis.fetch = (async () =>
    new Response(JSON.stringify({ error: 'not_clear', blockers: [{ session_id: 'job-1', reason: 'not_clear', review: 'excluded' }] }), {
      status: 409,
      headers: { 'content-type': 'application/json' },
    })) as unknown as typeof fetch
  try {
    await makeProjectPublic('p1', { signed_off_contact: 'ada@example.org' })
    throw new Error('the call should have rejected')
  } catch (e) {
    expect(e).toBeInstanceOf(ApiError)
    const err = e as ApiError
    expect(err.status).toBe(409)
    expect(err.code).toBe('not_clear')
    expect(err.blockers).toEqual([{ session_id: 'job-1', reason: 'not_clear', review: 'excluded' }])
  } finally {
    globalThis.fetch = real
  }
})

// A refusal with no blockers, such as an unparseable body or a bare status,
// yields an empty list, never undefined, because the screen maps over it.
test('a refusal with nothing named is an empty list', async () => {
  const real = globalThis.fetch
  globalThis.fetch = (async () => new Response('not json', { status: 500 })) as unknown as typeof fetch
  try {
    await makeProjectPublic('p1', { signed_off_contact: 'ada@example.org' })
    throw new Error('the call should have rejected')
  } catch (e) {
    expect((e as ApiError).blockers).toEqual([])
  } finally {
    globalThis.fetch = real
  }
})

// A 2xx whose body is not JSON, such as the dev server's own HTML for a path it
// does not proxy or a gateway page in front of the API, used to reach r.json()
// and reject with a SyntaxError. Nothing downstream recognized that error, so
// every screen reported it as "you appear to be offline", which is wrong: the
// network answered. It is now reported as a server fault, with its status and a
// code, so the mappers can name it as one.
test('a 2xx that is not JSON is a server fault, not a lost connection', async () => {
  const real = globalThis.fetch
  globalThis.fetch = (async () =>
    new Response('<!doctype html><title>Not the API</title>', { status: 200, headers: { 'content-type': 'text/html' } })) as unknown as typeof fetch
  try {
    await getMe()
    throw new Error('the call should have rejected')
  } catch (e) {
    expect(e).toBeInstanceOf(ApiError)
    expect((e as ApiError).status).toBe(200)
    expect((e as ApiError).code).toBe('malformed_response')
    expect(refusal(e).offline).toBe(false)
  } finally {
    globalThis.fetch = real
  }
})

// Only a fetch that never got an answer is classified as offline.
test('a fetch that never got an answer is offline', () => {
  expect(refusal(new TypeError('Failed to fetch')).offline).toBe(true)
})
