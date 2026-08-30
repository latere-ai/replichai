// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for a project's description (038) and the draft
// that offers to write one (039).
//
// A project could say what it was called and nothing else. Its title is either
// what somebody typed or the first paper's reference, so a good half of them
// were named after a doi: string and none could say why they existed — to their
// own owner returning after a month, to a collaborator arriving, or to the
// stranger the public zone is addressed to.
//
// Six things have to hold:
//
//   1. An owner whose project has no description is invited to write one; a
//      reader of the same project is shown nothing at all. Empty is a state,
//      not a placeholder somebody has to read past.
//   2. Writing one calls PATCH /projects/{id} with the description alone, and
//      the head re-reads without a reload.
//   3. It is edited in place, on the screen it is read on — not behind a
//      settings page.
//   4. The list shows it under the title, on one line.
//   5. A draft lands in the editor and nowhere else: asking for one stores
//      nothing, and what is finally saved is what the person left in the field.
//   6. A deployment with no drafting model offers no control at all, and its
//      descriptions are still typed.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5259
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
const TEXT = 'Two papers on whether a segmentation overlay changes what a driver trusts.'

let description = ''
let patches = []
let drafted = 0
let drafts = true
const DRAFTED = 'A model wrote this sentence about the project.'

const project = () => ({ id: PROJECT, owner: ME, title: 'Trust in automation', description, visibility: 'private', created_at: at(0) })
const json = (req, body, status = 200) => req.respond({ status, contentType: 'application/json', body: JSON.stringify(body) })

