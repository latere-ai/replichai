// A browser regression test for the operator dashboard on a phone (006t).
//
// The bug it guards: the shell was drawn for a desk. A fixed 236px rail sat
// beside the content with no way to fold it, the top bar's actions pushed the
// breadcrumb off, and the transcript's three columns overflowed the viewport
// sideways. Below 820px the rail must become a drawer behind a hamburger, and
// no operator screen may scroll horizontally.
//
// The assertions are geometry, not pixels, so it needs a real layout engine: it
// drives Chrome over a Vite dev server with the API mocked, at a phone viewport.
// Run it with `bun run test:mobile`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5198
const BASE = `http://localhost:${PORT}`

// Naming a browser is a demand that it run: an unset PUPPETEER_EXECUTABLE_PATH
// skips on a machine without Chrome, but a set one that does not exist fails, so
// CI cannot quietly stop guarding the regression.
const asked = process.env.PUPPETEER_EXECUTABLE_PATH
const CHROME = asked
  ? asked
  : ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/usr/bin/google-chrome', '/usr/bin/chromium', '/opt/pw-browsers/chromium'].find((p) =>
      require('node:fs').existsSync(p),
    )

if (!CHROME || !require('node:fs').existsSync(CHROME)) {
  if (asked) {
    console.error(`FAIL: PUPPETEER_EXECUTABLE_PATH=${asked} does not exist`)
    process.exit(1)
  }
  console.log('skip: no Chrome found (set PUPPETEER_EXECUTABLE_PATH)')
  process.exit(0)
}

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()

// A short but complete session: a few event shapes so the transcript viewer has
// its filter rail, stream, and inspector all rendered when it is stacked.
const events = () =>
  [
    { role: 'user', kind: 'message', content: 'Reproduce this paper.' },
    { role: 'agent', kind: 'message', content: 'Resolving the reference.' },
    { role: 'agent', kind: 'tool_use', tool: 'bash', content: 'ls -la /work/repo' },
    { role: 'agent', kind: 'tool_result', content: 'total 48' },
    { role: 'agent', kind: 'thinking', content: '' },
  ].map((e, i) => ({ ...e, time: at(1000 + i * 1000) }))

const job = {
  id: 'job-1',
  kind: 'replicate',
  ref: 'https://doi.org/10.1145/3562939.3567802',
  phase: 'done',
  created_at: at(0),
  phase_at: { submitted: at(0), resolving: at(1_000), running: at(2_000), publishing: at(5_000), done: at(6_000) },
}
const project = { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }

