// A browser regression test for the findings panes (021b, 021c).
//
// What it guards is the difference between a claim and an absence. Three things
// on these screens are easy to get subtly, silently wrong:
//
// An outer-loop row must never read as a result. The product's one hard rule is
// that the study with human participants is not scored, and a row that renders
// with an outcome tone says the opposite without a word changing.
//
// A verified claim that disagrees with the paper must still surface. That is the
// case a table of outcomes hides — the run reproduced the claim and found the
// paper's own figure wrong — and it is invisible unless the mismatch is rendered
// beside the outcome and repeated under "unresolved".
//
// A session with no findings must say so. Rendering an empty boundary table
// would tell the reader the paper had no claims, which is a false statement the
// product would be making on its own.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { spawn } from 'node:child_process'
import { createRequire } from 'node:module'

const require = createRequire(import.meta.url)
const puppeteer = (await import(require.resolve('puppeteer-core'))).default

const PORT = 5201
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

const fail = (msg) => {
  console.error(`FAIL: ${msg}`)
  process.exitCode = 1
}

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const PROJECT = 'proj-1'
const GRADED = 'job-graded' // has findings
const BARE = 'job-bare' // published, no findings

const project = { id: PROJECT, owner: 'p', title: 'Posture-Aware Pen+Touch', visibility: 'private', created_at: at(0) }
const paper = { key: 'doi:10.1145/3654777.3676417', ref: '10.1145/3654777.3676417', title: 'Sensing Posture-Aware Pen+Touch Interaction on Tablets', sessions: 2 }
const sessionOf = (id) => ({ id, project_id: PROJECT, paper_key: paper.key, ref: paper.ref, phase: 'done', repo_name: `open-hai/${id}`, created_at: at(0), updated_at: at(6e6), cost: 3842 })

// One findings file exercising every rule the panes have to respect: a verified
// claim that still disagrees with the paper, a blocked one, and an outer one.
const findings = {
  schema: 1,
  verdict: 'partial',
  summary: 'The classifier retrains from the released code and matches the reported accuracy within 1.8 pp.',
  graded_at: at(0),
  components: { verified: 1, partial: 0, blocked: 1, total: 2 },
  boundary: [
    {
      claim: 'Posture classifier reaches 91.4% accuracy',
      loop: 'inner',
      outcome: 'verified',
      evidence: 'Retrained from released code',
      citation: '§6.1',
      mismatch: { kind: 'numeric', reported: '91.4%', observed: '89.6%', delta: '-1.8 pp' },
    },
    { claim: 'End-to-end latency under 40 ms', loop: 'inner', outcome: 'blocked', evidence: "Requires the authors' tablet", citation: '§6.3' },
    { claim: 'Technique preferred over baseline (n=24)', loop: 'outer', outcome: 'not_scored', evidence: 'Human study — reported, never scored', citation: '§7' },
  ],
  decisions: [{ question: 'Which window length was used for the grip signal?', where: '§4, unspecified', assumed: '250 ms, from the figure axis', sensitivity: '±0.4 pp across 200–300 ms' }],
  scorecard: [
    { criterion: 'code', found: true, url: 'https://github.com/authors/posture-pen-touch' },
    { criterion: 'data', found: false, searched: ['OSF', 'the link in §5'] },
    { criterion: 'license', found: true, url: 'https://github.com/authors/posture-pen-touch/LICENSE' },
    { criterion: 'preregistration', found: false, searched: ['OSF registries'] },
    { criterion: 'supplement', found: true, url: 'https://dl.acm.org/doi/suppl/10.1145/3654777.3676417' },
  ],
  released: [
    { where: 'GitHub', what: 'authors/posture-pen-touch — code, no data', result: 'found', url: 'https://github.com/authors/posture-pen-touch' },
    { where: 'OSF', what: 'Searched by title and by all four author names', result: 'none' },
    { where: 'Dataset link in §5', what: 'https://tabletgrip.example/data — 404', result: 'dead' },
  ],
}

