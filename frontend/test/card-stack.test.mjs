// A browser regression test for the vertical rhythm between stacked cards.
//
// The bug it guards: a screen that renders two cards as siblings must put them
// in a `.stack`, which is the one place the 16px gap between cards is written.
// The public researcher page dropped the wrapper and hung both cards straight
// off `.screen`, which is a plain block — so the identity card and the
// signed-work card sat edge to edge with their 1px borders touching, reading as
// one mis-drawn box rather than two cards. (Settings had the same defect until
// 024a split it into one-card-per-tab, so it no longer stacks two siblings.)
//
// "The cards exist" passes against the broken code, and so does "the second card
// starts below the first". The discriminating measurement is the distance
// between the first card's bottom edge and the second card's top edge: zero when
// the wrapper is missing, a full gap when it is there. The screen is
// URL-addressable, so the test loads it directly with the API mocked. Run it
// with `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5199
const BASE = `http://localhost:${PORT}`

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

// The settings screen's second card is ProfileCard, and it renders nothing until
// /me/profile answers — an empty body is what the API sends before a profile is
// ever saved, and it is enough to draw the card.
const routes = [
  [/^\/me$/, { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { wallet: 42.5, start_reserve: 5 } }],
  [/^\/me\/profile$/, {}],
  [/^\/projects$/, []],
  [/^\/(public|shared)$/, []],
  [
    /^\/profiles\/[^/]+$/,
    {
      handle: 'tester',
      display_name: 'A Tester',
      institution: 'Some University',
      contact_email: 'tester@example.com',
      projects: [{ id: 'proj-1', title: 'Walk This Beam', signed_off_at: at(0) }],
    },
  ],
]

const fail = (msg) => {
  console.error(`FAIL: ${msg}`)
  process.exitCode = 1
}

// What a `.stack` puts between two cards. The floor is well under it so a change
// of taste in the gap does not fail the test, and well over zero so a stray 1px
// margin cannot pass for a gap.
const MIN_GAP = 8

const vite = spawn('npx', ['vite', '--port', String(PORT), '--strictPort'], { stdio: 'ignore' })
let browser
try {
  for (let i = 0; ; i++) {
    try {
      if ((await fetch(BASE)).ok) break
    } catch {
      /* not up yet */
    }
    // A cold dep-optimize pass takes tens of seconds on a first run.
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
      if (hit) return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(hit[1]) })
      if (u.pathname.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    }
    req.continue()
  })

  // A card is measured only against the card next to it: `.screen .card` also
  // matches cards nested inside another one, and those carry their own spacing.
  const gaps = async (label, want) => {
    const m = await page.evaluate(() => {
      const screen = document.querySelector('.screen')
      if (!screen) return { cards: 0 }
      const cards = [...screen.children].flatMap((c) => (c.classList.contains('card') ? [c] : [...c.children].filter((g) => g.classList.contains('card'))))
      return {
        cards: cards.length,
        gaps: cards.slice(1).map((c, i) => c.getBoundingClientRect().top - cards[i].getBoundingClientRect().bottom),
      }
    })
    // Measuring a gap between cards that are not both on screen proves nothing,
    // so the count is asserted before the distance.
    if (m.cards !== want) return fail(`${label} drew ${m.cards} top-level cards, want ${want} — the mock or the screen changed and the gap below is not the reported bug`)
    m.gaps.forEach((g, i) => {
      if (g < MIN_GAP) fail(`${label}: cards ${i + 1} and ${i + 2} are ${g.toFixed(1)}px apart, want at least ${MIN_GAP}px — they need a .stack wrapper`)
    })
    return m
  }

  // The defect, on the page a signed-out reader sees: identity above, signed
  // work below. (Settings no longer stacks two cards since 024a tabbed it.)
  await page.goto(`${BASE}/u/tester`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.screen .card')
  await new Promise((r) => setTimeout(r, 400))
  await gaps('public profile', 2)

  if (!process.exitCode) console.log('ok: stacked cards keep their gap')
} catch (e) {
  fail(e?.stack || String(e))
} finally {
  await browser?.close()
  vite.kill('SIGTERM')
}
