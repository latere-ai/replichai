// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser test for the Latere environments in the Admin console's platform
// policy (051).
//
// Which environments a Latere session may name used to be an environment
// variable of the deployment, so changing it was a redeploy. It is now a
// setting an admin changes in the console. What needs a browser is the round
// trip: the console shows the environments the runtime knows with the ones in
// force checked and base always on; a save that changes something else does
// not send the list, so a console that predates an environment never narrows
// it; and turning one on sends the whole list. Run it with
// `bun run test:browser`.
import { fail, settled, start } from './harness.mjs'

const EMAIL = 'admin@example.com'
const M = 1_000_000
const card = { models: [{ model: 'claude-opus-5-5', input: 4 * M, output: 20 * M, cache_read: 200_000, cache_write_5m: 5 * M, cache_write_1h: 8 * M }] }
const saves = []

const routes = [
  ['/me', { authenticated: true, principal: 'admin', email: EMAIL, tier: 'admin', credits: { wallet: 0, start_reserve: 5 * M } }],
  ['GET /admin/access', [{ email: EMAIL, tier: 'admin', bound: true, wallet: 0 }]],
  [
    'GET /admin/settings',
    {
      start_reserve: 5 * M,
      overdraft_cap: 20 * M,
      rates: card,
      latere_environments: ['base', 'science'],
      known_latere_environments: ['base', 'gui', 'science'],
    },
  ],
  [
    '/admin/settings',
    ({ body }) => {
      saves.push(body)
      return { ...body, known_latere_environments: ['base', 'gui', 'science'], updated_by: EMAIL }
    },
  ],
]

const h = await start()
try {
  const page = await h.page({ routes })
  await page.open('/console')
  await page.waitForSelector('[data-test="latere-environments"]', { timeout: 15000 })
  await settled(page)

  const checks = () =>
    page.$$eval('[data-test="latere-environments"] [role="checkbox"]', (boxes) =>
      boxes.map((b) => ({ label: b.textContent.trim(), on: b.getAttribute('aria-checked') === 'true', disabled: b.disabled })),
    )
  const first = await checks()
  if (first.length !== 3) fail(`the console offers ${first.length} environments, want the runtime's three`)
  const base = first.find((c) => c.label.startsWith('Base'))
  const science = first.find((c) => c.label.startsWith('Science'))
  const gui = first.find((c) => c.label.startsWith('GUI'))
  if (!base?.on || !base?.disabled) fail(`base reads ${JSON.stringify(base)}; it is always on and cannot be turned off`)
  if (!science?.on) fail('science is in force and shows unchecked')
  if (gui?.on) fail('gui is not in force and shows checked')

  /* ── A save of something else leaves the list alone ── */

  const reserve = await page.$('#start-reserve')
  await reserve.focus()
  await reserve.evaluate((el) => el.select())
  await page.keyboard.press('Backspace')
  await reserve.type('6')
  await page.click('[data-test="save-policy"]')
  await page.waitForFunction(() => document.querySelector('[data-test="save-policy"]')?.disabled === true, { timeout: 5000 }).catch(() => {})
  if (saves.length !== 1) fail(`changing the reserve sent ${saves.length} saves, want 1`)
  else if ('latere_environments' in saves[0]) fail(`a save of the reserve sent the environments: ${JSON.stringify(saves[0].latere_environments)}`)

  /* ── Turning one on sends the whole list ── */

  await page.evaluate(() => [...document.querySelectorAll('[data-test="latere-environments"] [role="checkbox"]')].find((b) => b.textContent.trim().startsWith('GUI'))?.click())
  if (!(await page.$eval('[data-test="save-policy"]', (b) => !b.disabled))) fail('turning on gui does not enable Save')
  await page.click('[data-test="save-policy"]')
  await page.waitForFunction(() => document.querySelector('[data-test="save-policy"]')?.disabled === true, { timeout: 5000 }).catch(() => {})
  const sent = saves[1]?.latere_environments ?? []
  if ([...sent].sort().join() !== 'base,gui,science') fail(`turning on gui sent ${JSON.stringify(sent)}, want base, gui and science`)
  await page.close()

  h.ok('an admin turns a Latere environment on, and only a change to the list sends it')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
