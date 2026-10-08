// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The footer is latere-ui's, one shape on every Latere site: the landing
// passes its lockup and its theme, and a wrapper of the app's own places it
// and joins the palette. No sheet of the app restyles the footer itself. A
// footer that should look different is a change to latere-ui, made for every
// site.
//
// This reads the stylesheets rather than the rendered page, because a rule
// that restyles the footer is a property of how a sheet is written: a browser
// probe would have to know in advance which property was changed.
import { describe, expect, it } from 'bun:test'
import { readdirSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { join, relative } from 'node:path'

const root = fileURLToPath(new URL('..', import.meta.url))

function sheets(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    if (e.name === 'node_modules' || e.name === 'dist' || e.name.startsWith('.')) return []
    const path = join(dir, e.name)
    return e.isDirectory() ? sheets(path) : e.name.endsWith('.css') ? [path] : []
  })
}

describe('the shared footer', () => {
  it('is restyled by no sheet of the app', () => {
    const found = sheets(root)
    expect(found.length).toBeGreaterThan(0)
    const restyles = found.flatMap((file) => {
      const css = readFileSync(file, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
      return Array.from(css.matchAll(/([^{};]+)\{/g), (m) => m[1].trim())
        .filter((selector) => /\.(lu-footer|site-footer)\b/.test(selector))
        .map((selector) => `${relative(root, file)}: ${selector}`)
    })
    expect(restyles).toEqual([])
  })
})
