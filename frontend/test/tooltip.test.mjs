// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for the app's tooltip.
//
// The bug it guards: the bubble used to be a `::after` pseudo-element on the
// trigger, positioned absolutely. Every card in the app rounds its corners with
// `overflow: hidden`, and an absolutely-positioned child is clipped by that, so
// a tip on any control inside a card was cut off at the card's edge and showed
// up on screen as a black stub with no text in it. The paper row's remove
// button was the report; eighteen `overflow: hidden` rules were affected.
//
// Clipping does not change an element's own box, so "the bubble exists and has a
// size" passes against the broken code too. The discriminating assertion is hit
// testing, which Chrome resolves against what is painted: at the bubble's own
// center, the topmost element must be the bubble. That plus "no ancestor of the
// bubble clips it" is what a container can break. It needs a real layout engine,
// so it drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { check, fail, settled, start } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()

const project = { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }
// A paper nobody has run yet: the remove button is gated on a session count of
// zero, and it is the control the report was filed against.
const paper = { key: 'pap-1', ref: '10.1145/3562939.3567802', title: 'Walk This Beam', sessions: 0 }

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { wallet: 42.5, start_reserve: 5 } }],
  ['/me/profile', {}],
  ['/projects', [{ ...project, session_count: 0, running: 0, latest_phase: '' }]],
  [/^\/projects\/[^/]+$/, { project, sessions: [], papers: [paper], may_submit: true, role: 'owner' }],
  [/^\/projects\/[^/]+\/papers$/, [paper]],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/projects\/[^/]+\/credits$/, { wallet: 20, start_reserve: 5 }],
  [/^\/(public|shared)$/, []],
]

// How many bubbles are up, read in the page: the bubble fades in from opacity 0.
const UP = "[...document.querySelectorAll('.tip-bubble')].filter((b) => +getComputedStyle(b).opacity > 0.5).length"

