// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for where the keyboard is while a dialog is up.
//
// The bug it guards: Modal set aria-modal and did nothing else about focus. The
// keyboard stayed on the button that opened the dialog — behind the backdrop —
// so Tab walked the page underneath, Enter re-fired the opener, and nothing
// brought focus back when the dialog closed. Every dialog in the app is one
// Modal, so the behaviour is asserted there once: focus lands inside on open,
// Tab and Shift+Tab wrap within it, and Escape returns it to the opener. Run it
// with `bun run test:browser`.
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
      const modal = document.querySelector('.modal')
      return { inside: !!modal && modal.contains(a), test: a?.dataset?.test ?? '', tag: a?.tagName ?? '' }
    })

  await page.open('/')
  await page.waitForSelector('[data-test="trash-project"]')
  await page.focus('[data-test="trash-project"]')
  await page.keyboard.press('Enter')
  await page.waitForSelector('.modal')

  // In: the keyboard is inside the dialog the moment it opens.
  const opened = await where()
  check(opened.inside, `focus should land inside the dialog when it opens, it is on ${JSON.stringify(opened)}`)

  // Around: Tab never leaves the dialog, and comes back round to the start.
  const controls = await page.evaluate(() => document.querySelectorAll('.modal a[href], .modal button:not([disabled]), .modal input:not([disabled])').length)
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
  await page.waitForSelector('.modal', { hidden: true })
  const closed = await where()
  check(closed.test === 'trash-project', `after the dialog closes focus should be back on its opener, it is on ${JSON.stringify(closed)}`)

  h.ok('a dialog takes the keyboard and gives it back')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
