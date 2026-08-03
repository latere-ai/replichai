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

// Naming a browser is a demand that it run: an unset PUPPETEER_EXECUTABLE_PATH
// skips on a machine without Chrome, but a set one that does not exist fails, so
// CI cannot quietly stop guarding the regression.
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

// The proportions of a real session: resolving takes seconds, running takes
// hours. A scrubber that divides its bar evenly would hide exactly that.
const RUNNING_AT = 14_000
const RUNNING_MS = 21_180_000

// A transcript with every card shape the viewer renders: a message with a title
// and a body, a tool call with its argument block, a collapsed tool result, a
// thought, and a grade — spread across a two-hour run so the scrubber has a real
// span to draw, and with some events charged so the histogram is token-weighted.
function events() {
  const shapes = [
    {
      role: 'agent',
      kind: 'message',
      content: "Resolving the reference\nThe DOI points to CHI '24. I will establish a stable identity and slug before searching for artifacts.",
    },
    { role: 'agent', kind: 'tool_use', tool: 'bash', content: 'ls -la /work/repo\ncat method/README.md' },
    { role: 'agent', kind: 'tool_result', content: 'total 48\ndrwxr-xr-x 8 root root 4096 .\n' },
    { role: 'agent', kind: 'thinking', content: '' },
    { role: 'agent', kind: 'grade', content: 'partial' },
  ]
  const out = [{ role: 'user', kind: 'message', content: 'Reproduce this paper.', time: at(RUNNING_AT) }]
  for (let i = 0; i < EVENTS; i++) {
    // Every event falls inside the running phase, as they do on a real run.
    const e = { ...shapes[i % shapes.length], time: at(RUNNING_AT + Math.round(((i + 1) / EVENTS) * RUNNING_MS)) }
    if (i % 5 === 1) e.usage = { InputTokens: 900 + i, OutputTokens: 120, CachedInputTokens: 40_000 }
    out.push(e)
  }
  return out
}
const job = {
  id: 'job-1',
  kind: 'replicate',
  ref: 'https://doi.org/10.1145/3562939.3567802',
  phase: 'done',
  created_at: at(0),
  phase_at: {
    submitted: at(0),
    resolving: at(7_000),
    running: at(RUNNING_AT),
    publishing: at(RUNNING_AT + RUNNING_MS),
    done: at(RUNNING_AT + RUNNING_MS + 17_000),
  },
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

  // 006s AC3: the phase bands are proportional to elapsed time. `running` is
  // 21e6 of the 21.2e6 ms the job spent, so it must own almost the whole bar —
  // an evenly-divided strip would put it at a fifth.
  const bands = await page.evaluate(() => {
    const bar = document.querySelector('.scrub-bar')
    if (!bar) return null
    const w = bar.getBoundingClientRect().width
    return [...document.querySelectorAll('.scrub-band')].map((b) => ({
      phase: b.dataset.phase,
      frac: +(b.getBoundingClientRect().width / w).toFixed(4),
    }))
  })
  if (!bands) fail('no scrubber rendered for a transcript whose events carry timestamps')
  else {
    const running = bands.find((b) => b.phase === 'running')
    if (!running) fail(`no running band; bands = ${JSON.stringify(bands)}`)
    else if (running.frac < 0.9) {
      fail(`running band is ${(running.frac * 100).toFixed(1)}% of the bar, want >90% — bands are not proportional. bands = ${JSON.stringify(bands)}`)
    }
  }

  // 006s AC4 + the reason the design's dimming was rejected: scrubbing scrolls
  // the stream and must not write a single per-card style. Snapshot every card's
  // inline style, drag across the bar, and require the snapshot to be unchanged
  // while the scroll position moved.
  const scrub = await page.evaluate(async () => {
    const stream = document.querySelector('.tx-stream')
    const bar = document.querySelector('.scrub-bar')
    const before = { top: stream.scrollTop, styles: [...document.querySelectorAll('.event')].map((c) => c.getAttribute('style') ?? '') }
    const box = bar.getBoundingClientRect()
    const send = (type, x) =>
      bar.dispatchEvent(new PointerEvent(type, { bubbles: true, pointerId: 1, clientX: x, clientY: box.top + box.height / 2 }))
    send('pointerdown', box.left + box.width * 0.5)
    send('pointermove', box.left + box.width * 0.8)
    await new Promise((r) => setTimeout(r, 900)) // the scroll is smooth
    const after = { top: stream.scrollTop, styles: [...document.querySelectorAll('.event')].map((c) => c.getAttribute('style') ?? '') }
    return {
      moved: after.top - before.top,
      restyled: after.styles.filter((s, i) => s !== before.styles[i]).length,
      cursor: !!document.querySelector('.scrub-cursor'),
    }
  })
  if (!scrub.cursor) fail('scrubbing left no cursor on the bar')
  if (scrub.moved <= 0) fail(`scrubbing moved the stream by ${scrub.moved}px, want a forward scroll`)
  if (scrub.restyled) fail(`scrubbing rewrote the inline style of ${scrub.restyled} cards — that is the per-card work this design avoids`)

  if (!process.exitCode) {
    console.log(`ok: ${m.n} event cards, none clipped, none overlapping; scrub moved ${Math.round(scrub.moved)}px, restyled 0 cards`)
  }
} catch (e) {
  fail(e.message)
} finally {
  await browser?.close()
  vite.kill()
}
