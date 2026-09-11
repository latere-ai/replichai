// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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
        // transparent. The `r` this replaced would fail nothing here — it was a
        // div — which is why the tag is asserted first.
        innerStroke: cs(inner).stroke,
        outerStroke: cs(outer).stroke,
        outerOpacity: +cs(outer).opacity,
        // The outer loop is drawn open. Losing the dash array turns the mark
        // into two concentric rings and the drawing stops saying anything.
        dashed: (inner.getAttribute('stroke-dasharray') || '') === '' && (outer.getAttribute('stroke-dasharray') || '') !== '',
        // The product's own wordmark stays font-independent. 040 self-hosts
        // Instrument Serif for the shared footer, so "the app does not ship it"
        // is no longer the reason — the reason is that an identity which waits
        // on a download is one that can be missing.
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
      if (/Instrument Serif/.test(mark.font)) fail(`the wordmark asks for ${mark.font}; the identity is drawn, not set`)
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
    await page.waitForFunction(() => window.scrollY > 0)
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
    const page = await h.page({ routes: [['/me', { authenticated: false }]] })
    await page.emulateMediaFeatures([{ name: 'prefers-reduced-motion', value: 'reduce' }])
    await page.open('/')
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

  /* ── Every word on the page is copy, not a key ── */

  {
    // t() falls back to the key when a locale has no string for it, which is
    // right for a locale that is behind and wrong for a key that exists
    // nowhere: the boundary band painted `landingVerdictBody` at readers for
    // three weeks because the strings had been deleted as unused while the
    // markup still asked for them. The compiler catches that now — t() is typed
    // against TKey — and this is the same claim made about the rendered page,
    // which is where a reader meets it.
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
    // The same claim about the other marker a reader must never meet. A seam is
    // an instruction to whoever sets the line; t() takes it out for everybody
    // else, and the tab once read "No page|at this address" because it did not.
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
    // column with 400px of nothing on either side. The page has one edge now,
    // and this is what says so: the gutter each band leaves is the page's own
    // margin rather than the remainder of a cap, on the widest viewport we
    // claim to serve.
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
    const bands = await page.evaluate(async () => {
      const out = []
      for (const band of document.querySelectorAll('.lp-hero, .lp-section, .lp-close-wrap')) {
        band.scrollIntoView()
        await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)))
        // The band's own box is full width by construction once the cap is
        // gone; what a reader sees is where its *contents* start and stop.
        const kids = [...band.children].map((c) => c.getBoundingClientRect()).filter((r) => r.width > 0)
        if (!kids.length) continue
        out.push({
          name: band.dataset.test || band.className,
          left: Math.min(...kids.map((r) => r.left)),
          right: Math.max(...kids.map((r) => r.right)),
        })
      }
      // The page width a band can actually reach, which is the viewport less a
      // classic scrollbar on a browser that draws one.
      return { bands: out, page: document.documentElement.clientWidth }
    })
    // Every band on the page: the hero, seven sections, the close. A band that
    // rendered nothing is skipped above and would otherwise go uncovered here.
    if (bands.bands.length < 9) fail(`only ${bands.bands.length} bands were measured at ${WIDE.width}px; the page did not render`)
    for (const b of bands.bands) {
      const rightGutter = bands.page - b.right
      if (b.left > 96) fail(`at ${WIDE.width}px the band ${b.name} starts ${Math.round(b.left)}px in; the page has one edge and it is the hero's`)
      if (Math.abs(b.left - rightGutter) > 2) fail(`at ${WIDE.width}px the band ${b.name} is off centre: ${Math.round(b.left)}px left, ${Math.round(rightGutter)}px right`)
    }
    // The verdict table is the one element with four columns of real content,
    // and it was the island that made the widening pointless: a 920px window
    // in a 2560px frame.
    const windowWidth = await page.evaluate(() => document.querySelector('.lp-window').getBoundingClientRect().width)
    if (windowWidth < 1200) fail(`at ${WIDE.width}px the verdict window is ${Math.round(windowWidth)}px wide; the evidence does not take the room the page gives it`)
    // Width bought with more columns is width spent well only up to a point. A
    // grid that answers 2560px with a seven-across strip of slivers is the same
    // waste, inverted — so the thing to measure is the card, not the track.
    const cards = await page.evaluate(() => {
      const widths = (sel) => [...document.querySelectorAll(sel)].map((e) => Math.round(e.getBoundingClientRect().width))
      return { fields: widths('.lp-field'), costs: widths('.lp-cost'), anatomy: widths('.lp-part'), registry: widths('.lp-card') }
    })
    for (const [name, w] of Object.entries(cards)) {
      if (!w.length) fail(`at ${WIDE.width}px the ${name} grid rendered no cards`)
      const thin = w.filter((n) => n < 340)
      if (thin.length) fail(`at ${WIDE.width}px ${thin.length} of the ${name} cards are under 340px wide (${w.join(', ')}); the grid shredded rather than filled`)
    }
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
    const page = await open({ authenticated: false }, { routes: [['/public', []]] })
    await page.waitForSelector('.lp-slab')
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
      // Each band is given what its reveal takes — the observer, then the
      // transition — and no more; one that never arrives is what is reported.
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
    // exactly the shape of an author byline — and the signer is the one person
    // it is never about. It has to say what the name is for.
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
    // A record nobody could fill in renders no line at all — not an empty one,
    // and not a bare separator.
    if (strip.cites[1] !== null) fail(`a card with no bibliography still rendered a citation: ${JSON.stringify(strip.cites[1])}`)
    if (!strip.links.every((h) => h?.startsWith('/p/'))) fail(`a strip card links nowhere useful: ${JSON.stringify(strip.links)}`)
    await page.close()
  }

  // An empty registry is not an empty band: the section is absent entirely,
  // because a heading over nothing is a claim about a community.
  {
    const page = await open({ authenticated: false }, { routes: [['/public', []]] })
    await page.waitForSelector('.landing')
    // "No band" means nothing until the registry has answered and the page has
    // drawn what it said. The request log is consulted rather than a response
    // waiter, because the answer may already have landed by the time the
    // landing is on screen — a waiter armed after that never resolves.
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
    // Hovered, too. 034 made the base `.btn:hover` repaint the fill *and* the
    // ink, so a slab button that overrides only the fill inherits the base's ink
    // — which is how the hero's sign-in became white on white, taking the latere
    // mark with it, since the mark fills with currentColor.
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

  /* ── The material has two states (040) ── */

  {
    // 033 built four blocks as a material rather than a theme and painted them
    // near-black in both appearances. On paper that is four holes down one page,
    // so 040 gave the material a light half. What is guarded here is that each
    // half is actually the half it claims: the grounds, and — the thing a colour
    // swap silently breaks — that the ink on each ground is still readable.
    const BLOCKS = {
      hero: '.lp-slab',
      boundary: '.lp-loops .lp-block',
      artifacts: '.lp-walk-panel',
      close: '.lp-close',
    }
    const probe = (page) =>
      page.evaluate((blocks) => {
        // Relative luminance, so "is this paper or is this ink" is one number
        // rather than three, and contrast is the WCAG ratio off the same value.
        const rgba = (c) => {
          const n = c.match(/\d+(\.\d+)?/g).map(Number)
          return [n[0], n[1], n[2], n.length > 3 ? n[3] : 1]
        }
        // Composite the ink over its own ground first. Half this page's quiet
        // ink is an alpha over the block, and read as opaque it would report a
        // contrast it does not have.
        const over = (fg, bg) => fg.slice(0, 3).map((v, i) => v * fg[3] + bg[i] * (1 - fg[3]))
        const lum = (c) => {
          const [r, g, b] = Array.isArray(c) ? c : rgba(c)
          const f = (v) => {
            const u = v / 255
            return u <= 0.03928 ? u / 12.92 : Math.pow((u + 0.055) / 1.055, 2.4)
          }
          return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)
        }
        const out = {}
        for (const [name, sel] of Object.entries(blocks)) {
          const el = document.querySelector(sel)
          if (!el) {
            out[name] = null
            continue
          }
          const bg = getComputedStyle(el).backgroundColor
          // The heading, the body and the quietest line each sit on this ground.
          const inks = [...el.querySelectorAll('h1, h2, h3, p, .lp-eyebrow, .lp-plate-label')]
            .slice(0, 12)
            .map((e) => getComputedStyle(e).color)
          const ratio = (a, b) => (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
          out[name] = {
            bg,
            lum: lum(bg),
            worst: inks.length ? Math.min(...inks.map((c) => ratio(lum(bg), lum(over(rgba(c), rgba(bg)))))) : null,
          }
        }
        return out
      }, BLOCKS)

    for (const [theme, want] of [
      ['light', { min: 0.72, max: 1 }],
      ['dark', { min: 0, max: 0.06 }],
    ]) {
      const page = await open({ authenticated: false }, { theme })
      await page.waitForSelector('.lp-close')
      // Every band starts at the pre-reveal opacity; the grounds are painted
      // regardless, but scrolling through is what the reader actually does.
      await scrollThrough(page)
      const got = await probe(page)
      for (const [name, v] of Object.entries(got)) {
        if (!v) {
          fail(`${theme}: the landing has no ${name} block`)
          continue
        }
        if (v.lum < want.min || v.lum > want.max) {
          fail(`in ${theme} mode the ${name} block is ${v.bg} (luminance ${v.lum.toFixed(3)}), outside ${want.min}–${want.max} — the block is painted for the other appearance`)
        }
        // 3:1 is the bar the product's quiet tier is written to; the heading
        // and body tiers sit far above it. The failure being caught is a ground
        // that flipped without its ink flipping, which lands near 1:1 and passes
        // every geometry assertion on this page.
        if (v.worst !== null && v.worst < 3) {
          fail(`in ${theme} mode the ${name} block's quietest ink is ${v.worst.toFixed(2)}:1 against ${v.bg}`)
        }
      }
      // The artifacts panel restated the boundary panel's ground as a literal
      // and won over it, which is why it stayed dark whatever the tokens said.
      if (got.artifacts && got.boundary && got.artifacts.bg !== got.boundary.bg) {
        fail(`in ${theme} mode the artifacts panel is ${got.artifacts.bg} and the boundary panel is ${got.boundary.bg}; both are the same material`)
      }
      await page.close()
    }
  }

  /* ── The footer's logotype is set in the face it names (040) ── */

  {
    // The shared footer sets "Latere AI" and all six product wordmarks in
    // Instrument Serif. Until 040 this app named that family and shipped no
    // font, so every one of them rendered in Georgia — the same logotype as
    // latere.ai, in a different face. Nothing else on the page notices: the
    // markup is right, the colour is right, and the text is there.
    const page = await open({ authenticated: false })
    await page.waitForSelector('.landing .site-footer .logo-text')
    const face = await page.evaluate(async () => {
      await document.fonts.ready
      const el = document.querySelector('.landing .site-footer .logo-text')
      const cs = getComputedStyle(el)
      // The FontFaceSet holds the faces the *document declared*, and nothing
      // else — a family that only exists on the reader's machine is not in it,
      // and neither is one that was named and never served. `fonts.check()`
      // looks like the right call here and is not: it answers "can this text be
      // painted", which is true of any family, spelled correctly or not.
      const faces = [...document.fonts].filter((f) => f.family.replace(/["']/g, '') === 'Instrument Serif')
      return {
        asked: cs.fontFamily,
        style: cs.fontStyle,
        served: faces.map((f) => f.style).sort(),
        loaded: faces.filter((f) => f.status === 'loaded').map((f) => f.style),
        // The gradient wordmarks, which are spans inside the products column
        // — not `.footer-brand`, the block that holds the Latere lockup.
        brands: [...document.querySelectorAll('.landing .footer-cols span[class$="-brand"]')].length,
      }
    })
    if (!/Instrument Serif/.test(face.asked)) fail(`the footer logotype asks for ${face.asked}`)
    if (face.style !== 'italic') fail(`the footer logotype is ${face.style}, and the Latere logotype is italic`)
    if (!face.served.includes('italic')) fail(`the footer logotype names Instrument Serif and the app declares ${JSON.stringify(face.served)}; it renders in the fallback serif`)
    // Declared is not served: a path typo gives a face that is in the set and
    // permanently 'error', and the page looks exactly as it did before 040.
    if (!face.loaded.includes('italic')) fail(`the italic face is declared and did not load (status of the declared faces: ${JSON.stringify(face.loaded)}); check /fonts/`)
    if (face.brands !== 6) fail(`the footer shows ${face.brands} product wordmarks, want six`)
    await page.close()
  }

  /* ── The page ends in the Latere footer, and it works (040) ── */

  {
    const page = await open({ authenticated: false })
    await page.waitForSelector('.landing .site-footer')
    const foot = await page.evaluate(() => {
      const f = document.querySelector('.landing .site-footer')
      const hrefs = [...f.querySelectorAll('a')].map((a) => a.getAttribute('href'))
      return {
        // The lineup, so the product is placed among the others rather than alone.
        products: hrefs.filter((h) => /^https:\/\/[a-z]+\.latere\.ai\/$/.test(h)).length,
        legal: ['/legal/privacy', '/legal/terms', '/legal/impressum'].filter((p) => hrefs.some((h) => h?.endsWith(p))).length,
        rights: /©|\(c\)/i.test(f.querySelector('.footer-bottom')?.textContent ?? ''),
        // A palette the app does not define would leave the ink unset, which
        // computes to the inherited colour rather than to nothing.
        ink: getComputedStyle(f.querySelector('.footer-col a')).color,
        muted: getComputedStyle(f.querySelector('.footer-tagline')).color,
        themes: [...f.querySelectorAll('.footer-seg-btn')].map((b) => b.textContent.trim()),
        active: [...f.querySelectorAll('.footer-seg-btn.is-active')].map((b) => b.textContent.trim()),
        langs: [...f.querySelectorAll('.footer-lang-select option')].map((o) => o.value),
        // The old one-line strip is gone, not left behind next to the new one.
        legacy: !!document.querySelector('.lp-foot'),
      }
    })
    if (foot.products < 6) fail(`the footer lists ${foot.products} products; the shared lineup is six`)
    if (foot.legal !== 3) fail(`the footer carries ${foot.legal} of the three legal links`)
    if (!foot.rights) fail('the footer has no copyright line')
    if (foot.ink === foot.muted) fail(`the footer's link ink and its muted ink are both ${foot.ink}; the palette aliases did not reach it`)
    if (foot.themes.length !== 3) fail(`the footer offers ${foot.themes.length} appearances, want light, dark, and auto`)
    // One language, because the app has one. A dropdown offering a translation
    // that does not exist is a promise the app cannot keep.
    if (foot.langs.length !== 1 || foot.langs[0] !== 'en') fail(`the footer offers ${JSON.stringify(foot.langs)}; the app ships English only`)
    if (foot.legacy) fail('the landing still carries its own one-line footer beside the shared one')
    if (foot.active.length !== 1) fail(`${foot.active.length} appearances are marked active`)

    // The control is wired to the app's own theme, not to a copy of it.
    const flipped = await page.evaluate(async () => {
      const before = document.documentElement.dataset.theme
      const btns = [...document.querySelectorAll('.landing .site-footer .footer-seg-btn')]
      // ☀ light, ☾ dark, ◐ auto — pick whichever is not the one showing now.
      btns[before === 'dark' ? 0 : 1].click()
      // The switch is waited for rather than given time; one that never comes
      // is what the first check below reports.
      for (const t0 = performance.now(); document.documentElement.dataset.theme === before && performance.now() - t0 < 1000; ) {
        await new Promise((r) => requestAnimationFrame(r))
      }
      return {
        before,
        after: document.documentElement.dataset.theme,
        stored: localStorage.getItem('theme'),
        active: [...document.querySelectorAll('.landing .site-footer .footer-seg-btn.is-active')].map((b) => b.textContent.trim()),
      }
    })
    if (flipped.after === flipped.before) fail(`the footer's appearance control did nothing; <html> stayed ${flipped.before}`)
    if (flipped.stored !== flipped.after) fail(`the footer changed the appearance to ${flipped.after} and stored ${flipped.stored}`)
    if (flipped.active.length !== 1) fail(`after the switch ${flipped.active.length} appearances are marked active`)
    await page.close()
  }

  /* ── The failure screens keep the plain card, and inherit the mark ── */

  {
    // A seam makes its segments unbreakable, and an unbreakable segment that
    // does not fit *overflows* — it does not break anyway. These two titles are
    // the narrowest headings in the product (a 420px card, less its padding, on
    // a 320px phone), so they are where a seam placed one word too far to the
    // right shows up as a card wider than the screen.
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
