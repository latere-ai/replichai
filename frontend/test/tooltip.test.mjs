// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for the app's tooltip.
//
// The bug it guards: the bubble used to be a `::after` pseudo-element on the
// trigger, positioned absolutely. Every card in the app rounds its corners with
// `overflow: hidden`, and an absolutely-positioned child is clipped by that — so
// a tip on any control inside a card was sliced off at the card's edge and
// showed up on screen as a black stub with no text in it. The paper row's remove
// button was the report; eighteen `overflow: hidden` rules were the reach.
//
// Clipping does not change an element's own box, so "the bubble exists and has a
// size" passes against the broken code too. The discriminating assertion is hit
// testing, which Chrome resolves against what is painted: at the bubble's own
// centre, the topmost element must be the bubble. That plus "no ancestor of the
// bubble clips it" is what a container can break. It needs a real layout engine,
// so it drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
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

const project = { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }
// A paper nobody has run yet: the remove button is gated on a session count of
// zero, and it is the control the report was filed against.
const paper = { key: 'pap-1', ref: '10.1145/3562939.3567802', title: 'Walk This Beam', sessions: 0 }

const routes = [
  [/^\/me$/, { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { wallet: 42.5, start_reserve: 5 } }],
  [/^\/me\/profile$/, {}],
  [/^\/projects$/, [{ ...project, session_count: 0, running: 0, latest_phase: '' }]],
  [/^\/projects\/[^/]+$/, { project, sessions: [], papers: [paper], may_submit: true, role: 'owner' }],
  [/^\/projects\/[^/]+\/papers$/, [paper]],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/projects\/[^/]+\/credits$/, { wallet: 20, start_reserve: 5 }],
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

  await page.goto(BASE, { waitUntil: 'networkidle0' })
  // Navigation is state, not a router: the dashboard's project row opens the
  // project screen, where the paper rows live.
  await page.waitForSelector('.row')
  await page.evaluate(() => document.querySelector('.row')?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
  await page.waitForSelector('.icon-btn.danger[data-tip], .icon-btn.danger')
  await new Promise((r) => setTimeout(r, 400)) // let the entrance animation settle

  // hover moves a real pointer onto the element and reports what the tip is,
  // where it sits, and — the part that catches clipping — what is actually
  // painted at its centre.
  const hover = async (selector, label) => {
    const el = await page.$(selector)
    if (!el) return fail(`no ${label} on screen (${selector})`)
    const box = await el.boundingBox()
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
    await new Promise((r) => setTimeout(r, 350)) // the bubble fades in
    const m = await page.evaluate(() => {
      const bubbles = [...document.querySelectorAll('.tip-bubble')].filter((b) => +getComputedStyle(b).opacity > 0.5)
      if (bubbles.length !== 1) return { n: bubbles.length }
      const b = bubbles[0]
      const r = b.getBoundingClientRect()
      // A tip must not take the pointer in the app — but hit testing is the only
      // read of what is painted where, and it skips a pointer-events:none box.
      // The property changes nothing about layout or clipping, so borrow it for
      // the probe and give it straight back.
      const prior = b.style.pointerEvents
      b.style.pointerEvents = 'auto'
      const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2)
      b.style.pointerEvents = prior
      // The other half of the same question, asked structurally: an ancestor with
      // a clip that does not contain the bubble is exactly the old defect.
      let clipper = ''
      for (let p = b.parentElement; p && !clipper; p = p.parentElement) {
        const cs = getComputedStyle(p)
        if (cs.overflowX === 'visible' && cs.overflowY === 'visible') continue
        const pr = p.getBoundingClientRect()
        if (r.left < pr.left - 0.5 || r.right > pr.right + 0.5 || r.top < pr.top - 0.5 || r.bottom > pr.bottom + 0.5) {
          clipper = p.className || p.tagName
        }
      }
      return {
        n: 1,
        text: b.textContent.trim(),
        rect: { left: r.left, top: r.top, right: r.right, bottom: r.bottom, w: r.width, h: r.height },
        clipper,
        // The bubble is the topmost painted thing at its own centre only if
        // nothing clipped it away.
        painted: !!hit && (hit === b || b.contains(hit)),
        inViewport: r.left >= 0 && r.top >= 0 && r.right <= innerWidth && r.bottom <= innerHeight,
        // Its own text must fit the box it was given.
        fits: b.scrollHeight <= b.clientHeight + 0.5 && b.scrollWidth <= b.clientWidth + 0.5,
        hidden: b.getAttribute('aria-hidden'),
      }
    })
    if (m.n !== 1) return fail(`hovering the ${label} showed ${m.n} visible tooltips, want exactly 1`)
    if (!m.text) fail(`the ${label}'s tooltip is empty`)
    if (m.clipper) fail(`the ${label}'s tooltip sits inside a clipping ancestor (${m.clipper}) that cuts it off`)
    if (!m.painted) fail(`the ${label}'s tooltip is not painted at its own centre — something covers or clips it (rect ${JSON.stringify(m.rect)})`)
    if (!m.inViewport) fail(`the ${label}'s tooltip runs off the viewport: ${JSON.stringify(m.rect)}`)
    if (!m.fits) fail(`the ${label}'s tooltip clips its own text`)
    if (m.hidden !== 'true') fail(`the ${label}'s tooltip is in the accessibility tree; the trigger carries the label`)
    return m
  }

  // The report: a remove button inside a card, whose bubble the card clipped.
  const remove = await hover('.icon-btn.danger', 'remove-paper button')

  // Away from the trigger, the bubble goes.
  await page.mouse.move(700, 860)
  await new Promise((r) => setTimeout(r, 350))
  const lingering = await page.evaluate(() => [...document.querySelectorAll('.tip-bubble')].filter((b) => +getComputedStyle(b).opacity > 0.5).length)
  if (lingering) fail(`${lingering} tooltips stayed up after the pointer left`)

  // A trigger in the header, at the viewport's right edge: the bubble has to be
  // pulled back inside rather than hang off the screen. This is what the old
  // hand-placed `align="end"` was for.
  const wallet = await hover('.quota-pill', 'wallet pill')

  // The appearance switcher's radios carry `data-tip` as a plain attribute
  // rather than a wrapper, because a radiogroup owns its radios directly. That
  // path has to raise the same bubble.
  const radio = await hover('.segmented.compact button[role="radio"][data-tip]', 'appearance radio')

  // Keyboard reach: focus alone must raise the tip, for anyone not using a mouse.
  await page.mouse.move(700, 860)
  await new Promise((r) => setTimeout(r, 250))
  const focused = await page.evaluate(async () => {
    const btn = document.querySelector('.icon-btn.danger')
    btn.focus()
    await new Promise((r) => setTimeout(r, 350))
    const b = [...document.querySelectorAll('.tip-bubble')].find((x) => +getComputedStyle(x).opacity > 0.5)
    return b ? b.textContent.trim() : ''
  })
  if (!focused) fail('focusing the remove button raised no tooltip')

  // Clicking a remove button takes its own row away. The bubble must go with the
  // trigger rather than hang over an empty list under a pointer that never moved.
  const orphan = await page.evaluate(async () => {
    const btn = document.querySelector('.icon-btn.danger')
    const box = btn.getBoundingClientRect()
    const send = (type) => btn.dispatchEvent(new PointerEvent(type, { bubbles: true, pointerId: 1, clientX: box.left + box.width / 2, clientY: box.top + box.height / 2 }))
    btn.focus()
    await new Promise((r) => setTimeout(r, 300))
    send('pointerdown')
    btn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    await new Promise((r) => setTimeout(r, 400))
    return [...document.querySelectorAll('.tip-bubble')].filter((b) => +getComputedStyle(b).opacity > 0.5).length
  })
  if (orphan) fail(`${orphan} tooltips outlived the trigger that was clicked away`)

  // Scrolling would leave a bubble stranded where its trigger used to be, so it
  // must go when the page moves under it.
  const stranded = await page.evaluate(async () => {
    document.querySelector('.icon-btn.danger').focus()
    await new Promise((r) => setTimeout(r, 300))
    ;(document.scrollingElement ?? document.documentElement).dispatchEvent(new Event('scroll', { bubbles: true }))
    await new Promise((r) => setTimeout(r, 200))
    return [...document.querySelectorAll('.tip-bubble')].filter((b) => +getComputedStyle(b).opacity > 0.5).length
  })
  if (stranded) fail(`${stranded} tooltips survived a scroll under them`)

  if (!process.exitCode) {
    console.log(
      `ok: remove-paper tip "${remove.text}" painted at ${Math.round(remove.rect.left)},${Math.round(remove.rect.top)} inside its card; ` +
        `wallet tip "${wallet.text}" clamped to the viewport; appearance radio tip "${radio.text}" without a wrapper; ` +
        `focus raises it, pointer-out and scroll dismiss it`,
    )
  }
} catch (e) {
  fail(e.message)
} finally {
  await browser?.close()
  vite.kill()
}
