// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { ApiError, makeProjectPublic } from './api'

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
    })) as typeof fetch
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
  globalThis.fetch = (async () => new Response('not json', { status: 500 })) as typeof fetch
  try {
    await makeProjectPublic('p1', { signed_off_contact: 'ada@example.org' })
    throw new Error('the call should have rejected')
  } catch (e) {
    expect((e as ApiError).blockers).toEqual([])
  } finally {
    globalThis.fetch = real
  }
})
