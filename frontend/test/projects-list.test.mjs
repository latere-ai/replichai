// A browser regression test for the merged projects list (018, restyled by 034).
//
// What it guards: the screen used to split a reader's work in two by a fact they
// did not choose — whether they created a project or were invited to it. The
// merge is not one line: the segmented control has to be gone, both kinds of
// project have to appear in one list, the Owner column has to say "You" for your
// own and link somebody else's public page for theirs, the Last-modified column
// has to render an age rather than a raw timestamp, the order has to follow that
// column, and the headline numbers have to count the merged list and not the
// owned half.
//
// 034 moved the owner and the date off the column grid and onto the title's meta
// line, and put a Published column and a filter row in the space that freed. What
// each of those facts *says* is unchanged and is still asserted here; where it
// sits is not. The three counters became two cards, and the numbers they show are
// asserted against the same fixture the rows are.
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

  // The split is gone, not hidden behind a default tab. 034 put a segmented
  // control back on this screen, so the assertion is no longer "there is none" —
  // it is that the one there is filters by *state*, which the reader picks, and
  // never by ownership, which they did not.
  const segs = await page.evaluate(() =>
    [...document.querySelectorAll('.screen .segmented')].map((g) => [...g.querySelectorAll('.seg-btn')].map((b) => b.textContent.trim())),
  )
  if (segs.length !== 1) fail(`the projects screen renders ${segs.length} segmented controls, want just the state filter`)
  for (const g of segs) {
    if (g.some((l) => /mine|shared|yours|invited/i.test(l))) fail(`a segmented control splits the list by ownership again: ${JSON.stringify(g)}`)
  }
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

  // The boundary cards count what the list now shows, not the owned half of it.
  // The fixture is two projects, five sessions, two of which published.
  const band = await page.evaluate(() => {
    const txt = (sel) => document.querySelector(sel)?.textContent.replace(/\s+/g, ' ').trim() || ''
    return { evidence: txt('[data-test="evidence-card"]'), credits: txt('[data-test="credits-card"]'), n: txt('[data-test="evidence-n"]') }
  })
  if (band.n !== '2') fail(`the published figure reads "${band.n}", want 2 — the sessions that committed a repository`)
  if (!/of 5 sessions/.test(band.evidence)) fail(`the evidence card does not give its denominator: ${band.evidence}`)
  if (!/across 2 projects/.test(band.evidence)) fail(`the evidence card does not say what it is across: ${band.evidence}`)
  // The remainder is stated, not left to be inferred from a bar.
  if (!/3 with nothing to hand over/.test(band.evidence)) fail(`the evidence card does not name the remainder: ${band.evidence}`)
  if (!/\$10\.00/.test(band.credits)) fail(`the credits card shows no wallet balance: ${band.credits}`)

  // A project that has published says what it handed over; one that has not says
  // so in words rather than showing an empty bar, which reads as a figure that
  // failed to load.
  const pub = await page.evaluate(() =>
    [...document.querySelectorAll('[data-test="project-rows"] .row')].map((r) => r.querySelector('[data-test="published-cell"]')?.textContent.replace(/\s+/g, ' ').trim() || ''),
  )
  if (!/2 of 2/.test(pub[0] || '')) fail(`the published project's cell reads "${pub[0]}", want its 2 of 2`)
  if (!/Nothing published/i.test(pub[1] || '')) fail(`the project that published nothing reads "${pub[1]}"`)

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

  // The columns have to be columns. A flex row sizes every cell to its own
  // content, so a "1 RUNNING" badge is wider than a "DONE" one and every cell to
  // its left starts somewhere different — which is what a list of eight projects
  // looked like. Geometry is the only way to see it: the text is identical
  // whether or not it lines up.
  const cols = await page.evaluate(() => {
    const rows = [...document.querySelectorAll('[data-test="project-rows"] .row')]
    const head = document.querySelector('[data-test="project-rows"] .project-head')
    const left = (el) => (el ? Math.round(el.getBoundingClientRect().left) : null)
    // Owner and date are no longer cells — they ride the title's meta line — so
    // the grid is title, published, status, visibility, and the first action.
    const cells = (row) => [
      left(row.querySelector('.row-main')),
      left(row.querySelector('[data-test="published-cell"]')),
      left(row.children[2]),
      left(row.children[3]),
      left(row.children[4]),
    ]
    return {
      rows: rows.map(cells),
      // The header is drawn from the same grid template, so its labels sit over
      // the cells they name rather than merely near them.
      head: head ? [...head.children].slice(0, 5).map((c) => left(c)) : null,
      labels: head ? [...head.children].slice(0, 5).map((c) => c.textContent.trim() || `col${[...head.children].indexOf(c)}`) : null,
    }
  })

  if (!cols.head) fail('the projects list has no column headings')
  else {
    // The four that carry content are named; the fifth is the action gutter.
    if (cols.labels.slice(0, 4).some((l) => /^col\d$/.test(l))) fail(`a column has no name: ${JSON.stringify(cols.labels)}`)
    for (const row of cols.rows) {
      row.forEach((x, i) => {
        if (x === null) return
        if (Math.abs(x - cols.head[i]) > 1) {
          fail(`column ${cols.labels[i]} starts at ${x} in a row but its heading at ${cols.head[i]}`)
        }
      })
    }
  }
  // Every row agrees with every other row, which is the reported defect.
  for (let i = 1; i < cols.rows.length; i++) {
    cols.rows[i].forEach((x, c) => {
      const first = cols.rows[0][c]
      if (x === null || first === null) return
      if (Math.abs(x - first) > 1) {
        fail(`column ${cols.labels?.[c] ?? c} starts at ${first} in the first row and ${x} in row ${i + 1}`)
      }
    })
  }

  // The filter narrows the rendered rows and restores them, and asks the server
  // for nothing to do it — a filter that refetched could disagree with the rows
  // it is filtering.
  const before = asked.length
  // Typed rather than assigned: a value written straight onto the node does not
  // fire the event React listens for, so the assertion would be about a DOM the
  // component never saw. Clearing selects the field's own text and deletes it,
  // for the same reason.
  const typeFilter = async (text) => {
    await page.click('[data-test="project-filter"]')
    await page.evaluate(() => {
      const el = document.querySelector('[data-test="project-filter"]')
      el.setSelectionRange(0, el.value.length)
    })
    await page.keyboard.press('Backspace')
    if (text) await page.type('[data-test="project-filter"]', text)
    await new Promise((r) => setTimeout(r, 200))
  }
  await typeFilter('Placebo')
  const filtered = await page.evaluate(() =>
    [...document.querySelectorAll('[data-test="project-rows"] .row .row-title')].map((e) => e.textContent.trim()),
  )
  if (filtered.length !== 1 || !/Placebo/.test(filtered[0] || '')) fail(`filtering by title left ${JSON.stringify(filtered)}`)
  if (asked.length !== before) fail(`filtering made ${asked.length - before} network calls; it narrows what is already loaded`)
  // Filtering by the owner's name, not only the title — the owner is on the row,
  // so it is a thing a reader would type.
  await typeFilter('Lovelace')
  const byOwner = await page.evaluate(() => document.querySelectorAll('[data-test="project-rows"] .row').length)
  if (byOwner !== 1) fail(`filtering by owner matched ${byOwner} rows, want the one Ada owns`)
  await typeFilter('')
  const restored = await page.evaluate(() => document.querySelectorAll('[data-test="project-rows"] .row').length)
  if (restored !== 2) fail(`clearing the filter left ${restored} rows, want both back`)

  // Narrow enough and the context column folds away; the row's identity does
  // not. A column that squeezes the title out is worse than one that leaves.
  // Owner is no longer a column at any width — it is on the meta line — so what
  // is asserted here is that it is still *readable*, and that Published is the
  // one that leaves.
  await page.setViewport({ width: 640, height: 900 })
  await new Promise((r) => setTimeout(r, 200))
  const narrow = await read()
  const pubShown = await page.evaluate(() => [...document.querySelectorAll('[data-test="published-cell"]')].some((c) => c.getClientRects().length))
  if (pubShown) fail('the Published column still takes space on a narrow screen')
  if (narrow.some((r) => !r.ownerShown)) fail('a row lost its owner on a narrow screen; it moved to the meta line, it did not leave')
  if (narrow.some((r) => !r.title)) fail('a row lost its title on a narrow screen')

  if (!process.exitCode) console.log('ok: the projects list is one list, in named columns that line up')
} finally {
  if (browser) await browser.close()
  vite.kill()
}
