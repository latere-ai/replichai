// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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
import { fail, json, settled, start, text, waitForText } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const PROJECT = 'proj-1'
const GRADED = 'job-graded' // has findings
const BARE = 'job-bare' // published, no findings
const DIED = 'job-died' // reached publishing and then failed: has a repo, was never graded
const REFUSED = 'job-refused' // wrote findings the system refused: a 422, not a 404

const project = { id: PROJECT, owner: 'p', title: 'Posture-Aware Pen+Touch', visibility: 'private', created_at: at(0) }
const paper = { key: 'doi:10.1145/3654777.3676417', ref: '10.1145/3654777.3676417', title: 'Sensing Posture-Aware Pen+Touch Interaction on Tablets', sessions: 2 }
const sessionOf = (id) => ({
  id,
  project_id: PROJECT,
  paper_key: paper.key,
  ref: paper.ref,
  // The one that died carries a repository and a failure. RepoName marks that
  // publishing began, not that it finished, and `failed` is terminal — so a
  // rail keyed on "terminal and has a repo" picks this one and hides the run
  // that actually produced findings.
  phase: id === DIED ? 'failed' : 'done',
  error: id === DIED ? 'publish: forge refused the commit' : undefined,
  repo_name: `open-hai/${id}`,
  created_at: at(0),
  updated_at: at(6e6),
  cost: 3842,
})

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
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', {}],
  ['/projects', [{ ...project, session_count: 2, running: 0, latest_phase: 'done', published_sessions: 2, spend_30d: 4_210_000 }]],
  // Newest first, as the job store returns them: the failed publish is the most
  // recent session of this paper.
  [/^\/projects\/[^/]+$/, { project, sessions: [sessionOf(DIED), sessionOf(GRADED), sessionOf(BARE)], papers: [paper], may_submit: true, role: 'owner' }],
  [/^\/projects\/[^/]+\/papers$/, [paper]],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/projects\/[^/]+\/credits$/, { wallet: 20, start_reserve: 5 }],
  [/^\/jobs\/([^/]+)$/, ({ params: [id] }) => sessionOf(id)],
  // Only the graded session has findings; the others answer 404, which is the
  // case the panes have to state rather than render as emptiness. A run whose
  // file failed validation is a different fact from one that never wrote a
  // file, and the pane has to say which.
  [
    /^\/jobs\/([^/]+)\/verdict$/,
    ({ params: [id] }) => {
      if (id === REFUSED) return json({ error: 'findings_invalid', problems: ['boundary[2].outcome: an outer-loop claim is verified'] }, 422)
      return id === GRADED ? findings : json({ error: 'no_findings' }, 404)
    },
  ],
  [/^\/jobs\/[^/]+\/transcript$/, { events: [], usage: null, live: false }],
  [/^\/jobs\/([^/]+)\/reproduction$/, ({ params: [id] }) => ({ id, repo: `open-hai/${id}`, title: 't', verdict: 'passed', review: 'clear', can_publish: true })],
  [/^\/jobs\/[^/]+\/artifacts$/, { files: ['README.md', 'REPRODUCIBILITY.md'] }],
  [/\/artifact$/, text('# readme')],
  [/^\/(public|shared)$/, []],
]

