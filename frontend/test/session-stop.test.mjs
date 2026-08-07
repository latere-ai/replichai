// A browser regression test for the Stop control on a running session.
//
// The bug it guards: stopping is not instant. POST /jobs/{id}/cancel returns as
// soon as the worker has been signalled, and the session it reads back can still
// be running — the backend run has yet to come down. The row cleared its
// "stopping" flag the moment that response landed, so the button snapped back to
// a live, red "Stop" over a session still badged RUNNING, and a second click
// fired a second cancel.
//
// "The button says Stopping… while the request is in flight" passes against the
// broken code too — that part always worked. The discriminating measurement is
// taken *after* the response has landed: with the session still non-terminal,
// the button must still be disabled and still read Stopping…. Run it with
// `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5198
const BASE = `http://localhost:${PORT}`

const asked = process.env.PUPPETEER_EXECUTABLE_PATH
const CHROME = asked
  ? asked
  : ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/usr/bin/google-chrome', '/usr/bin/chromium'].find((p) => require('node:fs').existsSync(p))

if (!CHROME || !require('node:fs').existsSync(CHROME)) {
  if (asked) {
    console.error(`FAIL: PUPPETEER_EXECUTABLE_PATH=${asked} does not exist`)
    process.exit(1)
  }
  console.log('skip: no Chrome found (set PUPPETEER_EXECUTABLE_PATH)')
  process.exit(0)
}

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()

const running = {
  id: 'job-1',
  kind: 'replicate',
  ref: 'https://dl.acm.org/doi/10.1145/3544548.3581433',
  phase: 'running',
  created_at: at(0),
  phase_at: { submitted: at(0), running: at(1000) },
}

const project = {
  project: { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) },
  sessions: [running],
  may_submit: true,
}

const routes = [
  [/^\/me$/, { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { wallet: 42.5, start_reserve: 5 } }],
  [/^\/me\/profile$/, {}],
  [/^\/projects$/, []],
  [/^\/(public|shared)$/, []],
  [/^\/projects\/proj-1$/, project],
]

const fail = (msg) => {
  console.error(`FAIL: ${msg}`)
  process.exitCode = 1
}

