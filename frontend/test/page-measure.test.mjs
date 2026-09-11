// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression for the page measure on the single-column screens.
//
// The wallet capped its column at 560px and the admin console at 720px, both
// pinned to the left edge of the panel. On a wide display that left most of the
// row empty while the content inside the cap was cramped: a statement line whose
// project is a full paper title wrapped over three lines, and the statement
// itself scrolled inside a 380px box that was already inside the page's scroll.
// The admin console stacked every explanation full-width above the control it
// explains, so the policy ran several screens deep.
//
// What this test holds:
//   - the column is centred, so the space that is left is even on both sides
//   - it is wide enough to be worth centring, not 560px of an 1800px panel
//   - a statement line keeps its three facts on one line, in three columns
//   - the statement has no scroll region of its own
//   - a policy setting reads beside its control, and drops under it when narrow
//   - nothing overflows sideways at either width
import { check, fail, settled, start } from './harness.mjs'

// The title that broke the old cap: a real paper name on a statement line.
const LONG = 'Short-Form Videos Degrade Our Capacity to Retain Intentions: Effect of Context Switching On Prospective Memory'
const at = (i) => new Date(1755_215_200_000 - i * 86_400_000).toISOString()
const entry = (i, o) => ({ id: `e${i}`, holder: 'p', actor: 'admin', created_at: at(i), ...o })
const entries = [
  entry(0, { amount: -5_000_000, kind: 'allocate', project_id: 'a', project_name: 'A hybrid indoor navigation system', project_alive: true }),
  entry(1, { amount: 100_000_000, kind: 'adjust' }),
  entry(2, { amount: -25_000_000, kind: 'allocate', project_id: 'c', project_name: LONG, project_alive: true }),
  entry(3, { amount: 25_000_000, kind: 'reclaim', project_id: 'c', project_name: LONG, project_alive: true }),
]
const rates = {
  models: [{ model: 'claude-haiku-4-5', input: 1_000_000, output: 5_000_000, cache_read: 100_000, cache_write_5m: 1_250_000, cache_write_1h: 2_000_000 }],
}
const settings = { start_reserve: 20_000_000, overdraft_cap: 200_000_000, rates, draft: { provider: 'anthropic', model: 'claude-haiku-4-5' } }

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'a@x', tier: 'admin', credits: { wallet: 55_000_000, start_reserve: 20_000_000 } }],
  ['/me/profile', {}],
  ['/me/credits', { balance: 55_000_000, entries, spread_bps: 300, spread_fixed_micro: 300_000, min_topup_micro: 5_000_000 }],
  ['/admin/access', [{ email: 'a@x', tier: 'admin', bound: true, wallet: 55_000_000 }]],
  ['/admin/settings', settings],
  ['/admin/solvency', { outstanding_micro: 51_800_000 }],
  [/^\/(projects|public|shared|trash)$/, []],
]

