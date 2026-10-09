// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for how long the suite waits on a slow machine.
//
// Every wait in the suite carries a budget written for a developer's machine:
// fifteen seconds for a page to load and show a screen, five for a list to
// open. The CI runner loads the same page about twenty times slower (a file
// that takes 0.4s locally took 8.5s there), so a budget with room to spare at
// a desk left about twice the usual time on the runner, and a busy moment ran
// it out. The suite then failed on a timeout naming an element, in a different
// file each time, on commits that touched no frontend code.
//
// So the harness stretches every wait by one factor, WEB_TEST_SLOW, which is
// 1 at a desk and larger on CI. This file sets the factor to 3 and checks that
// a wait which must fail takes three times its written budget to give up, for
// each kind of wait the suite uses. Run it with `bun run test:browser`.
process.env.WEB_TEST_SLOW = '3'
const { check, fail, start, waitUntil } = await import('./harness.mjs')

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', {}],
  [/^\/(projects|public|shared)$/, []],
]

// gaveUpAfter runs a wait that cannot succeed and returns how long it held on.
async function gaveUpAfter(wait) {
  const began = Date.now()
  try {
    await wait()
  } catch {
    return Date.now() - began
  }
  fail('a wait for something that never happens succeeded')
  return 0
}

const h = await start()
try {
  const page = await h.page({ routes })
  await page.open('/')
  await page.waitForSelector('.shell')

  const WRITTEN = 300
  const waits = {
    'page.waitForSelector': () => page.waitForSelector('#never-rendered', { timeout: WRITTEN }),
    'page.waitForFunction': () => page.waitForFunction(() => false, { timeout: WRITTEN }),
    'page.waitForResponse': () => page.waitForResponse(() => false, { timeout: WRITTEN }),
    waitUntil: () => waitUntil(() => false, { timeout: WRITTEN, what: 'nothing' }),
  }
  for (const [name, wait] of Object.entries(waits)) {
    const ms = await gaveUpAfter(wait)
    check(ms >= WRITTEN * 3 - 50, `${name} gave up after ${ms}ms on a ${WRITTEN}ms budget with the factor at 3, want about ${WRITTEN * 3}ms`)
    check(ms < WRITTEN * 3 + 2000, `${name} held on for ${ms}ms, far past the stretched budget of ${WRITTEN * 3}ms`)
  }

  h.ok('every wait is stretched by the slow-machine factor')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
