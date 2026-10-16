// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { renderToStaticMarkup } from 'react-dom/server'

import { MARK_GAP, Mark, markArc, Wordmark } from './brand'

// The mark's outer loop is one arc with one deliberate gap at every size it is
// drawn. An arc that met itself would claim the study with people is
// re-runnable, which the product never claims.
for (const size of [16, 24, 48, 160]) {
  test(`the mark at ${size}px draws an open outer arc and a closed inner ring`, () => {
    const svg = renderToStaticMarkup(<Mark size={size} />)
    const d = svg.match(/class="mark-outer" d="([^"]+)"/)?.[1] ?? ''
    // M x0 y0 A r r 0 large sweep x1 y1: two distinct endpoints, the long way
    // round, which leaves exactly one gap.
    const n = d.match(/-?\d+(\.\d+)?/g)?.map(Number) ?? []
    expect(n).toHaveLength(9)
    const [x0, y0, , , , large, sweep, x1, y1] = n
    expect(Math.hypot(x1 - x0, y1 - y0)).toBeGreaterThan(1)
    expect(large).toBe(1)
    expect(sweep).toBe(1)
    expect(svg).toMatch(/<circle class="mark-inner"/)
    expect(svg).not.toMatch(/stroke-dasharray/)
  })
}

// The gap is the size the mark promises, measured on the arc itself.
test('the gap spans the angle the mark names', () => {
  const n = markArc(12, 12, 10).match(/-?\d+(\.\d+)?/g)!.map(Number)
  const chord = Math.hypot(n[7] - n[0], n[8] - n[1])
  const angle = 2 * Math.asin(chord / 2 / 10) * (180 / Math.PI)
  expect(Math.abs(angle - MARK_GAP)).toBeLessThan(0.5)
})

// The wordmark is the name, set as text, with its seam.
test('the wordmark reads ReplicHAI with its seam', () => {
  const html = renderToStaticMarkup(<Wordmark />)
  expect(html.replace(/<[^>]+>/g, '')).toBe('ReplicHAI')
  expect(html).toContain('wordmark-seam')
})

// The favicon is the one place the mark sits on a ground the app does not
// choose, so its ink swaps for a dark browser tab.
test('the favicon swaps its ink for a dark tab', () => {
  const svg = readFileSync(new URL('../public/favicon.svg', import.meta.url), 'utf8')
  expect(svg).toContain('prefers-color-scheme:dark')
  expect(svg).toMatch(/\.mark-outer\{stroke:#0d0d0d\}/)
  expect(svg).toMatch(/@media \(prefers-color-scheme:dark\)\{\.mark-outer\{stroke:#ececec\}/)
})
