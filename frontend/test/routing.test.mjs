// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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
const JOB = 'job-a'
const JOB_B = 'job-b'
const project = { id: PROJECT, owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }

// Two papers, each with its own published session (012, 015). This is the shape
// the reported defect needed: the project's artifacts and verdict tabs picked
// the newest session with a repository and showed it under the project's own
// name, so paper B hid paper A entirely.
const run = (id, key, ref, repo) => ({
  id,
  kind: 'replicate',
  ref,
  phase: 'done',
  paper_key: key,
  repo_name: repo,
  created_at: at(0),
  phase_at: { submitted: at(0), done: at(9000) },
})
const job = run(JOB, 'pap-a', 'arxiv:2301.03711', 'open-hai/walk-this-beam')
const jobB = run(JOB_B, 'pap-b', 'arxiv:2401.00002', 'open-hai/retention-under-load')
const paper = { key: 'pap-a', ref: job.ref, title: 'Walk This Beam', sessions: 1 }
const paperB = { key: 'pap-b', ref: jobB.ref, title: 'Retention Under Load', sessions: 1 }

// Each session's own repository and its own grade. If any screen reads one
// session's files under the other's paper, these two lists are what says so.
const filesOf = { [JOB]: ['README.md', 'beam-results.csv'], [JOB_B]: ['README.md', 'retention-results.csv'] }
// The grader's own vocabulary (internal/provider/enum.go), so the tones and
// icons under test are the ones a real manifest produces. On screen a grade is
// read the way a person would say it, not as the snake_case identifier the
// record keeps.
const verdictOf = { [JOB]: 'passed', [JOB_B]: 'needs_revision' }
const shownAs = { passed: 'Passed', needs_revision: 'Needs revision' }

// Each session's own findings, so the project's verdict list can be asked to
// keep one paper's verdict off another paper's row. The two words differ from
// the grades above on purpose: a grade judges the run against the rubric, a
// verdict judges the paper, and this test now asserts both are shown where they
// belong rather than one standing in for the other.
const findingsOf = {
  [JOB]: {
    schema: 1,
    verdict: 'reproducible',
    summary: 'It reproduces.',
    graded_at: at(0),
    components: { verified: 3, partial: 0, blocked: 0, total: 3 },
    boundary: [{ claim: 'a', loop: 'inner', outcome: 'verified', evidence: 'e', citation: '§1' }],
  },
  [JOB_B]: {
    schema: 1,
    verdict: 'not_reproducible',
    summary: 'It does not.',
    graded_at: at(0),
    components: { verified: 0, partial: 0, blocked: 2, total: 2 },
    boundary: [{ claim: 'b', loop: 'inner', outcome: 'blocked', evidence: 'blocked', citation: '§2' }],
  },
}
const verdictShownAs = { [JOB]: 'Reproducible', [JOB_B]: 'Not reproducible' }

// A second project, so the screen can be asked to show one project after
// another: every piece of its state belongs to one project, and it is remounted
// per id rather than reused.
const OTHER = 'proj-2'
const titleOf = (id) => (id === OTHER ? 'Retention Under Load' : project.title)
// How long the second project takes to answer. Long enough to look at the screen
// while it is loading, short enough not to pad the run.
const SLOW_MS = 700
let meUnavailable = false

