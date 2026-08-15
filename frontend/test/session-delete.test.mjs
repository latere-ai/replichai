// A browser regression test for removing a session (032).
//
// What it guards is the dead end. A run that began publishing and never finished
// holds a repository with no reproduction record, and the publish gate blocks on
// exactly that — so one cancelled or crashed session made its project
// unpublishable forever, and nothing anywhere in the product could clear it. The
// refusal named the session and offered one thing to do with it: open it, and
// land on a transcript that does not mention publishing at all.
//
// So two surfaces have to work, and the second is the one that matters:
//
//   1. A finished session on the project screen carries a delete, behind the
//      app's own dialog, which says what survives it — the cost, and the
//      repository, which is closed rather than destroyed.
//   2. The blocker row inside the publish wizard carries the same delete, and
//      using it clears the way: the very next sign-off makes the project public.
//
// The second is asserted through to the *end state*, not to the row
// disappearing. A test that stops at the disappearing row passes on a stale list.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5252
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

const fail = (msg) => {
  console.error(`FAIL: ${msg}`)
  process.exitCode = 1
}

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const ME = 'p'
const PROJECT = 'proj-1'
const GOOD = 'job-good' // finished, cleared, publishable
const STUCK = 'job-stuck' // a repository, and no manifest it will ever gain

// The server's state, mutated by the calls the page makes — so this drives the
// real delete → publish round trip rather than two static fixtures.
const deleted = new Set()
let visibility = 'private'
const calls = []

const project = () => ({ id: PROJECT, owner: ME, title: 'Posture-Aware Pen+Touch', visibility, created_at: at(0) })
const paper = { key: 'doi:10.1145/3544548.3581433', ref: '10.1145/3544548.3581433', title: 'Sensing Posture-Aware Pen+Touch Interaction', sessions: 2 }
const sessionOf = (id) => ({
  id,
  project_id: PROJECT,
  paper_key: paper.key,
  ref: paper.ref,
  phase: id === STUCK ? 'failed' : 'done',
  error: id === STUCK ? 'interrupted by a restart in publishing; no live run to rejoin' : '',
  repo_name: `open-hai/${id}`,
  created_at: at(0),
  updated_at: at(6e6),
  cost: 3842,
})
const live = () => [GOOD, STUCK].filter((id) => !deleted.has(id)).map(sessionOf)

// What the server refuses with, recomputed from what is left — exactly what
// internal/httpapi does: a session with a repository and no manifest blocks, and
// a deleted one is not read at all.
const refusal = () => ({
  error: 'sessions_unfinished',
  blockers: live()
    .filter((s) => s.id === STUCK)
    .map((s) => ({ session_id: s.id, repo: s.repo_name, ref: s.ref, reason: 'unfinished' })),
})

const profile = { display_name: 'Ada Lovelace', contact_email: 'ada@example.org', handle: 'ada', public: true }

