// A browser regression test for the first screen (017).
//
// What it guards: the landing is a two-column grid whose one-column fallback
// *reorders* — the sign-in card moves above the three points so a phone gets the
// button without scrolling past the argument for it. That reordering is declared
// in `grid-template-areas`, which means source order and painted order disagree
// on purpose, and no unit test can tell you which one the reader gets. The same
// goes for "the button is above the fold": it is a claim about computed
// geometry at a named viewport.
//
// It also guards the mark. The one it replaced was the character `r` set in
// `--font-serif: 'Instrument Serif'` — a font with no `@font-face` anywhere in
// the repository, so the identity had always rendered in Georgia. The assertions
// below require an SVG that paints, in both themes, at every size it is used.
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

const fail = (msg) => {
  console.error(`FAIL: ${msg}`)
  process.exitCode = 1
}

const DESKTOP = { width: 1440, height: 900 }
const PHONE = { width: 390, height: 844 }

const vite = spawn('npx', ['vite', '--port', String(PORT), '--strictPort'], { stdio: 'ignore' })
let browser

// open serves one mocked `/me` and returns a page at `path`. `me` is the body
// the mock answers with; passing null makes the call fail, which is the outage
// case 014d distinguishes from being signed out.
const open = async (me, { path = '/', viewport = DESKTOP, theme = 'light' } = {}) => {
  const page = await browser.newPage()
  await page.setViewport(viewport)
  await page.evaluateOnNewDocument((t) => localStorage.setItem('theme', t), theme)
  await page.setRequestInterception(true)
  page.on('request', (req) => {
    const u = new URL(req.url())
    if (u.origin === BASE && u.pathname === '/me') {
      if (me === null) return req.respond({ status: 500, contentType: 'application/json', body: '{}' })
      return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(me) })
    }
    req.continue()
  })
  await page.goto(BASE + path, { waitUntil: 'networkidle0' })
  return page
}