const routes = [
  [/^\/me$/, { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { wallet: 20, start_reserve: 5 } }],
  [/^\/me\/profile$/, {}],
  [/^\/projects$/, [{ ...project, session_count: 1, running: 0, latest_phase: 'done' }]],
  // Echoes back whichever project was asked for, so the two are told apart by
  // what the screen renders rather than by the request that fetched it.
  [
    /^\/projects\/[^/]+$/,
    (id) => ({ project: { ...project, id, title: titleOf(id) }, sessions: id === OTHER ? [] : [jobB, job], may_submit: true }),
  ],
  [/^\/projects\/([^/]+)\/papers$/, (id) => (id === OTHER ? [] : [paper, paperB])],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/jobs\/([^/]+)$/, (id) => (id === JOB_B ? jobB : job)],
  [/^\/jobs\/[^/]+\/transcript$/, { events: [{ role: 'user', kind: 'message', content: 'Reproduce this paper.', time: at(0) }], usage: null, live: false }],
  [
    /^\/jobs\/([^/]+)\/reproduction$/,
    (id) => ({ id, repo: `open-hai/${id}`, title: 't', verdict: verdictOf[id] ?? 'partial', review: 'clear', can_publish: false }),
  ],
  [/^\/jobs\/([^/]+)\/artifacts$/, (id) => ({ files: filesOf[id] ?? [] })],
  [/^\/jobs\/([^/]+)\/verdict$/, (id) => findingsOf[id] ?? null],
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
      if (u.pathname === '/me' && meUnavailable) {
        return req.respond({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: 'unavailable' }) })
      }
      // One committed file, named after the session it belongs to — so a pane
      // showing the wrong session's file is visible in the rendered text, not
      // only in the list beside it.
      if (u.pathname.endsWith('/artifact')) {
        const id = u.pathname.split('/')[2]
        return req.respond({ status: 200, contentType: 'text/plain', body: `# ${id} ${u.searchParams.get('path')}` })
      }
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
      // Both screens carry a tab strip now (015), so a session is told by its
      // own container rather than by the transcript happening to be open.
      const tabOn = (root) => root.querySelector('.tab.on')?.textContent?.trim() ?? '?'
      const session = document.querySelector('.session-head .detail-tabs')
      if (session) return 'session:' + tabOn(session)
      if (document.querySelector('.detail-tabs')) return 'project:' + tabOn(document.querySelector('.detail-tabs'))
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

  // The reported defect, end to end (015). Two papers, each with a published
  // session: the artifacts of each must be the artifacts of *that* run. The old
  // project-level tab showed whichever session was newest, under an address that
  // named the project — so paper A's reproduction was in the product and
  // reachable by nothing.
  const filesShown = () => page.evaluate(() => [...document.querySelectorAll('.file-row .row-title')].map((e) => e.textContent.trim()))
  for (const [id, want] of [
    [JOB, filesOf[JOB]],
    [JOB_B, filesOf[JOB_B]],
  ]) {
    await page.goto(`${BASE}/p/${PROJECT}/s/${id}/artifacts`, { waitUntil: 'networkidle0' })
    await page.waitForSelector('.file-row')
    const got = await filesShown()
    if (JSON.stringify(got) !== JSON.stringify(want)) fail(`session ${id} listed ${JSON.stringify(got)}, want ${JSON.stringify(want)}`)
    // And the pane renders that session's file, not a neighbour's.
    const body = await page.evaluate(() => document.querySelector('.markdown-body, .event-io')?.textContent ?? '')
    if (!body.includes(id)) fail(`the artifact pane of session ${id} rendered ${JSON.stringify(body.slice(0, 60))}`)
    if ((await screen()) !== 'session:Artifacts') fail(`${await path()} rendered ${await screen()}`)
  }

  // Each session's record is its own, too. The grade lives on the run record
  // rather than under a "Verdict" heading now — it is the rubric's judgement of
  // the run, not the paper's verdict — but the invariant 015 wrote this for is
  // unchanged: one session's pane must never show another's.
  for (const id of [JOB, JOB_B]) {
    await page.goto(`${BASE}/p/${PROJECT}/s/${id}/verdict`, { waitUntil: 'networkidle0' })
    await page.waitForSelector('[data-test="run-record"]')
    const said = await page.evaluate(() => document.querySelector('[data-test="run-record"]')?.textContent?.trim() ?? '')
    const want = shownAs[verdictOf[id]]
    if (!said.includes(want)) fail(`session ${id} showed the grade ${JSON.stringify(said)}, want ${want}`)
  }

  // The session's tabs are places: clicking one moves the address, and Back
  // returns to the pane it came from.
  await page.goto(`${BASE}/p/${PROJECT}/s/${JOB}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.tx-stream')
  await page.evaluate(() => [...document.querySelectorAll('.detail-tabs .tab')].find((b) => /artifacts/i.test(b.textContent))?.click())
  await new Promise((r) => setTimeout(r, 300))
  if ((await path()) !== `/p/${PROJECT}/s/${JOB}/artifacts`) fail(`the session artifacts tab left the address at ${await path()}`)
  await page.goBack({ waitUntil: 'domcontentloaded' })
  await new Promise((r) => setTimeout(r, 400))
  if ((await path()) !== `/p/${PROJECT}/s/${JOB}`) fail(`Back from a session tab went to ${await path()}`)

  // The project's own artifacts tab is gone: there is no project-wide repository
  // to browse. The address falls back to the project, as any stray segment does.
  await page.goto(`${BASE}/p/${PROJECT}/artifacts`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.detail-tabs')
  if ((await path()) !== `/p/${PROJECT}`) fail(`the retired project artifacts address left the reader at ${await path()}`)

  // The project's verdict tab reads across papers: one row per graded session,
  // each naming its paper.
  await page.goto(`${BASE}/p/${PROJECT}/verdict`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.verdict-cell')
  await new Promise((r) => setTimeout(r, 300))
  const board = await page.evaluate(() => [...document.querySelectorAll('.rows > .row')].map((r) => r.textContent))
  if (board.length !== 2) fail(`the project verdict view has ${board.length} rows, want one per graded session`)
  for (const p of [paper, paperB]) {
    if (!board.some((r) => r.includes(p.title))) fail(`the project verdict view does not name ${p.title}: ${JSON.stringify(board)}`)
  }
  // Each row carries its own paper's verdict, not the grade of the run that
  // produced it and not the other paper's verdict.
  for (const [id, want] of Object.entries(verdictShownAs)) {
    if (!board.some((r) => r.includes(want))) fail(`the project verdict view does not report ${id} as ${want}: ${JSON.stringify(board)}`)
  }
  // And a row opens that session's own verdict.
  await page.evaluate(() => document.querySelector('.rows > .row')?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
  await new Promise((r) => setTimeout(r, 400))
  if (!/\/s\/[^/]+\/verdict$/.test(await path())) fail(`a verdict row opened ${await path()}`)

  await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.detail-tabs')

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

  // An address we do not own says so, at the address that was asked for (042c).
  // It used to render the dashboard and rewrite the URL to `/` — a page nobody
  // asked for, and to a crawler an unbounded set of URLs answering 200 with
  // identical content. The tab says it too, and Back still works.
  await page.goto(`${BASE}/nonsense`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('[data-test="not-found"]')
  if ((await path()) !== '/nonsense') fail(`a miss corrected the address to ${await path()}, want /nonsense`)
  if (!/no page at this address/i.test(await page.title())) fail(`a miss left the tab titled ${JSON.stringify(await page.title())}`)
  // A reload resolves to the same screen rather than somewhere else.
  await page.reload({ waitUntil: 'networkidle0' })
  await page.waitForSelector('[data-test="not-found"]')
  if ((await path()) !== '/nonsense') fail(`reloading a miss went to ${await path()}`)
  // And it is a way back, not a dead end.
  await page.evaluate(() => [...document.querySelectorAll('[data-test="not-found"] button')][0]?.click())
  await page.waitForSelector('.row')
  if ((await path()) !== '/') fail(`leaving the not-found screen went to ${await path()}`)

  // The tab follows the screen after the first paint (042a): the head the server
  // wrote is for the address the reader arrived at, and nothing on the server is
  // involved from the second screen onwards.
  await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.detail-tabs')
  if (!(await page.title()).startsWith(project.title)) fail(`a project left the tab titled ${JSON.stringify(await page.title())}`)
  const canonical = () => page.evaluate(() => document.querySelector('link[rel="canonical"]')?.getAttribute('href') ?? '')
  if (!(await canonical()).endsWith(`/p/${PROJECT}`)) fail(`a project's canonical is ${await canonical()}`)
  // A tab is a view of the record, so the canonical stays the record's own.
  await page.evaluate(() => [...document.querySelectorAll('.detail-tabs .tab')].find((b) => /verdict/i.test(b.textContent))?.click())
  await new Promise((r) => setTimeout(r, 300))
  if (!(await canonical()).endsWith(`/p/${PROJECT}`)) fail(`a project tab moved the canonical to ${await canonical()}`)
  await page.goto(`${BASE}/wallet`, { waitUntil: 'networkidle0' })
  await new Promise((r) => setTimeout(r, 300))
  if (!/wallet/i.test(await page.title())) fail(`the wallet left the tab titled ${JSON.stringify(await page.title())}`)

  // Failure to read /me is an outage, not evidence that the person signed out.
  // Keep the deep link, offer a retry, and never send them into a login loop.
  meUnavailable = true
  await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.auth-card')
  const unavailable = await page.evaluate(() => ({
    text: document.querySelector('.auth-card')?.textContent ?? '',
    login: !!document.querySelector('.auth-card a[href^="/login"]'),
  }))
  if (unavailable.login) fail('a /me outage rendered Sign in')
  if (!/unavailable/i.test(unavailable.text)) fail(`a /me outage rendered no service error: ${JSON.stringify(unavailable.text)}`)
  if ((await path()) !== `/p/${PROJECT}`) fail(`a /me outage discarded the deep link: ${await path()}`)
  meUnavailable = false
  await page.evaluate(() => [...document.querySelectorAll('.auth-card button')].find((b) => /try again/i.test(b.textContent))?.click())
  await page.waitForSelector('.detail-tabs')
  if ((await path()) !== `/p/${PROJECT}`) fail(`retrying /me navigated away from the deep link: ${await path()}`)

  if (!process.exitCode) console.log(`ok: routes survive browser history and an auth outage stays distinct from sign-out`)
} catch (e) {
  fail(e.message)
} finally {
  await browser?.close()
  vite.kill()
}
