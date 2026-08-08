// A browser regression test for the merged projects list (018).
//
// What it guards: the screen used to split a reader's work in two by a fact they
// did not choose — whether they created a project or were invited to it. The
// merge is not one line: the segmented control has to be gone, both kinds of
// project have to appear in one list, the Owner column has to say "You" for your
// own and link somebody else's public page for theirs, the Last-modified column
// has to render an age rather than a raw timestamp, the order has to follow that
// column, and the stat tiles have to count the merged list and not the owned half.
//
// None of that is reachable from a unit test: the assertions are about rendered
// rows and their painted order. It drives Chrome over a Vite dev server with the
// API mocked. Run it with `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5199
const BASE = `http://localhost:${PORT}`

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

const ME = 'me-sub'
const ago = (ms) => new Date(Date.now() - ms).toISOString()

// One project the reader owns and one they were invited to, in the order the
// server sends them: newest-modified first. Both come from /projects now — the
// point of the spec is that there is no second list to fetch.
const projects = [
  {
    id: 'shared-1',
    owner: 'ada-sub',
    owner_name: 'Ada Lovelace',
    owner_handle: 'ada',
    title: 'The Placebo Effect of Artificial Intelligence',
    visibility: 'public',
    created_at: ago(90 * 86_400_000),
    modified_at: ago(3 * 3_600_000),
    session_count: 2,
    published_sessions: 2,
    running: 0,
    latest_phase: 'done',
  },
  {
    id: 'mine-1',
    owner: ME,
    title: 'Point of no Undo',
    visibility: 'private',
    created_at: ago(120 * 86_400_000),
    modified_at: ago(2 * 86_400_000),
    // Ran three times and published none of them: a session count is not an
    // artifact count, so this row must offer no download at all.
    session_count: 3,
    published_sessions: 0,
    running: 0,
    latest_phase: 'failed',
  },
]

