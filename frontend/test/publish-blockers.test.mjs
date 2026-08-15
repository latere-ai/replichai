// A browser regression test for a refused publish (006f's gate, on screen).
//
// What it guards is that a refusal is actionable. The public flip used to answer
// every unpublishable project with one sentence — "its reproduction did not pass
// the redistribution check" — whatever the actual reason, and whichever session
// carried it. An owner read an accusation about content, went looking for
// third-party data, and found none, because their run had simply never finished.
//
// So three things have to hold on screen: the refusal says which of the reasons
// it was, it names every session in the way rather than the first, and each of
// those is one click from the place the reader can see it for themselves.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5251
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
const PROJECT = 'proj-1'
const FLAGGED = 'job-flagged' // committed data it may not redistribute
const UNFINISHED = 'job-unfinished' // has a repository, never wrote a manifest

const project = { id: PROJECT, owner: 'p', title: 'Posture-Aware Pen+Touch', visibility: 'private', created_at: at(0) }
const paper = { key: 'doi:10.1145/3654777.3676417', ref: '10.1145/3654777.3676417', title: 'Sensing Posture-Aware Pen+Touch Interaction', sessions: 2 }
const sessionOf = (id) => ({
  id,
  project_id: PROJECT,
  paper_key: paper.key,
  ref: paper.ref,
  phase: id === UNFINISHED ? 'failed' : 'done',
  repo_name: `open-hai/${id}`,
  created_at: at(0),
  updated_at: at(6e6),
  cost: 3842,
})

// What the server refuses with: the flagged tree is the headline, and both
// sessions are named. This is the body the screen has to turn into somewhere to
// go — 409 with `blockers`, exactly as internal/httpapi writes it.
const refusal = {
  error: 'not_clear',
  blockers: [
    { session_id: FLAGGED, repo: `open-hai/${FLAGGED}`, ref: paper.ref, reason: 'not_clear', review: 'excluded' },
    { session_id: UNFINISHED, repo: `open-hai/${UNFINISHED}`, ref: paper.ref, reason: 'unfinished' },
  ],
}

const profile = { display_name: 'Ada Lovelace', contact_email: 'ada@example.org', handle: 'ada', public: true }

