// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for the trash (019).
//
// What it guards: removing a project is two steps on purpose, and the whole
// point is that neither of them is a native browser dialog. The row action has
// to open the app's own modal, the modal has to say the thing that makes the
// step safe (it is reversible) and the thing that makes it consequential (a
// public project is withdrawn), confirming has to call DELETE and refresh the
// list, the trash screen has to be reachable from the rail and list what was
// removed, and Restore has to put it back.
//
// The permanent delete is here too, for the one assertion a unit test cannot
// make: its dialog spends its words on what does *not* go — the repositories
// close rather than being destroyed.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5200
const BASE = `http://localhost:${PORT}`

const asked = process.env.PUPPETEER_EXECUTABLE_PATH
const CHROME = asked
  ? asked
  : ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/usr/bin/google-chrome', '/usr/bin/chromium'].find((p) => require('node:fs').existsSync(p))

if (!CHROME || !require('node:fs').existsSync(CHROME)) {
  if (asked) {
    console.error(`FAIL: PUPPETEER_EXECUTABLE_PATH=${asked} does not exist`)
    process.exit(1)
  }
  console.log('skip: no Chrome found (set PUPPETEER_EXECUTABLE_PATH)')
  process.exit(0)
}

const ME = 'me-sub'
const ago = (ms) => new Date(Date.now() - ms).toISOString()

// The server's state, mutated by the calls the page makes — so the test drives
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
const calls = []

const fail = (msg) => {
  console.error(`FAIL: ${msg}`)
  process.exitCode = 1
}

