// A browser regression test for the app on a phone (030).
//
// What it guards: `.shell` is a flex row and `.rail` was `width: 236px;
// flex: none` with no shell-level media query anywhere in the stylesheet, so a
// 390px phone got 154px of content on every screen the app has. The topbar's
// controls ran to x=579 and could not be tapped, and body copy set three words
// to the line.
//
// None of that tripped the usual check. `.screen` is `overflow-y: auto`, which
// computes `overflow-x` to `auto` as well, so the spill was contained in a pane
// that scrolls sideways rather than in the document — `documentElement.
// scrollWidth` read clean while nothing was legible. This test therefore asks
// *both* questions: nothing past the right edge, and no pane quietly scrolling
// sideways to hide it.
//
// The drawer is the other half. Whether it is open is state, and whether the
// rail is folded is a stored preference — `sidebar.ts` is explicit that a width
// the operator chose is never overridden by one the window happened to be — so
// the two are asserted apart: the drawer must not read or write `sidebar`, and
// a stored fold must not survive into a drawer that has room for its labels.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5241
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

const PHONE = { width: 390, height: 844, isMobile: true, hasTouch: true }
const DESKTOP = { width: 1440, height: 900 }

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()

const PROJECT = 'proj-1'
const JOB = 'job-a'
const HANDLE = 'ada'

// Deliberately long: a title, an institution and an address that all fit at
// 1440 and none of which fit at 390 are what this test is about.
const TITLE = 'Retention Under Load: A Within-Subjects Study of Prolonged Attention'
const ADDRESS = 'a-rather-long-address@university-of-somewhere.example.edu'

const project = { id: PROJECT, owner: 'p', title: TITLE, visibility: 'public', created_at: at(0), modified_at: at(9000) }
const listed = { ...project, session_count: 3, running: 1, latest_phase: 'running' }
const job = {
  id: JOB,
  kind: 'replicate',
  ref: 'arxiv:2301.03711',
  phase: 'done',
  paper_key: 'pap-a',
  repo_name: 'open-hai/retention-under-load',
  created_at: at(0),
  phase_at: { submitted: at(0), done: at(9000) },
}

