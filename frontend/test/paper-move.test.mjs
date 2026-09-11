// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for moving a paper to another project (035).
//
// The thing it guards is that the move is offered *when it is needed*. A paper
// filed under the wrong project is almost always noticed after a replication
// has run against it — which is exactly the moment `removePaper` stops being
// allowed (012: the record of what was reproduced is not editable). So the
// earlier product had no correction available at the only time anybody wanted
// one, and a Move that hid itself behind "no sessions" would reproduce that.
//
// Three things have to hold, and the third is the one that matters:
//
//   1. A paper that has already been run still offers Move, while Remove is
//      gone. Those two conditions are opposites, and getting them the same way
//      round is the bug.
//   2. Picking a destination is the app's own control, not an OS-drawn <select>,
//      and the list excludes this project, published projects, and the trash —
//      the three things the server refuses.
//   3. Confirming calls POST /projects/{id}/papers/move with the right body, and
//      the screen re-reads: the row leaves the source *in place*, with no
//      reload to hide a list that never refetched.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { fail, json, noContent, start } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const ME = 'p'
const SOURCE = 'proj-source'
const TARGET = 'proj-target'
const PUBLISHED = 'proj-published'
const TRASHED = 'proj-trashed'
const KEY = 'doi:10.1145/3544548.3581433'

// The server's state, mutated by the move the page makes — so this drives the
// round trip rather than two static fixtures.
let holder = SOURCE
let moveBody = null

const projects = () => [
  { id: SOURCE, owner: ME, title: 'Filed by mistake', visibility: 'private', created_at: at(0) },
  { id: TARGET, owner: ME, title: 'Where it belongs', visibility: 'private', created_at: at(1) },
  { id: PUBLISHED, owner: ME, title: 'Already published', visibility: 'public', created_at: at(2) },
  { id: TRASHED, owner: ME, title: 'In the trash', visibility: 'private', created_at: at(3), deleted_at: at(4) },
]
const project = (id) => projects().find((p) => p.id === id)

const paper = { key: KEY, ref: '10.1145/3544548.3581433', title: 'Sensing Posture-Aware Pen+Touch Interaction', added_by: ME, added_at: at(0), sessions: 1 }
const session = {
  id: 'job-1',
  project_id: SOURCE,
  paper_key: KEY,
  ref: paper.ref,
  phase: 'done',
  repo_name: 'open-hai/pen-touch',
  created_at: at(0),
  updated_at: at(6e6),
  cost: 3842,
}
// The paper, and the run of it, are wherever the move last put them.
const papersOf = (id) => (holder === id ? [paper] : [])
const sessionsOf = (id) => (holder === id ? [{ ...session, project_id: id }] : [])

