// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for folding the navigation rail.
//
// What it guards: the rail is a fixed 236px column of the shell's flex row, and
// folding it is not one property. The width has to change; the labels have to
// stop taking space rather than merely turn invisible, because a `visibility:
// hidden` label still reserves its line box and pushes the icons off center in
// a 64px column; every destination has to stay clickable at its icon; the label
// has to survive as a tooltip; the main area has to claim the space the rail
// gave up; and the choice has to outlive a reload.
//
// None of that is reachable from a unit test: the assertions here are computed
// geometry and painted hit tests, which need a real layout engine. It drives
// Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { fail, settled, start } from './harness.mjs'

// The open rail's width and the folded one, as the stylesheet sets them.
const OPEN = 236
// The fold is sized to the desk's padding and the nav row's own 12px radius,
// not to the icon alone.
const FOLDED = 64

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()

const project = { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }

// An admin, so the rail carries both groups and the fold has to handle more
// than one; the Platform group's eyebrow is the label most likely to be left
// holding width open.
const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { wallet: 42.5, start_reserve: 5 } }],
  ['/me/profile', {}],
  ['/projects', [{ ...project, session_count: 0, running: 0, latest_phase: '' }]],
  [/^\/projects\/[^/]+$/, { project, sessions: [], papers: [], may_submit: true, role: 'owner' }],
  [/^\/projects\/[^/]+\/papers$/, []],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/projects\/[^/]+\/credits$/, { wallet: 20, start_reserve: 5 }],
  [/^\/(public|shared)$/, []],
  [/^\/admin\//, []],
]

const near = (got, want, slack = 1.5) => Math.abs(got - want) <= slack