const h = await start()
try {
  const page = await h.page({ viewport: { width: 1440, height: 1000 }, routes })
  // landed resolves once the browser holds the answer to `path`. Armed before a
  // screen opens, it is how a pane that must render nothing for an answer is
  // known to have received it.
  const landed = (path) => page.waitForResponse((r) => new URL(r.url()).pathname === path, { timeout: 10000 })

  /* ── The verdict pane, with findings ── */

  await page.open(`/p/${PROJECT}/s/${GRADED}/verdict`)
  await page.waitForSelector('.grid-table', { timeout: 15000 })
  // The run record fills in from its own read of the reproduction.
  await page.waitForSelector('[data-test="run-record"]', { timeout: 5000 }).catch(() => {})
  await settled(page)

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

  // One verdict word on the screen, and it is the paper's. The manifest's grade
  // is the rubric's judgement of the *run* — a careful audit of a paper that
  // does not reproduce passes it — so heading both "Verdict" put two verdicts on
  // one paper and invited the reader to take "Passed" as "it reproduced".
  const verdicts = await page.evaluate(() => {
    const txt = (el) => (el ? el.textContent.replace(/\s+/g, ' ').trim() : null)
    const record = document.querySelector('[data-test="run-record"]')
    return {
      words: [...document.querySelectorAll('.findings-word')].map((e) => txt(e)),
      record: txt(record),
      // The run record must still carry the facts only it has.
      hasReview: /clear/i.test(txt(record) || ''),
      hasRepo: !!record?.querySelector('a[href*="github.com"]'),
      hasGrade: /passed/i.test(txt(record) || ''),
    }
  })
  if (verdicts.words.length !== 1) fail(`the pane shows ${verdicts.words.length} verdict headlines, want one — the paper's`)
  if (!/partial/i.test(verdicts.words[0] || '')) fail(`the headline is ${verdicts.words[0]}, want the paper's verdict`)
  if (!verdicts.record) fail('the run record is gone; the redistribution review and the repository live there')
  else {
    if (/^\s*verdict/i.test(verdicts.record)) fail('the run record still calls itself a verdict')
    if (!verdicts.hasGrade) fail('the run record dropped the rubric grade instead of demoting it')
    if (!verdicts.hasReview) fail('the run record lost the redistribution review')
    if (!verdicts.hasRepo) fail('the run record lost the repository link')
  }

  if (read.decisions.length !== 1) fail(`the decisions section rendered ${read.decisions.length} entries`)
  else if (!/250 ms/.test(read.decisions[0]) || !/±0.4 pp/.test(read.decisions[0])) {
    fail(`a decision does not carry both its assumption and its sensitivity: ${read.decisions[0]}`)
  }

  /* ── The verdict pane, without findings ── */

  await page.open(`/p/${PROJECT}/s/${BARE}/verdict`)
  await waitForText(page, 'body', 'recorded no findings as data', { timeout: 5000 }).catch(() => {})
  const bare = await page.evaluate(() => ({
    table: !!document.querySelector('.grid-table'),
    body: document.body.textContent.replace(/\s+/g, ' '),
  }))
  if (bare.table) fail('a session with no findings rendered a boundary table')
  if (!/recorded no findings as data/i.test(bare.body)) fail('a session with no findings does not say why')

  /* ── The verdict pane, with findings the system refused ── */

  await page.open(`/p/${PROJECT}/s/${REFUSED}/verdict`)
  await waitForText(page, 'body', 'wrote findings the system refused', { timeout: 5000 }).catch(() => {})
  const refused = await page.evaluate(() => ({
    table: !!document.querySelector('.grid-table'),
    body: document.body.textContent.replace(/\s+/g, ' '),
  }))
  if (refused.table) fail('a session whose findings were refused rendered a boundary table')
  // A refusal and an absence are different facts about the run, and the pane
  // must not report the second when the first happened.
  if (!/wrote findings the system refused/i.test(refused.body)) {
    fail('a refused findings file is reported as if the run had written nothing')
  }

  /* ── The artifacts pane ── */

  await page.open(`/p/${PROJECT}/s/${GRADED}/artifacts`)
  await page.waitForSelector('.release-row', { timeout: 15000 })
  // The file browser fills in from its own read of the artifacts.
  await page.waitForSelector('.detail-grid .card .rows', { timeout: 5000 }).catch(() => {})
  await settled(page)

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

  // Nothing marks the absence on screen, so the pane is given the 404 first.
  const noFindings = landed(`/jobs/${BARE}/verdict`)
  await page.open(`/p/${PROJECT}/s/${BARE}/artifacts`)
  await page.waitForSelector('.detail-grid .card .rows', { timeout: 5000 }).catch(() => {})
  await noFindings
  const bareArt = await page.evaluate(() => ({
    released: !!document.querySelector('.release-row'),
    browser: !!document.querySelector('.detail-grid .card .rows'),
  }))
  if (bareArt.released) fail('a session with no findings rendered a released list')
  if (!bareArt.browser) fail('a session with no findings lost its file browser')

  /* ── The sessions tab's rail, and Publish… on the header (021e) ── */

  await page.open(`/p/${PROJECT}`)
  await page.waitForSelector('.findings-rail', { timeout: 15000 })
  // A card fills in from the verdict it reads.
  await waitForText(page, '.findings-rail > .card', 'components', { timeout: 5000 }).catch(() => {})

  const rail = await page.evaluate(() => {
    const txt = (el) => (el ? el.textContent.replace(/\s+/g, ' ').trim() : null)
    return {
      cards: [...document.querySelectorAll('.findings-rail > .card')].map((c) => txt(c)),
      publish: !!document.querySelector('.detail-head [data-test="publish-project"]'),
    }
  })

  // One card, because the project holds one paper with a graded session — and
  // it has to be the graded one, not the newer session that reached publishing
  // and then failed. That session has a repository and is terminal, so a rail
  // keyed on those two facts renders nothing here while findings exist.
  if (rail.cards.length !== 1) fail(`the rail rendered ${rail.cards.length} cards, want one per graded paper`)
  else {
    const c = rail.cards[0]
    for (const want of ['0.50', 'of 2 components', 'VERIFIED', 'BLOCKED']) {
      if (!c.includes(want)) fail(`the rail card is missing ${want}: ${c}`)
    }
    if (!/Code|Data/.test(c)) fail(`the rail card carries no scorecard: ${c}`)
  }
  if (!rail.publish) fail('the project header offers no Publish… button')

  /* ── The windowed spend (021e) ── */

  // 034 folded the three counters into two cards; the spend kept its own ruled
  // row on the credits card. Where it sits changed, what it must say did not: a
  // figure whose period is not stated is not a figure.
  await page.open('/')
  await page.waitForSelector('[data-test="spend-30d"]', { timeout: 15000 })
  // A dash stands in until the list has landed and the sum is known.
  await waitForText(page, '[data-test="spend-30d"]', '$', { timeout: 5000 }).catch(() => {})
  const spend = await page.evaluate(
    () => document.querySelector('[data-test="spend-30d"]')?.textContent.replace(/\s+/g, ' ').trim() || '',
  )
  if (!spend) fail('the projects screen no longer shows what the last thirty days cost')
  else {
    if (!/30 days/i.test(spend)) fail(`the spend does not state its window: ${spend}`)
    // The mocked project reports 4_210_000 micro-USD across the window.
    if (!/\$4\.21/.test(spend)) fail(`the spend reads ${spend}, want the server's windowed sum`)
  }

  h.ok('findings panes, rail, and the windowed spend')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
