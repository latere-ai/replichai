// A browser regression test for the transcript stream's layout.
//
// The bug it guards: `.tx-stream` is a scrolling flex column, and a flex column
// shrinks its children once their combined height exceeds the box. Every event
// card sets `overflow: hidden`, which drops its automatic minimum size to zero,
// so a long transcript collapsed all of its cards to their padding and clipped
// the text inside them. Short transcripts never overflowed and so never showed
// it — the defect only appears past a few dozen events.
//
// The assertion is geometry, not pixels: no event card may have more content
// than it has room for. It needs a real layout engine, so it drives Chrome over
// a Vite dev server with the API mocked. Run it with `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5199
const BASE = `http://localhost:${PORT}`
// Well past the point where the stream overflows its box — a short transcript
// fits, and a fitting transcript never shrinks.
const EVENTS = 400

const CHROME = [
  process.env.PUPPETEER_EXECUTABLE_PATH,
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  '/usr/bin/google-chrome',
  '/usr/bin/chromium',
].find((p) => p && require('node:fs').existsSync(p))

if (!CHROME) {
  console.log('skip: no Chrome found (set PUPPETEER_EXECUTABLE_PATH)')
  process.exit(0)
}

// A transcript with every card shape the viewer renders: a message, a tool call
// with its argument block, a collapsed tool result, a thought, and a grade.
function events() {
  const out = [{ role: 'user', kind: 'message', content: 'Reproduce this paper.' }]
  const shapes = [
    { role: 'agent', kind: 'message', content: "I'll start by acquiring the paper and understanding what I'm working with." },
    { role: 'agent', kind: 'tool_use', tool: 'bash', content: 'ls -la /work/repo\ncat method/README.md' },
    { role: 'agent', kind: 'tool_result', content: 'total 48\ndrwxr-xr-x 8 root root 4096 .\n' },
    { role: 'agent', kind: 'thinking', content: '' },
    { role: 'agent', kind: 'grade', content: 'partial' },
  ]
  for (let i = 0; i < EVENTS; i++) out.push(shapes[i % shapes.length])
  return out
}

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const job = {
  id: 'job-1',
  kind: 'replicate',
  ref: 'https://doi.org/10.1145/3562939.3567802',
  phase: 'done',
  created_at: at(0),
  phase_at: { submitted: at(0), resolving: at(7e3), running: at(21e6), publishing: at(21.1e6), done: at(21.2e6) },
}
const project = { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }

const routes = [
  [/^\/me$/, { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin' }],
  [/^\/me\/profile$/, {}],
  [/^\/projects$/, [{ ...project, session_count: 1, running: 0, latest_phase: 'done' }]],
  [/^\/projects\/[^/]+$/, { project, sessions: [job], may_submit: true }],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/jobs\/[^/]+$/, job],
  [/^\/jobs\/[^/]+\/transcript$/, { events: events(), usage: { InputTokens: 176, OutputTokens: 107827, CachedInputTokens: 11625361 }, live: false }],
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

  browser = await puppeteer.launch({ executablePath: CHROME, headless: true })
  const page = await browser.newPage()
  await page.setViewport({ width: 1440, height: 900 })
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
  // Navigation is state, not a router: projects -> project -> session.
  const openFirstRow = () => page.evaluate(() => document.querySelector('.row')?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
  await page.waitForSelector('.row')
  await openFirstRow()
  await page.waitForSelector('.row')
  await openFirstRow()
  await page.waitForSelector('.event')
  // Let the entrance animation finish, so no card is measured mid-transform.
  await new Promise((r) => setTimeout(r, 800))

  const m = await page.evaluate(() => {
    const cards = [...document.querySelectorAll('.event')]
    const clipped = cards.filter((c) => c.scrollHeight > c.clientHeight + 0.5).length
    let overlapping = 0
    for (let i = 0; i + 1 < cards.length; i++) {
      if (cards[i + 1].getBoundingClientRect().top < cards[i].getBoundingClientRect().bottom - 0.5) overlapping++
    }
    const stream = document.querySelector('.tx-stream')
    return { n: cards.length, clipped, overlapping, overflows: stream.scrollHeight > stream.clientHeight }
  })

  if (m.n !== EVENTS + 1) fail(`rendered ${m.n} cards, want ${EVENTS + 1}`)
  // Without overflow there is nothing to shrink, and the test proves nothing.
  if (!m.overflows) fail('the stream does not overflow its box — raise EVENTS')
  if (m.clipped) fail(`${m.clipped}/${m.n} event cards clip their own content`)
  if (m.overlapping) fail(`${m.overlapping} event cards overlap the next one`)
  if (!process.exitCode) console.log(`ok: ${m.n} event cards, none clipped, none overlapping`)
} catch (e) {
  fail(e.message)
} finally {
  await browser?.close()
  vite.kill()
}