const routes = [
  // The route under test.
  [
    'POST',
    /^\/projects\/[^/]+\/papers\/move$/,
    ({ body }) => {
      moveBody = body ?? {}
      holder = moveBody.to_project
      return noContent()
    },
  ],
  ['/me', { authenticated: true, principal: ME, email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', { display_name: 'Ada Lovelace', contact_email: 'ada@example.org', handle: 'ada', public: true }],
  ['/projects', () => projects().map((p) => ({ ...p, session_count: sessionsOf(p.id).length, running: 0, latest_phase: 'done', published_sessions: 0 }))],
  [/^\/projects\/([^/]+)$/, ({ params: [id] }) => ({ project: project(id), sessions: sessionsOf(id), papers: papersOf(id), may_submit: true, role: 'owner' })],
  [/^\/projects\/([^/]+)\/papers$/, ({ params: [id] }) => papersOf(id)],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/projects\/[^/]+\/credits$/, { wallet: 20, start_reserve: 5, available: 20 }],
  [/^\/jobs\/[^/]+$/, session],
  [/^\/jobs\/[^/]+\/transcript$/, { events: [], usage: null, live: false }],
  [/^\/jobs\/[^/]+\/(reproduction|verdict)$/, json({ error: 'not_found' }, 404)],
  [/\/artifacts$/, { files: [] }],
  ['/models', { tiers: [] }],
  [/^\/(public|shared)$/, []],
]

const h = await start()
try {
  const page = await h.page({ viewport: { width: 1440, height: 1000 }, routes })

  /* ── 1. Offered on a paper that has already been run ── */

  await page.open(`/p/${SOURCE}`)
  await page.waitForSelector('[data-test="move-paper"]', { timeout: 15000 })

  // Remove is gone, because the paper has a session. Move is not — and those
  // being opposites is the whole point of this test.
  // By its own marker, not by shape: the sessions nested under a paper are
  // `.row`s too and carry their own danger button (032), which is a different
  // control on a different record.
  if (await page.$('[data-test="remove-paper"]')) fail('a paper with a session still offers Remove')

  /* ── 2. The destination list, in the app's own control ── */

  await page.click('[data-test="move-paper"]')
  await page.waitForSelector('[data-test="move-paper-confirm"]', { timeout: 5000 })
  if (await page.$('.modal select')) fail('the destination picker is an OS-drawn <select>')

  // Waited for, not slept on: the destinations arrive from /projects, and until
  // they have the dialog shows a loading line with no control in it. A machine
  // running the whole browser suite back to back is slower than one running
  // this file alone — which is how this clicked on nothing and read an empty
  // list.
  await page.waitForSelector('.modal [role="combobox"], .modal .select-btn', { timeout: 10000 })
  await page.evaluate(() => document.querySelector('.modal [role="combobox"], .modal .select-btn')?.click())
  await page.waitForSelector('[role="option"]', { timeout: 10000 })
  const options = await page.$$eval('[role="option"]', (els) => els.map((e) => e.textContent.replace(/\s+/g, ' ').trim()))
  if (!options.includes('Where it belongs')) fail(`the destination list is missing the other private project: ${JSON.stringify(options)}`)
  if (options.includes('Filed by mistake')) fail('the destination list offers the project the paper is already in')
  if (options.includes('Already published')) fail('the destination list offers a published project, which the server refuses')
  if (options.includes('In the trash')) fail('the destination list offers a trashed project, which the server refuses')

  await page.evaluate(() => [...document.querySelectorAll('[role="option"]')].find((o) => /Where it belongs/.test(o.textContent))?.click())
  // The pick closes the list and puts the choice on the control.
  await page.waitForFunction(() => !document.querySelector('[role="option"]') && /Where it belongs/.test(document.querySelector('.modal [role="combobox"]')?.textContent ?? ''), { timeout: 5000 })

  /* ── 3. Through to the moved paper, without a reload ── */

  await page.click('[data-test="move-paper-confirm"]')
  await page.waitForFunction(() => !document.querySelector('.modal'), { timeout: 8000 })

  if (!moveBody) fail(`no move reached the server: ${JSON.stringify(page.calls.slice(-8))}`)
  else {
    if (moveBody.paper_key !== KEY) fail(`the move named paper_key ${moveBody.paper_key}, want ${KEY}`)
    if (moveBody.to_project !== TARGET) fail(`the move named to_project ${moveBody.to_project}, want ${TARGET}`)
  }

  // Asserted in place. A page.goto here would pass whether or not anything
  // refetched, which is the failure this shape exists to catch.
  await page.waitForFunction(() => !document.querySelector('[data-test="move-paper"]'), { timeout: 8000 })
  const emptied = await page.$$eval('.paper-group', (g) => g.length)
  if (emptied !== 0) fail(`the source still shows ${emptied} papers after the move`)

  // And it is really in the destination, with the run that was a run of it.
  await page.open(`/p/${TARGET}`)
  await page.waitForSelector('[data-test="move-paper"]', { timeout: 15000 })
  const arrived = await page.$eval('.paper-group', (g) => g.textContent.replace(/\s+/g, ' ').trim())
  if (!/Posture-Aware/.test(arrived)) fail(`the paper did not arrive in the destination: ${arrived}`)
  if (!/1 session/.test(arrived)) fail(`the paper arrived without its replication: ${arrived}`)

  h.ok('a paper that has been run can be moved, and its replication goes with it')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
