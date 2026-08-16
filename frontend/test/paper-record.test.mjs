// A browser regression test for a paper's bibliography (037).
//
// A project used to show a title and the string somebody typed to name the
// paper. Everything else — who wrote it, where it appeared, when — was fetched
// during resolution and thrown away, so a project read as a list of URLs.
//
// Four things have to hold:
//
//   1. A paper with a record reads as a citation: authors, venue, year, on one
//      line under the title. Long author lists are cut with "et al." rather than
//      taking the row.
//   2. A paper without one says so, which is also the invitation to fix it.
//   3. "Fill in details" is offered only when something is missing, calls
//      POST /projects/{id}/papers/refresh, and the rows re-read afterwards.
//   4. Correcting sends exactly what the dialog shows to PATCH
//      /projects/{id}/papers?key=…, including an author list split by line.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5258
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
const FULL = 'doi:10.1145/3772318.3791265'
const BARE = 'url:https://example.edu/one.pdf'

// The server's state, which the page's own calls change.
let papers = [
  {
    key: FULL,
    ref: 'https://doi.org/10.1145/3772318.3791265',
    title: 'Effects of Semantic Segmentation Visualization on Trust',
    added_by: ME,
    added_at: at(0),
    sessions: 0,
    record: {
      authors: ['Changkun Ou', 'Robin Welsch', 'Sven Mayer', 'Andreas Butz'],
      venue: 'Proc. ACM Hum.-Comput. Interact.',
      year: 2024,
      volume: '8',
      issue: 'CSCW1',
      pages: '1128-1145',
      publisher: 'ACM',
    },
  },
  { key: BARE, ref: 'https://example.edu/one.pdf', title: '', added_by: ME, added_at: at(1), sessions: 0 },
]
let refreshed = 0
let patchBody = null
let patchKey = null

