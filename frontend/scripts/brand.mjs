// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Generates the brand assets a component cannot serve: the favicon, the Apple
// touch icon, because iOS ignores SVG, and the social card, because crawlers do
// not rasterize SVG. All three are rendered from `src/brand.tsx` itself, the
// component the app draws, so the tab, the home screen, the card and the app
// can never show four different marks.
//
// The output is committed, so a build needs no browser. This runs by hand when
// the mark changes: `bun scripts/brand.mjs` from `frontend/`. Bun, not node,
// because it imports the TSX the app draws the mark with.
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { createElement as h } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

import { Lockup, Mark } from '../src/brand.tsx'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const CHROME =
  process.env.PUPPETEER_EXECUTABLE_PATH ||
  ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/usr/bin/google-chrome', '/usr/bin/chromium'].find((p) => existsSync(p))
if (!CHROME || !existsSync(CHROME)) {
  console.error('no Chrome found; set PUPPETEER_EXECUTABLE_PATH')
  process.exit(1)
}

// The two inks the mark is drawn in, light and dark: ink's text tone for the
// outer ring and latere-ui's registered replichai azure for the inner one.
const INK = { light: { outer: '#0d0d0d', inner: '#2369bd' }, dark: { outer: '#ececec', inner: '#78b1ed' } }

// The favicon is the mark at its smallest optical size with its colors in a
// style element, so the ink ring turns light on a dark browser tab, the way the
// platform's favicon does. A browser tab is the one place the mark sits on a
// ground the app does not choose.
const favicon = renderToStaticMarkup(h(Mark, { size: 16 }))
  .replace('<svg class="mark"', '<svg xmlns="http://www.w3.org/2000/svg"')
  .replace(' aria-hidden="true" focusable="false"', '')
  .replace(
    '>',
    `><style>.mark-outer{stroke:${INK.light.outer}}.mark-inner{stroke:${INK.light.inner}}@media (prefers-color-scheme:dark){.mark-outer{stroke:${INK.dark.outer}}.mark-inner{stroke:${INK.dark.inner}}}</style>`,
  )

// A page-local copy of the two faces the card sets, so the render does not
// depend on what the machine running this has installed.
const face = (family, file, style = 'normal', weight = 400) =>
  `@font-face{font-family:'${family}';font-style:${style};font-weight:${weight};src:url(data:font/woff2;base64,${readFileSync(`public/fonts/${file}`).toString('base64')}) format('woff2')}`
const FACES = [face('Inter', 'inter-400.woff2'), face('Inter', 'inter-500.woff2', 'normal', 500), face('Inter', 'inter-600.woff2', 'normal', 600)].join('')
const PAINT = (t) => `.mark-outer{stroke:${INK[t].outer}}.mark-inner{stroke:${INK[t].inner}}.wordmark{font-weight:600;letter-spacing:-.03em}.wordmark-seam{margin-left:.055em}.lockup{display:inline-flex;align-items:center;gap:.4em;line-height:1}`

// The touch icon is the mark on an ink tile, drawn square and full-bleed: iOS
// applies its own corner mask, and a tile that rounds itself first comes out a
// circle on the home screen while every icon beside it is a squircle.
const icon = `<style>${PAINT('dark')}body{margin:0}</style>
  <div style="width:180px;height:180px;background:#161616;display:grid;place-items:center">${renderToStaticMarkup(h(Mark, { size: 120 }))}</div>`

// The social card is the landing's lockup and claim at card scale, set as the
// landing sets them. A link preview is the first screen for anybody who arrives
// through somebody else, so it says what the page says.
const card = `<style>${FACES}${PAINT('light')}body{margin:0;font-family:Inter}</style>
  <div style="width:1200px;height:630px;background:#ffffff;color:#0d0d0d;font-family:Inter;display:flex;flex-direction:column;justify-content:center;padding:0 96px;box-sizing:border-box">
    <div style="margin-bottom:48px">${renderToStaticMarkup(h(Lockup, { size: 44 }))}</div>
    <div style="font:600 84px/1.04 Inter;letter-spacing:-0.035em">Does the paper reproduce?</div>
    <div style="margin-top:36px;font-size:27px;line-height:1.5;color:#525252;max-width:44ch">
      ReplicHAI re-runs the part of a paper a machine can run. The study with people is named, marked out of scope, and never simulated.
    </div>
  </div>`

mkdirSync('public', { recursive: true })
writeFileSync('public/favicon.svg', favicon + '\n')
console.log('wrote public/favicon.svg')

const browser = await puppeteer.launch({ executablePath: CHROME, headless: true })
try {
  for (const [name, html, w, ht] of [
    ['apple-touch-icon.png', icon, 180, 180],
    ['og.png', card, 1200, 630],
  ]) {
    const page = await browser.newPage()
    await page.setViewport({ width: w, height: ht, deviceScaleFactor: 1 })
    await page.setContent(html, { waitUntil: 'load' })
    await page.evaluate(() => document.fonts.ready)
    const buf = await page.screenshot({ type: 'png', clip: { x: 0, y: 0, width: w, height: ht } })
    writeFileSync(`public/${name}`, buf)
    console.log(`wrote public/${name} (${w}×${ht})`)
    await page.close()
  }
} finally {
  await browser.close()
}