const vite = spawn('npx', ['vite', '--port', String(PORT), '--strictPort'], { stdio: 'ignore' })
let browser
try {
  for (let i = 0; ; i++) {
    try {
      if ((await fetch(BASE)).ok) break
    } catch {
      /* not up yet */
    }
    if (i > 240) throw new Error('vite did not start')
    await new Promise((r) => setTimeout(r, 250))
  }

  browser = await puppeteer.launch({ executablePath: CHROME, headless: true })
  const page = await browser.newPage()
  await page.setViewport({ width: 1440, height: 900 })
  await page.setRequestInterception(true)
  page.on('request', (req) => {
    const u = new URL(req.url())
    if (u.origin !== BASE) return req.continue()
    const json = (body, status = 200) => req.respond({ status, contentType: 'application/json', body: JSON.stringify(body) })
    const path = u.pathname
    calls.push(`${req.method()} ${path}${u.search}`)

    if (path === '/me') return json({ authenticated: true, principal: ME, email: 'me@example.com', tier: 'user', credits: { wallet: 10_000_000, start_reserve: 5_000_000 } })
    if (path === '/me/profile') return json({})
    if (path === '/projects/trash') return json(trashed ? [{ ...project, deleted_at: ago(1000) }] : [])
    if (path === '/projects') return json(trashed ? [theirs] : [project, theirs])
    if (path === '/public') return json([])
    if (path === `/projects/${project.id}` && req.method() === 'GET') {
      // The project screen behind a trashed project's address: still its
      // owner's to read, and read-only.
      return json({
        project: trashed ? { ...project, deleted_at: ago(1000) } : project,
        sessions: [],
        may_submit: !trashed,
        credits: { balance: 0, available: 0, start_reserve: 5_000_000, may_reclaim: true },
      })
    }
    if (path === `/projects/${project.id}/papers`) return json([])
    if (path === `/projects/${project.id}/members`) return json([])
    if (path === `/projects/${project.id}` && req.method() === 'DELETE') {
      if (u.searchParams.get('purge') === '1') {
        trashed = false
        return req.respond({ status: 204, body: '' })
      }
      trashed = true
      return req.respond({ status: 204, body: '' })
    }
    if (path === `/projects/${project.id}/restore`) {
      trashed = false
      return req.respond({ status: 204, body: '' })
    }
    if (path.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    req.continue()
  })

  await page.goto(BASE, { waitUntil: 'networkidle0' })
  await page.waitForSelector('[data-test="project-rows"] .row')

  const rowCount = () => page.evaluate(() => document.querySelectorAll('[data-test="project-rows"] .row').length)
  const trashButtons = () => page.evaluate(() => document.querySelectorAll('[data-test="trash-project"]').length)

  if ((await rowCount()) !== 2) fail('the list did not start with both projects')
  // Removing a project you were invited to is not a thing that should be
  // possible, so the affordance is not there at all.
  if ((await trashButtons()) !== 1) fail(`${await trashButtons()} rows carry a trash button; only the one you own may`)

  // The row action opens the app's own dialog, not a native confirm.
  await page.click('[data-test="trash-project"]')
  await page.waitForSelector('.modal-backdrop [data-test="trash-confirm"]')
  const dialog = await page.evaluate(() => {
    const m = document.querySelector('.modal')
    return {
      inBody: document.body.contains(m) && m.closest('.modal-backdrop')?.parentElement === document.body,
      text: m.textContent,
    }
  })
  // The backdrop must portal to body: a promoted ancestor would confine it to
  // the content column and leave the rail live behind a "modal".
  if (!dialog.inBody) fail('the confirmation is not portalled to the body')
  if (!/restore it from the trash/i.test(dialog.text)) fail('the confirmation does not say the step is reversible')
  if (!/withdraws the publication/i.test(dialog.text)) fail('the confirmation does not warn that a public project is withdrawn')

  await page.click('[data-test="trash-confirm"]')
  await page.waitForFunction(() => document.querySelectorAll('[data-test="project-rows"] .row').length === 1)
  if (!calls.includes(`DELETE /projects/${project.id}`)) fail(`the confirmation did not call DELETE; calls: ${calls.join(', ')}`)

  // Trash and Settings are the rail's foot group — where work goes and personal
  // chrome live, apart from the destinations where work is done (024a). The
  // group is pinned to the bottom, Settings sits under Trash, and the pair is set
  // apart from the destinations above. A count of clickable rows would pass on
  // ones sitting directly under "Public registry", so this measures.
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
  await page.waitForSelector('.modal-backdrop [data-test="purge-confirm"]')
  const purgeText = await page.evaluate(() => document.querySelector('.modal').textContent)
  if (!/cannot be brought back/i.test(purgeText)) fail('the permanent-delete dialog does not say it is irreversible')
  if (!/closed, not destroyed/i.test(purgeText)) fail('the permanent-delete dialog does not say the repositories survive')
  // Escape, not a click on the backdrop: the backdrop's centre is the dialog.
  await page.keyboard.press('Escape')
  await page.waitForFunction(() => !document.querySelector('.modal-backdrop'))

  // A trashed project keeps its address (013), and the screen behind it has to
  // explain itself and offer nothing that can only fail: every owner control is
  // gone, and the way back is on the banner.
  await page.goto(`${BASE}/p/${project.id}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('[data-test="trashed-banner"]')
  const detail = await page.evaluate(() => ({
    banner: document.querySelector('[data-test="trashed-banner"]').textContent,
    // The rename pencil would 409 and reject unhandled; the People tab, the
    // publish panel and the credits card all route through the same refusal.
    rename: document.querySelectorAll('.detail-title-row button[aria-label], .detail-title-row .icon-btn').length,
    tabs: [...document.querySelectorAll('.detail-tabs button')].map((b) => b.textContent.trim()),
    credits: document.querySelectorAll('.stack .card .eyebrow').length,
  }))
  if (!/in the trash/i.test(detail.banner)) fail('the project screen does not say the project is in the trash')
  if (detail.rename) fail('the rename control is still on a trashed project, and every write there is refused')
  if (detail.tabs.includes('Collaborators')) fail('the People tab is still offered on a trashed project')

  // Restore puts it back where it was — from the banner, which is where a reader
  // who followed a link is standing.
  await page.click('[data-test="trashed-banner"] [data-test="restore"]')
  await page.waitForFunction(() => !document.querySelector('[data-test="trashed-banner"]'))
  if (!calls.includes(`POST /projects/${project.id}/restore`)) fail('Restore did not call the restore endpoint')

  if (!process.exitCode) console.log('ok: a project goes to the trash through the app’s own dialog, and comes back')
} finally {
  if (browser) await browser.close()
  vite.kill()
}
