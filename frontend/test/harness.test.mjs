// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// What the harness itself has to hold, because the rest of the suite is written
// on top of it and cannot see any of it.
//
// One browser for the run. Every file used to launch its own Chrome, so a run
// held one browser per job, each with its own zygote, network service, GPU
// process and renderers. A browser that dies under that pressure is not an error
// puppeteer raises: the call waiting on it is never answered, so the file blocks
// until some later wait times out and reports the element that wait named. That
// is how a browser that crashed was read as a landing page that never rendered.
//
// So two things are asserted here. The file runs in a browser it did not start,
// which is the pressure removed. And a browser that goes away is reported as a
// browser that went away.
import { check, start, startBrowser, watchBrowser } from './harness.mjs'

const h = await start()

try {
  /* ── One browser for the run ── */

  // A run that shares a server shares a browser: WEB_TEST_BASE is the runner
  // saying it started one of them, and WEB_TEST_WS is it saying it started the
  // other. `browser.process()` is the child a launch owns and is null for a
  // connection, so it says which of the two this file actually got.
  if (process.env.WEB_TEST_BASE) {
    check(!!process.env.WEB_TEST_WS, 'the run shares one server and no browser, so every file launches its own')
    check(h.browser.process() === null, 'the file launched its own browser instead of joining the run’s')
  } else {
    check(h.browser.process() !== null, 'a file run on its own has no browser to drive')
  }

  /* ── One context per file ── */

  // Pages in one browser share cookies and local storage. A file must not read
  // what another file wrote, so each takes a context, and that is what the
  // shared browser costs: this is the check that it is paid.
  {
    const page = await h.page({ routes: [['/me', { authenticated: false }]] })
    await page.open('/')
    await page.evaluate(() => localStorage.setItem('harness-probe', 'one'))
    await page.close()

    const other = await h.browser.createBrowserContext()
    const fresh = await other.newPage()
    await fresh.goto(`${h.base}/`, { waitUntil: 'domcontentloaded' })
    const leaked = await fresh.evaluate(() => localStorage.getItem('harness-probe'))
    check(leaked === null, `local storage crossed contexts: another context reads ${leaked}`)
    await other.close()
  }

  /* ── The runner starts the browser it names ── */

  {
    // The runner is the owner: it names the browser for its children and closes
    // it at the end, so it must not read that name back. A WEB_TEST_WS left in a
    // shell by an earlier run would otherwise point a whole run at a browser
    // nobody owns, and the run would close somebody else's process.
    const saved = process.env.WEB_TEST_WS
    process.env.WEB_TEST_WS = 'ws://127.0.0.1:1/devtools/browser/gone'
    const own = await startBrowser({ join: null }).catch((e) => e)
    if (saved === undefined) delete process.env.WEB_TEST_WS
    else process.env.WEB_TEST_WS = saved
    if (own instanceof Error) check(false, `the runner joined a stale browser: ${own.message}`)
    else {
      check(own.owned && !!own.browser, 'the runner joined a browser instead of starting the one it owns')
      await own.browser?.close().catch(() => {})
    }
  }

  /* ── A browser that goes away says so ── */

  {
    const events = {}
    const stub = { on: (name, fn) => (events[name] = fn) }
    let gone = 0
    const release = watchBrowser(stub, { onGone: () => gone++ })
    events.disconnected()
    check(gone === 1, 'a browser that disconnected mid-run was not reported')
    release()
    events.disconnected()
    check(gone === 1, 'closing the browser at the end was reported as losing it')
  }

  h.ok('the suite runs in one browser, a file cannot read another file’s storage, and a lost browser is named')
} catch (e) {
  check(false, e)
} finally {
  await h.close()
}
