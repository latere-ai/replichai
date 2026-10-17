// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for the app's selects.
//
// It guards what a reader of a select sees and does: the list opens under its
// button and is painted whole, not cut off by the card around it; the arrows
// and Enter pick an option and put it on the button; Escape closes the list and
// leaves the keyboard on the button; and scrolling the screen carries the open
// list along with its button rather than leaving it stranded. The select is
// latere-ui's GlassSelect; this checks it as the console uses it. Clipping does
// not change an element's own box, so the paint check is hit testing, which
// needs a real layout engine. Run it with `bun run test:browser`.
import { check, fail, settled, start } from './harness.mjs'

const routes = [
  ['/me', { authenticated: true, principal: 'admin', email: 'admin@example.com', tier: 'admin', credits: { wallet: 5_000_000, start_reserve: 5_000_000 } }],
  ['/me/profile', {}],
  [/^\/(projects|public|shared)$/, []],
  ['/admin/access', []],
  ['/admin/settings', { start_reserve: 5_000_000, overdraft_cap: 20_000_000, rates: { models: [] } }],
  ['/admin/solvency', { outstanding_micro: 0 }],
]

const h = await start()
try {
  const page = await h.page({ routes })
  await page.open('/console')
  await page.waitForSelector('[role="combobox"]')
  await settled(page)

  // What is on screen: the first select's button, its open list, and whether
  // each option is the topmost painted thing at its own center.
  const read = () =>
    page.evaluate(() => {
      const btn = document.querySelector('[role="combobox"]')
      const list = document.querySelector('[role="listbox"]')
      const b = btn.getBoundingClientRect()
      const out = { value: btn.textContent.trim(), focused: document.activeElement === btn, open: !!list }
      if (!list) return out
      const l = list.getBoundingClientRect()
      const options = [...list.querySelectorAll('[role="option"]')]
      out.gap = l.top - b.bottom
      // Lined up with the button, inside the menu's few pixels of padding, or
      // moved left of it to stay on screen.
      out.left = l.left - b.left
      out.inViewport = l.left >= 0 && l.top >= 0 && l.right <= innerWidth && l.bottom <= innerHeight
      out.painted = options.every((o) => {
        const r = o.getBoundingClientRect()
        const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2)
        return !!hit && (hit === o || o.contains(hit))
      })
      out.labels = options.map((o) => o.textContent.trim())
      return out
    })

  // Open: under the button, every option painted.
  await page.click('[role="combobox"]')
  await page.waitForSelector('[role="listbox"]')
  await settled(page)
  const opened = await read()
  check(opened.open, 'clicking the select did not open its list')
  check(opened.gap >= 0 && opened.gap <= 16, `the list should open just under its button, it is ${opened.gap}px from it`)
  check(opened.left <= 8, `the list should start at its button's left edge or left of it, it starts ${opened.left}px to the right`)
  check(opened.inViewport, 'the open list runs off the viewport')
  check(opened.painted, 'an option of the open list is covered or clipped')

  // Escape closes it and leaves the keyboard on the button.
  await page.keyboard.press('Escape')
  await page.waitForSelector('[role="listbox"]', { hidden: true })
  const escaped = await read()
  check(escaped.focused, 'after Escape the keyboard is not on the select')

  // The arrows and Enter pick: the next option goes onto the button.
  const before = escaped.value
  await page.keyboard.press('ArrowDown')
  await page.waitForSelector('[role="listbox"]')
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await page.waitForSelector('[role="listbox"]', { hidden: true })
  const picked = await read()
  check(picked.value && picked.value !== before, `ArrowDown and Enter did not change the choice from "${before}"`)
  check(picked.focused, 'after a pick the keyboard is not on the select')

  // A scroll of the screen moves the button, and the open list goes with it.
  await page.click('[role="combobox"]')
  await page.waitForSelector('[role="listbox"]')
  const scrolled = await page.evaluate(() => {
    const screen = document.querySelector('.screen')
    if (!screen || screen.scrollHeight <= screen.clientHeight + 40) return false
    screen.scrollTop += 30
    return true
  })
  if (scrolled) {
    await settled(page)
    const moved = await read()
    check(moved.open, 'a small scroll closed the list while its button was still in view')
    check(moved.gap >= 0 && moved.gap <= 16, `after a scroll the list is ${moved.gap}px from its button; it was left behind`)
  }
  await page.keyboard.press('Escape')

  h.ok(`a select opens painted under its button (${opened.labels?.join(', ')}), picks from the keyboard${scrolled ? ', and follows a scroll' : ''}`)
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
