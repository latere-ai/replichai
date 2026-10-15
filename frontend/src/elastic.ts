// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Elastic overscroll inside the pane being scrolled (047).
//
// A trackpad pull past the end of a list should stretch that list and nothing
// around it: the console's main pane or its rail, or the landing's body under
// its sticky header. Browsers disagree on who stretches. Chromium rubber-bands
// only the page itself, so the whole frame, rail and bar included, bounced, and
// the landing's sticky header bounced with the page. Safari and Firefox stretch
// an inner scroller on their own.
//
// So the page's own bounce is turned off (styles.css), and this draws the
// stretch where the browser does not: the landing's body in every browser,
// whose root no longer bounces, and the console's panes in Chromium only,
// where nothing else would. It follows the wheel while the pull lasts, with the
// rubber band's resistance, and springs back when the wheel goes quiet. Touch
// is left to the platform, which already stretches the pane under the finger,
// and a reader who asked for less motion gets no stretch at all.

// The scrollers that stretch on their own, and the landing's body, which
// stretches for the page.
const PANES = '.screen, .rail-nav'
const PAGE = '.lp-body'

// How far a pull can stretch, and how hard it resists: the rubber band's curve,
// which gives at first and stiffens toward REACH.
export const REACH = 160
const GIVE = 0.55

// A pull ends when the wheel has been quiet this long, or when a momentum
// stream has slowed to this. The momentum that follows an ended pull is
// ignored until it stops, so it cannot start a second one.
const SETTLE = 100
const TAIL = 3
const QUIET = 150

// The spring back is the stylesheet's transition; the class comes off when it
// is over, so a pane at rest carries no translate at all.
const SPRING = 520

// rubber is the stretch shown for a pull of `x` pixels of wheel.
export function rubber(x: number) {
  return Math.sign(x) * (1 - 1 / ((Math.abs(x) * GIVE) / REACH + 1)) * REACH
}

// chromium reports a browser that stretches only the page, never a pane.
export function chromium(ua: string) {
  return /Chrome\/|Chromium\//.test(ua)
}

type Pull = { host: HTMLElement; raw: number; timer: number }

// installElastic starts the one listener the whole app shares. It returns the
// function that removes it.
export function installElastic(win: Window = window): () => void {
  const doc = win.document
  const still = win.matchMedia?.('(prefers-reduced-motion: reduce)')
  const fine = win.matchMedia?.('(pointer: fine)')
  const panesStretch = chromium(win.navigator.userAgent)
  let pull: Pull | null = null
  let quietUntil = 0

  const end = () => {
    if (!pull) return
    const { host, timer } = pull
    win.clearTimeout(timer)
    pull = null
    host.classList.add('elastic-release')
    host.style.setProperty('--elastic', '0px')
    win.setTimeout(() => {
      if (pull?.host === host) return
      host.classList.remove('elastic-on', 'elastic-release')
      host.style.removeProperty('--elastic')
    }, SPRING)
  }

  // paneOf finds what a wheel over `target` would stretch. A scroller that is
  // not one of ours, such as a code block, owns its own wheel, and its ends
  // stretch nothing.
  const paneOf = (target: EventTarget | null) => {
    let el = target instanceof Element ? target : null
    while (el && el !== doc.documentElement) {
      if (el.matches(PANES)) return panesStretch ? { scroller: el, host: el as HTMLElement } : null
      if (el.matches(PAGE)) return doc.scrollingElement ? { scroller: doc.scrollingElement, host: el as HTMLElement } : null
      const oy = win.getComputedStyle(el).overflowY
      if ((oy === 'auto' || oy === 'scroll') && el.scrollHeight > el.clientHeight) return null
      el = el.parentElement
    }
    return null
  }

  const onWheel = (e: WheelEvent) => {
    // A pinch, a line-by-line mouse wheel, and a sideways swipe are not pulls.
    if (e.ctrlKey || e.deltaMode !== 0 || Math.abs(e.deltaX) > Math.abs(e.deltaY)) return
    if (still?.matches || !(fine?.matches ?? true)) return
    const now = win.performance.now()
    if (now < quietUntil) {
      quietUntil = now + QUIET
      return
    }
    const found = paneOf(e.target)
    if (!found) return end()
    const { scroller, host } = found
    if (pull && pull.host !== host) end()
    const dy = e.deltaY
    if (!pull) {
      const atTop = scroller.scrollTop <= 0
      const atEnd = Math.ceil(scroller.scrollTop + scroller.clientHeight) >= scroller.scrollHeight
      if (!((atTop && dy < 0) || (atEnd && dy > 0))) return
      pull = { host, raw: 0, timer: 0 }
    }
    const raw = pull.raw - dy
    // A pull that turns back past where it began hands the wheel back to the
    // scroll.
    if (pull.raw !== 0 && Math.sign(raw) !== Math.sign(pull.raw)) return end()
    pull.raw = raw
    host.classList.remove('elastic-release')
    host.classList.add('elastic-on')
    host.style.setProperty('--elastic', `${rubber(raw).toFixed(1)}px`)
    win.clearTimeout(pull.timer)
    if (Math.abs(dy) <= TAIL) {
      quietUntil = now + QUIET
      return end()
    }
    pull.timer = win.setTimeout(end, SETTLE)
  }

  win.addEventListener('wheel', onWheel, { passive: true })
  return () => win.removeEventListener('wheel', onWheel)
}