const h = await start()
try {
  const page = await h.page({ viewport: { width: 1600, height: 1000 }, routes })

  // open shows `path` at `width` with everything it fetches on screen, so the
  // measurement is of the loaded page, and lets the entrance animation finish:
  // a card mid-fade is mid-transform, and measures where it is rather than
  // where it ends up.
  const open = async (path, sel, width) => {
    await page.setViewport({ width, height: 1000 })
    await page.open(path, { waitUntil: 'networkidle0' })
    await page.waitForSelector(sel, { timeout: 15000 })
    await settled(page)
  }

  /* ── The wallet, wide ── */

  await open('/wallet', '.usage-line.ledger', 1600)

  const wallet = await page.evaluate(() => {
    const screen = document.querySelector('.screen')
    const box = screen.getBoundingClientRect()
    const style = getComputedStyle(screen)
    // clientLeft/clientWidth, not the border-box rect: a platform that reserves
    // a scrollbar gutter puts it inside the rect, and the right-hand gap would
    // read as that much more empty space.
    const inner = { left: box.left + screen.clientLeft, width: screen.clientWidth }
    const pad = { l: parseFloat(style.paddingLeft), r: parseFloat(style.paddingRight) }
    const col = screen.querySelector('.stack').getBoundingClientRect()
    const list = screen.querySelector('.usage-rows')
    const rows = [...screen.querySelectorAll('.usage-line.ledger')].map((r) => {
      const label = r.firstElementChild.getBoundingClientRect()
      const when = r.querySelector('.ledger-when').getBoundingClientRect()
      const amount = r.querySelector('.v').getBoundingClientRect()
      return {
        text: r.textContent.replace(/\s+/g, ' ').trim().slice(0, 30),
        // Against the date, which is always one line: a line-height that
        // computes to `normal` parses to NaN, and NaN > 1 is false, so the check
        // would pass on every row without measuring one.
        lines: Math.round(label.height / when.height),
        inOrder: when.left >= label.right - 1 && amount.left >= when.right - 1,
        rightEdge: Math.round(amount.right),
      }
    })
    return {
      left: Math.round(col.left - (inner.left + pad.l)),
      right: Math.round(inner.left + inner.width - pad.r - col.right),
      width: Math.round(col.width),
      nested: list.scrollHeight > list.clientHeight + 1,
      rows,
    }
  })

  check(Math.abs(wallet.left - wallet.right) <= 2, `the wallet column has ${wallet.left}px to its left and ${wallet.right}px to its right; it is meant to be centred`)
  check(wallet.width >= 800, `the wallet column is ${wallet.width}px wide in a 1600px window; the cap is back`)
  check(!wallet.nested, 'the statement scrolls inside the page, which already scrolls')
  check(wallet.rows.length === 4, `the statement shows ${wallet.rows.length} lines, want 4`)
  for (const r of wallet.rows) {
    check(r.lines <= 1, `"${r.text}" wraps over ${r.lines} lines at 1600px; the column is wide enough for one`)
    check(r.inOrder, `"${r.text}" does not read as what / when / how much across three columns`)
  }
  const edges = new Set(wallet.rows.map((r) => r.rightEdge))
  check(edges.size <= 1, `the amounts end at ${[...edges].join(', ')}; they are one column and must line up`)

  /* ── The admin console, wide: an explanation beside its control ── */

  // Read once, in the page: nothing may be drawn past the panel's edge, unless
  // it sits in a box that scrolls sideways on purpose. The price table is one,
  // on any screen too narrow for its five columns.
  const spills = () =>
    page.evaluate(() => {
      const box = document.querySelector('.screen').getBoundingClientRect()
      const clipped = (e) => {
        for (let p = e.parentElement; p; p = p.parentElement) {
          if (/auto|scroll/.test(getComputedStyle(p).overflowX)) return true
        }
        return false
      }
      return [...document.querySelectorAll('.screen *')].some((e) => e.getBoundingClientRect().right > box.right + 0.5 && !clipped(e))
    })

  await open('/console', '.policy-row', 1600)

  const policy = await page.evaluate(() => {
    const screen = document.querySelector('.screen')
    const box = screen.getBoundingClientRect()
    const style = getComputedStyle(screen)
    const inner = { left: box.left + screen.clientLeft, width: screen.clientWidth }
    const col = screen.querySelector('.stack').getBoundingClientRect()
    return {
      left: Math.round(col.left - (inner.left + parseFloat(style.paddingLeft))),
      right: Math.round(inner.left + inner.width - parseFloat(style.paddingRight) - col.right),
      rows: [...document.querySelectorAll('.policy-row')].map((r) => {
        const copy = r.querySelector('.policy-copy').getBoundingClientRect()
        const control = r.lastElementChild.getBoundingClientRect()
        return {
          name: r.querySelector('.admin-label')?.textContent.trim(),
          beside: control.left >= copy.right - 1,
          hint: !!r.querySelector('.policy-hint'),
        }
      }),
    }
  })

  check(Math.abs(policy.left - policy.right) <= 2, `the admin column has ${policy.left}px to its left and ${policy.right}px to its right; it is meant to be centred`)
  check(policy.rows.length >= 3, `the policy shows ${policy.rows.length} settings, want at least the reserve, the floor and the drafting model`)
  check(policy.rows.some((r) => r.hint), 'no policy setting carries its explanation; the copy was dropped rather than moved')
  for (const r of policy.rows) {
    check(r.beside, `"${r.name}" stacks its control under its explanation at 1600px, which is what made the console several screens deep`)
  }
  check(!(await spills()), 'something on the admin console is drawn past the edge of the panel')

  /* ── Narrow: the control drops under the words rather than squeezing them ── */

  await open('/console', '.policy-row', 780)

  const narrow = await page.evaluate(() => {
    const screen = document.querySelector('.screen')
    return {
      stacked: [...document.querySelectorAll('.policy-row')].every((r) => {
        const copy = r.querySelector('.policy-copy').getBoundingClientRect()
        return r.lastElementChild.getBoundingClientRect().top >= copy.bottom - 1
      }),
      scrollsSideways: screen.scrollWidth > screen.clientWidth + 1,
    }
  })

  check(narrow.stacked, 'at 780px a policy control still sits beside its explanation, squeezing both')
  check(!(await spills()), 'at 780px something on the admin console is drawn past the edge of the panel')
  check(!narrow.scrollsSideways, 'at 780px the admin console scrolls sideways')

  h.ok(
    `the wallet centres a ${wallet.width}px column with ${wallet.left}px either side, its statement lines run ${wallet.rows.map((r) => r.lines).join('/')} line each, and the console's policy reads beside its controls until 780px`,
  )
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