const h = await start()
try {
  const page = await h.page({ routes })
  const up = () => page.evaluate(UP)
  // bubbles waits for exactly `n` to be up, and gives up quietly: the count that
  // follows is the assertion.
  const bubbles = (n) => page.waitForFunction(`${UP} === ${n}`, { timeout: 2000 }).catch(() => {})

  await page.open('/')
  // Navigation is state, not a router: the dashboard's project row opens the
  // project screen, where the paper rows live.
  await page.waitForSelector('.row')
  await page.evaluate(() => document.querySelector('.row')?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
  await page.waitForSelector('.icon-btn.danger[data-tip], .icon-btn.danger')
  await settled(page)

  // hover moves a real pointer onto the element and reports what the tip is,
  // where it sits, and what is painted at its center, which is the part that
  // catches clipping.
  const hover = async (selector, label) => {
    const el = await page.$(selector)
    if (!el) return fail(`no ${label} on screen (${selector})`)
    const box = await el.boundingBox()
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
    // The bubble slides up as it fades in, so it is measured where it lands.
    await bubbles(1)
    await settled(page)
    const m = await page.evaluate(() => {
      const bubbles = [...document.querySelectorAll('.tip-bubble')].filter((b) => +getComputedStyle(b).opacity > 0.5)
      if (bubbles.length !== 1) return { n: bubbles.length }
      const b = bubbles[0]
      const r = b.getBoundingClientRect()
      // A tip must not take the pointer in the app, but hit testing is the only
      // read of what is painted where, and it skips a pointer-events:none box.
      // The property changes nothing about layout or clipping, so it is enabled
      // for the probe and restored straight after.
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
        // The bubble is the topmost painted thing at its own center only if
        // nothing clipped it away.
        painted: !!hit && (hit === b || b.contains(hit)),
        inViewport: r.left >= 0 && r.top >= 0 && r.right <= innerWidth && r.bottom <= innerHeight,
        // Its own text must fit the box it was given.
        fits: b.scrollHeight <= b.clientHeight + 0.5 && b.scrollWidth <= b.clientWidth + 0.5,
        hidden: b.getAttribute('aria-hidden'),
      }
    })
    if (m.n !== 1) return fail(`hovering the ${label} showed ${m.n} visible tooltips, want exactly 1`)
    check(m.text, `the ${label}'s tooltip is empty`)
    check(!m.clipper, `the ${label}'s tooltip sits inside a clipping ancestor (${m.clipper}) that cuts it off`)
    check(m.painted, `the ${label}'s tooltip is not painted at its own center — something covers or clips it (rect ${JSON.stringify(m.rect)})`)
    check(m.inViewport, `the ${label}'s tooltip runs off the viewport: ${JSON.stringify(m.rect)}`)
    check(m.fits, `the ${label}'s tooltip clips its own text`)
    check(m.hidden === 'true', `the ${label}'s tooltip is in the accessibility tree; the trigger carries the label`)
    return m
  }

  // The report: a remove button inside a card, whose bubble the card clipped.
  const remove = await hover('.icon-btn.danger', 'remove-paper button')

  // Away from the trigger, the bubble goes.
  await page.mouse.move(700, 860)
  await bubbles(0)
  const lingering = await up()
  check(!lingering, `${lingering} tooltips stayed up after the pointer left`)

  // A trigger in the header, at the viewport's right edge: the bubble has to be
  // pulled back inside rather than hang off the screen. This is what the old
  // hand-placed `align="end"` was for.
  const wallet = await hover('.quota-pill', 'wallet pill')

  // The appearance switcher's radios carry `data-tip` as a plain attribute
  // rather than a wrapper, because a radiogroup owns its radios directly. That
  // path has to raise the same bubble.
  const radio = await hover('.segmented.compact button[role="radio"][data-tip]', 'appearance radio')

  // focusRemove parks the pointer, takes focus off whatever holds it, and hands
  // it to the remove button from an unfocused state, so `focusin` fires even
  // when the button had it already, then waits for the bubble that focus raises.
  const focusRemove = async () => {
    await page.mouse.move(700, 860)
    await page.evaluate(() => document.activeElement?.blur?.())
    await bubbles(0)
    await page.evaluate(() => document.querySelector('.icon-btn.danger').focus())
    await bubbles(1)
  }

  // Keyboard reach: focus alone must raise the tip, for anyone not using a mouse.
  await focusRemove()
  const focused = await page.evaluate(() => [...document.querySelectorAll('.tip-bubble')].find((x) => +getComputedStyle(x).opacity > 0.5)?.textContent.trim() ?? '')
  check(focused, 'focusing the remove button raised no tooltip')

  // Clicking a remove button takes its own row away. The bubble must go with the
  // trigger rather than hang over an empty list under a pointer that never moved.
  await focusRemove()
  await page.evaluate(() => {
    const btn = document.querySelector('.icon-btn.danger')
    const box = btn.getBoundingClientRect()
    btn.dispatchEvent(new PointerEvent('pointerdown', { bubbles: true, pointerId: 1, clientX: box.left + box.width / 2, clientY: box.top + box.height / 2 }))
    btn.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
  await bubbles(0)
  const orphan = await up()
  check(!orphan, `${orphan} tooltips outlived the trigger that was clicked away`)

  // Scrolling would leave a bubble stranded where its trigger used to be, so it
  // must go when the page moves under it.
  await focusRemove()
  await page.evaluate(() => (document.scrollingElement ?? document.documentElement).dispatchEvent(new Event('scroll', { bubbles: true })))
  await bubbles(0)
  const stranded = await up()
  check(!stranded, `${stranded} tooltips survived a scroll under them`)

  h.ok(
    `remove-paper tip "${remove?.text}" painted at ${Math.round(remove?.rect.left)},${Math.round(remove?.rect.top)} inside its card; ` +
      `wallet tip "${wallet?.text}" clamped to the viewport; appearance radio tip "${radio?.text}" without a wrapper; ` +
      `focus raises it, pointer-out and scroll dismiss it`,
  )
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