const json = (req, body, status = 200) => req.respond({ status, contentType: 'application/json', body: JSON.stringify(body) })

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
  await page.setViewport({ width: 1440, height: 1000 })
  await page.setRequestInterception(true)
  page.on('request', (req) => {
    const u = new URL(req.url())
    if (u.origin !== BASE) return req.continue()
    const path = u.pathname
    const method = req.method()
    calls.push(`${method} ${path}`)

    // The route under test.
    const one = path.match(/^\/jobs\/([^/]+)$/)
    if (one && method === 'DELETE') {
      if (deleted.has(one[1])) return json(req, { error: 'not_found' }, 404)
      deleted.add(one[1])
      return req.respond({ status: 204, body: '' })
    }
    if (path.endsWith('/public') && method === 'POST') {
      const r = refusal()
      if (r.blockers.length) return json(req, r, 409)
      visibility = 'public'
      return json(req, project())
    }
    if (path === '/me/profile' && method === 'PATCH') return json(req, profile)

    if (path === '/me') return json(req, { authenticated: true, principal: ME, email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } })
    if (path === '/me/profile') return json(req, profile)
    if (path === '/projects') return json(req, [{ ...project(), session_count: live().length, running: 0, latest_phase: 'done', published_sessions: live().length }])
    if (path === `/projects/${PROJECT}`) return json(req, { project: project(), sessions: live(), papers: [paper], may_submit: true, role: 'owner' })
    if (path === `/projects/${PROJECT}/papers`) return json(req, [paper])
    if (path === `/projects/${PROJECT}/members`) return json(req, [])
    if (path === `/projects/${PROJECT}/credits`) return json(req, { wallet: 20, start_reserve: 5 })
    if (one) return deleted.has(one[1]) ? json(req, { error: 'not_found' }, 404) : json(req, sessionOf(one[1]))
    if (/^\/jobs\/[^/]+\/transcript$/.test(path)) return json(req, { events: [], usage: null, live: false })
    if (/^\/jobs\/[^/]+\/reproduction$/.test(path)) {
      const id = path.split('/')[2]
      if (id === STUCK) return json(req, { error: 'not_found' }, 404)
      return json(req, { id, repo: `open-hai/${id}`, title: 't', verdict: 'passed', review: 'clear', can_publish: true })
    }
    if (/^\/jobs\/[^/]+\/verdict$/.test(path)) return json(req, { error: 'no_findings' }, 404)
    if (path.endsWith('/artifacts')) return json(req, { files: [] })
    if (path === '/public' || path === '/shared') return json(req, [])
    if (path.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    req.continue()
  })

  const rows = () => page.$$eval('.blocker-row', (els) => els.map((r) => r.textContent.replace(/\s+/g, ' ').trim()))

  /* ── 1. The row action, behind the app's own dialog ── */

  await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('[data-test="delete-session"]', { timeout: 15000 })

  // Both finished sessions offer it, and it is not a native confirm: clicking it
  // has to raise the app's modal.
  const offered = await page.$$eval('[data-test="delete-session"]', (b) => b.length)
  if (offered !== 2) fail(`${offered} sessions offer a delete, want both finished ones`)

  await page.click('[data-test="delete-session"]')
  await page.waitForSelector('[data-test="delete-session-confirm"]', { timeout: 5000 })
  const dialog = await page.$eval('.modal', (el) => el.textContent.replace(/\s+/g, ' ').trim())
  // The dialog spends its words on what does *not* go. That is the whole reason
  // it is a dialog and not a bare button.
  if (!/ledger/i.test(dialog)) fail(`the dialog does not say the cost survives: ${dialog}`)
  if (!/closed, not destroyed/i.test(dialog)) fail(`the dialog does not say the repository survives: ${dialog}`)

  // Escape out: a dialog that cannot be declined is not a confirmation.
  await page.keyboard.press('Escape')
  await new Promise((r) => setTimeout(r, 250))
  if (await page.$('[data-test="delete-session-confirm"]')) fail('the delete dialog would not close')
  if (deleted.size) fail('a cancelled dialog deleted the session anyway')

  /* ── 2. The blocker row, and through to a published project ── */

  await page.waitForSelector('[data-test="publish-project"]', { timeout: 8000 })
  await page.click('[data-test="publish-project"]')
  await page.waitForSelector('.modal', { timeout: 5000 })
  for (let i = 0; i < 2; i++) {
    await page.evaluate(() => [...document.querySelectorAll('.modal button')].find((b) => /Continue/i.test(b.textContent))?.click())
    await new Promise((r) => setTimeout(r, 200))
  }
  await page.evaluate(() => document.querySelector('.modal .check, .modal [role="checkbox"]')?.click())
  await new Promise((r) => setTimeout(r, 150))
  const signOff = () => page.evaluate(() => [...document.querySelectorAll('.modal button')].find((b) => /Sign off/i.test(b.textContent))?.click())

  await signOff()
  await page.waitForSelector('[data-test="publish-blockers"]', { timeout: 8000 })
  await new Promise((r) => setTimeout(r, 250))

  const blocked = await rows()
  if (blocked.length !== 1) fail(`the refusal named ${blocked.length} sessions, want the one unfinished run`)
  if (!/never finished/i.test(blocked[0])) fail(`the row does not explain the unfinished run: ${blocked[0]}`)
  // The row carries its own remedy. Before 032 its only offer was "Open session"
  // — a transcript that says nothing about publishing.
  if (!(await page.$('[data-test="delete-blocker"]'))) fail('the unfinished blocker offers no way to remove it')

  // It confirms in the row, not in a second Modal: two modals would share the
  // Escape key, and one press would take the list of blockers with it.
  await page.click('[data-test="delete-blocker"]')
  await page.waitForSelector('[data-test="delete-blocker-confirm"]', { timeout: 5000 })
  if ((await page.$$eval('.modal', (m) => m.length)) !== 1) fail('the blocker delete opened a second modal over the wizard')
  await page.click('[data-test="delete-blocker-confirm"]')
  await page.waitForFunction(() => document.querySelectorAll('.blocker-row').length === 0, { timeout: 8000 })

  if (!deleted.has(STUCK)) fail('confirming the blocker delete did not call DELETE /jobs/{id}')
  if (deleted.has(GOOD)) fail('the delete removed the wrong session')

  // The end state, which is the point: the next sign-off publishes. Asserting
  // only that the row went away would pass on a list that never refetched.
  await signOff()
  await page.waitForFunction(() => !document.querySelector('.modal'), { timeout: 8000 })
  await new Promise((r) => setTimeout(r, 400))

  if (visibility !== 'public') fail(`the project is ${visibility} after the blocker was removed, want public`)
  if (!calls.includes(`DELETE /jobs/${STUCK}`)) fail(`no DELETE reached the server: ${JSON.stringify(calls.slice(-8))}`)

  // And the deleted session is gone from the screen it was on.
  await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.row', { timeout: 8000 })
  const shown = await page.$$eval('[data-test="delete-session"]', (b) => b.length)
  if (shown !== 1) fail(`${shown} sessions remain on the project screen, want the one that was kept`)

  if (!process.exitCode) console.log('PASS: an unfinished session can be deleted, and its project then publishes')
} catch (err) {
  fail(err.message)
} finally {
  if (browser) await browser.close()
  vite.kill()
}