const vite = spawn('npx', ['vite', '--port', String(PORT), '--strictPort'], { stdio: 'ignore' })
let browser
let cancels = 0
let cancelStatus = 200
let settled = false
try {
  for (let i = 0; ; i++) {
    try {
      if ((await fetch(BASE)).ok) break
    } catch {
      /* not up yet */
    }
    if (i > 240) throw new Error('vite did not start')
    await new Promise((r) => setTimeout(r, 250))
  }

  browser = await puppeteer.launch({ executablePath: CHROME, headless: true })
  const page = await browser.newPage()
  await page.setViewport({ width: 1440, height: 900 })
  await page.setRequestInterception(true)
  page.on('request', (req) => {
    const u = new URL(req.url())
    if (u.origin === BASE) {
      // The cancel the server really sends while a run is still coming down: the
      // signal landed, the session has not settled yet.
      if (u.pathname === '/jobs/job-1/cancel' && req.method() === 'POST') {
        cancels++
        if (cancelStatus !== 200) return req.respond({ status: cancelStatus, contentType: 'application/json', body: JSON.stringify({ error: 'cancel_failed' }) })
        return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(running) })
      }
      const hit = routes.find(([re]) => re.test(u.pathname))
      if (hit) return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(hit[1]) })
      // A short reconnect keeps the stream cheap to drive: each poll is a fresh
      // interception, so a frame can be armed at any point in the test.
      if (u.pathname.endsWith('/events')) {
        const frame = settled ? `event: status\ndata: ${JSON.stringify({ ...running, phase: 'failed', error: 'cancelled by owner' })}\n\n` : ''
        return req.respond({ status: 200, contentType: 'text/event-stream', body: `retry: 200\n\n${frame}` })
      }
    }
    req.continue()
  })

  // The Stop button is the only one in the session row.
  const stopBtn = () => page.$('.rows .row button.btn')
  const readBtn = async () => {
    const b = await stopBtn()
    if (!b) return null
    return page.evaluate((el) => ({ label: el.textContent.trim(), disabled: el.disabled, pending: el.classList.contains('pending') }), b)
  }

  // An early mismatch means the mock or the row changed, and the measurement
  // below would not be the reported bug — so those cases stop the run.
  await (async () => {
    await page.goto(`${BASE}/p/proj-1`, { waitUntil: 'domcontentloaded' })
    await page.waitForSelector('.rows .row button.btn')
    await new Promise((r) => setTimeout(r, 400)) // let the entrance animation settle

    const before = await readBtn()
    if (before?.label !== 'Stop' || before.disabled) return fail(`a running session should offer a live Stop, got ${JSON.stringify(before)}`)

    await (await stopBtn()).click()

    // Wait for the cancel to land and the row to have re-rendered on its answer.
    for (let i = 0; cancels === 0 && i < 100; i++) await new Promise((r) => setTimeout(r, 50))
    await new Promise((r) => setTimeout(r, 300))
    if (cancels !== 1) return fail(`expected exactly one cancel request, saw ${cancels}`)

    // The report. The session is still running, so the stop is still in flight.
    const after = await readBtn()
    if (!after) return fail('the Stop button vanished from a session that is still running')
    if (!after.disabled) fail(`the Stop button re-armed while the session was still running — a second click would send a second cancel (${JSON.stringify(after)})`)
    if (after.label !== 'Stopping…') fail(`the Stop button should read "Stopping…" while the session comes down, got ${JSON.stringify(after.label)}`)
    if (!after.pending) fail('the pending Stop button lost its animated state')

    // And a click on it does nothing: a disabled control sends no second cancel.
    await (await stopBtn()).click().catch(() => {})
    await new Promise((r) => setTimeout(r, 300))
    if (cancels !== 1) fail(`a click on the stopping button sent another cancel (${cancels} total)`)
  })()

  // A stop that does not land — the worker could not close the backend run — is
  // the other half: the session is still up, so the button has to come back
  // rather than sit dead, and the reason has to be on screen. The row carries
  // one error slot, and it still has to fit in the row.
  await (async () => {
    cancelStatus = 500
    cancels = 0
    await page.goto(`${BASE}/p/proj-1`, { waitUntil: 'domcontentloaded' })
    await page.waitForSelector('.rows .row button.btn')
    await new Promise((r) => setTimeout(r, 400))

    await (await stopBtn()).click()
    for (let i = 0; cancels === 0 && i < 100; i++) await new Promise((r) => setTimeout(r, 50))
    await new Promise((r) => setTimeout(r, 300))

    const back = await readBtn()
    if (!back || back.disabled || back.label !== 'Stop') fail(`a stop that failed must hand the button back, got ${JSON.stringify(back)}`)

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
    if (row.errors.length !== 1 || !row.errors[0].startsWith('Couldn’t stop')) fail(`the row should carry exactly one error, the stop failure — got ${JSON.stringify(row.errors)}`)
    if (row.overflow > 1) fail(`the row overflows by ${row.overflow}px once the stop error is in it`)
    if (row.chevInside === null || row.chevInside < 0) fail(`the stop error pushed the chevron out of the row (${row.chevInside}px)`)

    // "It's still running" stops being true the moment it stops running — by a
    // retry, by another window, or by the run ending on its own. The stream
    // settles the session here, and the message has to go with it.
    settled = true
    for (let i = 0; i < 60; i++) {
      const now = await page.evaluate(() => document.querySelector('.rows .row')?.textContent ?? '')
      if (now.includes('stopped')) break
      await new Promise((r) => setTimeout(r, 100))
    }
    const end = await page.evaluate(() => {
      const r = document.querySelector('.rows .row')
      return { text: r.textContent, errors: [...r.querySelectorAll('.err')].map((e) => e.textContent.trim()), btn: !!r.querySelector('button.btn') }
    })
    if (!end.text.includes('stopped')) return fail(`the stream never settled the session: ${JSON.stringify(end.text)}`)
    if (end.btn) fail('a settled session still offers Stop')
    if (end.errors.length) fail(`a settled session still shows a stop error: ${JSON.stringify(end.errors)}`)
  })()

  if (!process.exitCode) console.log('ok: Stop stays pending until the session settles')
} catch (e) {
  fail(e?.stack || String(e))
} finally {
  await browser?.close()
  vite.kill('SIGTERM')
}
