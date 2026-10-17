// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for list rows that open a place: a project, a
// registry entry, a session.
//
// Each such place has an address, so its row is a link to it: the keyboard
// reaches it with Tab and opens it with Enter, a middle-click or the context
// menu finds the link anywhere on the row, and the app opens the place in the
// same tab without a page load. A key pressed on a control inside the row is
// that control's: Enter on Stop stops the session, and must not also open it.
// A figure in the row that carries a tooltip still opens the row when clicked.
// Run it with `bun run test:browser`.
import { check, fail, start, waitUntil } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const project = { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }
const running = { id: 'job-1', kind: 'replicate', ref: 'arxiv:2301.03711', phase: 'running', created_at: at(0), phase_at: { submitted: at(0), running: at(1000) } }

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', {}],
  ['/projects', [{ ...project, session_count: 1, running: 1, latest_phase: 'running' }]],
  [/^\/(public|shared)$/, []],
  ['/projects/proj-1', { project, sessions: [running], may_submit: true, role: 'owner' }],
  ['/projects/proj-1/papers', []],
  ['/projects/proj-1/members', []],
  ['POST /jobs/job-1/cancel', running],
  ['/jobs/job-1', running],
]

const h = await start()
try {
  const page = await h.page({ routes })
  const active = () =>
    page.evaluate(() => {
      const a = document.activeElement
      return { tag: a?.tagName, href: a?.getAttribute('href') ?? '', row: !!a?.closest('[data-test="project-rows"] .row') }
    })
  // A mark on the window: a page load clears it, an in-app navigation keeps it.
  const mark = () => page.evaluate(() => (window.__sameTab = true))
  const stayed = () => page.evaluate(() => window.__sameTab === true)

  // A project row is a link to the project, Tab reaches it, and Enter opens it
  // in the same tab without a page load.
  await (async () => {
    await page.open('/')
    await page.waitForSelector('[data-test="project-rows"] .row')
    const href = await page.evaluate(() => document.querySelector('[data-test="project-rows"] .row a.row-link')?.getAttribute('href') ?? '')
    check(href === '/p/proj-1', `a project row should link to /p/proj-1, it links to ${JSON.stringify(href)}`)

    let reached = false
    for (let i = 0; i < 40 && !reached; i++) {
      await page.keyboard.press('Tab')
      const a = await active()
      reached = a.tag === 'A' && a.row && a.href === '/p/proj-1'
    }
    if (!check(reached, "forty Tabs never landed on a project row's link")) return
    await mark()
    await page.keyboard.press('Enter')
    await page.waitForFunction(() => location.pathname === '/p/proj-1', { timeout: 3000 }).catch(() => {})
    check((await page.evaluate(() => location.pathname)) === '/p/proj-1', 'Enter on a focused project row did not open the project')
    check(await stayed(), 'opening a project row reloaded the page')
  })()

  // The link covers the row: a middle-click or the context menu anywhere on it
  // lands on the project's link, which the browser opens in a new tab, and the
  // app leaves that click alone.
  await (async () => {
    await page.open('/')
    await page.waitForSelector('[data-test="project-rows"] .row')
    const spot = await page.evaluate(() => {
      const row = document.querySelector('[data-test="project-rows"] .row')
      const r = row.getBoundingClientRect()
      // Right of the title, over the row's columns rather than its text.
      const x = r.left + r.width * 0.6
      const y = r.top + r.height / 2
      return { x, y, href: document.elementFromPoint(x, y)?.closest('a')?.getAttribute('href') ?? '' }
    })
    check(spot.href === '/p/proj-1', `the middle of a project row is not its link; a pointer there finds ${JSON.stringify(spot.href)}`)
    await page.evaluate(() => {
      window.__aux = null
      window.addEventListener('auxclick', (e) => (window.__aux = { href: e.target.closest?.('a')?.getAttribute('href') ?? '', prevented: e.defaultPrevented }), { once: true })
    })
    await page.mouse.click(spot.x, spot.y, { button: 'middle' })
    const aux = await page.evaluate(() => window.__aux)
    check(aux?.href === '/p/proj-1' && !aux.prevented, `a middle-click on the row should reach its link untouched, got ${JSON.stringify(aux)}`)
    check((await page.evaluate(() => location.pathname)) === '/', 'a middle-click on the row navigated this tab')
  })()

  // A session row links to its session, a key on its Stop button is the
  // button's alone, and its running time, which carries a tooltip, still opens
  // the session.
  await (async () => {
    await page.open('/p/proj-1')
    await page.waitForSelector('.rows .row button.btn')
    const href = await page.evaluate(() => document.querySelector('.rows .row a.row-link')?.getAttribute('href') ?? '')
    check(href === '/p/proj-1/s/job-1', `a session row should link to /p/proj-1/s/job-1, it links to ${JSON.stringify(href)}`)
    await page.focus('.rows .row button.btn')
    await page.keyboard.press('Enter')
    await waitUntil(() => page.calls.includes('POST /jobs/job-1/cancel'), { what: 'Enter on Stop to send the cancel' })
    check((await page.evaluate(() => location.pathname)) === '/p/proj-1', 'Enter on the Stop button also opened the session')

    await page.open('/p/proj-1')
    await page.waitForSelector('.rows .row .tip')
    await mark()
    await page.click('.rows .row .tip')
    await page.waitForFunction(() => location.pathname === '/p/proj-1/s/job-1', { timeout: 3000 }).catch(() => {})
    check((await page.evaluate(() => location.pathname)) === '/p/proj-1/s/job-1', "a click on a session row's running time did not open the session")
    check(await stayed(), 'opening a session row reloaded the page')
  })()

  h.ok('a list row is a link to its place, opened from the keyboard, a click anywhere on it, or a new tab')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
