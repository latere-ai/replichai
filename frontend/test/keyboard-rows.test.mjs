// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for opening a list row from the keyboard.
//
// The bug it guards: five list rows — a project, a registry entry, a file, a
// paper, a session — were <div onClick> with no role, no tab stop and no key
// handler, so a keyboard could not reach them, let alone open them, while the
// transcript's event card had all three. Every clickable row now goes through
// one component, and this test walks the keyboard onto one and presses Enter.
//
// It also holds the guard that component needs: a key pressed on a control
// inside the row is that control's. Enter on Stop stops the session, and must
// not also open it. Run it with `bun run test:browser`.
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
      return { tag: a?.tagName, cls: a?.className ?? '', role: a?.getAttribute('role'), test: a?.dataset?.test ?? '' }
    })

  // Tab reaches a project row, and Enter opens it.
  await (async () => {
    await page.open('/')
    await page.waitForSelector('[data-test="project-rows"] .row')
    const row = await page.evaluate(() => {
      const r = document.querySelector('[data-test="project-rows"] .row')
      return { role: r.getAttribute('role'), tabIndex: r.tabIndex }
    })
    check(row.role === 'button' && row.tabIndex === 0, `a project row should be a button to the keyboard, got ${JSON.stringify(row)}`)

    let reached = false
    for (let i = 0; i < 40 && !reached; i++) {
      await page.keyboard.press('Tab')
      const a = await active()
      reached = a.tag === 'DIV' && /\brow\b/.test(a.cls)
    }
    if (!check(reached, 'forty Tabs never landed on a project row')) return
    await page.keyboard.press('Enter')
    await page.waitForFunction(() => location.pathname === '/p/proj-1', { timeout: 3000 }).catch(() => {})
    check((await page.evaluate(() => location.pathname)) === '/p/proj-1', 'Enter on a focused project row did not open the project')
  })()

  // Space opens it too, as it does any button.
  await (async () => {
    await page.open('/')
    await page.waitForSelector('[data-test="project-rows"] .row')
    await page.focus('[data-test="project-rows"] .row')
    await page.keyboard.press(' ')
    await page.waitForFunction(() => location.pathname === '/p/proj-1', { timeout: 3000 }).catch(() => {})
    check((await page.evaluate(() => location.pathname)) === '/p/proj-1', 'Space on a focused project row did not open the project')
  })()

  // A session row is reachable the same way, and a key on its Stop button is
  // the button's alone.
  await (async () => {
    await page.open('/p/proj-1')
    await page.waitForSelector('.rows .row button.btn')
    const row = await page.evaluate(() => {
      const r = document.querySelector('.rows .row')
      return { role: r.getAttribute('role'), tabIndex: r.tabIndex }
    })
    check(row.role === 'button' && row.tabIndex === 0, `a session row should be a button to the keyboard, got ${JSON.stringify(row)}`)
    await page.focus('.rows .row button.btn')
    await page.keyboard.press('Enter')
    await waitUntil(() => page.calls.includes('POST /jobs/job-1/cancel'), { what: 'Enter on Stop to send the cancel' })
    check((await page.evaluate(() => location.pathname)) === '/p/proj-1', 'Enter on the Stop button also opened the session')
  })()

  h.ok('a list row opens from the keyboard')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