const h = await start()
try {
  const page = await h.page({ routes })
  await page.open('/')
  await page.waitForSelector('.rail .rail-fold-btn')
  await settled(page)

  // read reports the whole geometry of the rail at once: its width, the width
  // the main area got, whether each nav row still paints its own icon, and
  // whether any label is still holding the column open.
  const read = () =>
    page.evaluate(() => {
      const rail = document.querySelector('.rail')
      const main = document.querySelector('.main')
      const rows = [...document.querySelectorAll('.rail .nav-btn')]
      const r = rail.getBoundingClientRect()
      return {
        folded: rail.classList.contains('rail-fold'),
        width: r.width,
        mainLeft: main.getBoundingClientRect().left,
        expanded: document.querySelector('.rail-fold-btn').getAttribute('aria-expanded'),
        // A row is reachable only if its own icon is the topmost painted element
        // at the icon's center. A row squeezed to zero, or covered by a label
        // that overflowed, fails here while still having a bounding box.
        rows: rows.map((b) => {
          const ico = b.querySelector('.nav-ico')
          const ir = ico.getBoundingClientRect()
          const hit = document.elementFromPoint(ir.left + ir.width / 2, ir.top + ir.height / 2)
          return {
            tip: b.getAttribute('data-tip') || '',
            label: b.querySelector('.nav-label')?.textContent.trim() || '',
            labelShown: !!b.querySelector('.nav-label')?.getClientRects().length,
            clickable: !!hit && (hit === b || b.contains(hit)),
            // The row must stay inside the rail it lives in. A label that only
            // went transparent keeps pushing the row wider than its column.
            overflows: b.getBoundingClientRect().right > rail.getBoundingClientRect().right + 0.5,
          }
        }),
        // The brand block and the group eyebrows are the other things that hold
        // width; none of them may still be laid out when folded.
        chrome: ['.rail-name', '.rail-beta', '.rail-logo', '.rail-group .eyebrow', '.user-meta'].map((sel) => ({
          sel,
          shown: [...document.querySelectorAll(sel)].some((e) => e.getClientRects().length > 0),
        })),
      }
    })

  const open = await read()
  if (open.folded) fail('the rail started folded; a first visit must name its destinations')
  if (!near(open.width, OPEN)) fail(`the open rail is ${open.width}px, want ${OPEN}px`)
  if (open.expanded !== 'true') fail(`the open rail's toggle reports aria-expanded=${open.expanded}, want "true"`)
  if (!open.rows.length) fail('the rail has no navigation rows')
  for (const row of open.rows) {
    if (!row.labelShown) fail(`the open rail hides the "${row.label}" label`)
    if (row.tip) fail(`the open rail puts "${row.label}" in a tooltip as well as on screen`)
    if (!row.clickable) fail(`the "${row.label}" row is not clickable in the open rail`)
  }

  // Fold it. The click is a real one on the real control, because the control
  // being reachable is half of what is tested.
  await page.click('.rail-fold-btn')
  await settled(page) // the width transition

  const shut = await read()
  if (!shut.folded) fail('clicking the toggle did not fold the rail')
  if (!near(shut.width, FOLDED)) fail(`the folded rail is ${shut.width}px, want ${FOLDED}px`)
  if (shut.expanded !== 'false') fail(`the folded rail's toggle reports aria-expanded=${shut.expanded}, want "false"`)
  // The point of folding: the space has to go to the content, not be left blank.
  // No +1: the rail has no right border, and the content is a sheet inset on
  // the desk, so the main area starts at the rail's own edge.
  if (!near(shut.mainLeft, FOLDED, 2)) fail(`the main area starts at ${shut.mainLeft}px after folding, want the rail's ${FOLDED}px edge`)
  if (shut.rows.length !== open.rows.length) fail(`folding dropped ${open.rows.length - shut.rows.length} navigation rows; every destination must stay reachable`)
  for (const row of shut.rows) {
    if (row.labelShown) fail(`the "${row.label}" label is still laid out in the folded rail`)
    if (!row.tip) fail(`the folded "${row.label}" row lost its label without gaining a tooltip`)
    if (row.tip !== row.label) fail(`the folded row's tooltip says "${row.tip}" but its label says "${row.label}"`)
    if (!row.clickable) fail(`the "${row.label}" row is not clickable in the folded rail`)
    if (row.overflows) fail(`the "${row.label}" row runs past the folded rail's edge`)
  }
  for (const c of shut.chrome) {
    if (c.shown) fail(`${c.sel} is still laid out in the folded rail and holds the column open`)
  }

  // The label has to reach the reader rather than only sit in an attribute, and
  // it has to do so without hiding the destination underneath it. The rail
  // stacks its rows vertically, so a bubble placed below its trigger lands on
  // the next row's icon: the tip that names one destination would cover the
  // next one.
  await page.hover('.rail .nav-btn')
  // The bubble fades up into place, so it is waited for and then let finish:
  // the position measured is where it came to rest, not a frame of its rise.
  await page.waitForFunction(() => [...document.querySelectorAll('.tip-bubble')].some((x) => +getComputedStyle(x).opacity > 0.5), { timeout: 3000 }).catch(() => {})
  await settled(page)
  const tip = await page.evaluate(() => {
    const b = [...document.querySelectorAll('.tip-bubble')].find((x) => +getComputedStyle(x).opacity > 0.5)
    if (!b) return { text: '' }
    const r = b.getBoundingClientRect()
    const rows = [...document.querySelectorAll('.rail .nav-btn')]
    // No row may be covered: neither the neighbors the bubble would land on if
    // it were placed below, nor the row being pointed at.
    const covered = rows
      .map((row) => ({ row: row.getAttribute('data-tip'), r: row.getBoundingClientRect() }))
      .filter(({ r: q }) => r.left < q.right && r.right > q.left && r.top < q.bottom && r.bottom > q.top)
      .map(({ row }) => row)
    return {
      text: b.textContent.trim(),
      covered,
      // Beside its trigger, and level with it: the placement that has somewhere
      // to go in a vertical stack.
      beside: r.left >= rows[0].getBoundingClientRect().right - 0.5,
      offCentre: Math.abs((r.top + r.bottom) / 2 - (rows[0].getBoundingClientRect().top + rows[0].getBoundingClientRect().bottom) / 2),
      inViewport: r.left >= 0 && r.right <= innerWidth && r.top >= 0 && r.bottom <= innerHeight,
    }
  })
  if (!tip.text) fail('hovering a folded nav row raised no tooltip, so the destination is unnamed')
  if (tip.covered?.length) fail(`the "${tip.text}" tooltip covers ${tip.covered.map((c) => `"${c}"`).join(', ')} — naming a destination must not hide one`)
  if (tip.beside === false) fail(`the "${tip.text}" tooltip is not beside its row; in a vertical rail that puts it on another destination`)
  if (tip.offCentre > 2) fail(`the "${tip.text}" tooltip sits ${Math.round(tip.offCentre)}px off its row's middle`)
  if (tip.inViewport === false) fail(`the "${tip.text}" tooltip runs off the viewport`)

  // A folded rail still navigates. A rail collapsed by width alone, with its
  // icons squeezed to nothing, would fail this assertion.
  await page.evaluate(() => document.querySelectorAll('.rail .nav-btn')[1].click())
  await page.waitForFunction(() => document.querySelectorAll('.rail .nav-btn')[1]?.classList.contains('active'), { timeout: 3000 }).catch(() => {})
  const moved = await page.evaluate(() => ({
    active: document.querySelector('.rail .nav-btn.active .nav-label')?.textContent.trim() || '',
    stillFolded: document.querySelector('.rail').classList.contains('rail-fold'),
  }))
  if (!moved.active) fail('clicking a folded nav row marked no destination current')
  if (!moved.stillFolded) fail('navigating unfolded the rail; the fold is the reader’s choice, not the screen’s')

  // The choice outlives a reload, which is what makes it a preference rather
  // than a one-off action.
  await page.reload({ waitUntil: 'domcontentloaded' })
  await page.waitForSelector('.rail .rail-fold-btn')
  await settled(page)
  const back = await read()
  if (!back.folded) fail('the rail forgot it was folded across a reload')
  if (!near(back.width, FOLDED)) fail(`the reloaded rail is ${back.width}px, want ${FOLDED}px`)

  // And unfolding restores the rail whole, from the persisted state rather than
  // from whatever the first render happened to leave behind.
  await page.click('.rail-fold-btn')
  await settled(page)
  const again = await read()
  if (again.folded) fail('clicking the toggle in a folded rail did not unfold it')
  if (!near(again.width, OPEN)) fail(`the unfolded rail is ${again.width}px, want ${OPEN}px`)
  for (const row of again.rows) {
    if (!row.labelShown) fail(`the "${row.label}" label did not come back when the rail unfolded`)
  }

  h.ok(
    `rail folds ${Math.round(open.width)}→${Math.round(shut.width)}px and back; ` +
      `${shut.rows.length} destinations stay clickable with tooltips (${shut.rows.map((r) => `"${r.tip}"`).join(', ')}); ` +
      `main claims the space; the fold survives a reload`,
  )
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
