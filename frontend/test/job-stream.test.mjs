// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for the live stream a running session is watched
// through.
//
// The bug it guards: the server sends a snapshot of the job the moment a stream
// connects (internal/httpapi/events.go). The session screen stored that snapshot
// in the very state its stream effect depended on, so every connect produced a
// frame, every frame a new job object, and every new job object a reconnect —
// a tight loop of GET /jobs/{id}/events for as long as a running session's page
// was open. A session row in a project had the same shape and reopened its
// stream on every refresh of the list around it.
//
// The measurement is the number of connections the browser opens. The mock
// answers a stream exactly as the server does, snapshot first, and holds the
// reconnect interval long, so within the window a screen that behaves opens one.
// Run it with `bun run test:browser`.
import { check, fail, sleep, start, waitUntil } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()

const running = {
  id: 'job-1',
  kind: 'replicate',
  ref: 'https://dl.acm.org/doi/10.1145/3544548.3581433',
  phase: 'running',
  created_at: at(0),
  phase_at: { submitted: at(0), running: at(1000) },
}
const project = { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { wallet: 42.5, start_reserve: 5 } }],
  ['/me/profile', {}],
  ['/projects', []],
  [/^\/(public|shared)$/, []],
  ['/projects/proj-1', { project, sessions: [running], may_submit: true }],
  ['/projects/proj-1/papers', []],
  ['/projects/proj-1/members', []],
  // The cancel the server sends while a run is still coming down: the signal
  // landed, the session has not settled yet.
  ['POST /jobs/job-1/cancel', running],
  ['/jobs/job-1', running],
  ['/jobs/job-1/transcript', { events: [], usage: null, live: true }],
]

const streams = (page) => page.calls.filter((c) => c === 'GET /jobs/job-1/events').length

const h = await start()
try {
  // The session screen, on a session that is running. One stream, held open.
  await (async () => {
    const page = await h.page({ routes })
    await page.open('/p/proj-1/s/job-1')
    await page.waitForSelector('.session-head .row-title')
    await waitUntil(() => streams(page) >= 1, { what: 'the session screen to open its stream' })
    // The window in which a loop shows itself: a reconnect on every frame opens
    // dozens of streams in this time, a screen that behaves opens none.
    await sleep(1500)
    check(streams(page) === 1, `the session screen opened ${streams(page)} streams for one running session; expected exactly one`)
    if (page.pageErrors.length) fail(`the session screen threw: ${page.pageErrors.join('; ')}`)
    await page.close()
  })()

  // A session row in a project, through a refresh of the list around it. The
  // stop lands, the screen re-reads the project, the row is handed a new object
  // for the same session — and must keep the stream it has.
  await (async () => {
    const page = await h.page({ routes })
    await page.open('/p/proj-1')
    await page.waitForSelector('.rows .row button.btn')
    await waitUntil(() => streams(page) >= 1, { what: 'the session row to open its stream' })
    const reads = () => page.calls.filter((c) => c === 'GET /projects/proj-1').length
    const before = reads()
    await (await page.$('.rows .row button.btn')).click()
    await waitUntil(() => reads() > before, { what: 'the project to be re-read after the stop' })
    await sleep(500)
    check(streams(page) === 1, `a session row reopened its stream on a refresh of the list: ${streams(page)} streams for one running session`)
    await page.close()
  })()

  // The stream still reaches the row: a snapshot that says the session has
  // settled settles the row, with no refresh of the list to help it.
  await (async () => {
    const stopped = { ...running, phase: 'failed', error: 'cancelled by owner', phase_at: { ...running.phase_at, failed: at(5000) } }
    const page = await h.page({ routes, sse: { jobOf: () => stopped } })
    await page.open('/p/proj-1')
    await page.waitForSelector('.rows .row')
    await page.waitForFunction(() => document.querySelector('.rows .row')?.textContent.includes('stopped'), { timeout: 5000 }).catch(() => {})
    const row = await page.evaluate(() => {
      const r = document.querySelector('.rows .row')
      return { text: r?.textContent ?? '', stop: !!r?.querySelector('button.btn') }
    })
    check(row.text.includes('stopped'), `the stream's snapshot never reached the row: ${JSON.stringify(row.text)}`)
    check(!row.stop, 'a row settled by its stream still offers Stop')
    await page.close()
  })()

  h.ok('a running session is watched through exactly one stream')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
