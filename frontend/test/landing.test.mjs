// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for the first screen (017, rebuilt by 029 and 033).
//
// What it guards: the landing is a page a researcher reads, and the sign-in
// never leaves the screen. 017 guaranteed that with a fold, the sign-in button's
// box above 900px, which a page that scrolls cannot keep. The replacement is
// stronger and independent of page length: a sticky header, asserted at the top
// of the page and at the bottom of it, on both viewports. Those two assertions
// supersede 017's criteria 3 and 4.
//
// It also guards the figures. Three of them carry the argument, and a reader who
// has asked for less motion must get the finished state of each rather than a
// frozen first frame. That is a claim about computed style, which no unit test
// can make.
//
// It also guards the mark. The one it replaced was the character `r` set in
// `--font-serif: 'Instrument Serif'`, a font with no `@font-face` anywhere in
// the repository, so the identity had always rendered in Georgia. The assertions
// below require an SVG that paints, in both themes, at every size it is used.
//
// 033 rebuilt the page from the design and added four things to guard: the hero
// is now a slab that has to hold its own copy at both viewports and stay legible
// when its WebGL scene never loads; the walkthrough dims steps it is not on,
// which is motion and must be disabled under `reduce`; the file list names the
// product's own output and must match the publish gate; and the verdict panel is
// a fabrication, so it has to be marked as one and must never carry a paper key
// that resolves.
//
// 047 set the page like a paper and guards what that changed: the hero is a
// drawn figure rather than a WebGL scene, and it runs only when it should and
// rests on its finished frame otherwise; the header's sign-in stands down while
// the hero's or the close's is on screen, so a reader is offered the way in
// once at a time; the example verdict's rate is a footnote under its table; the
// page sets one face, Inter, which it ships, and no serif; every text tone reads
// at 4.5:1 on its ground in both themes; every control shows its focus ring;
// and the figure arriving shifts nothing.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { DESKTOP, fail, json, settled, sleep, start, waitUntil } from './harness.mjs'

const PHONE = { width: 390, height: 844 }
// The widest screen the page claims to serve. 044 added it: nothing in this
// suite could see a band that stopped at 1180px on a 2560px display.
const WIDE = { width: 2560, height: 1440 }

const h = await start()

// open serves one mocked `/me` and returns a page at `path`. `me` is the body
// the mock answers with; passing null makes the call fail, which is the outage
// case 014d distinguishes from being signed out. `routes` answers whatever else
// a block needs the server to say.
const open = async (me, { path = '/', viewport = DESKTOP, theme = 'light', routes = [] } = {}) => {
  const page = await h.page({ viewport, routes: [['/me', me === null ? json({}, 500) : me], ...routes] })
  await page.evaluateOnNewDocument((t) => localStorage.setItem('theme', t), theme)
  await page.open(path)
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

// scrollThrough walks the page band by band, as a reader does, giving each one a
// frame to land before moving on to the next.
const scrollThrough = (page) =>
  page.evaluate(async () => {
    for (const b of document.querySelectorAll('.lp-hero, .lp-section, .lp-close-wrap')) {
      b.scrollIntoView()
      await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))
    }
  })

