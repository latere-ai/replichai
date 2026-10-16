// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for where elastic scrolling happens (047).
//
// What it guards: a trackpad pull past the end of a pane stretches that pane
// and nothing around it. Chromium rubber-bands only the page itself, so before
// 047 a pull at the top of the console moved the whole frame, rail and bar
// included, and a pull on the landing moved its sticky header with the page.
// The page's own bounce is now off, and elastic.ts draws the stretch inside the
// pane: the main pane, the rail, or the landing's body under its header.
//
// Two things here are reachable only in a real browser: the computed
// overscroll behavior of the root and the panes, and what a wheel event does to
// boxes on screen. It drives Chrome over a Vite dev server with the API mocked.
// Run it with `bun run test:browser`.
import { SLOW, fail, settled, sleep, start } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const project = { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }

const routes = [
  ['/me/profile', {}],
  ['/projects', [{ ...project, session_count: 0, running: 0, latest_phase: '' }]],
  ['/public', []],
]
const SIGNED_IN = { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }
const SIGNED_OUT = { authenticated: false }

const h = await start()

// asTrackpad makes `page` a machine with a fine pointer, as a laptop with a
// trackpad is. elastic.ts stretches only under a fine pointer, and headless
// Chrome on a server with no input device reports none, so without this the
// suite would test a machine no pull ever comes from. Chrome cannot emulate the
// pointer media feature, so the query is answered in the page before the app
// reads it.
const asTrackpad = (page) =>
  page.evaluateOnNewDocument(() => {
    const real = window.matchMedia.bind(window)
    window.matchMedia = (query) => {
      const list = real(query)
      if (!/\(\s*pointer\s*:\s*fine\s*\)/.test(query)) return list
      return new Proxy(list, { get: (target, key) => (key === 'matches' ? true : Reflect.get(target, key).bind?.(target) ?? Reflect.get(target, key)) })
    }
  })

// pull sends a trackpad's pixel wheel events over the middle of `pane` and
// reports the largest stretch the pane showed and whether `still` moved while
// the pull lasted, then the pane at rest once the wheel has gone quiet. Both are
// recorded inside the page as they happen: a pull ends SETTLE ms after the last
// wheel, so a read sent from here afterwards can arrive after the stretch has
// already gone back to 0, which it does on a slow runner.
const pull = async (page, pane, still, dy) => {
  const at = await page.evaluate((s) => {
    const r = document.querySelector(s).getBoundingClientRect()
    return { x: r.x + r.width / 2, y: r.y + Math.min(r.height / 2, 300) }
  }, pane)
  await page.evaluate(
    (p, s) => {
      const host = document.querySelector(p)
      const anchor = document.querySelector(s)
      const top = anchor.getBoundingClientRect().top
      const seen = { elastic: 0, stillMoved: false }
      const look = () => {
        seen.elastic = Math.max(seen.elastic, parseFloat(host.style.getPropertyValue('--elastic') || '0'))
        if (anchor.getBoundingClientRect().top !== top) seen.stillMoved = true
      }
      const observer = new MutationObserver(look)
      observer.observe(host, { attributes: true, attributeFilter: ['style'] })
      let frame = requestAnimationFrame(function tick() {
        look()
        frame = requestAnimationFrame(tick)
      })
      window.__pull = { seen, stop: () => (observer.disconnect(), cancelAnimationFrame(frame)) }
    },
    pane,
    still,
  )
  await page.mouse.move(at.x, at.y)
  for (let i = 0; i < 8; i++) {
    await page.mouse.wheel({ deltaY: dy })
    await sleep(16)
  }
  await sleep(900 * SLOW)
  const seen = await page.evaluate(() => {
    window.__pull.stop()
    return window.__pull.seen
  })
  const after = await page.evaluate((s) => {
    const host = document.querySelector(s)
    const moved = host.matches('.lp-body') ? host : host.firstElementChild
    return { translate: getComputedStyle(moved).translate }
  }, pane)
  return { during: { elastic: seen.elastic }, stillMoved: seen.stillMoved, after }
}

try {
  /* ── The console's frame never bounces; its panes do ── */

  {
    const page = await h.page({ routes: [['/me', SIGNED_IN], ...routes] })
    await asTrackpad(page)
    await page.open('/')
    await page.waitForSelector('.screen')
    await settled(page)
    const css = await page.evaluate(() => {
      const ob = (s) => getComputedStyle(document.querySelector(s)).overscrollBehaviorY
      return { html: ob('html'), body: ob('body'), screen: ob('.screen'), rail: ob('.rail-nav') }
    })
    if (css.html !== 'none' || css.body !== 'none') fail(`with the console mounted the page's overscroll is ${css.html}/${css.body}; the frame must not bounce`)
    if (css.screen !== 'contain' || css.rail !== 'contain') fail(`the panes' overscroll is ${css.screen}/${css.rail}; each keeps its bounce to itself`)

    for (const [pane, still] of [
      ['.screen', '.topbar'],
      ['.rail-nav', '.rail-head'],
    ]) {
      const got = await pull(page, pane, still, -40)
      if (!(got.during.elastic > 0)) fail(`a pull at the top of ${pane} stretches nothing (${got.during.elastic}px)`)
      if (got.during.elastic > 160) fail(`a pull on ${pane} stretched ${got.during.elastic}px, past its reach`)
      if (got.stillMoved) fail(`a pull on ${pane} moved ${still}; the frame around a pane stays where it is`)
      if (got.after.translate !== 'none') fail(`${pane} did not spring back: it rests at ${got.after.translate}`)
    }
    await page.close()
  }

  /* ── The landing stretches under its header, and the header stays ── */

  {
    const page = await h.page({ routes: [['/me', SIGNED_OUT], ...routes] })
    await asTrackpad(page)
    await page.open('/')
    await page.waitForSelector('.lp-body')
    await settled(page)
    const got = await pull(page, '.lp-body', '.lp-bar', -40)
    if (!(got.during.elastic > 0)) fail(`a pull at the top of the landing stretches nothing (${got.during.elastic}px)`)
    if (got.stillMoved) fail("a pull on the landing moved its header; the page stretches under it")
    if (got.after.translate !== 'none') fail(`the landing did not spring back: it rests at ${got.after.translate}`)
    await page.close()
  }

  /* ── A reader who asked for less motion gets no stretch ── */

  {
    const page = await h.page({ routes: [['/me', SIGNED_IN], ...routes] })
    await asTrackpad(page)
    await page.emulateMediaFeatures([{ name: 'prefers-reduced-motion', value: 'reduce' }])
    await page.open('/')
    await page.waitForSelector('.screen')
    await settled(page)
    const got = await pull(page, '.screen', '.topbar', -40)
    if (got.during.elastic !== 0) fail(`under reduced motion a pull still stretches the pane by ${got.during.elastic}px`)
    await page.close()
  }

  h.ok('a pull stretches the pane it is over, and never the frame or the header')
} finally {
  await h.close()
}