const project = { id: PROJECT, owner: ME, title: 'A project', visibility: 'private', created_at: at(0) }
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

    // The two routes under test.
    if (/^\/projects\/[^/]+\/papers\/refresh$/.test(path) && method === 'POST') {
      refreshed++
      // The source answers for the bare paper: this is what a fill-in does.
      papers = papers.map((p) =>
        p.key === BARE
          ? { ...p, title: 'A Paper Nobody Had Looked Up', record: { authors: ['Ada Lovelace'], venue: 'Interacting with Computers', year: 2022 } }
          : p,
      )
      return json(req, { filled: 1, papers })
    }
    if (/^\/projects\/[^/]+\/papers$/.test(path) && method === 'PATCH') {
      patchKey = u.searchParams.get('key')
      patchBody = JSON.parse(req.postData() || '{}')
      papers = papers.map((p) => (p.key === patchKey ? { ...p, title: patchBody.title, record: { ...p.record, ...patchBody, authors: patchBody.authors } } : p))
      return json(req, papers.find((p) => p.key === patchKey))
    }

    if (path === '/me') return json(req, { authenticated: true, principal: ME, email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } })
    if (path === '/me/profile') return json(req, { display_name: 'Ada Lovelace', contact_email: 'ada@example.org', handle: 'ada', public: true })
    if (path === '/projects') return json(req, [{ ...project, session_count: 0, running: 0 }])
    if (/^\/projects\/[^/]+$/.test(path)) return json(req, { project, sessions: [], papers, may_submit: true, role: 'owner' })
    if (/^\/projects\/[^/]+\/papers$/.test(path)) return json(req, papers)
    if (/^\/projects\/[^/]+\/members$/.test(path)) return json(req, [])
    if (/^\/projects\/[^/]+\/credits$/.test(path)) return json(req, { wallet: 20, start_reserve: 5, available: 20 })
    if (path === '/models') return json(req, { tiers: [] })
    if (path === '/public' || path === '/shared') return json(req, [])
    if (path.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    req.continue()
  })

  const cites = () => page.$$eval('[data-test="citation"], [data-test="citation-missing"]', (els) => els.map((e) => e.textContent.replace(/\s+/g, ' ').trim()))

  /* ── 1 and 2. A record reads as a citation; a paper without one says so ── */

  await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('[data-test="citation"]', { timeout: 15000 })

  let lines = await cites()
  if (lines.length !== 2) fail(`expected a line under each of the two papers, got ${JSON.stringify(lines)}`)
  if (!/Changkun Ou/.test(lines[0]) || !/Proc\. ACM/.test(lines[0]) || !/2024/.test(lines[0])) {
    fail(`the citation does not read as one: ${JSON.stringify(lines[0])}`)
  }
  if (!/et al\./.test(lines[0])) fail(`four authors were printed in full: ${JSON.stringify(lines[0])}`)
  if (/Andreas Butz/.test(lines[0])) fail('the fourth author is in the row rather than behind "et al."')
  if (!/No details/i.test(lines[1])) fail(`a paper with no record should say so: ${JSON.stringify(lines[1])}`)

  /* ── 3. Filling in what is missing ── */

  const fillBtn = await page.$('[data-test="fill-in-papers"]')
  if (!fillBtn) fail('a project with an unfilled paper offers no way to fill it in')
  await fillBtn.click()
  await page.waitForFunction(() => !document.querySelector('[data-test="citation-missing"]'), { timeout: 10000 })
  if (refreshed !== 1) fail(`the fill-in called the endpoint ${refreshed} times`)

  lines = await cites()
  if (!/Ada Lovelace/.test(lines[1]) || !/2022/.test(lines[1])) fail(`the filled-in paper did not re-read: ${JSON.stringify(lines[1])}`)
  // With nothing left to fill, the control goes: a button that can only say
  // "nothing to do" is furniture.
  await page.waitForFunction(() => !document.querySelector('[data-test="fill-in-papers"]'), { timeout: 10000 })

  /* ── 4. Correcting what a source got wrong ── */

  await page.click('[data-test="edit-paper"]')
  await page.waitForSelector('[data-test="edit-paper-save"]', { timeout: 5000 })

  // The dialog opens on what is stored, so a person edits rather than retypes.
  const venueBefore = await page.$eval('[data-test="paper-venue"]', (e) => e.value)
  if (venueBefore !== 'Proc. ACM Hum.-Comput. Interact.') fail(`the dialog did not open on the stored venue: ${JSON.stringify(venueBefore)}`)
  const authorsBefore = await page.$eval('[data-test="paper-authors"]', (e) => e.value)
  if (authorsBefore.split('\n').length !== 4) fail(`authors are not one per line: ${JSON.stringify(authorsBefore)}`)

  await page.$eval('[data-test="paper-venue"]', (e) => {
    e.value = ''
  })
  await page.type('[data-test="paper-venue"]', 'CHI 2024')
  await page.$eval('[data-test="paper-authors"]', (e) => {
    e.value = ''
  })
  await page.type('[data-test="paper-authors"]', 'Ada Lovelace\nGrace Hopper')
  await page.click('[data-test="edit-paper-save"]')
  await page.waitForFunction(() => !document.querySelector('[data-test="edit-paper-save"]'), { timeout: 10000 })

  if (patchKey !== FULL) fail(`the correction named ${patchKey}`)
  if (!patchBody || patchBody.venue !== 'CHI 2024') fail(`the corrected venue was not sent: ${JSON.stringify(patchBody)}`)
  if (JSON.stringify(patchBody.authors) !== JSON.stringify(['Ada Lovelace', 'Grace Hopper'])) {
    fail(`the author list was not split by line: ${JSON.stringify(patchBody?.authors)}`)
  }
  if (patchBody.year !== 2024) fail(`a field the person left alone was not sent as it stands: ${JSON.stringify(patchBody)}`)

  await page.waitForFunction(() => /CHI 2024/.test(document.querySelector('[data-test="citation"]')?.textContent ?? ''), { timeout: 10000 })

  if (!process.exitCode) console.log('ok: a paper reads as a citation, fills in what is missing, and takes a correction')
} catch (err) {
  fail(err.message)
} finally {
  if (browser) await browser.close()
  vite.kill()
}