try {
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
        // transparent. The `r` this replaced was a div and would fail none of
        // these checks, which is why the tag is asserted first.
        innerStroke: cs(inner).stroke,
        outerStroke: cs(outer).stroke,
        outerOpacity: +cs(outer).opacity,
        // The outer loop is drawn open: one arc with one gap (047), so its
        // length is short of the full circle it sits on. A closed outer ring
        // claims the study already runs, which it does not yet.
        open: outer.tagName.toLowerCase() === 'path' && outer.getTotalLength() < 0.9 * 2 * Math.PI * 9.2 && inner.tagName.toLowerCase() === 'circle' && (inner.getAttribute('stroke-dasharray') || '') === '',
        // The wordmark is set in Inter, which the app ships and preloads (047).
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
      if (!mark.open) fail('the outer loop is not drawn open; a closed outer ring claims the study already runs')
      if (!/^"?Inter/.test(mark.font) || /serif/i.test(mark.font.replace(/sans-serif/gi, ''))) fail(`the wordmark asks for ${mark.font}; it is set in Inter, which the app ships`)
      if (mark.word !== 'ReplicHAI') fail(`the wordmark reads "${mark.word}"`)
      if (!mark.seam) fail('the wordmark lost its seam')
    }
    await page.close()
  }

  // The mark has to survive the other theme: it owns its color, so a token that
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

  /* ── The way in is on screen at every scroll position, once ── */

  for (const viewport of [DESKTOP, PHONE]) {
    const page = await open({ authenticated: false }, { viewport })
    await page.waitForSelector('.landing .lp-bar')
    // The sign-in buttons a reader can see and press right now: inside the
    // viewport, below the sticky header, and not hidden. 029 kept one on screen
    // at every position with a sticky header; 047 keeps it to one at a time, so
    // the header's stands down while the hero's or the close's is on screen.
    const visible = () =>
      page.evaluate((vh) => {
        const header = document.querySelector('.lp-bar').getBoundingClientRect().bottom
        const shown = (e) => {
          const cs = getComputedStyle(e)
          const r = e.getBoundingClientRect()
          const inHeader = !!e.closest('.lp-bar')
          return cs.visibility !== 'hidden' && +cs.opacity > 0.5 && r.width >= 90 && r.bottom <= vh && r.top >= (inHeader ? 0 : header)
        }
        return [...document.querySelectorAll('[data-test="lp-signin"], [data-signin-inline]')].filter(shown).map((e) => (e.closest('.lp-bar') ? 'header' : e.closest('.lp-close') ? 'close' : 'hero'))
      }, viewport.height)
    // The observer that decides reports a frame after the scroll, and the
    // header's button then fades over a transition; the count is taken once
    // both are done.
    const rest = async () => {
      await sleep(120)
      await settled(page)
    }
    const once = async (where) => {
      await rest()
      const got = await visible()
      if (got.length !== 1) fail(`at ${viewport.width}px, ${where}, ${got.length} sign-ins are on screen (${got.join(', ') || 'none'}); a reader is offered the way in once at a time`)
      return got
    }
    const top = await once('at the top')
    if (top[0] && top[0] !== 'hero') fail(`at ${viewport.width}px the first screen offers the ${top[0]}'s sign-in rather than the hero's`)
    // The page is long on purpose. The requirement is that the sign-in stays
    // reachable throughout.
    const scrolled = await page.evaluate(() => {
      window.scrollTo(0, Math.round(document.body.scrollHeight / 3))
      return document.documentElement.scrollHeight > window.innerHeight
    })
    if (!scrolled) fail(`at ${viewport.width}px the landing does not scroll; it is meant to be a page, not a card`)
    await page.waitForFunction(() => window.scrollY > 0)
    const mid = await once('a third of the way down')
    if (mid[0] && mid[0] !== 'header') fail(`at ${viewport.width}px mid-page the way in is the ${mid[0]}'s, want the header's`)
    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight))
    await once('at the bottom')
    // Hidden means out of reach, not only out of sight: the standing-down
    // header button is out of the tab order while it is hidden.
    await page.evaluate(() => window.scrollTo(0, 0))
    await rest()
    const tabbable = await page.evaluate(() => getComputedStyle(document.querySelector('[data-test="lp-signin"]')).visibility)
    if (tabbable !== 'hidden') fail(`at the top the header's standing-down sign-in is ${tabbable}; a hidden button must leave the tab order too`)

    // Nothing runs off the side at either width.
    const wide = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1)
    if (wide) fail(`the landing scrolls sideways at ${viewport.width}px`)

    // The argument is on the page, not merely in the DOM.
    const copy = await page.evaluate(() => document.querySelector('.landing-lead').textContent.length)
    if (copy < 120) fail(`the landing lead is ${copy} characters; the point of this screen is that it says what the product does`)
    const pts = await page.evaluate(() => document.querySelectorAll('.landing-point').length)
    if (pts !== 3) fail(`the landing shows ${pts} points, want the inner loop, the outer loop, and the verdict`)
    const open_ = await page.evaluate(() => document.querySelectorAll('.landing-point.open').length)
    if (open_ !== 1) fail(`${open_} points are drawn open; exactly one, the outer loop, does not run yet`)
    await page.close()
  }

  /* ── Figure 1, and the bands it sits above ── */

  {
    const page = await open({ authenticated: false })
    await page.waitForSelector('.hf-svg')
    // A drawing, not a scene: one svg, no canvas anywhere on the page.
    const fig = await page.evaluate(() => {
      const cs = (s) => getComputedStyle(document.querySelector(s))
      return {
        svg: document.querySelector('.hf-svg')?.tagName.toLowerCase(),
        canvases: document.querySelectorAll('.landing canvas').length,
        outer: cs('.hf-outer').stroke,
        inner: cs('.hf-inner').stroke,
        stations: document.querySelectorAll('.hf-station').length,
        caption: document.querySelector('.hf-caption')?.textContent.trim() ?? '',
        steps: document.querySelectorAll('.lp-step').length,
        parts: document.querySelectorAll('.lp-part').length,
      }
    })
    if (fig.svg !== 'svg') fail('Figure 1 is not an svg')
    if (fig.canvases) fail(`the landing draws ${fig.canvases} canvases; Figure 1 is a drawing`)
    if (fig.outer === fig.inner) fail(`both loops are painted ${fig.outer}; the boundary is the color as much as the gap`)
    if (fig.stations !== 4) fail(`the inner loop shows ${fig.stations} stations, want implement, run, compare and record`)
    if (!/^Figure 1\./.test(fig.caption)) fail(`Figure 1's caption reads "${fig.caption.slice(0, 40)}"; a figure is numbered under itself`)
    if (fig.steps !== 4) fail(`the pipeline figure shows ${fig.steps} steps, want four`)
    if (fig.parts !== 3) fail(`the anatomy figure shows ${fig.parts} parts, want claim, evidence, and verdict`)

    // The marker travels the inner loop once the rings are drawn, and the pause
    // control stops it where it is and says so.
    const where = () => page.evaluate(() => document.querySelector('.hf-marker').getAttribute('transform'))
    await sleep(1800)
    const a = await where()
    await sleep(400)
    const b = await where()
    if (!a || a === b) fail(`the marker does not travel the loop (${a} then ${b}); the figure claims a re-run and shows a still`)
    await page.click('[data-test="hf-pause"]')
    await sleep(100)
    const c = await where()
    await sleep(400)
    const d = await where()
    if (c !== d) fail('pausing the figure does not stop the marker')
    const paused = await page.evaluate(() => {
      const b = document.querySelector('[data-test="hf-pause"]')
      return { pressed: b.getAttribute('aria-pressed'), label: b.getAttribute('aria-label') }
    })
    if (paused.pressed !== 'true' || !/play/i.test(paused.label || '')) fail(`paused, the control reads ${JSON.stringify(paused)}; it has to say it would play`)
    await page.click('[data-test="hf-pause"]')
    await sleep(400)
    if ((await where()) === d) fail('playing the figure again does not move the marker')
    await page.close()
  }

  /* ── Nothing shifts as the page arrives ── */

  {
    // Figure 1's box is the viewBox's ratio from the first frame, and the faces
    // are preloaded with `block`, so the first screen moves nothing once
    // painted. Layout shift is what a reader reads as imprecision.
    const page = await h.page({ routes: [['/me', { authenticated: false }]] })
    await page.evaluateOnNewDocument(() => {
      window.__shift = 0
      new PerformanceObserver((l) => {
        for (const e of l.getEntries()) if (!e.hadRecentInput) window.__shift += e.value
      }).observe({ type: 'layout-shift', buffered: true })
    })
    await page.open('/')
    await page.waitForSelector('.hf-svg')
    await sleep(1500)
    const shift = await page.evaluate(() => window.__shift)
    if (shift > 0.01) fail(`the first screen shifts by ${shift.toFixed(3)} as it arrives`)
    const fonts = await page.evaluate(async () => {
      await document.fonts.ready
      const serifs = [...document.querySelectorAll('.landing *')].filter((e) => {
        const f = getComputedStyle(e).fontFamily.replace(/sans-serif/gi, '')
        return /serif|Georgia|Times/i.test(f) && e.textContent.trim()
      }).length
      return { inter: document.fonts.check('600 16px Inter') && document.fonts.check('400 16px Inter'), serifs }
    })
    if (!fonts.inter) fail('Inter did not load; the page falls back to the system face')
    if (fonts.serifs) fail(`${fonts.serifs} elements on the landing set a serif; the page sets one face, Inter (047)`)
    await page.close()
  }

  /* ── Reduced motion gets the finished page, not a frozen first frame ── */

  {
    const page = await h.page({ routes: [['/me', { authenticated: false }]] })
    await page.emulateMediaFeatures([{ name: 'prefers-reduced-motion', value: 'reduce' }])
    await page.open('/')
    await page.waitForSelector('.lp-section')
    const still = await page.evaluate(() => {
      const bands = [...document.querySelectorAll('.lp-section, .lp-hero, .lp-step, .lp-part')]
      return {
        hidden: bands.filter((e) => +getComputedStyle(e).opacity < 0.99).length,
        animated: bands.filter((e) => getComputedStyle(e).animationName !== 'none').length,
        figure: [...document.querySelectorAll('.hf *')].filter((e) => getComputedStyle(e).animationName !== 'none').length,
        marker: document.querySelector('.hf-marker').getAttribute('transform'),
        pause: getComputedStyle(document.querySelector('[data-test="hf-pause"]')).display,
      }
    })
    if (still.hidden) fail(`${still.hidden} parts of the landing are transparent under reduced motion; the resting state has to be the finished one`)
    if (still.animated) fail(`${still.animated} bands still animate under reduced motion`)
    if (still.figure) fail(`${still.figure} parts of Figure 1 still animate under reduced motion`)
    if (still.pause !== 'none') fail('the figure offers a pause control under reduced motion, where it never runs')
    // The finished frame: the marker parked on "record", the left of the loop,
    // and staying there.
    await sleep(500)
    const parked = await page.evaluate(() => document.querySelector('.hf-marker').getAttribute('transform'))
    if (parked !== still.marker) fail('the marker moves under reduced motion')
    const x = Number((still.marker || '').match(/translate\(([\d.]+)/)?.[1])
    if (!(x < 300)) fail(`under reduced motion the marker rests at ${still.marker}; the finished frame parks it on "record"`)

    // The walkthrough dims the steps it is not on and folds their artifacts down
    // to a header strip. Both are motion by this page's own definition, so under
    // `reduce` the driver must never start: every step lit, every plate open. A
    // reader who asked for less movement is not made to scroll to find three
    // quarters of a band.
    await page.evaluate(() => {
      document.querySelector('.lp-walk').scrollIntoView()
    })
    // A window rather than a wait: the claim is that nothing happens. A driver
    // that did start paints on the scroll event and dims over half a second, so
    // this is long enough for either to show, and there is no state to wait for.
    await sleep(400)
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

  /* ── The hero carries its own copy beside the figure ── */

  for (const viewport of [DESKTOP, PHONE]) {
    const page = await open({ authenticated: false }, { viewport })
    await page.waitForSelector('.lp-hero-copy')
    const hero = await page.evaluate(() => {
      const copy = document.querySelector('.lp-hero-copy')
      const inside = (sel) => {
        const c = document.querySelector(sel)
        return !!c && copy.contains(c) && c.getBoundingClientRect().width > 0
      }
      const svg = document.querySelector('.hf-svg').getBoundingClientRect()
      return {
        h1: copy.querySelector('h1')?.textContent.trim().length ?? 0,
        signin: inside('[data-test="lp-hero-signin"]'),
        registry: inside('[data-test="lp-hero-registry"]'),
        figure: svg.width > 0 && svg.height > 0,
      }
    })
    if (hero.h1 < 20) fail(`at ${viewport.width}px the hero carries no claim`)
    if (!hero.signin) fail(`at ${viewport.width}px the hero's sign-in is not with its claim`)
    if (!hero.registry) fail(`at ${viewport.width}px the hero's registry link is not with its claim`)
    if (!hero.figure) fail(`at ${viewport.width}px Figure 1 has no box`)
    await page.close()
  }

  /* ── Every word on the page is copy, not a key ── */

  {
    // t() falls back to the key when a locale has no string for it, which is
    // right for a locale that is behind and wrong for a key that exists
    // nowhere: the boundary band showed `landingVerdictBody` to readers for
    // three weeks because the strings had been deleted as unused while the
    // markup still asked for them. The compiler catches that now, because t()
    // is typed against TKey, and this makes the same check on the rendered
    // page, which is where a reader would see it.
    const page = await open({ authenticated: false })
    await page.waitForSelector('.lp-section')
    await scrollThrough(page)
    const keys = await page.evaluate(() => {
      const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
      const bad = []
      for (let n = walk.nextNode(); n; n = walk.nextNode()) {
        const text = n.textContent.trim()
        // A key is one lowerCamelCase word: no space, no punctuation, and an
        // interior capital. Real copy on this page is either a sentence, a
        // filename, or a proper noun, and none of those match.
        if (/^[a-z][a-zA-Z]{9,}$/.test(text) && /[A-Z]/.test(text)) bad.push(text)
      }
      return bad
    })
    if (keys.length) fail(`the landing paints ${keys.length} untranslated keys at readers: ${JSON.stringify([...new Set(keys)])}`)
    // The same check for the other marker a reader must never see. A seam is
    // an instruction to whoever sets the line; t() removes it for every other
    // consumer, and the tab once read "No page|at this address" because it did
    // not.
    const seam = await page.evaluate(() => {
      const bad = []
      const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
      for (let n = walk.nextNode(); n; n = walk.nextNode()) {
        if (n.textContent.includes('|')) bad.push(n.textContent.trim().slice(0, 60))
      }
      if (document.title.includes('|')) bad.push(`<title> ${document.title}`)
      return bad
    })
    if (seam.length) fail(`the landing shows the seam marker to readers: ${JSON.stringify(seam)}`)
    await page.close()
  }

  /* ── The page is one width, top to bottom (044) ── */

  {
    // The hero is full-bleed and every band under it used to be capped at
    // 1180px, so a wide screen got a full-width first screen followed by a
    // column with 400px of empty space on either side. The page has one edge
    // now, and this asserts it: the gutter each band leaves is the page's own
    // margin rather than the remainder of a cap, on the widest viewport the
    // page claims to serve.
    // With published work, so the registry strip is a band with contents to
    // measure rather than one this loop skips.
    const published = Array.from({ length: 6 }, (_, i) => ({
      id: `p${i}`,
      title: `Attention Is All You Need — decoder ablation ${i}`,
      signed_off_name: 'Ada Lovelace',
      session_count: 3,
      visibility: 'public',
      papers: [{ title: 'Attention Is All You Need', authors: ['Ashish Vaswani', 'Noam Shazeer'], venue: 'NeurIPS', year: 2017 }],
    }))
    const page = await open({ authenticated: false }, { viewport: WIDE, routes: [['/public', published]] })
    await page.waitForSelector('[data-test="lp-strip"] .lp-card')
    // Every band, including the ones a reveal observer has not reached yet:
    // geometry is measured on the box, and a transparent box measures the same.
    // 047 sets every band on the platform's container, min(1200px, 100% − 96px),
    // so the measure is that each band shares the hero's two edges and sits
    // centered: one column down the page, never a band of its own width.
    const bands = await page.evaluate(async () => {
      const out = []
      for (const band of document.querySelectorAll('.lp-hero, .lp-section, .lp-close-wrap')) {
        band.scrollIntoView()
        await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))
        const r = band.getBoundingClientRect()
        out.push({ name: band.dataset.test || band.className, left: r.left, right: r.right })
      }
      return { bands: out, page: document.documentElement.clientWidth }
    })
    // Every band on the page: the hero, seven sections, the close. A band that
    // rendered nothing would otherwise go uncovered here.
    if (bands.bands.length < 9) fail(`only ${bands.bands.length} bands were measured at ${WIDE.width}px; the page did not render`)
    const hero = bands.bands[0]
    const width = Math.min(1200, bands.page - 96)
    if (hero && Math.abs(hero.right - hero.left - width) > 2) fail(`at ${WIDE.width}px the hero is ${Math.round(hero.right - hero.left)}px wide, want the ${width}px container`)
    for (const b of bands.bands) {
      if (Math.abs(b.left - hero.left) > 2 || Math.abs(b.right - hero.right) > 2) fail(`at ${WIDE.width}px the band ${b.name} runs ${Math.round(b.left)}–${Math.round(b.right)}, off the hero's ${Math.round(hero.left)}–${Math.round(hero.right)}`)
      if (Math.abs(b.left - (bands.page - b.right)) > 2) fail(`at ${WIDE.width}px the band ${b.name} is off center`)
    }
    // The verdict fills the column it is set in, and no grid shreds its cards
    // into tracks too narrow to read.
    const fill = await page.evaluate(() => {
      const w = document.querySelector('.lp-window').getBoundingClientRect().width
      const col = document.querySelector('[data-test="lp-anatomy"] .lp-showcase').getBoundingClientRect().width
      return { w, col }
    })
    if (Math.abs(fill.w - fill.col) > 2) fail(`at ${WIDE.width}px the verdict window is ${Math.round(fill.w)}px in a ${Math.round(fill.col)}px column`)
    const cards = await page.evaluate(() => {
      const widths = (sel) => [...document.querySelectorAll(sel)].map((e) => Math.round(e.getBoundingClientRect().width))
      return { fields: widths('.lp-field'), costs: widths('.lp-cost'), anatomy: widths('.lp-part'), registry: widths('.lp-card') }
    })
    for (const [name, w] of Object.entries(cards)) {
      if (!w.length) fail(`at ${WIDE.width}px the ${name} grid rendered no cards`)
      const thin = w.filter((n) => n < 240)
      if (thin.length) fail(`at ${WIDE.width}px ${thin.length} of the ${name} cards are under 240px wide (${w.join(', ')}); the grid shredded rather than filled`)
    }
    await page.close()
  }

  /* ── Every band reaches the page, motion or not ── */

  {
    // The counterpart to the reduced-motion block, and the one that was missing:
    // with motion allowed every band starts transparent and is brought in by
    // an observer, so a band whose reveal ref was never wired is on the page,
    // occupying its full height, and invisible. Geometry assertions do not catch
    // that, because a box at opacity 0 measures exactly like a box at opacity 1,
    // which is how a blank hero reached production.
    const page = await open({ authenticated: false }, { routes: [['/public', []]] })
    await page.waitForSelector('.lp-hero')
    // The hero is above the fold and must be visible with no scrolling at all.
    // Its reveal is a transition, so it is waited for; one that never comes is
    // the failure the check reports.
    await page.waitForFunction(() => +getComputedStyle(document.querySelector('.lp-hero')).opacity >= 0.9, { timeout: 3000 }).catch(() => {})
    const heroOpacity = await page.evaluate(() => +getComputedStyle(document.querySelector('.lp-hero')).opacity)
    if (heroOpacity < 0.9) fail(`the hero is at opacity ${heroOpacity} on a page nobody has scrolled; the first screen is invisible`)
    const dark = await page.evaluate(async () => {
      const bands = [...document.querySelectorAll('.lp-hero, .lp-section')]
      const seen = (b) => +getComputedStyle(b).opacity >= 0.9
      const frame = () => new Promise((r) => requestAnimationFrame(r))
      // Each band is given the time its reveal takes, the observer and then the
      // transition, and no more; one that never arrives is reported.
      for (const b of bands) {
        b.scrollIntoView()
        for (const t0 = performance.now(); !seen(b) && performance.now() - t0 < 1500; ) await frame()
      }
      return bands.filter((b) => !seen(b)).map((b) => b.className)
    })
    if (dark.length) fail(`${dark.length} bands never became visible after being scrolled to: ${JSON.stringify(dark)}`)
    await page.close()
  }

  /* ── The walkthrough drives, where driving it is motion a reader allows ── */

  {
    const page = await open({ authenticated: false })
    await page.waitForSelector('.lp-walk')
    await page.evaluate(() => {
      document.querySelector('.lp-step[data-step="2"]').scrollIntoView({ block: 'center' })
      window.dispatchEvent(new Event('scroll'))
    })
    // The driver paints on the scroll and then dims and folds over half a
    // second, so the count is taken once the transitions it started have ended.
    await settled(page)
    const lit = await page.evaluate(() => {
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
      // The example's rate is derived from its table and set as that table's
      // footnote (047): under it, and no larger than its cells.
      foot: (() => {
        const f = document.querySelector('.lp-verdict-foot')
        const t = document.querySelector('.lp-verdict-table')
        const cell = document.querySelector('.lp-verdict-table td')
        if (!f || !t || !cell) return null
        return { below: f.getBoundingClientRect().top >= t.getBoundingClientRect().bottom, size: parseFloat(getComputedStyle(f).fontSize), cell: parseFloat(getComputedStyle(cell).fontSize), text: f.textContent }
      })(),
      // Every DOI-shaped string on the page. The verdict this page shows is
      // invented, and an invented verdict attached to somebody's real DOI is the
      // exact failure this product exists to catch.
      dois: (document.body.innerText.match(/10\.\d{4,9}\/[^\s]+/g) || []),
    }))
    // Every file the method asks a session to write (`method.Deliverables()` in
    // internal/method, which the publication gate reads too), and src/.
    const want = ['README.md', 'REPRODUCIBILITY.md', 'UNVERIFIED.md', 'SOURCES.md', 'verdict.json', 'instrument.json', 'src/']
    if (JSON.stringify(shown.files) !== JSON.stringify(want)) fail(`the page says a session ships ${JSON.stringify(shown.files)}, and the method asks for ${JSON.stringify(want)}`)
    if (!shown.foot) fail('the example verdict has no footnote for its rate')
    else {
      if (!shown.foot.below) fail('the example rate sits above the table it is derived from')
      if (shown.foot.size > shown.foot.cell) fail(`the example rate is set at ${shown.foot.size}px over cells at ${shown.foot.cell}px; a rate is a footnote, never a headline`)
      if (!/72%/.test(shown.foot.text) || !/verified/i.test(shown.foot.text)) fail(`the footnote does not carry the rate with its counts: ${shown.foot.text}`)
    }
    if (!shown.example) fail('the demo verdict is not marked as an example; it is a fabrication and has to say so')
    // An empty match set would satisfy the filter below in the same way as "only
    // the illustrative key is there", and this guard must never pass because it
    // checked nothing.
    if (!shown.dois.some((d) => d.startsWith('10.0000/'))) fail('the paper-key guard matched nothing, so it cannot prove the demo key is the illustrative one')
    // 10.0000 is unassigned, so it resolves to nothing. Any other registrant
    // prefix means the page hung a made-up verdict on a real paper.
    const real = shown.dois.filter((d) => !d.startsWith('10.0000/'))
    if (real.length) fail(`the landing shows a resolvable paper key beside an invented verdict: ${JSON.stringify(real)}`)
    await page.close()
  }

  /* ── The published strip is a band a reader can see ── */

  {
    // The strip renders only once its fetch lands, which is after the first
    // paint, and every band on this page starts at the pre-reveal opacity. A
    // reveal that was wired up on mount alone never observed this one, so the
    // section was on the page, occupying its full height, and invisible.
    const published = [
      {
        id: 'a',
        title: 'Attention Is All You Need — decoder ablation',
        signed_off_name: 'Ada Lovelace',
        session_count: 3,
        visibility: 'public',
        // Two papers, the first one cited (043). The second's authors must never
        // appear on the first's line, and its existence must be counted.
        papers: [
          { title: 'Attention Is All You Need', authors: ['Ashish Vaswani', 'Noam Shazeer', 'Niki Parmar', 'Jakob Uszkoreit'], venue: 'NeurIPS', year: 2017 },
          { title: 'Point of no Undo', authors: ['Barbara Liskov'], venue: 'UIST', year: 2023 },
        ],
      },
      // A paper nobody could look up. The card must say nothing rather than
      // render an empty line, a stray separator, or a count with no citation.
      { id: 'b', title: 'The Placebo Effect of Artificial Intelligence', signed_off_name: 'Grace Hopper', session_count: 1, visibility: 'public', papers: [{ title: 'The Placebo Effect' }] },
    ]
    const page = await open({ authenticated: false }, { routes: [['/public', published]] })
    await page.waitForSelector('[data-test="lp-strip"]')
    const strip = await page.evaluate(async () => {
      const band = document.querySelector('[data-test="lp-strip"]')
      band.scrollIntoView()
      const seen = (e) => {
        const cs = getComputedStyle(e)
        const r = e.getBoundingClientRect()
        return +cs.opacity > 0.9 && cs.visibility !== 'hidden' && r.width > 0 && r.height > 0
      }
      const all = () => [...document.querySelectorAll('.lp-card')]
      // The band reveals when the observer reaches it and its cards arrive one
      // after the next, so the wait is for the last of them; a card that never
      // shows is what the counts below report.
      const frame = () => new Promise((r) => requestAnimationFrame(r))
      for (const t0 = performance.now(); !(seen(band) && all().every(seen)) && performance.now() - t0 < 3000; ) await frame()
      const cards = all()
      return {
        band: seen(band),
        cards: cards.length,
        painted: cards.filter(seen).length,
        titles: cards.map((c) => c.querySelector('.lp-card-title')?.textContent.trim()),
        bylines: cards.map((c) => c.querySelector('.lp-card-by')?.textContent.trim()),
        cites: cards.map((c) => c.querySelector('[data-test="lp-card-cite"]')?.textContent.trim() ?? null),
        links: cards.map((c) => c.getAttribute('href')),
      }
    })
    if (!strip.band) fail('the published band is on the page and invisible')
    if (strip.cards !== 2) fail(`the strip rendered ${strip.cards} cards for two published projects`)
    if (strip.painted !== strip.cards) fail(`${strip.cards - strip.painted} of ${strip.cards} strip cards never became visible`)
    if (!strip.titles.every(Boolean)) fail(`a strip card has no title: ${JSON.stringify(strip.titles)}`)
    // The card's second line is a person's name under a paper's title, which is
    // the shape of an author byline, and the signer is not an author. The line
    // has to say what the name is for.
    const signers = ['Ada Lovelace', 'Grace Hopper']
    strip.bylines.forEach((by, i) => {
      if (!by?.includes(signers[i])) fail(`strip card ${i} does not name its signer: ${by}`)
      if (by.trim() === signers[i]) fail(`strip card ${i} names "${by}" and nothing else; a bare name reads as the paper's author`)
      if (!/sign/i.test(by)) fail(`strip card ${i} says "${by}"; it has to say the name signed the reproduction`)
    })
    // 043: the card names the paper it is a reproduction of. The project title
    // is a name somebody typed; this line is the paper's own.
    const cite = strip.cites[0]
    if (!cite) fail('the first strip card shows no citation for a paper that has one')
    else {
      for (const want of ['Ashish Vaswani', 'NeurIPS', '2017']) {
        if (!cite.includes(want)) fail(`the strip citation does not carry ${want}: ${cite}`)
      }
      if (!/et al\./.test(cite)) fail(`a four-author paper is listed in full on a card: ${cite}`)
      if (cite.includes('Jakob Uszkoreit')) fail(`the strip citation lists a fourth author it should have dropped: ${cite}`)
      // The second paper is counted, never merged: its author on the first
      // paper's line would attribute one paper's work to the other.
      if (cite.includes('Barbara Liskov') || cite.includes('UIST')) fail(`the strip citation merged a second paper into the first: ${cite}`)
      if (!cite.includes('1 more')) fail(`a two-paper project does not count its second paper: ${cite}`)
    }
    // A record nobody could fill in renders no line at all: not an empty one,
    // and not a bare separator.
    if (strip.cites[1] !== null) fail(`a card with no bibliography still rendered a citation: ${JSON.stringify(strip.cites[1])}`)
    if (!strip.links.every((h) => h?.startsWith('/p/'))) fail(`a strip card links nowhere useful: ${JSON.stringify(strip.links)}`)
    await page.close()
  }

  // An empty registry is not an empty band: the section is absent entirely,
  // because a heading with nothing under it would state that nobody has
  // published.
  {
    const page = await open({ authenticated: false }, { routes: [['/public', []]] })
    await page.waitForSelector('.landing')
    // "No band" proves nothing until the registry has answered and the page has
    // drawn the result. The request log is consulted rather than a response
    // waiter, because the answer may already have landed by the time the
    // landing is on screen, and a waiter armed after that never resolves.
    await waitUntil(() => page.calls.includes('GET /public'), { what: 'the registry to be asked' })
    await settled(page)
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
    // own round trip is asserted from an address that still needs an account.
    // public-shell.test.mjs guards the public deep link.
    const page = await open({ authenticated: false }, { path: '/new' })
    await page.waitForSelector('.landing')
    const href = await page.evaluate(() => document.querySelector('[data-test="lp-signin"]').getAttribute('href'))
    if (href !== '/login?next=%2Fnew') fail(`signed out at /new the button points at ${href}; the address has to survive the round trip`)
    // The button carries the provider's mark, painted in the button's own ink.
    // A mark that inherited a color from somewhere else would be invisible on
    // the brand fill and nothing else here would notice.
    const provider = await page.evaluate(() => {
      const btnEl = document.querySelector('[data-test="lp-signin"]')
      const svg = btnEl?.querySelector('svg')
      if (!svg) return null
      const r = svg.getBoundingClientRect()
      const btn = getComputedStyle(btnEl)
      return { w: r.width, h: r.height, fill: getComputedStyle(svg).fill, ink: btn.color, bg: btn.backgroundColor, paths: svg.querySelectorAll('path').length }
    })
    // Hovered, too. 034 made the base `.btn:hover` repaint the fill and the
    // ink, so a slab button that overrides only the fill inherits the base's
    // ink. That is how the hero's sign-in became white on white, taking the
    // latere mark with it, since the mark fills with currentColor.
    const hovered = await page.evaluate(async () => {
      const out = []
      for (const sel of ['[data-test="lp-hero-signin"]', '.lp-close .btn:not(.ghost)']) {
        const el = document.querySelector(sel)
        if (!el) continue
        el.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }))
        // :hover is not settable from script, so read the rules that would apply.
        const rules = [...document.styleSheets].flatMap((s) => {
          try {
            return [...s.cssRules]
          } catch {
            return []
          }
        })
        const decl = {}
        for (const r of rules) {
          if (!r.selectorText || !/:hover/.test(r.selectorText)) continue
          const bare = r.selectorText.replaceAll(':hover', '')
          let hit = false
          try {
            hit = el.matches(bare)
          } catch {
            hit = false
          }
          if (!hit) continue
          if (r.style.backgroundColor) decl.bg = r.style.backgroundColor
          if (r.style.color) decl.fg = r.style.color
        }
        const resolve = (v) => {
          if (!v) return ''
          const probe = document.createElement('span')
          probe.style.color = v
          el.appendChild(probe)
          const got = getComputedStyle(probe).color
          probe.remove()
          return got
        }
        out.push({ sel, bg: resolve(decl.bg), fg: resolve(decl.fg) })
      }
      return out
    })
    for (const h of hovered) {
      if (h.bg && h.fg && h.bg === h.fg) fail(`hovered, ${h.sel} paints its label ${h.fg} on a ${h.bg} fill — the button goes blank under the pointer`)
    }
    if (!provider) fail('the sign-in button lost the latere mark')
    else {
      if (provider.paths < 6) fail(`the latere mark draws ${provider.paths} paths; the canonical one has six`)
      if (provider.h < 8 || provider.w < 12) fail(`the latere mark is ${provider.w}×${provider.h}px, too small to read`)
      if (provider.fill !== provider.ink) fail(`the latere mark is filled ${provider.fill} but the button's ink is ${provider.ink}`)
      if (provider.fill === provider.bg) fail('the latere mark is filled in the button background and cannot be seen')
    }
    await page.close()
  }

  /* ── Every word reads on its ground, in both themes (047) ── */

  {
    // 040 guarded four dark blocks; 047 removed them, and what is left to guard
    // is the thing that block test was for: that no ink lands on a ground it
    // cannot be read on. Every text element on the page is measured against
    // the first opaque ground behind it, its ink composited over that ground
    // first, and must reach WCAG's 4.5:1. Measured at rest, under reduced
    // motion, because the walkthrough dims the steps it is not on by design.
    for (const theme of ['light', 'dark']) {
      const page = await h.page({ routes: [['/me', { authenticated: false }]] })
      await page.emulateMediaFeatures([{ name: 'prefers-reduced-motion', value: 'reduce' }])
      await page.evaluateOnNewDocument((t) => localStorage.setItem('theme', t), theme)
      await page.open('/')
      await page.waitForSelector('.lp-close')
      const worst = await page.evaluate(() => {
        const rgba = (c) => {
          const n = c.match(/\d+(\.\d+)?/g).map(Number)
          return [n[0], n[1], n[2], n.length > 3 ? n[3] : 1]
        }
        const over = (fg, bg) => fg.slice(0, 3).map((v, i) => v * fg[3] + bg[i] * (1 - fg[3]))
        const lum = ([r, g, b]) => {
          const f = (v) => {
            const u = v / 255
            return u <= 0.03928 ? u / 12.92 : Math.pow((u + 0.055) / 1.055, 2.4)
          }
          return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)
        }
        const ground = (e) => {
          for (let n = e; n; n = n.parentElement) {
            const c = rgba(getComputedStyle(n).backgroundColor)
            if (c[3] > 0.9) return c
          }
          return rgba(getComputedStyle(document.body).backgroundColor)
        }
        const out = []
        const texts = [...document.querySelectorAll('.landing h1, .landing h2, .landing h3, .landing p, .landing li, .landing span, .landing a, .landing th, .landing td, .landing b')].filter(
          (e) => [...e.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim()) && e.getBoundingClientRect().width > 0 && !e.closest('footer.lu-footer'),
        )
        for (const e of texts) {
          const bg = ground(e)
          const fg = over(rgba(getComputedStyle(e).color), bg)
          const a = lum(fg)
          const b = lum(bg)
          const ratio = (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
          if (ratio < 4.5) out.push(`${e.className || e.tagName} "${e.textContent.trim().slice(0, 30)}" ${ratio.toFixed(2)}:1`)
        }
        // The figure's labels are svg text, painted by fill.
        const board = ground(document.querySelector('.hf-board'))
        for (const t of document.querySelectorAll('.hf-svg text')) {
          const fg = over(rgba(getComputedStyle(t).fill), board)
          const a = lum(fg)
          const b = lum(board)
          const ratio = (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
          if (ratio < 4.5) out.push(`figure text "${t.textContent.trim()}" ${ratio.toFixed(2)}:1`)
        }
        return out
      })
      if (worst.length) fail(`in ${theme} mode ${worst.length} texts read under 4.5:1: ${JSON.stringify(worst.slice(0, 8))}`)
      await page.close()
    }
  }

  /* ── Every control shows where the keyboard is (047) ── */

  {
    const page = await open({ authenticated: false })
    await page.waitForSelector('.lp-hero')
    const missing = []
    for (let i = 0; i < 10; i++) {
      await page.keyboard.press('Tab')
      const ring = await page.evaluate(() => {
        const e = document.activeElement
        if (!e || e === document.body) return null
        const cs = getComputedStyle(e)
        return { name: (e.textContent || e.getAttribute('aria-label') || e.tagName).trim().slice(0, 30), shown: cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) >= 2 }
      })
      if (ring && !ring.shown) missing.push(ring.name)
    }
    if (missing.length) fail(`${missing.length} controls show no focus ring: ${JSON.stringify(missing)}`)
    await page.close()
  }

  /* ── The footer opens with the product's own lockup ── */

  {
    // The shared footer is one shape on every Latere site, and a site passes it
    // only its lockup. This one is the header's: the drawn mark and the
    // wordmark, linked home. A footer that fell back to the package's default
    // would read "Latere AI" here, on a page that names ReplicHAI everywhere
    // else.
    const page = await open({ authenticated: false })
    await page.waitForSelector('.landing footer.lu-footer')
    const lock = await page.evaluate(() => {
      const head = document.querySelector('.lp-bar .landing-lockup')
      const foot = document.querySelector('.landing footer.lu-footer .lu-footer-lockup')
      const ours = foot?.querySelector('.landing-lockup')
      const mark = ours?.querySelector('svg.mark')
      const cs = (e) => getComputedStyle(e)
      return {
        href: foot?.querySelector('a')?.getAttribute('href') ?? null,
        word: ours?.querySelector('.wordmark')?.textContent.trim() ?? null,
        rings: !!mark?.querySelector('.mark-outer') && !!mark?.querySelector('.mark-inner'),
        // The same element as the header's, so it is set at the same size and
        // in the same face.
        same:
          !!head &&
          !!ours &&
          cs(head).fontSize === cs(ours).fontSize &&
          cs(head.querySelector('.wordmark')).fontFamily === cs(ours.querySelector('.wordmark')).fontFamily,
        fallback: !!foot?.querySelector('.lu-footer-word'),
      }
    })
    if (lock.href !== '/') fail(`the footer's lockup links ${JSON.stringify(lock.href)}, want the product's home`)
    if (lock.word !== 'ReplicHAI') fail(`the footer's lockup reads ${JSON.stringify(lock.word)}, want ReplicHAI`)
    if (!lock.rings) fail("the footer's lockup has no drawn mark with both loops")
    if (!lock.same) fail("the footer's lockup is not set as the header's")
    if (lock.fallback) fail("the footer draws latere-ui's default lockup beside or instead of the product's")
    await page.close()
  }

  /* ── The page ends in the Latere footer, and it works (040) ── */

  {
    const page = await open({ authenticated: false })
    await page.waitForSelector('.landing footer.lu-footer')
    const foot = await page.evaluate(() => {
      const f = document.querySelector('.landing footer.lu-footer')
      const hrefs = [...f.querySelectorAll('a')].map((a) => a.getAttribute('href') ?? '')
      return {
        // The lineup, so the product is placed among the others rather than
        // alone: five groups in four columns, Research under Applications.
        groups: [...f.querySelectorAll('[data-footer-group]')].map((g) => g.dataset.footerGroup),
        // Each product link names its product, whose gradient it takes under
        // the pointer or keyboard focus.
        brands: [...f.querySelectorAll('.lu-footer-link[data-brand]')].map((a) => a.dataset.brand),
        gallery: hrefs.includes('https://latere.site/'),
        // The chat as the lineup names it, and the catalog that describes
        // every product, on the company site.
        chat: f.querySelector('[data-footer-group="applications"] a[href="https://chat.latere.ai/"]')?.textContent.trim() ?? null,
        catalog: f.querySelector('[data-footer-group="company"] a[href="https://latere.ai/products"]')?.textContent.trim() ?? null,
        ours: f.querySelector('[data-footer-group="research"] a[href="https://replichai.latere.ai/"]')?.textContent.trim() ?? null,
        platform: hrefs.includes('https://platform.latere.ai/console'),
        // Drive, Topos, Cella and Lux are sections of the platform console,
        // reached through its one link, and have no entry of their own.
        retired: hrefs.filter((h) => /^https:\/\/(drive|topos|cella|lux)\.latere\.ai/.test(h)),
        legal: ['/legal/privacy', '/legal/terms', '/legal/impressum'].filter((p) => hrefs.some((h) => h.endsWith(p))).length,
        rights: /©|\(c\)/i.test(f.querySelector('.lu-footer-bottom')?.textContent ?? ''),
        // A palette the app does not define would leave the ink unset, which
        // computes to the inherited color rather than to nothing.
        ink: getComputedStyle(f.querySelector('.lu-footer-link')).color,
        muted: getComputedStyle(f.querySelector('.lu-footer-bottom p')).color,
        // The old one-line strip is gone, not left behind next to the new one.
        legacy: !!document.querySelector('.lp-foot'),
      }
    })
    if (foot.groups.join() !== 'applications,research,platform,company,legal') fail(`the footer's groups are ${JSON.stringify(foot.groups)}, want applications, research, platform, company and legal`)
    if (foot.brands.join() !== 'chat,replichai,platform') fail(`the footer's product links are ${JSON.stringify(foot.brands)}, want chat, replichai and platform`)
    if (!foot.gallery) fail('the footer does not link the gallery')
    if (foot.chat !== 'Latere Chat') fail(`the footer lists the chat as ${JSON.stringify(foot.chat)}, want Latere Chat`)
    if (foot.catalog !== 'Products') fail(`the footer's link to the product catalog reads ${JSON.stringify(foot.catalog)}, want Products`)
    if (foot.ours !== 'ReplicHAI') fail(`the research group does not link this product by name (found ${JSON.stringify(foot.ours)})`)
    if (!foot.platform) fail('the footer does not link the platform console')
    if (foot.retired.length) fail(`the footer still links ${JSON.stringify(foot.retired)}; those products are platform sections now`)
    if (foot.legal !== 3) fail(`the footer carries ${foot.legal} of the three legal links`)
    if (!foot.rights) fail('the footer has no copyright line')
    if (foot.ink === foot.muted) fail(`the footer's link ink and its muted ink are both ${foot.ink}; the palette aliases did not reach it`)
    if (foot.legacy) fail('the landing still carries its own one-line footer beside the shared one')

    // The appearance control is an icon button opening a menu of choices with
    // the current one checked. The items exist only while the menu is open, so
    // each read opens it first.
    const menu = async (which) => {
      await page.click(`.landing footer.lu-footer .lu-${which}-menu .lu-pref-trigger`)
      await page.waitForSelector(`.landing footer.lu-footer .lu-${which}-menu [role="menuitemradio"]`)
      return page.evaluate((w) => {
        const root = document.querySelector(`.landing footer.lu-footer .lu-${w}-menu`)
        const items = [...root.querySelectorAll('[role="menuitemradio"]')]
        return {
          trigger: root.querySelector('.lu-pref-trigger').getAttribute('aria-label'),
          items: items.map((i) => i.textContent.trim()),
          checked: items.filter((i) => i.getAttribute('aria-checked') === 'true').map((i) => i.textContent.trim()),
        }
      }, which)
    }
    const shut = async (which) => {
      await page.keyboard.press('Escape')
      await page.waitForFunction((w) => !document.querySelector(`.landing footer.lu-footer .lu-${w}-menu [role="menuitemradio"]`), { timeout: 2000 }, which)
    }

    // `open` seeds the light appearance, so the trigger names it and the menu
    // checks it.
    const themes = await menu('theme')
    if (themes.items.join() !== 'Light,Dark,System') fail(`the footer offers the appearances ${JSON.stringify(themes.items)}, want light, dark, and system`)
    if (themes.checked.length !== 1 || themes.checked[0] !== 'Light') fail(`the appearance menu checks ${JSON.stringify(themes.checked)}, want only Light`)
    if (themes.trigger !== 'Theme: Light') fail(`the appearance control is labeled "${themes.trigger}", want "Theme: Light"`)
    await shut('theme')

    // One language, because the app has one: the footer offers no language
    // menu at all, rather than a menu that pretends to a choice.
    if (await page.evaluate(() => !!document.querySelector('.landing footer.lu-footer .lu-locale-menu'))) {
      fail('the footer offers a language menu; the app ships English only')
    }

    // The control is wired to the app's own theme, not to a copy of it. The
    // choice is made by name: the menu lists Light, Dark and System, and picks
    // whichever of Light and Dark is not showing now.
    const before = await page.evaluate(() => document.documentElement.dataset.theme)
    const want = before === 'dark' ? 'Light' : 'Dark'
    await menu('theme')
    await page.evaluate((label) => {
      const items = [...document.querySelectorAll('.landing footer.lu-footer .lu-theme-menu [role="menuitemradio"]')]
      items.find((i) => i.textContent.trim() === label).click()
    }, want)
    // The switch is waited for rather than given time; one that never comes is
    // what the first check below reports.
    await page.waitForFunction((b) => document.documentElement.dataset.theme !== b, { timeout: 1000 }, before).catch(() => {})
    const flipped = await page.evaluate(() => ({
      after: document.documentElement.dataset.theme,
      stored: localStorage.getItem('theme'),
      open: !!document.querySelector('.landing footer.lu-footer .lu-theme-menu [role="menuitemradio"]'),
    }))
    if (flipped.after === before) fail(`the footer's appearance control did nothing; <html> stayed ${before}`)
    if (flipped.stored !== flipped.after) fail(`the footer changed the appearance to ${flipped.after} and stored ${flipped.stored}`)
    if (flipped.open) fail('the appearance menu stayed open after a choice')
    const again = await menu('theme')
    if (again.checked.length !== 1 || again.checked[0] !== want) fail(`after the switch the appearance menu checks ${JSON.stringify(again.checked)}, want only ${want}`)
    if (again.trigger !== `Theme: ${want}`) fail(`after the switch the appearance control is labeled "${again.trigger}", want "Theme: ${want}"`)
    await shut('theme')
    await page.close()
  }

  /* ── The failure screens keep the plain card, and inherit the mark ── */

  {
    // A seam makes its segments unbreakable, and an unbreakable segment that
    // does not fit overflows rather than breaking. These two titles are the
    // narrowest headings in the product, in a 420px card less its padding on a
    // 320px phone, so a seam placed one word too far to the right shows up here
    // as a card wider than the screen.
    for (const [me, what] of [
      [null, 'the auth-outage card'],
      [{ authenticated: true, principal: 'p', email: 'nobody@example.com', tier: 'none' }, 'the no-access card'],
    ]) {
      const page = await open(me, { viewport: { width: 320, height: 700 } })
      await page.waitForSelector('.auth-center .auth-card h2')
      const fit = await page.evaluate(() => {
        const h = document.querySelector('.auth-card h2')
        const card = document.querySelector('.auth-card')
        return {
          over: document.documentElement.scrollWidth > document.documentElement.clientWidth,
          spills: Math.round(h.scrollWidth) > Math.round(card.clientWidth),
          text: h.textContent.trim(),
        }
      })
      if (fit.over) fail(`at 320px ${what} pushes the page sideways; a seam in "${fit.text}" is holding a segment too wide to fit`)
      if (fit.spills) fail(`at 320px the title of ${what} ("${fit.text}") is wider than the card it is in`)
      await page.close()
    }
  }

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

  h.ok('the landing is a page that reads, and the way in never leaves the screen')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
