// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser test for the model-tier picker on the papers card (031).
//
// Every session this product ran before 031 ran on the "best" tier, because
// nothing offered a choice. The thing worth testing is not that a control
// renders — it is that the tier a person picks is the tier the request carries,
// and that the prices beside each tier are the server's rather than numbers the
// client made up. Both are invisible to a test that only reads the DOM, so this
// one drives the picker and inspects what actually left the browser.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5211
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
const paper = { key: 'doi:x', ref: '10.1145/3654777.3676417', title: 'Point of no Undo', sessions: 0 }
const credits = { balance: 100_000_000, available: 100_000_000, start_reserve: 20_000_000 }

// The card the server would serve. Micro-USD per million tokens, which is the
// unit the platform's rate card is kept in.
const tiers = [
  { tier: 'best', model: 'claude-opus-5', input: 5_000_000, output: 25_000_000, named: true },
  { tier: 'balanced', model: 'claude-sonnet-5', input: 3_000_000, output: 15_000_000, named: true },
  { tier: 'fast', model: 'claude-haiku-4-5-20251001', input: 1_000_000, output: 5_000_000, named: true },
]

// What the start request actually carried.
let started = null

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
      if (u.pathname === '/me') return json({ authenticated: true, principal: 'p', email: 'e@x', tier: 'user', credits: { wallet: 100_000_000, start_reserve: 20_000_000 } })
      if (u.pathname === '/me/profile') return json({})
      if (u.pathname === '/models') return json({ tiers })
      if (u.pathname === `/projects/${PROJECT}/sessions` && req.method() === 'POST') {
        started = JSON.parse(req.postData())
        return json({ job_id: 'j-new', project_id: PROJECT })
      }
      if (/^\/projects\/[^/]+$/.test(u.pathname)) return json({ project, sessions: [], papers: [paper], may_submit: true, role: 'owner', credits })
      if (/^\/projects\/[^/]+\/papers$/.test(u.pathname)) return json([paper])
      if (/^\/projects\/[^/]+\/members$/.test(u.pathname)) return json([])
      if (/^\/(projects|public|shared)$/.test(u.pathname)) return json([])
      if (u.pathname.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    }
    req.continue()
  })

  await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.tier-pick', { timeout: 15000 })
  await new Promise((r) => setTimeout(r, 300))

  /* ── The picker offers every tier the server named, at the server's prices ── */

  await page.click('.tier-pick .sel-btn')
  await page.waitForSelector('.sel-list', { timeout: 5000 })
  const options = await page.evaluate(() => [...document.querySelectorAll('.sel-list .sel-opt')].map((o) => o.textContent.replace(/\s+/g, ' ').trim()))

  if (options.length !== 3) fail(`the picker offers ${options.length} tiers, want the 3 the server listed`)
  for (const [i, want] of [
    ['Best', '$5.00', '$25.00'],
    ['Balanced', '$3.00', '$15.00'],
    ['Fast', '$1.00', '$5.00'],
  ].entries()) {
    const got = options[i] ?? ''
    for (const part of want) {
      if (!got.includes(part)) fail(`tier ${i} reads "${got}"; it must name ${part} — the prices are the server's, not the client's`)
    }
  }

  /* ── The tier picked is the tier the request carries ── */

  await page.evaluate(() => [...document.querySelectorAll('.sel-list .sel-opt')].find((o) => /Fast/.test(o.textContent))?.click())
  await new Promise((r) => setTimeout(r, 200))
  await page.evaluate(() => [...document.querySelectorAll('.paper-group .btn')].find((b) => /Start replication/i.test(b.textContent))?.click())
  await new Promise((r) => setTimeout(r, 500))

  if (!started) fail('pressing Start sent no request')
  else {
    if (started.tier !== 'fast') fail(`Start sent tier ${JSON.stringify(started.tier)}, want "fast" — the choice did not reach the request`)
    if (started.paper_key !== paper.key) fail(`Start sent paper_key ${JSON.stringify(started.paper_key)}, want ${JSON.stringify(paper.key)}`)
  }

  /* ── The control is inside its card ── */

  const clipped = await page.evaluate(() => {
    const pick = document.querySelector('.tier-pick')
    const card = pick.closest('.card')
    return pick.getBoundingClientRect().right > card.getBoundingClientRect().right + 0.5
  })
  if (clipped) fail('the tier picker is clipped at the card edge')

  if (!process.exitCode) console.log('ok: the tier picker prices every tier from the server and sends the one chosen')
} catch (err) {
  fail(err.message)
} finally {
  if (browser) await browser.close()
  vite.kill()
}
