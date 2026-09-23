// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for the Stop control on a running session.
//
// The bug it guards: stopping is not instant. POST /jobs/{id}/cancel returns as
// soon as the worker has been signaled, and the session it reads back can still
// be running because the backend run has yet to come down. The row cleared its
// "stopping" flag the moment that response landed, so the button snapped back to
// a live, red "Stop" over a session still badged RUNNING, and a second click
// fired a second cancel.
//
// "The button says Stopping… while the request is in flight" passes against the
// broken code too; that part always worked. The discriminating measurement is
// taken after the response has landed: with the session still non-terminal,
// the button must still be disabled and still read Stopping…. Run it with
// `bun run test:browser`.
import { check, fail, json, settled, sleep, sse, start, waitUntil } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()

const running = {
  id: 'job-1',
  kind: 'replicate',
  ref: 'https://dl.acm.org/doi/10.1145/3544548.3581433',
  phase: 'running',
  created_at: at(0),
  phase_at: { submitted: at(0), running: at(1000) },
}
const stopped = { ...running, phase: 'failed', error: 'cancelled by owner' }

const project = {
  project: { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) },
  sessions: [running],
  may_submit: true,
}

let cancels = 0
let cancelStatus = 200
let isSettled = false

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { wallet: 42.5, start_reserve: 5 } }],
  ['/me/profile', {}],
  ['/projects', []],
  [/^\/(public|shared)$/, []],
  ['/projects/proj-1', project],
  // The cancel the server really sends while a run is still coming down: the
  // signal landed, the session has not settled yet.
  [
    'POST /jobs/job-1/cancel',
    () => {
      cancels++
      return cancelStatus === 200 ? running : json({ error: 'cancel_failed' }, cancelStatus)
    },
  ],
  // A short reconnect keeps the stream cheap to drive: each poll is a fresh
  // interception, so the settled frame can be armed at any point in the test.
  [/^\/jobs\/job-1\/events$/, () => sse([isSettled ? stopped : running], { retry: 200 })],
]

const h = await start()
try {
  const page = await h.page({ routes })

  // The Stop button is the only one in the session row.
  const stopBtn = () => page.$('.rows .row button.btn')
  const readBtn = async () => {
    const b = await stopBtn()
    if (!b) return null
    return page.evaluate((el) => ({ label: el.textContent.trim(), disabled: el.disabled, pending: el.classList.contains('pending') }), b)
  }

  // An early mismatch means the mock or the row changed, and the measurement
  // below would not be the reported bug, so those cases stop the run.
  await (async () => {
    await page.open('/p/proj-1')
    await page.waitForSelector('.rows .row button.btn')
    await settled(page)

    const before = await readBtn()
    if (before?.label !== 'Stop' || before.disabled) return fail(`a running session should offer a live Stop, got ${JSON.stringify(before)}`)

    await (await stopBtn()).click()

    // Wait for the cancel to land and the row to have re-rendered on its answer.
    // The re-render itself is the measurement, so it is given a moment rather
    // than waited for: the broken row and the fixed one both change here.
    await waitUntil(() => cancels === 1, { what: 'the cancel to be sent' })
    await sleep(300)

    // The report. The session is still running, so the stop is still in flight.
    const after = await readBtn()
    if (!after) return fail('the Stop button vanished from a session that is still running')
    check(after.disabled, `the Stop button re-armed while the session was still running — a second click would send a second cancel (${JSON.stringify(after)})`)
    check(after.label === 'Stopping…', `the Stop button should read "Stopping…" while the session comes down, got ${JSON.stringify(after.label)}`)
    check(after.pending, 'the pending Stop button lost its animated state')

    // And a click on it does nothing: a disabled control sends no second cancel.
    await (await stopBtn()).click().catch(() => {})
    await sleep(300)
    check(cancels === 1, `a click on the stopping button sent another cancel (${cancels} total)`)
  })()

  // A stop that does not land, because the worker could not close the backend
  // run, is the other half: the session is still up, so the button has to come
  // back rather than stay disabled, and the reason has to be on screen. The row
  // carries one error slot, and it still has to fit in the row.
  await (async () => {
    cancelStatus = 500
    cancels = 0
    await page.open('/p/proj-1')
    await page.waitForSelector('.rows .row button.btn')
    await settled(page)

    await (await stopBtn()).click()
    await waitUntil(() => cancels === 1, { what: 'the failing cancel to be sent' })
    await page.waitForFunction(() => {
      const b = document.querySelector('.rows .row button.btn')
      return b && !b.disabled
    })

    const back = await readBtn()
    check(back && !back.disabled && back.label === 'Stop', `a stop that failed must hand the button back, got ${JSON.stringify(back)}`)

    const row = await page.evaluate(() => {
      const r = document.querySelector('.rows .row')
      const errs = [...r.querySelectorAll('.err')]
      const chev = r.querySelector('.chev')
      return {
        errors: errs.map((e) => e.textContent.trim()),
        overflow: r.scrollWidth - r.clientWidth,
        chevInside: chev ? r.getBoundingClientRect().right - chev.getBoundingClientRect().right : null,
      }
    })
    check(row.errors.length === 1 && row.errors[0].startsWith('Couldn’t stop'), `the row should carry exactly one error, the stop failure — got ${JSON.stringify(row.errors)}`)
    check(row.overflow <= 1, `the row overflows by ${row.overflow}px once the stop error is in it`)
    check(row.chevInside !== null && row.chevInside >= 0, `the stop error pushed the chevron out of the row (${row.chevInside}px)`)

    // "It's still running" stops being true the moment it stops running, whether
    // by a retry, by another window, or by the run ending on its own. The stream
    // settles the session here, and the message has to go with it.
    isSettled = true
    await page.waitForFunction(() => document.querySelector('.rows .row')?.textContent.includes('stopped'), { timeout: 6000 }).catch(() => {})
    const end = await page.evaluate(() => {
      const r = document.querySelector('.rows .row')
      return { text: r.textContent, errors: [...r.querySelectorAll('.err')].map((e) => e.textContent.trim()), btn: !!r.querySelector('button.btn') }
    })
    if (!end.text.includes('stopped')) return fail(`the stream never settled the session: ${JSON.stringify(end.text)}`)
    check(!end.btn, 'a settled session still offers Stop')
    check(!end.errors.length, `a settled session still shows a stop error: ${JSON.stringify(end.errors)}`)
  })()

  h.ok('Stop stays pending until the session settles')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
