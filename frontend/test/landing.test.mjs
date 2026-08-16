// A browser regression test for the first screen (017, rebuilt by 029 and 033).
//
// What it guards: the landing is a page a researcher reads, and the way in never
// leaves the screen. 017 guaranteed that with a fold — the sign-in button's box
// above 900px — and a page that scrolls cannot keep that promise. The
// replacement is stronger and length-independent: a sticky header, asserted at
// the top of the page *and* at the bottom of it, on both viewports. Those two
// assertions supersede 017's criteria 3 and 4.
//
// It also guards what a figure is for. Three of them carry the argument, and a
// reader who has asked for less motion must get the finished state of each
// rather than a frozen first frame — a claim about computed style that no unit
// test can make.
//
// It also guards the mark. The one it replaced was the character `r` set in
// `--font-serif: 'Instrument Serif'` — a font with no `@font-face` anywhere in
// the repository, so the identity had always rendered in Georgia. The assertions
// below require an SVG that paints, in both themes, at every size it is used.
//
// 033 rebuilt the page from the design, and added four things worth guarding:
// the hero is now a slab that has to hold its own copy at both viewports and
// stay legible when its WebGL scene never loads; the walkthrough *dims* steps
// it is not on, which is motion and must stand down under `reduce`; the file
// list names the product's own output and must match the publish gate; and the
// verdict panel is a fabrication, so it has to be marked as one and must never
// carry a paper key that resolves.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5239
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
const open = async (me, { path = '/', viewport = DESKTOP, theme = 'light', json = {} } = {}) => {
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
    if (u.origin === BASE && u.pathname in json) {
      return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(json[u.pathname]) })
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

  /* ── The way in is on screen at every scroll position ── */

  for (const viewport of [DESKTOP, PHONE]) {
    const page = await open({ authenticated: false }, { viewport })
    await page.waitForSelector('.landing .lp-bar')
    const seen = async (where) => {
      const b = await box(page, '[data-test="lp-signin"]')
      if (!b) return fail(`${where}: the header has no sign-in`)
      if (b.top < 0 || b.bottom > viewport.height) fail(`at ${viewport.width}px, ${where}, the sign-in sits at ${b.top}–${b.bottom} outside the ${viewport.height}px viewport`)
      if (b.width < 90) fail(`the sign-in is ${b.width}px wide at ${viewport.width}px`)
    }
    await seen('at the top')
    // The page is long on purpose. The promise is that the way in survives that.
    const scrolled = await page.evaluate(() => {
      window.scrollTo(0, document.body.scrollHeight)
      return document.documentElement.scrollHeight > window.innerHeight
    })
    if (!scrolled) fail(`at ${viewport.width}px the landing does not scroll; it is meant to be a page, not a card`)
    await new Promise((r) => setTimeout(r, 120))
    await seen('at the bottom')

    // Nothing runs off the side at either width.
    const wide = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1)
    if (wide) fail(`the landing scrolls sideways at ${viewport.width}px`)

    // The argument is on the page, not merely in the DOM.
    const copy = await page.evaluate(() => document.querySelector('.landing-lead').textContent.length)
    if (copy < 120) fail(`the landing lead is ${copy} characters; the point of this screen is that it says what the product does`)
    const pts = await page.evaluate(() => document.querySelectorAll('.landing-point').length)
    if (pts !== 3) fail(`the landing shows ${pts} points, want the inner loop, the outer loop, and the verdict`)
    const open_ = await page.evaluate(() => document.querySelectorAll('.landing-point.open').length)
    if (open_ !== 1) fail(`${open_} points are drawn open; exactly one — the outer loop — is not re-runnable`)
    await page.close()
  }

  /* ── The three figures ── */

  {
    const page = await open({ authenticated: false })
    await page.waitForSelector('.lp-loop-svg')
    // Every figure is on the page, and the loop figure paints in both rings.
    const fig = await page.evaluate(() => {
      const svg = document.querySelector('.lp-loop-svg')
      const cs = (s) => getComputedStyle(document.querySelector(s))
      return {
        loop: svg?.tagName.toLowerCase(),
        outer: cs('.lp-ring-outer').stroke,
        inner: cs('.lp-ring-inner').stroke,
        runnerAnim: cs('.lp-ring-runner').animationName,
        steps: document.querySelectorAll('.lp-step').length,
        parts: document.querySelectorAll('.lp-part').length,
      }
    })
    if (fig.loop !== 'svg') fail('the loop figure is not an svg')
    if (fig.outer === fig.inner) fail(`both loops are painted ${fig.outer}; the boundary is the colour as much as the dash`)
    if (fig.runnerAnim === 'none') fail('nothing on the closed loop moves; the figure claims a re-run and shows a still')
    if (fig.steps !== 4) fail(`the pipeline figure shows ${fig.steps} steps, want four`)
    if (fig.parts !== 3) fail(`the anatomy figure shows ${fig.parts} parts, want claim, evidence, and verdict`)
    await page.close()
  }

  /* ── Reduced motion gets the finished page, not a frozen first frame ── */

  {
    const page = await browser.newPage()
    await page.setViewport(DESKTOP)
    await page.emulateMediaFeatures([{ name: 'prefers-reduced-motion', value: 'reduce' }])
    await page.setRequestInterception(true)
    page.on('request', (req) => {
      const u = new URL(req.url())
      if (u.origin === BASE && u.pathname === '/me') return req.respond({ status: 200, contentType: 'application/json', body: '{"authenticated":false}' })
      req.continue()
    })
    await page.goto(BASE, { waitUntil: 'networkidle0' })
    await page.waitForSelector('.lp-section')
    const still = await page.evaluate(() => {
      const bands = [...document.querySelectorAll('.lp-section, .lp-hero, .lp-step, .lp-part')]
      return {
        hidden: bands.filter((e) => +getComputedStyle(e).opacity < 0.99).length,
        animated: bands.filter((e) => getComputedStyle(e).animationName !== 'none').length,
        runner: getComputedStyle(document.querySelector('.lp-ring-runner')).animationName,
      }
    })
    if (still.hidden) fail(`${still.hidden} parts of the landing are transparent under reduced motion; the resting state has to be the finished one`)
    if (still.animated) fail(`${still.animated} bands still animate under reduced motion`)
    if (still.runner !== 'none') fail('the loop runner still spins under reduced motion')

    // The walkthrough dims the steps it is not on and folds their artifacts down
    // to a header strip. Both of those are motion by this page's own definition,
    // so under `reduce` the driver must never start: every step lit, every plate
    // open. A reader who asked for less movement is not asked to scroll to find
    // three quarters of a band.
    await page.evaluate(() => {
      document.querySelector('.lp-walk').scrollIntoView()
    })
    await new Promise((r) => setTimeout(r, 400))
    const walk = await page.evaluate(() => {
      const steps = [...document.querySelectorAll('.lp-step')]
      const plates = [...document.querySelectorAll('.lp-plate')]
      return {
        dim: steps.filter((s) => +getComputedStyle(s).opacity < 0.99).length,
        folded: plates.filter((p) => p.style.maxHeight !== '').length,
        plates: plates.length,
      }
    })
    if (walk.plates !== 4) fail(`the walkthrough shows ${walk.plates} artifacts for four steps`)
    if (walk.dim) fail(`${walk.dim} walkthrough steps are dimmed under reduced motion`)
    if (walk.folded) fail(`${walk.folded} artifacts are folded shut under reduced motion; the resting state is the open one`)
    await page.close()
  }

  /* ── The hero slab carries its own copy, with or without the scene ── */

  for (const viewport of [DESKTOP, PHONE]) {
    const page = await open({ authenticated: false }, { viewport })
    await page.waitForSelector('.lp-slab')
    const slab = await page.evaluate(() => {
      const el = document.querySelector('.lp-slab')
      const r = el.getBoundingClientRect()
      const inside = (sel) => {
        const c = document.querySelector(sel)
        if (!c) return false
        const b = c.getBoundingClientRect()
        return b.width > 0 && b.left >= r.left - 1 && b.right <= r.right + 1 && b.bottom <= r.bottom + 1
      }
      return {
        // The slab paints its own dark ground. The scene is lazy, optional, and
        // allowed to fail; if the copy's legibility rode on it, a browser with
        // no WebGL would get white text on white.
        bg: getComputedStyle(el).backgroundColor,
        h1: document.querySelector('.lp-slab h1')?.textContent.trim().length ?? 0,
        signin: inside('[data-test="lp-hero-signin"]'),
        registry: inside('[data-test="lp-hero-registry"]'),
      }
    })
    if (!/^rgba?\((\d+), (\d+), (\d+)/.test(slab.bg) || slab.bg.includes(', 0)')) fail(`at ${viewport.width}px the hero slab has no ground of its own (${slab.bg}); the scene is allowed to fail and the copy is not`)
    if (slab.h1 < 20) fail(`at ${viewport.width}px the hero slab carries no claim`)
    if (!slab.signin) fail(`at ${viewport.width}px the hero's sign-in is not inside the slab`)
    if (!slab.registry) fail(`at ${viewport.width}px the hero's registry link is not inside the slab`)
    await page.close()
  }

  /* ── Every band reaches the page, motion or not ── */

  {
    // The counterpart to the reduced-motion block, and the one that was missing:
    // with motion *allowed* every band starts transparent and is brought in by
    // an observer, so a band whose reveal ref was never wired is on the page,
    // occupying its full height, and invisible. Geometry assertions do not catch
    // that — a box at opacity 0 measures exactly like a box at opacity 1 — which
    // is how a blank hero reached production.
    const page = await open({ authenticated: false }, { json: { '/public': [] } })
    await page.waitForSelector('.lp-slab')
    await new Promise((r) => setTimeout(r, 900))
    // The hero is above the fold and must be visible with no scrolling at all.
    const heroOpacity = await page.evaluate(() => +getComputedStyle(document.querySelector('.lp-hero')).opacity)
    if (heroOpacity < 0.9) fail(`the hero is at opacity ${heroOpacity} on a page nobody has scrolled; the first screen is invisible`)
    const dark = await page.evaluate(async () => {
      const bands = [...document.querySelectorAll('.lp-hero, .lp-section')]
      for (const b of bands) {
        b.scrollIntoView()
        await new Promise((r) => setTimeout(r, 260))
      }
      await new Promise((r) => setTimeout(r, 800))
      return bands.filter((b) => +getComputedStyle(b).opacity < 0.9).map((b) => b.className)
    })
    if (dark.length) fail(`${dark.length} bands never became visible after being scrolled to: ${JSON.stringify(dark)}`)
    await page.close()
  }

  /* ── The walkthrough drives, where driving it is motion a reader allows ── */

  {
    const page = await open({ authenticated: false })
    await page.waitForSelector('.lp-walk')
    const lit = await page.evaluate(async () => {
      document.querySelector('.lp-step[data-step="2"]').scrollIntoView({ block: 'center' })
      window.dispatchEvent(new Event('scroll'))
      await new Promise((r) => setTimeout(r, 700))
      const steps = [...document.querySelectorAll('.lp-step')]
      return {
        on: steps.filter((s) => +getComputedStyle(s).opacity > 0.99).length,
        // Exactly one artifact is open at a time; two open plates overlap and
        // each shows its body through the other.
        open: [...document.querySelectorAll('.lp-plate')].filter((p) => p.getBoundingClientRect().height > 60).length,
      }
    })
    if (lit.on !== 1) fail(`${lit.on} walkthrough steps are lit at once; the band is about one step at a time`)
    if (lit.open !== 1) fail(`${lit.open} artifacts are open at once`)
    await page.close()
  }

  /* ── The page names the product's own output, and nobody else's paper ── */

  {
    const page = await open({ authenticated: false })
    await page.waitForSelector('.lp-file')
    const shown = await page.evaluate(() => ({
      files: [...document.querySelectorAll('.lp-file-name')].map((e) => e.textContent.trim()),
      example: !!document.querySelector('.lp-tag.example'),
      // Every DOI-shaped string on the page. The verdict this page shows is
      // invented, and an invented verdict hung on somebody's real DOI is the
      // exact failure this product exists to catch.
      dois: (document.body.innerText.match(/10\.\d{4,9}\/[^\s]+/g) || []),
    }))
    // The five paths `internal/publish/gate.go` treats as worker-written.
    const want = ['README.md', 'REPRODUCIBILITY.md', 'src/', 'UNVERIFIED.md', 'SOURCES.md']
    if (JSON.stringify(shown.files) !== JSON.stringify(want)) fail(`the page says a session ships ${JSON.stringify(shown.files)}, and the publish gate ships ${JSON.stringify(want)}`)
    if (!shown.example) fail('the demo verdict is not marked as an example; it is a fabrication and has to say so')
    // An empty match set would satisfy the filter below identically to "only the
    // illustrative key is there", and this is the one guard on the page that
    // must never pass because it looked at nothing.
    if (!shown.dois.some((d) => d.startsWith('10.0000/'))) fail('the paper-key guard matched nothing, so it cannot prove the demo key is the illustrative one')
    // 10.0000 is unassigned, so it resolves to nothing. Any other registrant
    // prefix means the page hung a made-up verdict on a real paper.
    const real = shown.dois.filter((d) => !d.startsWith('10.0000/'))
    if (real.length) fail(`the landing shows a resolvable paper key beside an invented verdict: ${JSON.stringify(real)}`)
    await page.close()
  }

  /* ── The published strip is a band a reader can see ── */

  {
    // The strip renders only once its fetch lands, which is *after* the first
    // paint — and every band on this page starts at the pre-reveal opacity. A
    // reveal that was wired up on mount alone never observed this one, so the
    // section was on the page, occupying its full height, and invisible.
    const published = [
      { id: 'a', title: 'Attention Is All You Need — decoder ablation', signed_off_name: 'Ada Lovelace', session_count: 3, visibility: 'public' },
      { id: 'b', title: 'The Placebo Effect of Artificial Intelligence', signed_off_name: 'Grace Hopper', session_count: 1, visibility: 'public' },
    ]
    const page = await open({ authenticated: false }, { json: { '/public': published } })
    await page.waitForSelector('[data-test="lp-strip"]')
    const strip = await page.evaluate(async () => {
      const band = document.querySelector('[data-test="lp-strip"]')
      band.scrollIntoView()
      await new Promise((r) => setTimeout(r, 900))
      const seen = (e) => {
        const cs = getComputedStyle(e)
        const r = e.getBoundingClientRect()
        return +cs.opacity > 0.9 && cs.visibility !== 'hidden' && r.width > 0 && r.height > 0
      }
      const cards = [...document.querySelectorAll('.lp-card')]
      return {
        band: seen(band),
        cards: cards.length,
        painted: cards.filter(seen).length,
        titles: cards.map((c) => c.querySelector('.lp-card-title')?.textContent.trim()),
        bylines: cards.map((c) => c.querySelector('.lp-card-by')?.textContent.trim()),
        links: cards.map((c) => c.getAttribute('href')),
      }
    })
    if (!strip.band) fail('the published band is on the page and invisible')
    if (strip.cards !== 2) fail(`the strip rendered ${strip.cards} cards for two published projects`)
    if (strip.painted !== strip.cards) fail(`${strip.cards - strip.painted} of ${strip.cards} strip cards never became visible`)
    if (!strip.titles.every(Boolean)) fail(`a strip card has no title: ${JSON.stringify(strip.titles)}`)
    // The card's second line is a person's name under a paper's title, which is
    // exactly the shape of an author byline — and the signer is the one person
    // it is never about. It has to say what the name is for.
    const signers = ['Ada Lovelace', 'Grace Hopper']
    strip.bylines.forEach((by, i) => {
      if (!by?.includes(signers[i])) fail(`strip card ${i} does not name its signer: ${by}`)
      if (by.trim() === signers[i]) fail(`strip card ${i} names "${by}" and nothing else; a bare name reads as the paper's author`)
      if (!/sign/i.test(by)) fail(`strip card ${i} says "${by}"; it has to say the name signed the reproduction`)
    })
    if (!strip.links.every((h) => h?.startsWith('/p/'))) fail(`a strip card links nowhere useful: ${JSON.stringify(strip.links)}`)
    await page.close()
  }

  // An empty registry is not an empty band: the section is absent entirely,
  // because a heading over nothing is a claim about a community.
  {
    const page = await open({ authenticated: false }, { json: { '/public': [] } })
    await page.waitForSelector('.landing')
    const there = await page.evaluate(() => !!document.querySelector('[data-test="lp-strip"]'))
    if (there) fail('an empty registry still renders the published band')
    await page.close()
  }

  /* ── The registry is linked, now that it can be read signed out (028) ── */

  {
    const page = await open({ authenticated: false })
    await page.waitForSelector('.landing')
    const links = await page.evaluate(() => [...document.querySelectorAll('.landing a[href="/registry"]')].length)
    if (!links) fail('the landing links nowhere; 028 opened the registry so that it could')
    await page.close()
  }

  /* ── The way in still works ── */

  {
    // A project address renders the app signed out since 028, so the landing's
    // own round trip is asserted from an address that still needs an account —
    // the public deep link is public-shell.test.mjs's to guard.
    const page = await open({ authenticated: false }, { path: '/new' })
    await page.waitForSelector('.landing')
    const href = await page.evaluate(() => document.querySelector('[data-test="lp-signin"]').getAttribute('href'))
    if (href !== '/login?next=%2Fnew') fail(`signed out at /new the button points at ${href}; the address has to survive the round trip`)
    // The button carries the provider's mark, painted in the button's own ink.
    // A mark that inherited a colour from somewhere else would be invisible on
    // the brand fill and nothing else here would notice.
    const provider = await page.evaluate(() => {
      const btnEl = document.querySelector('[data-test="lp-signin"]')
      const svg = btnEl?.querySelector('svg')
      if (!svg) return null
      const r = svg.getBoundingClientRect()
      const btn = getComputedStyle(btnEl)
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
