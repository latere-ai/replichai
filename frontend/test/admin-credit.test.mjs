// Browser regression for an admin adding credits to their own wallet.
//
// The admin people table and the header are fed by different endpoints. The
// table refresh alone used to leave the header's /me snapshot stale until a
// reload, so this test requires both surfaces to show the completed top-up.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5203
const BASE = `http://localhost:${PORT}`
const EMAIL = 'admin@example.com'

const asked = process.env.PUPPETEER_EXECUTABLE_PATH
const CHROME = asked
  ? asked
  : ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/usr/bin/google-chrome', '/usr/bin/chromium'].find((p) => require('node:fs').existsSync(p))

if (!CHROME || !require('node:fs').existsSync(CHROME)) {
  if (asked) {
    console.error(`FAIL: PUPPETEER_EXECUTABLE_PATH=${asked} does not exist`)
    process.exit(1)
  }
  console.log('skip: no Chrome found (set PUPPETEER_EXECUTABLE_PATH)')
  process.exit(0)
}

let wallet = 20_000_000
const json = (req, body) => req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
const fail = (msg) => {
  console.error(`FAIL: ${msg}`)
  process.exitCode = 1
}

const vite = spawn('npx', ['vite', '--port', String(PORT), '--strictPort'], { stdio: 'ignore' })
let browser
try {
  for (let i = 0; ; i++) {
    try {
      if ((await fetch(BASE)).ok) break
    } catch {
      // Keep waiting for Vite.
    }
    if (i > 240) throw new Error('vite did not start')
    await new Promise((resolve) => setTimeout(resolve, 250))
  }

  browser = await puppeteer.launch({ executablePath: CHROME, headless: true })
  const page = await browser.newPage()
  await page.setViewport({ width: 1440, height: 900 })
  await page.setRequestInterception(true)
  page.on('request', async (req) => {
    const { pathname } = new URL(req.url())
    if (pathname === '/me') return json(req, { authenticated: true, principal: 'admin', email: EMAIL, tier: 'admin', credits: { wallet, start_reserve: 5_000_000 } })
    if (pathname === '/admin/access' && req.method() === 'GET') return json(req, [{ email: EMAIL, tier: 'admin', bound: true, wallet }])
    if (pathname === `/admin/access/${encodeURIComponent(EMAIL)}/credits` && req.method() === 'POST') {
      wallet += JSON.parse(req.postData()).amount
      return json(req, { email: EMAIL, wallet })
    }
    if (pathname === '/admin/settings') return json(req, { start_reserve: 5_000_000, overdraft_cap: 20_000_000, rates: { models: [] } })
    req.continue()
  })

  await page.goto(`${BASE}/console`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.member-row')
  const before = await page.$eval('.quota-pill', (el) => el.textContent.trim())
  if (before !== '$20.00') fail(`header started at ${JSON.stringify(before)}, want "$20.00"`)

  await page.evaluate(() => [...document.querySelectorAll('.member-row .btn')].find((button) => /add credits/i.test(button.textContent))?.click())
  await page.waitForSelector('.modal[role="dialog"]')
  await page.evaluate(() => [...document.querySelectorAll('.modal-foot .btn')].find((button) => /^save$/i.test(button.textContent.trim()))?.click())

  // First prove the top-up finished and the admin table received its refresh.
  await page.waitForFunction(() => [...document.querySelectorAll('.member-row')].some((row) => row.textContent.includes('$30.00')))
  // The same completed action must refresh the header without a reload.
  await page.waitForFunction(() => document.querySelector('.quota-pill')?.textContent.trim() === '$30.00')

  if (!process.exitCode) console.log('ok: an admin self-top-up refreshes the header balance')
} catch (e) {
  fail(e.message)
} finally {
  await browser?.close()
  vite.kill()
}