const routes = [
  [/^\/me$/, { authenticated: true, principal: ME, email: 'me@example.com', tier: 'user', credits: { wallet: 10_000_000, start_reserve: 5_000_000 } }],
  [/^\/me\/profile$/, {}],
  [/^\/projects$/, projects],
  [/^\/public$/, []],
]

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
      /* not up yet */
    }
    if (i > 240) throw new Error('vite did not start')
    await new Promise((r) => setTimeout(r, 250))
  }

  browser = await puppeteer.launch({ executablePath: CHROME, headless: true })
  const page = await browser.newPage()
  await page.setViewport({ width: 1440, height: 900 })
  await page.setRequestInterception(true)
  const asked = []
  page.on('request', (req) => {
    const u = new URL(req.url())
    if (u.origin === BASE) {
      asked.push(u.pathname)
      const hit = routes.find(([re]) => re.test(u.pathname))
      if (hit) return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(hit[1]) })
      if (u.pathname.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    }
    req.continue()
  })

  await page.goto(BASE, { waitUntil: 'networkidle0' })
  await page.waitForSelector('[data-test="project-rows"] .row')

  const read = () =>
    page.evaluate(() =>
      [...document.querySelectorAll('[data-test="project-rows"] .row')].map((row) => {
        const owner = row.querySelector('[data-test="owner"]')
        const modified = row.querySelector('[data-test="modified"]')
        return {
          title: row.querySelector('.row-title')?.textContent.trim() || '',
          owner: owner?.textContent.trim() || '',
          ownerLink: owner?.querySelector('a')?.getAttribute('href') || '',
          ownerShown: !!owner?.getClientRects().length,
          modified: modified?.textContent.trim() || '',
          modifiedTip: modified?.getAttribute('data-tip') || '',
        }
      }),
    )

  const rows = await read()
  if (rows.length !== 2) fail(`the list shows ${rows.length} rows, want both the owned and the shared project in one list`)

  // The split is gone, not hidden behind a default tab.
  const tabs = await page.evaluate(() => document.querySelectorAll('.screen .segmented').length)
  if (tabs) fail('the projects screen still renders a segmented control; ownership is a column now, not a tab')
  if (asked.some((p) => p === '/shared')) fail('the screen still fetches /shared')

  // Order follows the column the reader can see.
  if (rows[0].title !== projects[0].title) fail(`the first row is "${rows[0].title}", want the most recently modified "${projects[0].title}"`)

  // Owner: "You" for your own, the other person's name for theirs, linking to
  // their public page.
  const mine = rows.find((r) => r.title === 'Point of no Undo')
  const theirs = rows.find((r) => r.title.startsWith('The Placebo Effect'))
  if (!mine || !theirs) fail('both projects must be listed by title')
  if (mine && mine.owner !== 'You') fail(`your own project's owner reads "${mine.owner}", want "You"`)
  if (theirs && theirs.owner !== 'Ada Lovelace') fail(`the shared project's owner reads "${theirs.owner}", want the owner's name`)
  if (theirs && theirs.ownerLink !== '/u/ada') fail(`the owner's name links to "${theirs.ownerLink}", want /u/ada`)
  if (mine && mine.ownerLink) fail('your own name links to a profile page; "You" is not a link')

  // Last modified: an age to scan, the exact moment behind it. A raw ISO string
  // on the row is the failure this column exists to avoid.
  for (const r of rows) {
    if (!r.modified) fail(`"${r.title}" shows no modified time`)
    if (/\d{4}-\d{2}-\d{2}T/.test(r.modified)) fail(`"${r.title}" shows a raw timestamp (${r.modified}), want an age`)
    if (!r.modifiedTip) fail(`"${r.title}" has no exact time behind its age`)
  }
  if (theirs && !/hour/.test(theirs.modified)) fail(`the shared project reads "${theirs.modified}", want an age in hours`)
  if (mine && !/day/.test(mine.modified)) fail(`your own project reads "${mine.modified}", want an age in days`)
  // The download sits on the project that has something to hand over, not the
  // one whose sessions all failed before publishing.
  const downloadRow = await page.evaluate(() => {
    const a = document.querySelector('[data-test="download-project"]')
    return a ? a.closest('.row').querySelector('.row-title').textContent.trim() : ''
  })
  if (downloadRow !== projects[0].title) fail(`the download is on "${downloadRow}", want the project with published sessions`)

  // The tiles count what the list now shows, not the owned half of it.
  const stats = await page.evaluate(() => [...document.querySelectorAll('.stat .stat-value')].map((e) => e.textContent.trim()))
  if (stats[0] !== '2') fail(`the projects tile reads ${stats[0]}, want 2 — the merged list`)
  if (stats[1] !== '5') fail(`the sessions tile reads ${stats[1]}, want 5 — every row's sessions`)

  // The download is a real link to the archive, so the browser saves it rather
  // than the tab buffering a body that size — and a project that has produced
  // nothing offers no download at all (020).
  const downloads = await page.evaluate(() =>
    [...document.querySelectorAll('[data-test="download-project"]')].map((a) => ({ tag: a.tagName, href: a.getAttribute('href') })),
  )
  if (downloads.length !== 1) fail(`${downloads.length} rows offer a download; only the one with a published session may`)
  for (const d of downloads) {
    if (d.tag !== 'A') fail(`the download is a <${d.tag}>; it has to be a link for the browser to save the file`)
    if (!/^\/projects\/[^/]+\/archive$/.test(d.href)) fail(`the download points at ${d.href}, want the project's archive`)
  }

  // Narrow enough and the two context columns fold away; the row's identity does
  // not. A column that squeezes the title out is worse than one that leaves.
  await page.setViewport({ width: 640, height: 900 })
  await new Promise((r) => setTimeout(r, 200))
  const narrow = await read()
  if (narrow.some((r) => r.ownerShown)) fail('the Owner column still takes space on a narrow screen')
  if (narrow.some((r) => !r.title)) fail('a row lost its title on a narrow screen')

  if (!process.exitCode) console.log('ok: the projects list is one list, owned and shared together')
} finally {
  if (browser) await browser.close()
  vite.kill()
}