// box reads the geometry of one selector, plus whether its mark actually paints.
const box = (page, sel) =>
  page.evaluate((s) => {
    const e = document.querySelector(s)
    if (!e) return null
    const r = e.getBoundingClientRect()
    return { top: r.top, left: r.left, right: r.right, bottom: r.bottom, width: r.width, height: r.height }
  }, sel)

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

  /* ── The mark ── */

  {
    const page = await open({ authenticated: false })
    await page.waitForSelector('.landing')
    const mark = await page.evaluate(() => {
      const m = document.querySelector('.landing-lockup .mark')
      if (!m) return null
      const inner = m.querySelector('.mark-inner')
      const outer = m.querySelector('.mark-outer')
      const cs = (e) => getComputedStyle(e)
      const r = m.getBoundingClientRect()
      return {
        tag: m.tagName.toLowerCase(),
        w: r.width,
        h: r.height,
        // A mark that paints: both rings have a real stroke and neither is
        // transparent. The `r` this replaced would fail nothing here — it was a
        // div — which is why the tag is asserted first.
        innerStroke: cs(inner).stroke,
        outerStroke: cs(outer).stroke,
        outerOpacity: +cs(outer).opacity,
        // The outer loop is drawn open. Losing the dash array turns the mark
        // into two concentric rings and the drawing stops saying anything.
        dashed: (inner.getAttribute('stroke-dasharray') || '') === '' && (outer.getAttribute('stroke-dasharray') || '') !== '',
        // Nothing in the identity may depend on a font the app does not ship.
        font: cs(document.querySelector('.wordmark')).fontFamily,
        seam: !!document.querySelector('.wordmark .wordmark-seam'),
        word: document.querySelector('.wordmark').textContent.trim(),
      }
    })
    if (!mark) fail('the landing has no mark')
    else {
      if (mark.tag !== 'svg') fail(`the mark is a <${mark.tag}>, want an <svg> that needs no font`)
      if (mark.w < 20 || mark.h < 20) fail(`the landing mark is ${mark.w}×${mark.h}px, too small to be the identity`)
      if (mark.innerStroke === 'none' || /rgba\(.*, *0\)/.test(mark.innerStroke)) fail(`the mark's inner ring does not paint (stroke: ${mark.innerStroke})`)
      if (mark.outerStroke === 'none' || mark.outerOpacity < 0.2) fail(`the mark's outer ring does not paint (stroke: ${mark.outerStroke}, opacity: ${mark.outerOpacity})`)
      if (!mark.dashed) fail('the outer loop is not drawn open; a closed outer ring claims the study is re-runnable')
      if (/Instrument Serif/.test(mark.font)) fail(`the wordmark asks for ${mark.font}, which the app does not ship`)
      if (mark.word !== 'ReplicHAI') fail(`the wordmark reads "${mark.word}"`)
      if (!mark.seam) fail('the wordmark lost its seam')
    }
    await page.close()
  }

  // The mark has to survive the other theme: it owns its colour, so a token that
  // resolves to the page background would leave a blank square in dark mode.
  {
    const page = await open({ authenticated: false }, { theme: 'dark' })
    await page.waitForSelector('.landing')
    const same = await page.evaluate(() => {
      const bg = getComputedStyle(document.body).backgroundColor
      const stroke = getComputedStyle(document.querySelector('.mark-inner')).stroke
      return { bg, stroke }
    })
    if (same.bg === same.stroke) fail(`in dark mode the mark is painted in the page background (${same.stroke})`)
    await page.close()
  }

  /* ── Two columns, and the button above the fold ── */

  {
    const page = await open({ authenticated: false })
    await page.waitForSelector('.landing')
    const brand = await box(page, '.landing-brand')
    const auth = await box(page, '.landing-auth')
    const points = await box(page, '.landing-points')
    const btn = await box(page, '.landing-auth .btn')
    if (!brand || !auth || !points || !btn) fail('the desktop landing is missing one of its three areas')
    else {
      if (auth.left < brand.right) fail(`at ${DESKTOP.width}px the sign-in card overlaps the copy (card starts at ${auth.left}, copy ends at ${brand.right})`)
      if (points.left !== brand.left) fail(`the three points start at ${points.left}px but the copy above them at ${brand.left}px; they share a column`)
      if (btn.bottom > DESKTOP.height) fail(`the sign-in button ends at ${btn.bottom}px, below the ${DESKTOP.height}px fold`)
      if (btn.width < 200) fail(`the sign-in button is ${btn.width}px wide; it is the only action on the screen`)
      // The argument has to be on the screen, not merely in the DOM.
      const copy = await page.evaluate(() => document.querySelector('.landing-lead').textContent.length)
      if (copy < 120) fail(`the landing lead is ${copy} characters; the point of this screen is that it says what the product does`)
      const pts = await page.evaluate(() => document.querySelectorAll('.landing-point').length)
      if (pts !== 3) fail(`the landing shows ${pts} points, want the inner loop, the outer loop, and the verdict`)
      // One of them is the outer loop, and it is the one drawn open.
      const open_ = await page.evaluate(() => document.querySelectorAll('.landing-point.open').length)
      if (open_ !== 1) fail(`${open_} points are drawn open; exactly one — the outer loop — is not re-runnable`)
    }
    await page.close()
  }

  /* ── One column on a phone, reordered so the button stays reachable ── */

  {
    const page = await open({ authenticated: false }, { viewport: PHONE })
    await page.waitForSelector('.landing')
    const brand = await box(page, '.landing-brand')
    const auth = await box(page, '.landing-auth')
    const points = await box(page, '.landing-points')
    const btn = await box(page, '.landing-auth .btn')
    if (!brand || !auth || !points || !btn) fail('the phone landing is missing one of its three areas')
    else {
      if (Math.abs(auth.left - brand.left) > 1) fail(`at ${PHONE.width}px the landing is still two columns (card at ${auth.left}, copy at ${brand.left})`)
      if (!(brand.top < auth.top && auth.top < points.top)) {
        fail(`on a phone the order is brand@${brand.top} card@${auth.top} points@${points.top}; want brand, then the card, then the points`)
      }
      if (btn.bottom > PHONE.height) fail(`on a phone the sign-in button ends at ${btn.bottom}px, below the ${PHONE.height}px fold`)
      // Nothing may run off the side: the card has a 340px minimum on desktop.
      const wide = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1)
      if (wide) fail('the phone landing scrolls sideways')
    }
    await page.close()
  }

  /* ── The way in still works ── */

  {
    const page = await open({ authenticated: false }, { path: '/p/proj-1' })
    await page.waitForSelector('.landing')
    const href = await page.evaluate(() => document.querySelector('.landing-auth .btn').getAttribute('href'))
    if (href !== '/login?next=%2Fp%2Fproj-1') fail(`signed out at /p/proj-1 the button points at ${href}; the deep link has to survive the round trip`)
    // The button carries the provider's mark, painted in the button's own ink.
    // A mark that inherited a colour from somewhere else would be invisible on
    // the brand fill and nothing else here would notice.
    const provider = await page.evaluate(() => {
      const svg = document.querySelector('.landing-auth .btn svg')
      if (!svg) return null
      const r = svg.getBoundingClientRect()
      const btn = getComputedStyle(document.querySelector('.landing-auth .btn'))
      return { w: r.width, h: r.height, fill: getComputedStyle(svg).fill, ink: btn.color, bg: btn.backgroundColor, paths: svg.querySelectorAll('path').length }
    })
    if (!provider) fail('the sign-in button lost the latere mark')
    else {
      if (provider.paths < 6) fail(`the latere mark draws ${provider.paths} paths; the canonical one has six`)
      if (provider.h < 8 || provider.w < 12) fail(`the latere mark is ${provider.w}×${provider.h}px, too small to read`)
      if (provider.fill !== provider.ink) fail(`the latere mark is filled ${provider.fill} but the button's ink is ${provider.ink}`)
      if (provider.fill === provider.bg) fail('the latere mark is filled in the button background and cannot be seen')
    }
    await page.close()
  }

  /* ── The failure screens keep the plain card, and inherit the mark ── */

  {
    const page = await open(null)
    await page.waitForSelector('.auth-center .auth-card')
    const state = await page.evaluate(() => ({
      landing: !!document.querySelector('.landing'),
      mark: document.querySelector('.auth-card .mark')?.tagName.toLowerCase(),
      action: !!document.querySelector('.auth-card button, .auth-card a'),
    }))
    if (state.landing) fail('an auth outage renders the landing; somebody whose session just broke is not an audience for a pitch')
    if (state.mark !== 'svg') fail('the auth-outage card lost the mark')
    if (!state.action) fail('the auth-outage card offers no way out')
    await page.close()
  }

  {
    const page = await open({ authenticated: true, principal: 'p', email: 'nobody@example.com', tier: 'none' })
    await page.waitForSelector('.auth-center .auth-card')
    const state = await page.evaluate(() => ({
      landing: !!document.querySelector('.landing'),
      mark: document.querySelector('.auth-card .mark')?.tagName.toLowerCase(),
      email: document.querySelector('.auth-card p').textContent.includes('nobody@example.com'),
    }))
    if (state.landing) fail('a revoked account is shown the landing instead of what happened to it')
    if (state.mark !== 'svg') fail('the no-access card lost the mark')
    if (!state.email) fail('the no-access card no longer names the account it is talking about')
    await page.close()
  }

  if (!process.exitCode) console.log('PASS: landing')
} catch (err) {
  fail(err.message)
} finally {
  if (browser) await browser.close()
  vite.kill()
}
