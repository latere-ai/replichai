// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for the trash.
//
// What it guards: removing a project is two steps on purpose, and neither of
// them is a native browser dialog. The row action has to open the app's own
// modal; the modal has to say what makes the step safe, that it is reversible,
// and what makes it consequential, that a public project is withdrawn;
// confirming has to call DELETE and refresh the list; the trash screen has to be
// reachable from the rail and list what was removed; and Restore has to put it
// back.
//
// The permanent delete is here too, for the one assertion a unit test cannot
// make: its dialog states what does not go, namely that the repositories close
// rather than being destroyed.
import { fail, noContent, start } from './harness.mjs'

const ME = 'me-sub'
const ago = (ms) => new Date(Date.now() - ms).toISOString()

// The server's state, mutated by the calls the page makes, so the test drives
// the real trash → restore round trip rather than two static fixtures.
const project = {
  id: 'mine-1',
  owner: ME,
  title: 'Point of no Undo',
  visibility: 'public',
  created_at: ago(120 * 86_400_000),
  modified_at: ago(2 * 86_400_000),
  session_count: 3,
  running: 0,
  latest_phase: 'done',
}
// A project somebody else owns: it must carry no trash button at all.
const theirs = { ...project, id: 'shared-1', owner: 'ada-sub', owner_name: 'Ada Lovelace', title: 'The Placebo Effect', visibility: 'private' }
let trashed = false
const removed = () => ({ ...project, deleted_at: ago(1000) })

const routes = [
  ['/me', { authenticated: true, principal: ME, email: 'me@example.com', tier: 'user', credits: { wallet: 10_000_000, start_reserve: 5_000_000 } }],
  ['/me/profile', {}],
  ['/projects/trash', () => (trashed ? [removed()] : [])],
  ['/projects', () => (trashed ? [theirs] : [project, theirs])],
  ['/public', []],
  // The project screen behind a trashed project's address: still its owner's to
  // read, and read-only.
  [
    `GET /projects/${project.id}`,
    () => ({
      project: trashed ? removed() : project,
      sessions: [],
      may_submit: !trashed,
      credits: { balance: 0, available: 0, start_reserve: 5_000_000, may_reclaim: true },
    }),
  ],
  [`/projects/${project.id}/papers`, []],
  [`/projects/${project.id}/members`, []],
  [
    `DELETE /projects/${project.id}`,
    ({ query }) => {
      trashed = query.get('purge') !== '1'
      return noContent()
    },
  ],
  [
    `/projects/${project.id}/restore`,
    () => {
      trashed = false
      return noContent()
    },
  ],
]

