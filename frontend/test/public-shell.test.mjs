// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for browsing signed out (028).
//
// What it guards: until now every address sent a reader with no account to the
// sign-in screen, and the one thing this product publishes — a reproduction
// somebody signed — was unreadable to anybody who had not joined. The registry
// and everything under it now render in the app's own frame, with the
// destinations that need an account absent rather than disabled.
//
// None of it is reachable from a unit test: the assertions are about which
// frame renders, what is in the rail, and where the sign-in leads. It drives
// Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5203
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

const ago = (ms) => new Date(Date.now() - ms).toISOString()

// One published project, exactly as the server sends it to a reader with no
// stake: named by its signer, and carrying no owning principal, no spend, and no
// per-session cost.
const published = {
  id: 'pub-1',
  title: 'The Placebo Effect of Artificial Intelligence',
  visibility: 'public',
  created_at: ago(90 * 86_400_000),
  modified_at: ago(3 * 3_600_000),
  signed_off_at: ago(2 * 86_400_000),
  signed_off_name: 'Ada Lovelace',
  owner_name: 'Ada Lovelace',
  owner_handle: 'ada',
  signer_handle: 'ada',
  session_count: 1,
  published_sessions: 1,
  running: 0,
  latest_phase: 'done',
  // What it is a reproduction of (043), as the list carries it: the citation,
  // and no abstract.
  papers: [{ title: 'The Placebo Effect of Artificial Intelligence', authors: ['Ada Lovelace', 'Grace Hopper'], venue: 'CHI', year: 2024 }],
}

const detail = {
  project: { ...published },
  sessions: [
    {
      id: 'job-1',
      kind: 'audit',
      project_id: 'pub-1',
      ref: 'arxiv:2301.03711',
      phase: 'done',
      repo_name: 'open-hai/placebo',
      created_at: ago(4 * 86_400_000),
      updated_at: ago(2 * 86_400_000),
    },
  ],
  may_submit: false,
  signer_handle: 'ada',
}

const session = {
  id: 'job-1',
  kind: 'audit',
  project_id: 'pub-1',
  ref: 'arxiv:2301.03711',
  phase: 'done',
  repo_name: 'open-hai/placebo',
  created_at: ago(4 * 86_400_000),
  updated_at: ago(2 * 86_400_000),
}

const routes = [
  [/^\/public$/, 200, [published]],
  [/^\/jobs\/job-1$/, 200, session],
  [/^\/jobs\/job-1\/transcript$/, 200, { events: [], usage: null, live: false }],
  [/^\/projects\/pub-1$/, 200, detail],
  [/^\/projects\/pub-1\/papers$/, 200, []],
  // Anything private reads as missing, which is the one answer the server gives
  // for private, withdrawn, and never-existed alike.
  [/^\/projects\/private-1$/, 404, { error: 'not_found' }],
  [/^\/projects\/private-1\/papers$/, 404, { error: 'not_found' }],
]

const fail = (msg) => {
  console.error(`FAIL: ${msg}`)
  process.exitCode = 1
}

const vite = spawn('npx', ['vite', '--port', String(PORT), '--strictPort'], { stdio: 'ignore' })
let browser

// open serves the mocked API with `me` as the identity and returns a page at
// `path`.
const open = async (me, path) => {
  const page = await browser.newPage()
  const asked = []
  page.asked = asked
  await page.setViewport({ width: 1440, height: 900 })
  await page.setRequestInterception(true)
  page.on('request', (req) => {
    const u = new URL(req.url())
    if (u.origin === BASE) {
      asked.push(u.pathname)
      if (u.pathname === '/me') return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(me) })
      const hit = routes.find(([re]) => re.test(u.pathname))
      if (hit) return req.respond({ status: hit[1], contentType: 'application/json', body: JSON.stringify(hit[2]) })
      if (u.pathname.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    }
    req.continue()
  })
  page.on('pageerror', (e) => fail(`page error: ${e.message}`))
  await page.goto(BASE + path, { waitUntil: 'networkidle0' })
  return page
}

