// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for where the keyboard is while a dialog is up.
//
// It guards that the keyboard never stays behind the dim layer, where Tab would
// walk the page underneath and Enter re-fire the opener. Every dialog in the
// app is latere-ui's GlassModal, so the behavior is asserted on one: focus
// lands inside on open, Tab and Shift+Tab stay within it, and Escape returns it
// to the opener. Run it with `bun run test:browser`.
import { check, fail, start } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const project = { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', {}],
  ['/projects', [{ ...project, session_count: 0, running: 0 }]],
  [/^\/(public|shared)$/, []],
]

const h = await start()
try {
  const page = await h.page({ routes })
  const where = () =>
    page.evaluate(() => {
      const a = document.activeElement
      const modal = document.querySelector('[role="dialog"][aria-modal="true"]')
      return { inside: !!modal && modal.contains(a), test: a?.dataset?.test ?? '', tag: a?.tagName ?? '' }
    })

  await page.open('/')
  await page.waitForSelector('[data-test="trash-project"]')
  await page.focus('[data-test="trash-project"]')
  await page.keyboard.press('Enter')
  await page.waitForSelector('[role="dialog"][aria-modal="true"]')

  // In: the keyboard is inside the dialog the moment it opens.
  const opened = await where()
  check(opened.inside, `focus should land inside the dialog when it opens, it is on ${JSON.stringify(opened)}`)

  // Around: Tab never leaves the dialog, and comes back round to the start.
  const controls = await page.evaluate(() => document.querySelectorAll(':is([role="dialog"][aria-modal="true"]) :is(a[href], button:not([disabled]), input:not([disabled]))').length)
  let left = 0
  for (let i = 0; i < controls + 2; i++) {
    await page.keyboard.press('Tab')
    if (!(await where()).inside) left++
  }
  check(left === 0, `Tab left the dialog ${left} times in ${controls + 2} presses`)
  await page.keyboard.down('Shift')
  await page.keyboard.press('Tab')
  await page.keyboard.up('Shift')
  check((await where()).inside, 'Shift+Tab left the dialog')

  // Out: closing the dialog returns the keyboard to what opened it.
  await page.keyboard.press('Escape')
  await page.waitForSelector('[role="dialog"][aria-modal="true"]', { hidden: true })
  const closed = await where()
  check(closed.test === 'trash-project', `after the dialog closes focus should be back on its opener, it is on ${JSON.stringify(closed)}`)

  // A dialog that asks for one value puts the keyboard in its field, so typing
  // can start at once, and still gives it back to the button that opened it.
  const admin = await h.page({
    routes: [
      ['/me', { authenticated: true, principal: 'admin', email: 'admin@example.com', tier: 'admin', credits: { wallet: 20_000_000, start_reserve: 5_000_000 } }],
      ['/me/profile', {}],
      [/^\/(projects|public|shared)$/, []],
      ['GET /admin/access', [{ email: 'admin@example.com', tier: 'admin', bound: true, wallet: 20_000_000 }]],
      ['GET /admin/settings', { start_reserve: 5_000_000, overdraft_cap: 20_000_000, rates: { models: [] } }],
      ['/admin/solvency', { outstanding_micro: 0 }],
    ],
  })
  await admin.open('/console')
  await admin.waitForSelector('.member-row .btn')
  await admin.evaluate(() => {
    const add = [...document.querySelectorAll('.member-row .btn')].find((b) => /add credits/i.test(b.textContent))
    add.dataset.test = 'add-credits'
    add.focus()
  })
  await admin.keyboard.press('Enter')
  await admin.waitForSelector('[role="dialog"][aria-modal="true"] input')
  // The field is focused from an effect after the dialog opens, so the check
  // waits for it rather than reading the first frame.
  await admin.waitForFunction(() => document.activeElement?.closest('[role="dialog"]') && document.activeElement.tagName === 'INPUT', { timeout: 3000 }).catch(() => {})
  const field = await admin.evaluate(() => ({ tag: document.activeElement?.tagName ?? '', inside: !!document.activeElement?.closest('[role="dialog"]') }))
  check(field.inside && field.tag === 'INPUT', `the amount field should have the keyboard when the dialog opens, it is on ${JSON.stringify(field)}`)
  await admin.keyboard.press('Escape')
  await admin.waitForSelector('[role="dialog"][aria-modal="true"]', { hidden: true })
  const back = await admin.evaluate(() => document.activeElement?.dataset?.test ?? document.activeElement?.tagName ?? '')
  check(back === 'add-credits', `after the amount dialog closes focus should be back on Add credits, it is on ${back}`)

  h.ok('a dialog takes the keyboard and gives it back')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