const routes = [
  [/^\/me$/, { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { wallet: 20, start_reserve: 5 } }],
  [/^\/me\/profile$/, { handle: HANDLE, display_name: 'Ada Lovelace', institution: 'University of Somewhere', contact_email: ADDRESS }],
  [
    /^\/me\/credits$/,
    {
      balance: 12_500_000,
      spread_bps: 500,
      spread_fixed_micro: 300_000,
      min_topup_micro: 5_000_000,
      entries: [
        { id: 'e1', holder: 'p', amount: 20_000_000, kind: 'topup', actor: 'p', created_at: at(0) },
        { id: 'e2', holder: 'p', amount: -5_000_000, kind: 'allocate', project_id: PROJECT, project_name: TITLE, project_alive: true, actor: 'p', created_at: at(1000) },
      ],
    },
  ],
  [/^\/projects$/, [listed]],
  [/^\/projects\/trash$/, [{ ...listed, id: 'proj-2', running: 0, latest_phase: 'done' }]],
  [/^\/projects\/[^/]+\/papers$/, [{ key: 'pap-a', ref: job.ref, title: TITLE, sessions: 1 }]],
  [/^\/projects\/[^/]+\/members$/, [{ email: ADDRESS, role: 2, bound: true }]],
  [
    /^\/projects\/[^/]+$/,
    (id) => ({
      project: { ...project, id },
      sessions: [job],
      may_submit: true,
      signer_handle: HANDLE,
      credits: { balance: 5_000_000, available: 4_000_000, start_reserve: 1_000_000, may_reclaim: true },
    }),
  ],
  [
    /^\/jobs\/[^/]+\/transcript$/,
    {
      events: [
        { role: 'user', kind: 'message', content: 'Reproduce this paper.', time: at(0) },
        { role: 'assistant', kind: 'tool_use', name: 'bash', content: 'python analysis/recompute_attention_decline.py --input data/trials.csv --bootstrap 10000', time: at(2000) },
      ],
      usage: { InputTokens: 12345, OutputTokens: 6789, CachedInputTokens: 4096 },
      live: false,
    },
  ],
  [/^\/jobs\/[^/]+\/reproduction$/, (id) => ({ id, repo: 'open-hai/retention-under-load', title: TITLE, verdict: 'passed', review: 'clear', can_publish: true })],
  [/^\/jobs\/[^/]+\/artifacts$/, { files: ['README.md', 'analysis/recompute_attention_decline.py'] }],
  [
    /^\/jobs\/[^/]+\/verdict$/,
    {
      schema: 1,
      verdict: 'reproducible',
      summary: 'The inner loop reproduces at the reported magnitude.',
      graded_at: at(9000),
      components: { verified: 3, partial: 1, blocked: 0, total: 4 },
      boundary: [
        { claim: 'The attention score declines monotonically over the session.', loop: 'inner', outcome: 'verified', evidence: 'Recomputed from the released trial table.', citation: '§4.2' },
        { claim: 'Participants self-report lower engagement after 40 minutes.', loop: 'outer', outcome: 'blocked', evidence: 'Requires people; never simulated.', citation: '§5.1' },
      ],
    },
  ],
  [/^\/jobs\/[^/]+$/, job],
  [/^\/(public|shared)$/, [{ ...listed, running: 0, latest_phase: 'done' }]],
  [
    /^\/profiles\/[^/]+$/,
    { handle: HANDLE, display_name: 'Ada Lovelace', institution: 'University of Somewhere, Department of Rather Long Names', contact_email: ADDRESS, projects: [{ id: PROJECT, title: TITLE, signed_off_at: at(9000) }] },
  ],
  [/^\/admin\/access$/, [{ email: ADDRESS, tier: 'user', wallet: -1_500_000, created_at: at(0), bound: true, frozen: true }]],
  [/^\/admin\/solvency$/, { outstanding_micro: 1_500_000 }],
  [/^\/admin\//, {}],
]

// Every screen inside the shell. The landing has its own test (017) and is a
// different layout, so it is not swept here.
const SCREENS = [
  ['projects', '/'],
  ['registry', '/registry'],
  ['trash', '/trash'],
  ['submit', '/new'],
  ['wallet', '/wallet'],
  ['settings', '/settings'],
  ['settings/profile', '/settings/profile'],
  ['console', '/console'],
  ['profile', `/u/${HANDLE}`],
  ['project', `/p/${PROJECT}`],
  ['project/verdict', `/p/${PROJECT}/verdict`],
  ['project/people', `/p/${PROJECT}/people`],
  ['session', `/p/${PROJECT}/s/${JOB}`],
  ['session/verdict', `/p/${PROJECT}/s/${JOB}/verdict`],
  ['session/artifacts', `/p/${PROJECT}/s/${JOB}/artifacts`],
]

const vite = spawn('npx', ['vite', '--port', String(PORT), '--strictPort'], { stdio: 'ignore' })
let browser

// open serves the mocked API and returns a page at `path`. `sidebar` seeds the
// stored fold, which the phone must ignore and the desktop must obey.
const open = async (path, { viewport = PHONE, sidebar } = {}) => {
  const page = await browser.newPage()
  await page.setViewport(viewport)
  // The fold is always written, never left over: localStorage is shared by
  // every page this browser opens, and one test seeding `folded` would
  // otherwise decide what the next one measures.
  await page.evaluateOnNewDocument(
    (s) => {
      localStorage.setItem('theme', 'light')
      localStorage.setItem('sidebar', s)
    },
    sidebar ?? 'open',
  )
  await page.setRequestInterception(true)
  page.on('request', (req) => {
    const u = new URL(req.url())
    if (u.origin === BASE) {
      if (u.pathname.endsWith('/artifact')) return req.respond({ status: 200, contentType: 'text/plain', body: '# a file\n\nOne line.\n' })
      if (u.pathname.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
      const hit = routes.find(([re]) => re.test(u.pathname))
      if (hit) {
        const body = typeof hit[1] === 'function' ? hit[1](u.pathname.split('/')[2]) : hit[1]
        return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
      }
    }
    req.continue()
  })
  await page.goto(BASE + path, { waitUntil: 'networkidle0' })
  return page
}

// spill reports what a phone cannot see: anything painted past the right edge,
// and any pane hiding that fact behind its own sideways scroll.
//
// The closed drawer is excluded — it is off the left edge on purpose — and so
// is anything inside a container this app *means* to scroll sideways. That
// allow-list is named rather than derived, because `.screen` computes to
// `overflow-x: auto` too and deriving it would excuse the whole bug.
const spill = (page, vw) =>
  page.evaluate((w) => {
    const nav = document.querySelector('.rail')
    const over = []
    for (const e of document.querySelectorAll('body *')) {
      if (nav && (e === nav || nav.contains(e))) continue
      const r = e.getBoundingClientRect()
      if (r.width === 0 && r.height === 0) continue
      if (r.right <= w + 1 && r.left >= -1) continue
      const cs = getComputedStyle(e)
      if (cs.position === 'fixed' || cs.overflowX === 'auto' || cs.overflowX === 'scroll' || cs.overflowX === 'hidden') continue
      if (e.closest('.grid-table-wrap, .rate-card, pre, code')) continue
      if (e.parentElement && over.some((o) => o.el === e.parentElement)) continue
      over.push({ el: e, sel: e.tagName.toLowerCase() + (typeof e.className === 'string' && e.className ? '.' + e.className.trim().split(/\s+/).join('.') : ''), right: Math.round(r.right) })
    }
    const panes = []
    for (const e of document.querySelectorAll('.screen, .tx-body, .main, .topbar')) {
      if (e.scrollWidth > e.clientWidth + 1) panes.push(`${e.className.split(/\s+/)[0]} (${e.scrollWidth} > ${e.clientWidth})`)
    }
    return {
      doc: document.documentElement.scrollWidth - window.innerWidth,
      panes,
      over: over.slice(0, 4).map(({ sel, right }) => `${sel.slice(0, 70)} ends at ${right}`),
    }
  }, vw)

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

  /* ── The rail leaves the flow, on every screen ── */

  for (const [name, path] of SCREENS) {
    const page = await open(path)
    try {
      // A session is its own full-height layout rather than a `.screen` pane.
      await page.waitForSelector('.screen, .tx-body', { timeout: 15000 })
    } catch {
      fail(`${name}: ${path} rendered no screen`)
      await page.close()
      continue
    }
    // Give the entry animation its 0.3s: a card mid-fade is mid-transform, and
    // a transformed box measures where it is, not where it lands.
    await new Promise((r) => setTimeout(r, 400))

    // The profile is a page for strangers and renders outside the shell.
    const shell = await page.evaluate(() => !!document.querySelector('.shell'))
    if (shell) {
      const w = await page.evaluate(() => ({
        main: Math.round(document.querySelector('.main').getBoundingClientRect().width),
        railInFlow: document.querySelector('.rail').getBoundingClientRect().right > 0,
      }))
      if (w.main !== PHONE.width) fail(`${name}: the content column is ${w.main}px of a ${PHONE.width}px phone; the rail is still taking its 236`)
      if (w.railInFlow) fail(`${name}: the rail is still on screen with the drawer closed`)
    }

    const s = await spill(page, PHONE.width)
    if (s.doc > 1) fail(`${name}: the document scrolls ${s.doc}px sideways`)
    for (const p of s.panes) fail(`${name}: ${p} scrolls sideways — a pane hiding its own overflow is what made this invisible`)
    for (const o of s.over) fail(`${name}: ${o}, past the ${PHONE.width}px edge`)
    await page.close()
  }

  /* ── The drawer opens, and everything closes it ── */

  {
    const page = await open('/')
    await page.waitForSelector('.rail-open-btn')
    const isOpen = () => page.evaluate(() => document.querySelector('.rail').classList.contains('rail-open'))
    const railLeft = () => page.evaluate(() => Math.round(document.querySelector('.rail').getBoundingClientRect().left))

    if (await isOpen()) fail('the drawer is open on arrival; a phone must land on the screen the link named')
    if (!(await page.evaluate(() => getComputedStyle(document.querySelector('.rail-open-btn')).display !== 'none'))) {
      fail('the phone has no control that opens the drawer')
    }

    const openIt = async () => {
      await page.click('.rail-open-btn')
      await new Promise((r) => setTimeout(r, 300))
      if (!(await isOpen())) fail('clicking the hamburger did not open the drawer')
      if ((await railLeft()) !== 0) fail(`the open drawer sits at x=${await railLeft()}, not against the edge`)
    }
    const shut = async (how) => {
      await new Promise((r) => setTimeout(r, 300))
      if (await isOpen()) fail(`${how} did not close the drawer`)
    }

    await openIt()
    // Beside the drawer, not through it: the scrim spans the viewport and its
    // centre is behind the 264px rail, so clicking the element's middle would
    // land on the rail.
    await page.mouse.click(340, 500)
    await shut('a tap on the scrim')

    await openIt()
    await page.keyboard.press('Escape')
    await shut('Escape')

    await openIt()
    await page.click('.rail-close-btn')
    await shut('the drawer’s own close control')

    // Opening a destination dismisses it: the drawer is covering the screen it
    // is navigating to.
    await openIt()
    await page.evaluate(() => [...document.querySelectorAll('.rail-nav .nav-btn')].find((b) => /wallet/i.test(b.textContent)).click())
    await shut('opening a destination')
    if ((await page.evaluate(() => location.pathname)) !== '/wallet') fail('opening a destination from the drawer did not navigate')

    await page.close()
  }

  /* ── The fold is a desktop preference, and the drawer is not it ── */

  {
    // A rail left folded on a laptop must not reach the phone as five unlabelled
    // icons: the drawer has room for the words.
    const page = await open('/', { sidebar: 'folded' })
    await page.waitForSelector('.rail-open-btn')
    await page.click('.rail-open-btn')
    await new Promise((r) => setTimeout(r, 300))
    const read = await page.evaluate(() => ({
      labels: [...document.querySelectorAll('.rail-nav .nav-label')].filter((e) => getComputedStyle(e).display !== 'none').length,
      width: Math.round(document.querySelector('.rail').getBoundingClientRect().width),
      stored: localStorage.getItem('sidebar'),
    }))
    if (read.labels < 3) fail(`the open drawer names ${read.labels} of its destinations; a touch screen has no hover to read the rest`)
    if (read.width < 200) fail(`the open drawer is ${read.width}px wide — that is the folded rail, not a drawer`)
    if (read.stored !== 'folded') fail(`opening the drawer rewrote the stored fold to ${read.stored}; it is a preference this width must not touch`)
    await page.close()
  }

  /* ── Every control in the topbar is reachable ── */

  {
    for (const path of ['/', `/p/${PROJECT}/s/${JOB}`]) {
      const page = await open(path)
      await page.waitForSelector('.topbar-actions')
      await new Promise((r) => setTimeout(r, 300))
      const outside = await page.evaluate((w) => {
        const bad = []
        for (const e of document.querySelectorAll('.topbar button, .topbar a')) {
          const r = e.getBoundingClientRect()
          if (r.width === 0) continue
          if (r.right > w + 1 || r.left < -1) bad.push(`${e.getAttribute('aria-label') || e.textContent.trim().slice(0, 24)} [${Math.round(r.left)}..${Math.round(r.right)}]`)
        }
        return bad
      }, PHONE.width)
      for (const b of outside) fail(`at ${path} the topbar control ${b} is off a ${PHONE.width}px screen`)
      await page.close()
    }
  }

  /* ── The panes that were columns are rows ── */

  {
    const page = await open(`/p/${PROJECT}/s/${JOB}`)
    await page.waitForSelector('.tx-body')
    const cols = await page.evaluate(() => getComputedStyle(document.querySelector('.tx-body')).gridTemplateColumns.split(' ').length)
    if (cols !== 1) fail(`the transcript is ${cols} columns at 390px`)
    await page.close()
  }

  {
    const page = await open('/')
    await page.waitForSelector('.project-list .row')
    // A paper title is what a row is. Squeezed into a 1fr track beside five
    // fixed ones it had ~120px; on its own line it has the card.
    const title = await page.evaluate(() => Math.round(document.querySelector('.project-list .row .row-main').getBoundingClientRect().width))
    if (title < 280) fail(`a project's title cell is ${title}px wide; the row is still trying to be a table`)
    await page.close()
  }

  /* ── Desktop is untouched ── */

  {
    const page = await open('/', { viewport: DESKTOP })
    await page.waitForSelector('.rail')
    const desk = await page.evaluate(() => ({
      rail: Math.round(document.querySelector('.rail').getBoundingClientRect().width),
      left: Math.round(document.querySelector('.rail').getBoundingClientRect().left),
      hamburger: getComputedStyle(document.querySelector('.rail-open-btn')).display,
      close: getComputedStyle(document.querySelector('.rail-close-btn')).display,
      fold: getComputedStyle(document.querySelector('.rail-fold-btn')).display,
      scrim: !!document.querySelector('.rail-scrim'),
      label: !!document.querySelector('.btn-topbar .btn-label')?.offsetWidth,
    }))
    if (desk.rail !== 236) fail(`at ${DESKTOP.width}px the rail is ${desk.rail}px, want the 236 it has always been`)
    if (desk.left !== 0) fail('the desktop rail left the flow')
    if (desk.hamburger !== 'none') fail('the hamburger is showing on a desktop that has its rail')
    if (desk.close !== 'none') fail('the drawer’s close control is showing on a desktop')
    if (desk.fold === 'none') fail('the desktop lost its fold control')
    if (desk.scrim) fail('a scrim is in the desktop DOM')
    if (!desk.label) fail('the New project button lost its label on a desktop')

    // And the fold still folds, and still persists.
    await page.click('.rail-fold-btn')
    await new Promise((r) => setTimeout(r, 300))
    const folded = await page.evaluate(() => ({ w: Math.round(document.querySelector('.rail').getBoundingClientRect().width), stored: localStorage.getItem('sidebar') }))
    if (folded.w > 80) fail(`folding the desktop rail left it ${folded.w}px wide`)
    if (folded.stored !== 'folded') fail(`the fold was not stored (sidebar=${folded.stored})`)
    await page.close()
  }

  if (!process.exitCode) console.log('PASS: mobile')
} catch (err) {
  fail(err.message)
} finally {
  if (browser) await browser.close()
  vite.kill()
}