const serve = (page, { me = ME } = {}) => {
  page.on('request', (req) => {
    const u = new URL(req.url())
    if (u.origin !== BASE) return req.continue()
    const path = u.pathname
    const method = req.method()

    if (/^\/projects\/[^/]+$/.test(path) && method === 'PATCH') {
      const body = JSON.parse(req.postData() || '{}')
      patches.push(body)
      if (body.description !== undefined) description = body.description
      return json(req, project())
    }
    if (path === '/me') return json(req, { authenticated: true, principal: me, email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } })
    if (path === '/me/profile') return json(req, { display_name: 'Ada Lovelace', contact_email: 'ada@example.org', handle: 'ada', public: true })
    if (path === '/projects') return json(req, [{ ...project(), session_count: 0, running: 0, modified_at: at(0) }])
    if (/^\/projects\/[^/]+$/.test(path)) return json(req, { project: project(), sessions: [], papers: [], may_submit: me === ME, role: me === ME ? 'owner' : 'member' })
    if (/^\/projects\/[^/]+\/papers$/.test(path)) return json(req, [])
    if (/^\/projects\/[^/]+\/members$/.test(path)) return json(req, [])
    if (/^\/projects\/[^/]+\/credits$/.test(path)) return json(req, { wallet: 20, start_reserve: 5, available: 20 })
    if (path === '/models') return json(req, { tiers: [], drafting: drafts })
    if (/^\/projects\/[^/]+\/description\/draft$/.test(path) && method === 'POST') {
      drafted++
      return json(req, { description: DRAFTED, model: 'small-1', cost_micro: 1200 })
    }
    if (path === '/public' || path === '/shared') return json(req, [])
    if (path.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    req.continue()
  })
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

  /* ── 1, 2, 3. The owner writes one, in place ── */

  const page = await browser.newPage()
  await page.setViewport({ width: 1440, height: 1000 })
  await page.setRequestInterception(true)
  serve(page)

  await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('[data-test="describe-add"]', { timeout: 15000 })
  if (await page.$('[data-test="description"]')) fail('a project with no description rendered one')

  await page.click('[data-test="describe-add"]')
  await page.waitForSelector('[data-test="describe-input"]', { timeout: 5000 })
  // In place: the editor is in the project's own head, not on another screen.
  const inHead = await page.$eval('[data-test="describe-editor"]', (e) => !!e.closest('.detail-head'))
  if (!inHead) fail('the description is edited somewhere other than the project head')

  await page.type('[data-test="describe-input"]', TEXT)
  await page.click('[data-test="describe-save"]')
  await page.waitForSelector('[data-test="description"]', { timeout: 10000 })

  if (patches.length !== 1) fail(`saving sent ${patches.length} requests`)
  if (patches[0].description !== TEXT) fail(`the description sent was ${JSON.stringify(patches[0])}`)
  if ('title' in patches[0]) fail('describing a project also sent its title, which it must not touch')

  const shown = await page.$eval('[data-test="description"]', (e) => e.textContent.trim())
  if (!shown.startsWith(TEXT)) fail(`the head shows ${JSON.stringify(shown)}`)

  /* ── 4. On the list, under the title ── */

  await page.goto(`${BASE}/`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('[data-test="row-description"]', { timeout: 10000 })
  const row = await page.$eval('[data-test="row-description"]', (e) => ({
    text: e.textContent.trim(),
    lines: e.getBoundingClientRect().height,
    underTitle: e.previousElementSibling?.className.includes('row-title') ?? false,
  }))
  if (row.text !== TEXT) fail(`the list row shows ${JSON.stringify(row.text)}`)
  if (!row.underTitle) fail('the description is not under the title in the list')
  if (row.lines > 24) fail(`the list row's description is ${row.lines}px tall; it must stay one line`)
  await page.close()

  /* ── 5. A draft is a suggestion, not a save (039) ── */

  {
    const p = await browser.newPage()
    await p.setViewport({ width: 1440, height: 1000 })
    await p.setRequestInterception(true)
    serve(p)
    description = ''
    patches = []
    await p.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
    await p.waitForSelector('[data-test="describe-add"]', { timeout: 15000 })
    await p.click('[data-test="describe-add"]')
    await p.waitForSelector('[data-test="describe-draft"]', { timeout: 5000 })

    await p.click('[data-test="describe-draft"]')
    await p.waitForFunction((want) => document.querySelector('[data-test="describe-input"]')?.value === want, { timeout: 10000 }, DRAFTED)
    if (drafted !== 1) fail(`the draft control called the endpoint ${drafted} times`)
    // Into the editor, and nowhere else: nothing is stored until somebody saves.
    if (patches.length !== 0) fail('asking for a draft wrote the description without anybody accepting it')
    if (await p.$('[data-test="description"]')) fail('the draft was rendered as the project’s description before it was saved')

    // Saving is the person's act, and it saves what is in the field.
    await p.click('[data-test="describe-save"]')
    await p.waitForSelector('[data-test="description"]', { timeout: 10000 })
    if (patches.length !== 1 || patches[0].description !== DRAFTED) fail(`saving after a draft sent ${JSON.stringify(patches)}`)
    await p.close()
  }

  /* ── 5, the other half. A deployment that cannot draft offers no control ── */

  {
    drafts = false
    description = ''
    const p = await browser.newPage()
    await p.setViewport({ width: 1440, height: 1000 })
    await p.setRequestInterception(true)
    serve(p)
    await p.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
    await p.waitForSelector('[data-test="describe-add"]', { timeout: 15000 })
    await p.click('[data-test="describe-add"]')
    await p.waitForSelector('[data-test="describe-input"]', { timeout: 5000 })
    await new Promise((r) => setTimeout(r, 300))
    if (await p.$('[data-test="describe-draft"]')) fail('a deployment with no drafting model offered a control that can only fail')
    // And the field still works, which is the whole point of the fallback.
    await p.type('[data-test="describe-input"]', 'Typed by hand.')
    await p.click('[data-test="describe-save"]')
    await p.waitForSelector('[data-test="description"]', { timeout: 10000 })
    await p.close()
    drafts = true
  }

  /* ── 1, the other half. A reader is shown nothing to fill in ── */

  const reader = await browser.newPage()
  await reader.setViewport({ width: 1440, height: 1000 })
  await reader.setRequestInterception(true)
  serve(reader, { me: 'somebody-else' })
  description = ''
  await reader.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await reader.waitForSelector('.detail-head', { timeout: 15000 })
  await new Promise((r) => setTimeout(r, 400))
  if (await reader.$('[data-test="describe-add"]')) fail('a reader who cannot edit was invited to write a description')
  if (await reader.$('[data-test="description"]')) fail('an empty description rendered as an empty paragraph')

  if (!process.exitCode) console.log('ok: a project says what it is about, written in place and read in the list')
} catch (err) {
  fail(err.message)
} finally {
  if (browser) await browser.close()
  vite.kill()
}