const routes = [
  [/^\/me$/, { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  [/^\/me\/profile$/, profile],
  [/^\/projects$/, [{ ...project, session_count: 2, running: 0, latest_phase: 'done', published_sessions: 2 }]],
  [/^\/projects\/[^/]+$/, { project, sessions: [sessionOf(FLAGGED), sessionOf(UNFINISHED)], papers: [paper], may_submit: true, role: 'owner' }],
  [/^\/projects\/[^/]+\/papers$/, [paper]],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/projects\/[^/]+\/credits$/, { wallet: 20, start_reserve: 5 }],
  [/^\/jobs\/([^/]+)$/, (id) => sessionOf(id)],
  [/^\/jobs\/[^/]+\/transcript$/, { events: [], usage: null, live: false }],
  [/^\/(public|shared)$/, []],
]

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
    if (u.origin === BASE) {
      if (u.pathname.endsWith('/public') && req.method() === 'POST') {
        return req.respond({ status: 409, contentType: 'application/json', body: JSON.stringify(refusal) })
      }
      if (u.pathname === '/me/profile' && req.method() === 'PATCH') {
        return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(profile) })
      }
      // The flagged session's manifest: the review is what the verdict pane has
      // to say out loud, not just colour a pill with.
      if (/^\/jobs\/[^/]+\/reproduction$/.test(u.pathname)) {
        const id = u.pathname.split('/')[2]
        if (id === UNFINISHED) return req.respond({ status: 404, contentType: 'application/json', body: JSON.stringify({ error: 'not_found' }) })
        return req.respond({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ id, repo: `open-hai/${id}`, title: 't', verdict: 'passed', review: 'excluded', can_publish: false }),
        })
      }
      if (/^\/jobs\/[^/]+\/verdict$/.test(u.pathname)) {
        return req.respond({ status: 404, contentType: 'application/json', body: JSON.stringify({ error: 'no_findings' }) })
      }
      if (u.pathname.endsWith('/artifacts')) return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify({ files: [] }) })
      const hit = routes.find(([re]) => re.test(u.pathname))
      if (hit) {
        const m = u.pathname.match(hit[0])
        const body = typeof hit[1] === 'function' ? hit[1](m[1]) : hit[1]
        return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
      }
      if (u.pathname.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    }
    req.continue()
  })

  const text = (sel) => page.$eval(sel, (el) => el.textContent.replace(/\s+/g, ' ').trim())

  /* ── Walk the wizard to the refusal ── */

  await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('[data-test="publish-project"]', { timeout: 15000 })
  await page.click('[data-test="publish-project"]')
  await page.waitForSelector('.modal', { timeout: 5000 })
  // Three steps: what becomes public, who stands behind it, responsibility. The
  // profile is prefilled and publishable, so both Continues are live.
  for (let i = 0; i < 2; i++) {
    await page.evaluate(() => [...document.querySelectorAll('.modal button')].find((b) => /Continue/i.test(b.textContent))?.click())
    await new Promise((r) => setTimeout(r, 200))
  }
  await page.evaluate(() => document.querySelector('.modal .check, .modal [role="checkbox"]')?.click())
  await new Promise((r) => setTimeout(r, 150))
  await page.evaluate(() => [...document.querySelectorAll('.modal button')].find((b) => /Sign off/i.test(b.textContent))?.click())

  await page.waitForSelector('[data-test="publish-blockers"]', { timeout: 8000 })
  await new Promise((r) => setTimeout(r, 200))

  const refused = await page.evaluate(() => ({
    message: document.querySelector('.modal .err')?.textContent.replace(/\s+/g, ' ').trim() ?? '',
    rows: [...document.querySelectorAll('.blocker-row')].map((r) => r.textContent.replace(/\s+/g, ' ').trim()),
  }))

  // Both sessions, not the first one: an owner who fixes one must not walk into
  // the same refusal with nothing more to go on.
  if (refused.rows.length !== 2) fail(`the refusal named ${refused.rows.length} sessions, want both`)
  if (!refused.rows.some((r) => /cannot be redistributed/i.test(r))) fail(`no row explains the flagged tree: ${JSON.stringify(refused.rows)}`)
  if (!refused.rows.some((r) => /never finished/i.test(r))) fail(`no row explains the unfinished run: ${JSON.stringify(refused.rows)}`)
  // And the headline is about content, because one of them is.
  if (!/redistribut/i.test(refused.message)) fail(`the refusal message does not name the reason: ${refused.message}`)

  /* ── Follow a blocker to the session it names ── */

  await page.evaluate(() => document.querySelectorAll('[data-test="open-blocker"]')[0]?.click())
  await page.waitForSelector('[data-test="run-record"]', { timeout: 8000 })
  await new Promise((r) => setTimeout(r, 200))

  const landed = page.url()
  if (!landed.includes(`/s/${FLAGGED}/verdict`)) fail(`opening the flagged blocker landed at ${landed}`)

  // The place it landed says what is wrong, in words. A pill reading "excluded"
  // beside "Redistribution" is a field name, not an explanation.
  const blocks = await text('[data-test="blocks-publishing"]').catch(() => '')
  if (!/blocks publishing/i.test(blocks)) fail(`the flagged session does not say it blocks publishing: ${blocks}`)
  if (!/third-party data/i.test(blocks)) fail(`the flagged session does not say what was found: ${blocks}`)
  const warn = await page.$eval('[data-test="blocks-publishing"]', (el) => getComputedStyle(el).color)
  if (!warn || warn === 'rgb(0, 0, 0)') fail(`the blocking notice is not toned: ${warn}`)

  if (!process.exitCode) console.log('PASS: a refused publish names its blockers and opens them')
} catch (err) {
  fail(err.message)
} finally {
  if (browser) await browser.close()
  vite.kill()
}
