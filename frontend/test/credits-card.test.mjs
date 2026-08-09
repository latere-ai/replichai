// A browser regression test for the project credits card.
//
// Three defects, each invisible to a test that reads text:
//
// The two amounts did not line up. `.usage-line .v` is `margin-left: auto`, so
// both should have been flush right — but the second row was wrapped in a
// <Tip>, whose <span> is inline, and an inline box around a block row collapses
// that row to its content. The value then sits against its label. Only geometry
// sees it.
//
// The amount field took integers. It is a money field on every screen it
// appears, and stripping the decimal point meant a project could be allocated
// whole dollars only — a $16.82 balance could not be moved back in one go.
//
// The card ran the full width of the screen to show two numbers and a stepper,
// pushing the sessions below the fold. It belongs in the rail beside them, which
// is narrower than the three controls it holds — so the row has to wrap rather
// than clip its third button.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5209
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

const at = (i) => new Date(1754_215_200_000 - i * 86_400_000).toISOString()
const PROJECT = 'proj-1'
const project = { id: PROJECT, owner: 'p', title: 'Point of no Undo', visibility: 'private', created_at: at(0) }
const paper = { key: 'doi:x', ref: '10.1145/3654777.3676417', title: 'Point of no Undo', sessions: 1 }
const job = { id: 'j1', project_id: PROJECT, paper_key: paper.key, ref: paper.ref, phase: 'done', repo_name: 'open-hai/x', created_at: at(1), updated_at: at(0), cost: 3_842_000 }
// Short of the reserve, so the card shows its warning and both buttons.
const credits = { balance: 16_820_000, available: 16_820_000, start_reserve: 20_000_000, may_reclaim: true }

// What the allocate request actually carried, so the test checks the amount
// that left rather than the digits on screen.
let allocated = null

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
    if (u.origin === BASE) {
      const json = (body) => req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
      if (u.pathname === '/me') return json({ authenticated: true, principal: 'p', email: 'e@x', tier: 'user', credits: { wallet: 10_000_000, start_reserve: 20_000_000 } })
      if (u.pathname === '/me/profile') return json({})
      if (u.pathname === `/projects/${PROJECT}/credits` && req.method() === 'POST') {
        allocated = JSON.parse(req.postData()).amount
        return json(credits)
      }
      if (/^\/projects\/[^/]+$/.test(u.pathname)) return json({ project, sessions: [job], papers: [paper], may_submit: true, role: 'owner', credits })
      if (/^\/projects\/[^/]+\/(papers)$/.test(u.pathname)) return json([paper])
      if (/^\/projects\/[^/]+\/(members)$/.test(u.pathname)) return json([])
      if (/^\/jobs\/[^/]+\/verdict$/.test(u.pathname)) return req.respond({ status: 404, contentType: 'application/json', body: '{"error":"no_findings"}' })
      if (/^\/jobs\//.test(u.pathname)) return json(job)
      if (/^\/(projects|public|shared)$/.test(u.pathname)) return json([])
      if (u.pathname.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    }
    req.continue()
  })

  await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.usage-rows', { timeout: 15000 })
  await new Promise((r) => setTimeout(r, 400))

  /* ── The amounts line up ── */

  const rows = await page.evaluate(() => {
    const card = document.querySelector('.usage-rows').closest('.card')
    return [...card.querySelectorAll('.usage-line')].map((line) => {
      const v = line.querySelector('.v')
      return {
        label: line.textContent.replace(/\s+/g, ' ').trim(),
        right: v ? Math.round(v.getBoundingClientRect().right) : null,
        // A row that collapsed to its content is narrower than the list holding
        // it, which is the shape of the defect rather than only its symptom.
        width: Math.round(line.getBoundingClientRect().width),
        listWidth: Math.round(line.parentElement.getBoundingClientRect().width),
      }
    })
  })
  if (rows.length !== 2) fail(`the credits card shows ${rows.length} amounts, want the balance and what is available`)
  else {
    const [a, b] = rows
    if (Math.abs(a.right - b.right) > 1) fail(`the amounts end at ${a.right} and ${b.right}; they are in one column and must line up`)
    for (const r of rows) {
      if (r.listWidth - r.width > 2) fail(`the row "${r.label}" is ${r.width}px inside a ${r.listWidth}px list; something collapsed it`)
    }
  }

  /* ── The field takes cents ── */

  await page.type('.numf-input', '16.82')
  const typed = await page.$eval('.numf-input', (el) => el.value)
  if (typed !== '16.82') fail(`typing 16.82 left the field reading ${JSON.stringify(typed)}`)

  // And the amount that leaves is the amount typed, in micro-USD.
  await page.evaluate(() => [...document.querySelectorAll('.card .btn')].find((b) => /allocate/i.test(b.textContent))?.click())
  await new Promise((r) => setTimeout(r, 400))
  if (allocated !== 16_820_000) fail(`Allocate sent ${allocated}, want 16820000 — the cents were dropped`)

  // The stepper keeps them too: it used to parse an integer and throw the
  // fraction away on every press.
  await page.type('.numf-input', '5.25')
  await page.evaluate(() => document.querySelector('.numf-step')?.parentElement.querySelectorAll('.numf-step')[1].click())
  const stepped = await page.$eval('.numf-input', (el) => el.value)
  if (stepped !== '6.25') fail(`stepping 5.25 up gave ${JSON.stringify(stepped)}, want 6.25`)

  /* ── The card is in the rail, and nothing in it is clipped ── */

  const layout = await page.evaluate(() => {
    const card = document.querySelector('.usage-rows').closest('.card')
    const grid = card.closest('.detail-grid')
    const papers = document.querySelector('[data-test="project-rows"], .paper-group')?.closest('.card')
    const r = card.getBoundingClientRect()
    const controls = [...card.querySelectorAll('.field-row > *')].map((c) => ({
      text: c.textContent.replace(/\s+/g, ' ').trim().slice(0, 20),
      overflows: c.getBoundingClientRect().right > r.right + 0.5,
    }))
    return {
      inGrid: !!grid,
      width: Math.round(r.width),
      // Beside the papers card, not above it.
      besidePapers: papers ? r.left >= papers.getBoundingClientRect().right - 1 : null,
      controls,
    }
  })
  if (!layout.inGrid) fail('the credits card is not in the two-column layout; it runs the full width')
  if (layout.width > 420) fail(`the credits card is ${layout.width}px wide; it holds two numbers and a stepper`)
  if (layout.besidePapers === false) fail('the credits card sits above the papers rather than beside them')
  for (const c of layout.controls) {
    if (c.overflows) fail(`the control "${c.text}" is clipped at the card's edge; the row has to wrap in the rail`)
  }

  if (!process.exitCode) console.log('ok: the credits card lines up, takes cents, and fits its rail')
} catch (err) {
  fail(err.message)
} finally {
  if (browser) await browser.close()
  vite.kill()
}