const SIGNED_OUT = { authenticated: false }

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

  /* ── The registry, signed out ── */

  {
    const page = await open(SIGNED_OUT, '/registry')
    await page.waitForSelector('.shell')
    const shell = await page.evaluate(() => ({
      landing: !!document.querySelector('.landing'),
      rows: document.querySelectorAll('.screen .rows .row').length,
      rail: [...document.querySelectorAll('.rail .nav-btn')].map((b) => b.textContent.trim()),
      signIn: document.querySelector('[data-test="rail-signin"]')?.getAttribute('href') || '',
      topbarSignIn: document.querySelector('[data-test="topbar-signin"]')?.getAttribute('href') || '',
      wallet: !!document.querySelector('.wallet-pill'),
      newProject: [...document.querySelectorAll('.topbar-actions button')].some((b) => /new/i.test(b.getAttribute('aria-label') || '')),
      user: !!document.querySelector('.user-btn'),
      cite: document.querySelector('.screen .rows .row [data-test="row-citation"]')?.textContent.trim() || '',
    }))
    if (shell.landing) fail('the registry rendered the landing; signed-out browsing is the app, not a preview of it')
    // 043: a row says which paper it reproduces, to the reader who has no
    // account and is deciding whether to open it.
    for (const want of ['Ada Lovelace', 'CHI', '2024']) {
      if (!shell.cite.includes(want)) fail(`the signed-out registry row does not cite ${want}: ${shell.cite || 'no citation at all'}`)
    }
    if (!shell.rows) fail('the signed-out registry lists nothing')
    if (shell.rail.length !== 1) fail(`the signed-out rail offers ${shell.rail.length} destinations (${shell.rail.join(', ')}), want only the registry`)
    if (shell.signIn !== '/login?next=%2Fregistry') fail(`the rail sign-in points at ${shell.signIn || 'nothing'}`)
    if (shell.topbarSignIn !== '/login?next=%2Fregistry') fail(`the topbar sign-in points at ${shell.topbarSignIn || 'nothing'}`)
    if (shell.wallet) fail('a reader with no account is shown a wallet')
    if (shell.newProject) fail('a reader with no account is offered a new project')
    if (shell.user) fail('a reader with no account is shown an account')
    await page.close()
  }

  /* ── A published project and its session, signed out ── */

  {
    const page = await open(SIGNED_OUT, '/p/pub-1')
    await page.waitForSelector('.shell .detail-head')
    const got = await page.evaluate(() => ({
      title: document.querySelector('.detail-head')?.textContent || '',
      // Nothing on a project a reader has no stake in offers to change it.
      publish: !!document.querySelector('[data-test="publish-project"]'),
      people: [...document.querySelectorAll('.detail-tabs button')].map((b) => b.textContent.trim()),
      signIn: document.querySelector('[data-test="rail-signin"]')?.getAttribute('href') || '',
      crumb: document.querySelector('.crumb button')?.textContent.trim() || '',
    }))
    if (!got.title.includes('Placebo')) fail('the published project did not render for a signed-out reader')
    if (got.publish) fail('a signed-out reader is offered the publish wizard')
    if (got.people.some((tab) => /member|people/i.test(tab))) fail('a signed-out reader is offered the member roll')
    if (got.signIn !== '/login?next=%2Fp%2Fpub-1') fail(`the sign-in does not carry this address: ${got.signIn}`)
    if (!/registry/i.test(got.crumb)) fail(`the breadcrumb roots at "${got.crumb}", want the registry — the reader has no projects screen`)
    await page.close()
  }

  /* ── A session under a published project ── */

  {
    const page = await open(SIGNED_OUT, '/p/pub-1/s/job-1')
    await page.waitForSelector('.tx .detail-head')
    // The live stream is an operator route and stays gated. Opened here it would
    // be refused and reopened for as long as the page is up, so it must not be
    // opened at all.
    const streamed = page.asked.some((p) => p.endsWith('/events'))
    if (streamed) fail(`a signed-out session screen opened the gated live stream: ${page.asked.join(' ')}`)
    await page.close()
  }

  /* ── An address that is not public ── */

  {
    const page = await open(SIGNED_OUT, '/p/private-1')
    await page.waitForSelector('[data-test="not-readable"]')
    const href = await page.evaluate(() => document.querySelector('[data-test="not-readable-signin"]')?.getAttribute('href') || '')
    if (href !== '/login?next=%2Fp%2Fprivate-1') fail(`the not-public screen's sign-in does not round-trip: ${href}`)
    await page.close()
  }

  /* ── A screen there is nothing to show without an account ── */

  {
    const page = await open(SIGNED_OUT, '/wallet')
    await page.waitForSelector('.landing')
    const shell = await page.evaluate(() => !!document.querySelector('.shell'))
    if (shell) fail('the wallet rendered an empty app frame instead of the landing')
    await page.close()
  }

  /* ── Signed in, nothing moved ── */

  {
    const page = await open({ authenticated: true, principal: 'me-sub', email: 'me@example.com', tier: 'user' }, '/registry')
    await page.waitForSelector('.shell .rail')
    const rail = await page.evaluate(() => ({
      items: document.querySelectorAll('.rail .nav-btn').length,
      user: !!document.querySelector('.user-btn'),
      signIn: !!document.querySelector('[data-test="rail-signin"]'),
    }))
    if (rail.items < 4) fail(`a signed-in reader lost rail destinations (${rail.items})`)
    if (!rail.user) fail('a signed-in reader lost their account button')
    if (rail.signIn) fail('a signed-in reader is offered a sign-in')
    await page.close()
  }

  if (!process.exitCode) console.log('ok: the registry and what is under it read signed out, in the app’s own frame')
} finally {
  if (browser) await browser.close()
  vite.kill()
}