const routes = [
  [/^\/me$/, { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  [/^\/me\/profile$/, {}],
  [/^\/projects$/, [{ ...project, session_count: 2, running: 0, latest_phase: 'done', published_sessions: 2, spend_30d: 4_210_000 }]],
  [/^\/projects\/[^/]+$/, { project, sessions: [sessionOf(GRADED), sessionOf(BARE)], papers: [paper], may_submit: true, role: 'owner' }],
  [/^\/projects\/[^/]+\/papers$/, [paper]],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/projects\/[^/]+\/credits$/, { wallet: 20, start_reserve: 5 }],
  [/^\/jobs\/([^/]+)$/, (id) => sessionOf(id)],
  [/^\/jobs\/[^/]+\/transcript$/, { events: [], usage: null, live: false }],
  [/^\/jobs\/([^/]+)\/reproduction$/, (id) => ({ id, repo: `open-hai/${id}`, title: 't', verdict: 'passed', review: 'clear', can_publish: true })],
  [/^\/jobs\/([^/]+)\/artifacts$/, () => ({ files: ['README.md', 'REPRODUCIBILITY.md'] })],
  [/^\/(public|shared)$/, []],
]

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
  await page.setViewport({ width: 1440, height: 1000 })
  await page.setRequestInterception(true)
  page.on('request', (req) => {
    const u = new URL(req.url())
    if (u.origin === BASE) {
      // Only the graded session has findings; the other answers 404, which is
      // the case the panes have to state rather than render as emptiness.
      if (/^\/jobs\/[^/]+\/verdict$/.test(u.pathname)) {
        const id = u.pathname.split('/')[2]
        if (id !== GRADED) return req.respond({ status: 404, contentType: 'application/json', body: JSON.stringify({ error: 'no_findings' }) })
        return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(findings) })
      }
      if (u.pathname.endsWith('/artifact')) {
        return req.respond({ status: 200, contentType: 'text/plain', body: '# readme' })
      }
      const hit = routes.find(([re]) => re.test(u.pathname))
      if (hit) {
        const m = u.pathname.match(hit[0])
        const body = typeof hit[1] === 'function' ? hit[1](m[1]) : hit[1]
        return req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(body) })
      }
      if (u.pathname.endsWith('/events')) return req.respond({ status: 200, contentType: 'text/event-stream', body: '' })
    }
    req.continue()
  })

  /* ── The verdict pane, with findings ── */

  await page.goto(`${BASE}/p/${PROJECT}/s/${GRADED}/verdict`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.grid-table', { timeout: 15000 })
  await new Promise((r) => setTimeout(r, 300))

  const read = await page.evaluate(() => {
    const txt = (el) => (el ? el.textContent.replace(/\s+/g, ' ').trim() : null)
    const rows = [...document.querySelectorAll('.grid-table tbody tr')].map((tr) => {
      const tds = [...tr.querySelectorAll('td')]
      return {
        claim: txt(tds[0]),
        loop: txt(tds[1]),
        outcome: txt(tds[2]),
        outcomeColor: getComputedStyle(tds[2]).color,
        outer: tr.classList.contains('row-outer'),
        claimColor: getComputedStyle(tds[0].querySelector('.claim-text')).color,
        mismatch: txt(tr.querySelector('.mismatch')),
      }
    })
    return {
      summary: txt(document.querySelector('.findings-summary')),
      word: txt(document.querySelector('.findings-word')),
      rate: txt(document.querySelector('.findings-rate')),
      decomp: txt(document.querySelector('.findings-decomp')),
      bars: [...document.querySelectorAll('.decomp-bar > span')].length,
      rows,
      unresolved: [...document.querySelectorAll('.unresolved-row')].map((r) => txt(r)),
      decisions: [...document.querySelectorAll('.decision')].map((d) => txt(d)),
      body: document.body.textContent,
    }
  })

  if (!read.summary || read.summary.length < 40) fail(`the summary did not render: ${read.summary}`)
  if (!/partial/i.test(read.word || '')) fail(`the verdict word is ${read.word}`)
  if (read.rows.length !== 3) fail(`the boundary rendered ${read.rows.length} rows, want 3`)

  // The rate never appears alone: its decomposition is in the same block.
  if (!read.rate) fail('the rate did not render')
  if (!/verified/i.test(read.decomp || '') || !/blocked/i.test(read.decomp || '')) {
    fail(`the rate is not accompanied by its decomposition: ${read.decomp}`)
  }
  if (read.bars < 1) fail('the decomposition bar did not render')

  const outer = read.rows.find((r) => /OUTER/.test(r.loop || ''))
  if (!outer) fail('no outer-loop row rendered')
  else {
    if (!/NOT SCORED/i.test(outer.outcome || '')) fail(`the outer row reads "${outer.outcome}"; the human study is never scored`)
    if (!outer.outer) fail('the outer row is not marked as one, so it is only distinguishable by reading the label')
    // Its tone must not be one of the result tones, which is what makes the
    // rule visible without reading.
    const results = read.rows.filter((r) => !r.outer).map((r) => r.outcomeColor)
    if (results.includes(outer.outcomeColor)) fail(`the outer row is toned like a result (${outer.outcomeColor})`)
  }

  // The case a table of outcomes hides.
  const verified = read.rows.find((r) => /VERIFIED/.test(r.outcome || ''))
  if (!verified?.mismatch) fail('a verified claim that disagrees with the paper shows no mismatch')
  else {
    for (const want of ['91.4%', '89.6%', '-1.8 pp']) {
      if (!verified.mismatch.includes(want)) fail(`the mismatch does not print ${want}: ${verified.mismatch}`)
    }
  }

  // Unresolved lists the blocked claim and the disagreeing one, and no outer row.
  if (read.unresolved.length !== 2) fail(`unresolved lists ${read.unresolved.length} rows, want the blocked one and the mismatching one`)
  if (read.unresolved.some((r) => /preferred over baseline/i.test(r))) {
    fail('an outer-loop claim is listed as unresolved; it was never scored, so there is nothing to resolve')
  }
  if (!read.unresolved.some((r) => /latency/i.test(r))) fail('the blocked claim is not listed as unresolved')

  if (read.decisions.length !== 1) fail(`the decisions section rendered ${read.decisions.length} entries`)
  else if (!/250 ms/.test(read.decisions[0]) || !/±0.4 pp/.test(read.decisions[0])) {
    fail(`a decision does not carry both its assumption and its sensitivity: ${read.decisions[0]}`)
  }

  /* ── The verdict pane, without findings ── */

  await page.goto(`${BASE}/p/${PROJECT}/s/${BARE}/verdict`, { waitUntil: 'networkidle0' })
  await new Promise((r) => setTimeout(r, 500))
  const bare = await page.evaluate(() => ({
    table: !!document.querySelector('.grid-table'),
    body: document.body.textContent.replace(/\s+/g, ' '),
  }))
  if (bare.table) fail('a session with no findings rendered a boundary table')
  if (!/recorded no findings as data/i.test(bare.body)) fail('a session with no findings does not say why')

  /* ── The artifacts pane ── */

  await page.goto(`${BASE}/p/${PROJECT}/s/${GRADED}/artifacts`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.release-row', { timeout: 15000 })
  await new Promise((r) => setTimeout(r, 300))

  const art = await page.evaluate(() => {
    const txt = (el) => (el ? el.textContent.replace(/\s+/g, ' ').trim() : null)
    const rows = [...document.querySelectorAll('.release-row')].map((r) => ({
      where: txt(r.querySelector('.release-where')),
      text: txt(r),
      dot: getComputedStyle(r.querySelector('.release-dot')).backgroundColor,
    }))
    const released = document.querySelector('.release-row')
    const repo = document.querySelector('.detail-grid .card .rows')
    return {
      rows,
      scores: [...document.querySelectorAll('.score-row')].map((s) => txt(s)),
      searched: document.body.textContent.includes('OSF registries'),
      // Authors first: the released list must start above the file browser.
      authorsFirst: !!released && !!repo && released.getBoundingClientRect().top < repo.getBoundingClientRect().top,
      browser: !!repo,
    }
  })

  if (art.rows.length !== 3) fail(`the released list rendered ${art.rows.length} rows, want 3`)
  if (!art.browser) fail('the reproduction repository browser disappeared')
  if (!art.authorsFirst) fail('the reproduction repository renders above what the authors released')

  const none = art.rows.find((r) => /OSF/.test(r.where || ''))
  const dead = art.rows.find((r) => /Dataset link/.test(r.where || ''))
  if (!none || !dead) fail('the none and dead rows did not both render')
  else if (none.dot === dead.dot) {
    fail(`nothing-published and a dead link share a tone (${dead.dot}); the second is one an author can still fix`)
  }

  if (art.scores.length !== 5) fail(`the scorecard rendered ${art.scores.length} criteria, want 5`)
  if (!art.searched) fail('a criterion marked not found does not name where it looked')

  /* ── The artifacts pane, without findings ── */

  await page.goto(`${BASE}/p/${PROJECT}/s/${BARE}/artifacts`, { waitUntil: 'networkidle0' })
  await new Promise((r) => setTimeout(r, 500))
  const bareArt = await page.evaluate(() => ({
    released: !!document.querySelector('.release-row'),
    browser: !!document.querySelector('.detail-grid .card .rows'),
  }))
  if (bareArt.released) fail('a session with no findings rendered a released list')
  if (!bareArt.browser) fail('a session with no findings lost its file browser')

  /* ── The sessions tab's rail, and Publish… on the header (021e) ── */

  await page.goto(`${BASE}/p/${PROJECT}`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.findings-rail', { timeout: 15000 })
  await new Promise((r) => setTimeout(r, 400))

  const rail = await page.evaluate(() => {
    const txt = (el) => (el ? el.textContent.replace(/\s+/g, ' ').trim() : null)
    const cards = [...document.querySelectorAll('.findings-rail > .card')].map((c) => txt(c))
    const buttons = [...document.querySelectorAll('.detail-head button')].map((b) => txt(b))
    return {
      cards,
      publish: !!document.querySelector('.detail-head [data-test="publish-project"]'),
      elapsed: [...document.querySelectorAll('.row .tabular')].map((e) => txt(e)),
    }
  })

  // One card, because the project holds one paper with a graded session. A
  // second paper would be a second card, never a single project-wide figure.
  if (rail.cards.length !== 1) fail(`the rail rendered ${rail.cards.length} cards, want one per graded paper`)
  else {
    const c = rail.cards[0]
    for (const want of ['0.50', 'of 2 components', 'VERIFIED', 'BLOCKED']) {
      if (!c.includes(want)) fail(`the rail card is missing ${want}: ${c}`)
    }
    if (!/Code|Data/.test(c)) fail(`the rail card carries no scorecard: ${c}`)
  }
  if (!rail.publish) fail('the project header offers no Publish… button')

  /* ── The spend tile (021e) ── */

  await page.goto(`${BASE}/`, { waitUntil: 'networkidle0' })
  await page.waitForSelector('.stat-grid', { timeout: 15000 })
  await new Promise((r) => setTimeout(r, 300))
  const tiles = await page.evaluate(() =>
    [...document.querySelectorAll('.stat-grid > *')].map((s) => s.textContent.replace(/\s+/g, ' ').trim()),
  )
  const spend = tiles.find((s) => /spend/i.test(s))
  if (!spend) fail(`no spend tile among ${JSON.stringify(tiles)}`)
  else {
    if (!/30 days/i.test(spend)) fail(`the spend tile does not state its window: ${spend}`)
    // The mocked project reports 4_210_000 micro-USD across the window.
    if (!/\$4\.21/.test(spend)) fail(`the spend tile shows ${spend}, want the server's windowed sum`)
  }

  if (!process.exitCode) console.log('PASS: findings panes, rail, and spend tile')
} catch (err) {
  fail(err.message)
} finally {
  if (browser) await browser.close()
  vite.kill()
}
