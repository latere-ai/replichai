// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Generates the raster brand assets that a vector cannot cover: the Apple touch
// icon, because iOS ignores SVG, and the social card, because crawlers do not
// rasterise SVG.
// Both come out of the same geometry the in-app mark uses, so the three can
// never drift into three different marks.
//
// The output is committed, so a build needs no browser. This runs by hand when
// the mark changes: `node scripts/brand.mjs` from `frontend/`.
import { createRequire } from 'node:module'
import { mkdirSync, writeFileSync, existsSync } from 'node:fs'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const CHROME =
  process.env.PUPPETEER_EXECUTABLE_PATH ||
  ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/usr/bin/google-chrome', '/usr/bin/chromium'].find((p) => existsSync(p))
if (!CHROME || !existsSync(CHROME)) {
  console.error('no Chrome found; set PUPPETEER_EXECUTABLE_PATH')
  process.exit(1)
}

// One mark, two sizes, six dashes, the same six `brand.tsx` draws, so the tab
// icon, the social card, and the app all show one mark rather than three.
const mark = (px, { tile = false, segments = 6, radius = 0 } = {}) => {
  const s = px / 32
  const c = 2 * Math.PI * 10.4 * s
  const seg = c / segments
  const dash = seg * 0.62
  // Two-tone off a tile: white rings on an azure tile for the home-screen icon;
  // vlag's two poles, crimson outer and azure inner, on transparency everywhere
  // else, matching the in-app mark.
  const outerInk = tile ? '#ffffff' : '#a9373b'
  const innerInk = tile ? '#ffffff' : '#2369bd'
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${px} ${px}" width="${px}" height="${px}">
    ${tile ? `<rect width="${px}" height="${px}" rx="${radius}" fill="#2369bd"/>` : ''}
    <circle cx="${px / 2}" cy="${px / 2}" r="${10.4 * s}" fill="none" stroke="${outerInk}" stroke-opacity="${tile ? 0.55 : 1}" stroke-width="${2.3 * s}" stroke-dasharray="${dash} ${seg - dash}"/>
    <circle cx="${px / 2}" cy="${px / 2}" r="${5.6 * s}" fill="none" stroke="${innerInk}" stroke-width="${3.1 * s}"/>
  </svg>`
}

const SANS = "-apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif"

// The touch icon is the tile variant at 180px with six dashes, matching
// favicon.svg, because an icon is read small everywhere it appears. It is drawn square
// and full-bleed: iOS applies its own corner mask, and a tile that rounds itself
// first comes out a circle on the home screen while every icon beside it is a
// squircle.
const icon = `<body style="margin:0"><div style="width:180px;height:180px">${mark(180, { tile: true, radius: 0 })}</div></body>`

// The social card is the landing's own lockup and claim, at card scale. It says
// the same thing the page says, because a link preview is the first screen for
// anybody who arrives through somebody else.
const card = `<body style="margin:0">
  <div style="width:1200px;height:630px;background:#fafaf9;color:#0c0a09;font-family:${SANS};display:flex;flex-direction:column;justify-content:center;padding:0 92px;box-sizing:border-box">
    <div style="display:flex;align-items:center;gap:18px;margin-bottom:44px">
      <div style="width:52px;height:52px">${mark(52)}</div>
      <div style="font-size:38px;font-weight:600;letter-spacing:-0.035em">replic<span style="margin-left:0.055em">hai</span></div>
    </div>
    <div style="font-size:78px;font-weight:600;letter-spacing:-0.04em;line-height:1.04;max-width:15ch">Does the paper actually reproduce?</div>
    <div style="margin-top:38px;font-size:27px;line-height:1.5;color:#5c5b58;max-width:44ch">
      It runs the paper. The inner loop is implemented and re-run; the outer loop — the study with people — is named, bounded, and never simulated.
    </div>
  </div>
</body>`

mkdirSync('public', { recursive: true })
const browser = await puppeteer.launch({ executablePath: CHROME, headless: true })
try {
  for (const [name, html, w, h] of [
    ['apple-touch-icon.png', icon, 180, 180],
    ['og.png', card, 1200, 630],
  ]) {
    const page = await browser.newPage()
    await page.setViewport({ width: w, height: h, deviceScaleFactor: 1 })
    await page.setContent(html, { waitUntil: 'load' })
    const buf = await page.screenshot({ type: 'png', clip: { x: 0, y: 0, width: w, height: h } })
    writeFileSync(`public/${name}`, buf)
    console.log(`wrote public/${name} (${w}×${h})`)
    await page.close()
  }
} finally {
  await browser.close()
}
