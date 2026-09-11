// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for how often the list of projects is read, and by
// whom.
//
// The bug it guards: three places read /projects for themselves: the rail for
// its count, on every change of screen; the projects screen for its rows; the
// move-paper dialog for its targets. Opening the dashboard cost two reads, every
// screen change one more, and the rail's count went stale the moment a project
// was thrown away from the list beside it, because nothing told the rail. The
// list is now one shared store: read once, handed to every subscriber, re-read
// when something changes it. Run it with `bun run test:browser`.
import { check, fail, noContent, settled, start, waitUntil } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const mk = (id, title) => ({ id, owner: 'p', title, visibility: 'private', created_at: at(0), session_count: 0, running: 0 })
let list = [mk('proj-a', 'Walk This Beam'), mk('proj-b', 'Retention Under Load')]

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', {}],
  [/^\/(public|shared)$/, []],
  ['/projects/trash', []],
  [
    'DELETE',
    /^\/projects\/([^/]+)$/,
    ({ params: [id] }) => {
      list = list.filter((p) => p.id !== id)
      return noContent()
    },
  ],
  ['/projects', () => list],
]

const h = await start()
try {
  const page = await h.page({ routes })
  const reads = () => page.calls.filter((c) => c === 'GET /projects').length
  const railCount = () => page.evaluate(() => document.querySelector('.nav-btn .count')?.textContent.trim() ?? null)

  // The dashboard: the rail and the list want the same list, and read it once.
  await page.open('/')
  await page.waitForSelector('[data-test="project-rows"] .row')
  await settled(page)
  check(reads() === 1, `opening the dashboard read /projects ${reads()} times; the rail and the list should share one read`)
  check((await railCount()) === '2', `the rail should count 2 projects, it shows ${JSON.stringify(await railCount())}`)

  // A change of screen is not a change of the list: nothing re-reads it.
  await page.evaluate(() => {
    history.pushState(null, '', '/settings')
    dispatchEvent(new PopStateEvent('popstate'))
  })
  await page.waitForFunction(() => location.pathname === '/settings' && !document.querySelector('[data-test="project-rows"]'))
  await settled(page)
  check(reads() === 1, `a change of screen re-read /projects (${reads()} reads)`)

  // Throwing a project away changes the list, so it is re-read once, and the
  // rail, which nothing used to notify, follows.
  await page.evaluate(() => {
    history.pushState(null, '', '/')
    dispatchEvent(new PopStateEvent('popstate'))
  })
  await page.waitForSelector('[data-test="project-rows"] .row')
  const before = reads()
  await page.click('[data-test="trash-project"]')
  await page.waitForSelector('.modal-backdrop [data-test="trash-confirm"]')
  await page.click('[data-test="trash-confirm"]')
  await waitUntil(() => page.calls.some((c) => c.startsWith('DELETE /projects/')), { what: 'the trash to be sent' })
  await waitUntil(() => reads() > before, { what: 'the list to be re-read after the trash' })
  await page.waitForFunction(() => document.querySelectorAll('[data-test="project-rows"] .row').length === 1)
  await settled(page)
  check(reads() === before + 1, `a trash should re-read /projects once, saw ${reads() - before} reads`)
  check((await railCount()) === '1', `after a trash the rail should count 1 project, it shows ${JSON.stringify(await railCount())}`)

  h.ok('the list of projects is read once and shared')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
