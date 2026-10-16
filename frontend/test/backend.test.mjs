// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for where an unmocked API call ends up.
//
// A request no route names is passed on to the dev server, which proxies API
// paths to a backend. The dev server's default backend is localhost:8080, a
// port the suite does not own; on a shared runner a service there could accept
// the connection and answer slowly or not at all, leaving a screen waiting on a
// request this suite never meant to make.
//
// So the suite runs its own backend and points the proxy at it. The check is
// that an unmocked call is answered by that backend, which marks its answers,
// and promptly, with the error a refused proxy gives. Run it with
// `bun run test:browser`.
import { check, fail, start, STAND_IN_HEADER } from './harness.mjs'

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', {}],
  [/^\/(projects|public|shared)$/, []],
]

const h = await start()
try {
  const page = await h.page({ routes })
  await page.open('/')
  await page.waitForSelector('.shell')

  // /models is an API path the dev server proxies and this file does not mock.
  const answer = await page.evaluate(async (header) => {
    const began = performance.now()
    const res = await fetch('/models')
    return { status: res.status, from: res.headers.get(header), ms: performance.now() - began }
  }, STAND_IN_HEADER)

  check(answer.from === 'stand-in', `an unmocked API call was not answered by the suite's own backend (marker header: ${answer.from}); it went to whatever the dev server proxies to`)
  check(answer.status === 500, `an unmocked API call answered ${answer.status}, want the 500 a refused proxy gives`)
  check(answer.ms < 5000, `an unmocked API call took ${Math.round(answer.ms)}ms to be refused`)

  h.ok('an unmocked API call is answered by the suite, not by port 8080')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
