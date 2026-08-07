// A browser regression test for the app's addresses (013).
//
// The bug it guards: navigation used to be one useState and the address bar never
// moved, so a reload started over at the dashboard, no screen could be linked to,
// and Back left the app. Every assertion here is about the browser's own state —
// the location after a click, the screen after a reload, the screen after Back —
// which is exactly what a unit test of pathOf/viewOf cannot reach.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5197
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

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()

const PROJECT = 'proj-1'
const JOB = 'job-1'
const project = { id: PROJECT, owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }
const job = { id: JOB, kind: 'replicate', ref: 'arxiv:2301.03711', phase: 'done', created_at: at(0), phase_at: { submitted: at(0), done: at(9000) } }
const paper = { key: 'pap-1', ref: 'arxiv:2301.03711', title: 'Walk This Beam', sessions: 1 }

// A second project, so the screen can be asked to show one project after
// another: every piece of its state belongs to one project, and it is remounted
// per id rather than reused.
const OTHER = 'proj-2'
const titleOf = (id) => (id === OTHER ? 'Retention Under Load' : project.title)
// How long the second project takes to answer. Long enough to look at the screen
// while it is loading, short enough not to pad the run.
const SLOW_MS = 700

const routes = [
  [/^\/me$/, { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { wallet: 20, start_reserve: 5 } }],
  [/^\/me\/profile$/, {}],
  [/^\/projects$/, [{ ...project, session_count: 1, running: 0, latest_phase: 'done' }]],
  // Echoes back whichever project was asked for, so the two are told apart by
  // what the screen renders rather than by the request that fetched it.
  [
    /^\/projects\/[^/]+$/,
    (id) => ({ project: { ...project, id, title: titleOf(id) }, sessions: id === OTHER ? [] : [job], papers: [paper], may_submit: true }),
  ],
  [/^\/projects\/[^/]+\/papers$/, [paper]],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/jobs\/[^/]+$/, job],
  [/^\/jobs\/[^/]+\/transcript$/, { events: [{ role: 'user', kind: 'message', content: 'Reproduce this paper.', time: at(0) }], usage: null, live: false }],
  [/^\/jobs\/[^/]+\/reproduction$/, { id: JOB, repo: 'r', title: 't', verdict: 'partial', can_publish: false }],
  [/^\/jobs\/[^/]+\/artifacts$/, { files: [] }],
  [/^\/(public|shared)$/, []],
  [/^\/admin\//, []],
]

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
    if (u.origin === BASE) {
      const hit = routes.find(([re]) => re.test(u.pathname))
      if (hit) {
        // A route body may be a function of the id in the path, for the two
        // projects that have to answer differently.
        const body = typeof hit[1] === 'function' ? hit[1](u.pathname.split('/')[2]) : hit[1]
        const send = () => req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
        // The second project answers slowly on purpose: the defect a remount
        // prevents is only visible while a fetch is in flight, and an instant
        // mock closes that window before anything can look through it.
        if (u.pathname === `/projects/${OTHER}`) return void setTimeout(send, SLOW_MS)
        return send()
      }
      if (u.pathname.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    }
    req.continue()
  })

  const path = () => page.evaluate(() => location.pathname)
  // Which screen is up, read off the DOM rather than off the app's own state.
  const screen = () =>
    page.evaluate(() => {
      if (document.querySelector('.tx-stream')) return 'session'
      if (document.querySelector('.detail-tabs')) return 'project:' + (document.querySelector('.detail-tabs .tab.on')?.textContent?.trim() ?? '?')
      if (document.querySelector('.screen-title')) return 'screen:' + document.querySelector('.screen-title').textContent.trim()
      if (document.querySelector('.auth-card')) return 'signin'
      return 'unknown'
    })
  const at_ = async (label) => `${label}: ${await path()} -> ${await screen()}`

  // A click into a project, then into its session: each is a place, so each gets
  // an address.
  await page.goto(BASE, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.row')
  if ((await path()) !== '/') fail(`the dashboard is not at / (${await at_('dashboard')})`)
  await page.click('.row')
  await page.waitForSelector('.detail-tabs')
  if ((await path()) !== `/p/${PROJECT}`) fail(`opening a project left the address at ${await path()}, want /p/${PROJECT}`)

  // A tab is shareable state, so it is in the address too.
  await page.evaluate(() => [...document.querySelectorAll('.detail-tabs .tab')].find((b) => /verdict/i.test(b.textContent))?.click())
  await new Promise((r) => setTimeout(r, 250))
  if ((await path()) !== `/p/${PROJECT}/verdict`) fail(`the verdict tab left the address at ${await path()}`)

  // Reloading a tab's address comes back to that tab, which is the whole claim.
  await page.reload({ waitUntil: 'networkidle0' })
  await page.waitForSelector('.detail-tabs')
  if (!(await screen()).startsWith('project:')) fail(`a reload of ${await path()} did not land on the project (${await at_('reload')})`)
  const tabAfterReload = await page.evaluate(() => document.querySelector('.detail-tabs .tab.on')?.textContent?.trim() ?? '')
  if (!/verdict/i.test(tabAfterReload)) fail(`a reload of /p/${PROJECT}/verdict opened the ${tabAfterReload} tab`)

  // Into a session by its own link, cold: the transcript renders and the
  // breadcrumb fills in from what the screen loaded rather than from the click
  // that never happened.
  await page.goto(`${BASE}/p/${PROJECT}/s/${JOB}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.tx-stream')
  if ((await path()) !== `/p/${PROJECT}/s/${JOB}`) fail(`a session link was rewritten to ${await path()}`)
  await new Promise((r) => setTimeout(r, 400))
  const crumb = await page.evaluate(() => document.querySelector('.crumb')?.textContent ?? '')
  if (!crumb.includes(project.title)) fail(`the breadcrumb of a cold-loaded session does not name its project: ${JSON.stringify(crumb)}`)
  if (!crumb.includes(job.ref)) fail(`the breadcrumb of a cold-loaded session does not name its paper: ${JSON.stringify(crumb)}`)

  // Back and Forward walk in-app history rather than leaving the app.
  await page.goto(BASE, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.row')
  await page.click('.row')
  await page.waitForSelector('.detail-tabs')
  // A session row, not a paper row: both are `.row` on this screen, and only a
  // session's carries a phase badge.
  await page.evaluate(() => document.querySelector('.row:has(.phase-slot)')?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
  await page.waitForSelector('.tx-stream')
  const deep = await path()
  await page.goBack({ waitUntil: 'domcontentloaded' })
  await new Promise((r) => setTimeout(r, 400))
  if ((await path()) !== `/p/${PROJECT}`) fail(`Back from a session went to ${await path()}, want /p/${PROJECT}`)
  if (!(await screen()).startsWith('project:')) fail(`Back from a session rendered ${await screen()}`)
  await page.goForward({ waitUntil: 'domcontentloaded' })
  await new Promise((r) => setTimeout(r, 400))
  if ((await path()) !== deep) fail(`Forward went to ${await path()}, want ${deep}`)
  await page.goBack({ waitUntil: 'domcontentloaded' })
  await page.goBack({ waitUntil: 'domcontentloaded' })
  await new Promise((r) => setTimeout(r, 400))
  if ((await path()) !== '/') fail(`two Backs from a session ended at ${await path()}, want /`)

  // The rail's own destinations, each at the address the spec names. The registry
  // and the console are not at /public and /admin because the API owns both.
  for (const [selector, want] of [
    ['registry', '/registry'],
    ['console', '/console'],
  ]) {
    await page.evaluate(
      (which) => [...document.querySelectorAll('.nav-btn')].find((b) => new RegExp(which === 'registry' ? 'public|registry' : 'admin|console', 'i').test(b.textContent))?.click(),
      selector,
    )
    await new Promise((r) => setTimeout(r, 300))
    if ((await path()) !== want) fail(`the ${selector} is at ${await path()}, want ${want}`)
  }
  await page.evaluate(() => document.querySelector('.user-btn')?.click())
  await new Promise((r) => setTimeout(r, 300))
  if ((await path()) !== '/settings') fail(`settings is at ${await path()}, want /settings`)
  await page.evaluate(() => [...document.querySelectorAll('.topbar-actions .btn')].pop()?.click())
  await new Promise((r) => setTimeout(r, 300))
  if ((await path()) !== '/new') fail(`new project is at ${await path()}, want /new`)

  // One project screen after another. The screen is remounted per project, so
  // nothing of the first survives into the second — reusing the instance showed
  // the previous project's title and sessions until the fetch landed.
  await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.detail-tabs')
  const heading = () => page.evaluate(() => document.querySelector('.detail-title')?.textContent?.trim() ?? '')
  if ((await heading()) !== project.title) fail(`the first project is titled ${JSON.stringify(await heading())}`)
  await page.evaluate((id) => history.pushState(null, '', `/p/${id}`) || dispatchEvent(new PopStateEvent('popstate')), OTHER)
  // Mid-flight: nothing of the first project may still be on screen while the
  // second is loading — not its title, not its session row.
  await new Promise((r) => setTimeout(r, SLOW_MS / 2))
  const inFlight = await page.evaluate((was) => ({
    title: document.querySelector('.detail-title')?.textContent?.trim() ?? '',
    stale: !!document.querySelector('.row:has(.phase-slot)'),
    was,
  }), project.title)
  if (inFlight.title === inFlight.was) fail(`while the second project loads, the first project's title is still on screen: ${JSON.stringify(inFlight.title)}`)
  if (inFlight.stale) fail("while the second project loads, the first project's session row is still on screen")
  await new Promise((r) => setTimeout(r, SLOW_MS))
  if ((await heading()) !== titleOf(OTHER)) fail(`moving to a second project ended at ${JSON.stringify(await heading())}`)
  if (await page.evaluate(() => !!document.querySelector('.row:has(.phase-slot)'))) {
    fail("a second project with no sessions is showing the first project's session row")
  }

  // A mistyped tab keeps the project it names, rather than dropping the reader
  // back at the dashboard, and the address is corrected to the project's own.
  await page.goto(`${BASE}/p/${PROJECT}/nonsense`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.detail-tabs')
  if ((await path()) !== `/p/${PROJECT}`) fail(`a mistyped tab left the address at ${await path()}, want /p/${PROJECT}`)

  // An address we do not own is the app, at a corrected address — not a dead end,
  // and not a URL that keeps resolving somewhere else on every reload.
  await page.goto(`${BASE}/nonsense`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.row')
  if ((await path()) !== '/') fail(`an unknown address stayed at ${await path()}, want / (${await at_('stray')})`)

  if (!process.exitCode) console.log(`ok: every screen has an address; reload, Back and Forward all hold; a stray path is corrected to /`)
} catch (e) {
  fail(e.message)
} finally {
  await browser?.close()
  vite.kill()
}
