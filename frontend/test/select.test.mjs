// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for the Select's scroll listener.
//
// The bug it guards: while the list is open, a scroll anywhere dismisses it, so
// the list arms a one-shot capture listener on window. The effect's cleanup
// removed the resize listener and not that one, so every open that ended any
// other way, by Escape, a pick or a click outside, left a listener behind, and a
// Select opened ten times had ten of them waiting on the next scroll, each
// referencing a component that may since have unmounted.
//
// The measurement is the browser's own count of scroll listeners on window,
// read through the DevTools protocol, before and after opening and closing the
// list without scrolling. Run it with `bun run test:browser`.
import { check, fail, settled, start } from './harness.mjs'

const routes = [
  ['/me', { authenticated: true, principal: 'admin', email: 'admin@example.com', tier: 'admin', credits: { wallet: 5_000_000, start_reserve: 5_000_000 } }],
  ['/me/profile', {}],
  [/^\/(projects|public|shared)$/, []],
  ['/admin/access', []],
  ['/admin/settings', { start_reserve: 5_000_000, overdraft_cap: 20_000_000, rates: { models: [] } }],
  ['/admin/solvency', { outstanding_micro: 0 }],
]

const h = await start()
try {
  const page = await h.page({ routes })
  const cdp = await page.createCDPSession()
  await cdp.send('Runtime.enable')
  const scrollListeners = async () => {
    const { result } = await cdp.send('Runtime.evaluate', { expression: 'window' })
    const { listeners } = await cdp.send('DOMDebugger.getEventListeners', { objectId: result.objectId })
    return listeners.filter((l) => l.type === 'scroll').length
  }

  await page.open('/console')
  await page.waitForSelector('.sel-btn')
  await settled(page)

  const before = await scrollListeners()
  for (let i = 0; i < 3; i++) {
    await page.click('.sel-btn')
    await page.waitForSelector('.sel-list')
    await page.keyboard.press('Escape')
    await page.waitForSelector('.sel-list', { hidden: true })
  }
  const after = await scrollListeners()
  check(after === before, `${after - before} scroll listeners were left on window after opening and closing the list three times`)

  // The listener still does its one job: a scroll while the list is open closes
  // it.
  await page.click('.sel-btn')
  await page.waitForSelector('.sel-list')
  await page.evaluate(() => window.dispatchEvent(new Event('scroll')))
  await page.waitForSelector('.sel-list', { hidden: true, timeout: 2000 }).catch(() => fail('a scroll no longer dismisses the open list'))
  check((await scrollListeners()) === before, 'the scroll that dismissed the list left its listener behind')

  h.ok('a Select leaves no scroll listener behind')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