const routes = [
  [/^\/me$/, { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { available: 1200 } }],
  [/^\/me\/profile$/, {}],
  [/^\/projects$/, [{ ...project, session_count: 1, running: 0, latest_phase: 'done' }]],
  [/^\/projects\/[^/]+$/, { project, sessions: [job], may_submit: true }],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/jobs\/[^/]+$/, job],
  [/^\/jobs\/[^/]+\/transcript$/, { events: events(), usage: { InputTokens: 176, OutputTokens: 1078, CachedInputTokens: 11625 }, live: false }],
  [/^\/jobs\/[^/]+\/reproduction$/, { id: 'job-1', repo: 'r', title: 't', verdict: 'partial', can_publish: false }],
  [/^\/jobs\/[^/]+\/artifacts$/, { files: [] }],
  [/^\/(public|shared)$/, []],
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
    if (i > 60) throw new Error('vite did not start')
    await new Promise((r) => setTimeout(r, 250))
  }

  browser = await puppeteer.launch({ executablePath: CHROME, headless: true, args: ['--no-sandbox'] })
  const page = await browser.newPage()
  // A common phone. The rail's own width (236px) is more than half of this, so a
  // rail that stays in the flow is the whole failure the drawer fixes.
  await page.setViewport({ width: 390, height: 844 })
  await page.setRequestInterception(true)
  page.on('request', (req) => {
    const u = new URL(req.url())
    if (u.origin === BASE) {
      const hit = routes.find(([re]) => re.test(u.pathname))
      if (hit) return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(hit[1]) })
      if (u.pathname.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    }
    req.continue()
  })

  await page.goto(BASE, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.row')

  const rect = (sel) => page.evaluate((s) => {
    const el = document.querySelector(s)
    if (!el) return null
    const r = el.getBoundingClientRect()
    const cs = getComputedStyle(el)
    return { left: r.left, right: r.right, width: r.width, display: cs.display, visible: cs.display !== 'none' }
  }, sel)
  const noHScroll = (where) =>
    page.evaluate(() => {
      const de = document.documentElement
      return { over: de.scrollWidth - window.innerWidth, inner: window.innerWidth, scroll: de.scrollWidth }
    }).then((m) => {
      // One pixel of slack for sub-pixel rounding.
      if (m.over > 1) fail(`${where}: document scrolls horizontally by ${m.over}px (scrollWidth ${m.scroll} > innerWidth ${m.inner})`)
    })

  // ── Phone shell: the rail is a drawer, closed by default ──
  await noHScroll('projects')
  const menu = await rect('.menu-btn')
  if (!menu?.visible) fail('the hamburger (.menu-btn) is not shown at a phone viewport')
  let railClosed = await rect('.rail')
  if (!railClosed) fail('no rail in the DOM')
  else if (railClosed.right > 1) fail(`the rail is not off-screen by default: its right edge is at ${railClosed.right}px`)

  // Opening slides the rail in and raises the scrim.
  await page.click('.menu-btn')
  await new Promise((r) => setTimeout(r, 400)) // the drawer transition
  let railOpen = await rect('.rail')
  if (railOpen.left < -1) fail(`opening did not bring the rail on-screen: left edge at ${railOpen.left}px`)
  if (railOpen.right < 200) fail(`the opened rail is too narrow to be the panel: right edge at ${railOpen.right}px`)
  const scrim = await page.evaluate(() => {
    const s = document.querySelector('.rail-scrim')
    if (!s) return null
    const cs = getComputedStyle(s)
    return { opacity: +cs.opacity, clicks: cs.pointerEvents }
  })
  if (!scrim || scrim.opacity < 0.5 || scrim.clicks === 'none') fail(`the scrim is not an active overlay when open: ${JSON.stringify(scrim)}`)

  // The scrim closes the drawer. Tap its visible part — the area to the right of
  // the drawer, since the rail sits above the scrim over its own width.
  await page.mouse.click(360, 400)
  await new Promise((r) => setTimeout(r, 400))
  railClosed = await rect('.rail')
  if (railClosed.right > 1) fail(`clicking the scrim did not close the drawer: right edge at ${railClosed.right}px`)

  // Navigating closes the drawer too: open it, tap a destination, and the rail
  // is gone while the view has changed.
  await page.click('.menu-btn')
  await new Promise((r) => setTimeout(r, 400))
  await page.evaluate(() => [...document.querySelectorAll('.rail .nav-btn')].find((b) => /public/i.test(b.textContent))?.click())
  await new Promise((r) => setTimeout(r, 400))
  railClosed = await rect('.rail')
  if (railClosed.right > 1) fail(`navigating did not close the drawer: right edge at ${railClosed.right}px`)
  await noHScroll('public')

  // ── Phone transcript: three columns become one, still no side-scroll ──
  await page.evaluate(() => [...document.querySelectorAll('.rail .nav-btn')].find((b) => /project/i.test(b.textContent))?.click())
  await page.waitForSelector('.row')
  const openFirstRow = () => page.evaluate(() => document.querySelector('.row')?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
  await openFirstRow() // projects -> project
  await page.waitForSelector('.row')
  await openFirstRow() // project -> session
  await page.waitForSelector('.tx-body')
  await new Promise((r) => setTimeout(r, 300))
  await noHScroll('session transcript')
  const body = await page.evaluate(() => {
    const b = document.querySelector('.tx-body')
    const stream = document.querySelector('.tx-stream')
    const side = document.querySelector('.tx-side')
    if (!b || !stream || !side) return null
    return {
      cols: getComputedStyle(b).gridTemplateColumns,
      streamW: stream.getBoundingClientRect().width,
      sideW: side.getBoundingClientRect().width,
      inner: window.innerWidth,
    }
  })
  if (!body) fail('the transcript viewer did not render its body/stream/side')
  // Stacked, not tiled: the stream and the filter rail each take (nearly) the
  // full width rather than sharing a row.
  else if (body.streamW < body.inner * 0.8) fail(`the stream is not full-width when stacked: ${Math.round(body.streamW)}px of ${body.inner}px`)
  else if (body.sideW < body.inner * 0.8) fail(`the filter rail is not full-width when stacked: ${Math.round(body.sideW)}px of ${body.inner}px`)

  // ── Desk width: the rail is a static column and the hamburger is gone ──
  await page.setViewport({ width: 1440, height: 900 })
  await new Promise((r) => setTimeout(r, 200))
  const deskMenu = await rect('.menu-btn')
  if (deskMenu?.visible) fail('the hamburger is still shown at desk width')
  const deskRail = await rect('.rail')
  if (Math.abs(deskRail.left) > 1) fail(`the rail is not a static left column at desk width: left edge at ${deskRail.left}px`)
  const deskCols = await page.evaluate(() => getComputedStyle(document.querySelector('.tx-body')).gridTemplateColumns)
  if (deskCols.split(' ').length < 3) fail(`the transcript lost its three columns at desk width: "${deskCols}"`)

  if (!process.exitCode) {
    console.log(
      `ok: phone rail folds to a drawer (off-screen → opens → scrim/nav close it); ` +
        `no horizontal scroll on projects, public, or transcript; transcript stacks to one column; ` +
        `desk width keeps the static rail and three columns`,
    )
  }
} catch (e) {
  fail(e.message)
} finally {
  await browser?.close()
  vite.kill()
}
