// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { ApiError, getMe, makeProjectPublic } from './api'
import { refusal } from './errors'

// The client used to read one field off a refusal — `error` — and drop the rest
// of the body. A publish refusal now carries the sessions standing in the way,
// and none of the screen's work on them means anything if they are thrown away
// here. So the parse is asserted at the seam, through a real call.
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

// A refusal with no blockers at all — an unpublishable body, a bare status — is
// an empty list, never undefined: the screen maps over it.
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

// A 2xx whose body is not JSON — the dev server's own HTML for a path it does
// not proxy, a gateway page from in front of the API — used to reach r.json()
// and reject with a SyntaxError. Nothing downstream knew that error, so every
// screen filed it under "you appear to be offline", which is the one thing it
// is not: the network answered. It is a fault of the server's, reported with
// its status and a code, so the mappers can say "we broke" instead.
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

// The classification the fix exists for: a fetch that never got an answer is
// still offline, and only that is.
test('a fetch that never got an answer is offline', () => {
  expect(refusal(new TypeError('Failed to fetch')).offline).toBe(true)
})
