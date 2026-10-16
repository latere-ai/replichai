// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for a researcher's public page when the address
// moves from one handle to another without a reload, by Back, Forward or a link.
//
// It guards two things: a "not found" for one handle is cleared when the
// address moves to another, and a slow 404 for a handle the reader has left
// never lands on the page they moved to.
// Run it with `bun run test:browser`.
import { check, delay, fail, json, sleep, start } from './harness.mjs'

const beatrix = { handle: 'b', display_name: 'Beatrix Potter', projects: [] }
const missing = json({ error: 'not_found' }, 404)

// go moves the address the way the browser does for Back and Forward: the
// router follows popstate, and the screen is handed the new handle in place.
const go = (page, path) =>
  page.evaluate((p) => {
    history.pushState(null, '', p)
    dispatchEvent(new PopStateEvent('popstate'))
  }, path)

const h = await start()
try {
  // A profile that exists, opened after one that does not.
  await (async () => {
    const page = await h.page({
      routes: [
        ['/me', { authenticated: false }],
        ['/profiles/a', missing],
        ['/profiles/b', beatrix],
      ],
    })
    await page.open('/u/a')
    await page.waitForSelector('.auth-card h2')
    await go(page, '/u/b')
    await page.waitForSelector('.profile-name', { timeout: 3000 }).catch(() => {})
    const shown = await page.evaluate(() => ({
      name: document.querySelector('.profile-name')?.textContent.trim() ?? null,
      notFound: !!document.querySelector('.auth-card'),
    }))
    check(shown.name === 'Beatrix Potter', `after a missing profile, an existing one should render; got ${JSON.stringify(shown)}`)
    check(!shown.notFound, 'the previous handle’s "not found" is still on screen over an existing profile')
    await page.close()
  })()

  // A slow answer for the handle the reader has left must not land on the one
  // they are looking at.
  await (async () => {
    const page = await h.page({
      routes: [
        ['/me', { authenticated: false }],
        ['/profiles/a', () => delay(700, missing)],
        ['/profiles/b', beatrix],
      ],
    })
    await page.open('/u/a')
    await page.waitForSelector('.center-load')
    await go(page, '/u/b')
    await page.waitForSelector('.profile-name')
    // The window in which the abandoned 404 arrives.
    await sleep(900)
    const shown = await page.evaluate(() => ({
      name: document.querySelector('.profile-name')?.textContent.trim() ?? null,
      notFound: !!document.querySelector('.auth-card'),
    }))
    check(shown.name === 'Beatrix Potter' && !shown.notFound, `a late 404 for a handle the reader left replaced the page they were on: ${JSON.stringify(shown)}`)
    await page.close()
  })()

  h.ok('a public profile follows the handle in the address')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
