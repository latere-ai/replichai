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
import { fail, settled, sleep, start } from './harness.mjs'

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

// pull sends a trackpad's pixel wheel events over the middle of `pane` and
// reports, mid-pull, the stretch and where `still` sits, then the same once the
// wheel has gone quiet.
const pull = async (page, pane, still, dy) => {
  const at = await page.evaluate((s) => {
    const r = document.querySelector(s).getBoundingClientRect()
    return { x: r.x + r.width / 2, y: r.y + Math.min(r.height / 2, 300) }
  }, pane)
  const top = () => page.evaluate((s) => document.querySelector(s).getBoundingClientRect().top, still)
  const before = await top()
  await page.mouse.move(at.x, at.y)
  for (let i = 0; i < 8; i++) {
    await page.mouse.wheel({ deltaY: dy })
    await sleep(16)
  }
  const shift = (s) =>
    page.evaluate((s) => {
      const host = document.querySelector(s)
      const moved = host.matches('.lp-body') ? host : host.firstElementChild
      return { elastic: parseFloat(host.style.getPropertyValue('--elastic') || '0'), translate: getComputedStyle(moved).translate }
    }, s)
  const during = await shift(pane)
  const stillMoved = (await top()) !== before
  await sleep(900)
  const after = await shift(pane)
  return { during, stillMoved, after }
}

try {
  /* ── The console's frame never bounces; its panes do ── */

  {
    const page = await h.page({ routes: [['/me', SIGNED_IN], ...routes] })
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
