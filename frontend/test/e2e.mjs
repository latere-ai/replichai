// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The `e2e` script of the frontend gate: the browser suite (test/run.mjs) in
// the Chromium that the shared CI pipeline installs with Playwright, or the
// browser PUPPETEER_EXECUTABLE_PATH names.
//
// Unlike `bun run test:browser`, which skips cleanly when the machine has no
// Chrome, this script fails: the gate names this package as one whose tests
// drive a browser, so a run that found none proved nothing.
//
//   bun run e2e
import { spawnSync } from 'node:child_process'
import { existsSync, readdirSync } from 'node:fs'
import { homedir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { findChrome } from './harness.mjs'

const here = dirname(fileURLToPath(import.meta.url))

// playwrightChromium is the newest Chromium that `playwright install chromium`
// left in its cache, or null. The cache sits at PLAYWRIGHT_BROWSERS_PATH when
// that is set, and at the platform's usual place otherwise.
function playwrightChromium() {
  const root =
    process.env.PLAYWRIGHT_BROWSERS_PATH ||
    (process.platform === 'darwin' ? join(homedir(), 'Library/Caches/ms-playwright') : join(homedir(), '.cache/ms-playwright'))
  if (!existsSync(root)) return null
  // The numeric suffix is the browser revision, so the highest is the newest.
  const revisions = readdirSync(root)
    .filter((d) => /^chromium-\d+$/.test(d))
    .sort((a, b) => Number(b.slice(9)) - Number(a.slice(9)))
  const executables = [
    'chrome-linux64/chrome',
    'chrome-linux/chrome',
    'chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
    'chrome-mac-x64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing',
  ]
  for (const rev of revisions) {
    for (const rel of executables) {
      const path = join(root, rev, rel)
      if (existsSync(path)) return path
    }
  }
  return null
}

const env = { ...process.env }
if (!env.PUPPETEER_EXECUTABLE_PATH) {
  const installed = playwrightChromium()
  if (installed) {
    env.PUPPETEER_EXECUTABLE_PATH = installed
    env.WEB_TEST_NO_SANDBOX = '1'
  } else if (!findChrome()) {
    console.error('FAIL: no browser to drive: set PUPPETEER_EXECUTABLE_PATH, or run `bunx playwright install chromium`')
    process.exit(1)
  }
}
// CI builds right before this script, so it serves that build through `vite
// preview`: the dev server compiles each module on request, which on a shared
// runner makes page loads far slower than the suite's waits allow. A
// workstation keeps the dev server, so a run tests the code as it is now.
if (process.env.CI && !env.WEB_TEST_BUILT) env.WEB_TEST_BUILT = '1'

const run = spawnSync(process.execPath, [join(here, 'run.mjs')], { env, stdio: 'inherit' })
if (run.error) {
  console.error(`FAIL: ${run.error.message}`)
  process.exit(1)
}
process.exit(run.status ?? 1)