const h = await start()
try {
  const page = await h.page({ routes })
  await page.open('/')
  await page.waitForSelector('[data-test="project-rows"] .row')

  const rowCount = () => page.evaluate(() => document.querySelectorAll('[data-test="project-rows"] .row').length)
  const trashButtons = () => page.evaluate(() => document.querySelectorAll('[data-test="trash-project"]').length)

  if ((await rowCount()) !== 2) fail('the list did not start with both projects')
  // Removing a project you were invited to is not a thing that should be
  // possible, so the affordance is not there at all.
  if ((await trashButtons()) !== 1) fail(`${await trashButtons()} rows carry a trash button; only the one you own may`)

  // The row action opens the app's own dialog, not a native confirm.
  await page.click('[data-test="trash-project"]')
  await page.waitForSelector('[role="dialog"][aria-modal="true"] [data-test="trash-confirm"]')
  const dialog = await page.evaluate(() => {
    const m = document.querySelector('[role="dialog"][aria-modal="true"]')
    // What a pointer on the rail would land on while the dialog is up.
    const onRail = document.elementFromPoint(8, innerHeight / 2)
    return { railLive: !!onRail?.closest('.rail'), text: m.textContent }
  })
  // The dim layer covers the whole window: a promoted ancestor would confine it
  // to the content column and leave the rail live behind a "modal".
  if (dialog.railLive) fail('the rail can still be clicked behind the confirmation')
  if (!/restore it from the trash/i.test(dialog.text)) fail('the confirmation does not say the step is reversible')
  if (!/withdraws the publication/i.test(dialog.text)) fail('the confirmation does not warn that a public project is withdrawn')

  await page.click('[data-test="trash-confirm"]')
  await page.waitForFunction(() => document.querySelectorAll('[data-test="project-rows"] .row').length === 1)
  if (!page.calls.includes(`DELETE /projects/${project.id}`)) fail(`the confirmation did not call DELETE; calls: ${page.calls.join(', ')}`)

  // Trash and Settings are the rail's foot group, which holds where work goes
  // and personal chrome, apart from the destinations where work is done.
  // The group is pinned to the bottom, Settings sits under Trash, and the pair
  // is set apart from the destinations above. A count of clickable rows would
  // pass on rows sitting directly under "Public registry", so this measures
  // positions.
  const railTrash = await page.evaluate(() => {
    const btns = [...document.querySelectorAll('.rail .nav-btn')]
    const trash = btns.find((b) => b.textContent.trim() === 'Trash')
    const settings = btns.find((b) => b.textContent.trim() === 'Settings')
    if (!trash || !settings) return null
    const nav = document.querySelector('.rail-nav')
    const above = btns[btns.indexOf(trash) - 1] // the last workspace/platform destination
    const at = {
      // Settings is the foot's bottom-most row, pinned to the rail's foot.
      footGapBelow: nav.getBoundingClientRect().bottom - settings.getBoundingClientRect().bottom,
      // Trash sits directly above Settings, both in the foot group.
      trashToSettings: settings.getBoundingClientRect().top - trash.getBoundingClientRect().bottom,
      // The foot group is set apart from the destinations above it.
      gapAbove: trash.getBoundingClientRect().top - above.getBoundingClientRect().bottom,
    }
    trash.click()
    return at
  })
  if (!railTrash) fail('the rail is missing its Trash or Settings destination')
  else if (railTrash.footGapBelow > 30) fail(`the foot group sits ${Math.round(railTrash.footGapBelow)}px above the rail's foot, want it at the bottom`)
  else if (railTrash.trashToSettings > 20) fail(`Trash and Settings are ${Math.round(railTrash.trashToSettings)}px apart, want them adjacent in the foot group`)
  else if (railTrash.gapAbove < 20) fail(`the foot group is ${Math.round(railTrash.gapAbove)}px below the destination above it, want it set apart`)
  await page.waitForSelector('[data-test="trash-rows"] .row')
  if (new URL(page.url()).pathname !== '/trash') fail(`the trash lives at ${new URL(page.url()).pathname}, want /trash`)
  const inTrash = await page.evaluate(() => {
    const row = document.querySelector('[data-test="trash-rows"] .row')
    return { title: row.querySelector('.row-title').textContent.trim(), deleted: row.querySelector('[data-test="modified"]')?.textContent.trim() || '' }
  })
  if (inTrash.title !== project.title) fail(`the trash shows "${inTrash.title}", want the removed project`)
  if (!inTrash.deleted) fail('the trash row shows no deleted time')

  // The permanent delete says what it does not remove.
  await page.click('[data-test="purge"]')
  await page.waitForSelector('[role="dialog"][aria-modal="true"] [data-test="purge-confirm"]')
  const purgeText = await page.evaluate(() => document.querySelector('[role="dialog"][aria-modal="true"]').textContent)
  if (!/cannot be brought back/i.test(purgeText)) fail('the permanent-delete dialog does not say it is irreversible')
  if (!/made private, not deleted/i.test(purgeText)) fail('the permanent-delete dialog does not say the repositories survive')
  // Escape, not a click on the dim layer: the layer's center is the dialog.
  await page.keyboard.press('Escape')
  await page.waitForFunction(() => !document.querySelector('[role="dialog"][aria-modal="true"]'))

  // A trashed project keeps its address, and the screen behind it has to
  // explain itself and offer nothing that can only fail: every owner control is
  // gone, and the way back is on the banner.
  await page.open(`/p/${project.id}`)
  await page.waitForSelector('[data-test="trashed-banner"]')
  const detail = await page.evaluate(() => ({
    banner: document.querySelector('[data-test="trashed-banner"]').textContent,
    // The rename pencil would 409 and reject unhandled; the People tab, the
    // publish button and the credits card all route through the same refusal.
    rename: document.querySelectorAll('.detail-title-row button[aria-label], .detail-title-row .icon-btn').length,
    tabs: [...document.querySelectorAll('.detail-tabs button')].map((b) => b.textContent.trim()),
    credits: document.querySelectorAll('.stack .card .eyebrow').length,
  }))
  if (!/in the trash/i.test(detail.banner)) fail('the project screen does not say the project is in the trash')
  if (detail.rename) fail('the rename control is still on a trashed project, and every write there is refused')
  if (detail.tabs.includes('Collaborators')) fail('the People tab is still offered on a trashed project')

  // Restore puts it back where it was, from the banner, which is where a reader
  // who followed a link lands.
  await page.click('[data-test="trashed-banner"] [data-test="restore"]')
  await page.waitForFunction(() => !document.querySelector('[data-test="trashed-banner"]'))
  if (!page.calls.includes(`POST /projects/${project.id}/restore`)) fail('Restore did not call the restore endpoint')

  h.ok('a project goes to the trash through the app’s own dialog, and comes back')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
