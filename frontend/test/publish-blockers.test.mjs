// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for a refused publish, as the publish gate's
// refusal reaches the screen.
//
// It guards that a refusal is actionable. Three things have to hold on screen:
// the refusal says which of the reasons it was, it names every session in the
// way rather than the first, and each of those is one click from the place the
// reader can see it for themselves. A generic sentence would send an owner
// looking for a content problem when their run had simply never finished.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { fail, json, start } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const PROJECT = 'proj-1'
const FLAGGED = 'job-flagged' // committed data it may not redistribute
const UNFINISHED = 'job-unfinished' // has a repository, never wrote a manifest

const project = { id: PROJECT, owner: 'p', title: 'Posture-Aware Pen+Touch', visibility: 'private', created_at: at(0) }
const paper = { key: 'doi:10.1145/3654777.3676417', ref: '10.1145/3654777.3676417', title: 'Sensing Posture-Aware Pen+Touch Interaction', sessions: 2 }
const sessionOf = (id) => ({
  id,
  project_id: PROJECT,
  paper_key: paper.key,
  ref: paper.ref,
  phase: id === UNFINISHED ? 'failed' : 'done',
  repo_name: `open-hai/${id}`,
  created_at: at(0),
  updated_at: at(6e6),
  cost: 3842,
})

// What the server refuses with: the flagged tree is the headline, and both
// sessions are named. This is the body the screen has to turn into a link to
// each session: 409 with `blockers`, exactly as internal/httpapi writes it.
const refusal = {
  error: 'not_clear',
  blockers: [
    { session_id: FLAGGED, repo: `open-hai/${FLAGGED}`, ref: paper.ref, reason: 'not_clear', review: 'excluded' },
    { session_id: UNFINISHED, repo: `open-hai/${UNFINISHED}`, ref: paper.ref, reason: 'unfinished' },
  ],
}

const profile = { display_name: 'Ada Lovelace', contact_email: 'ada@example.org', handle: 'ada', public: true }

const routes = [
  ['POST', /^\/projects\/[^/]+\/public$/, json(refusal, 409)],
  ['PATCH /me/profile', profile],
  // The flagged session's manifest: the review is what the verdict pane has
  // to say out loud, not just color a pill with.
  [
    /^\/jobs\/([^/]+)\/reproduction$/,
    ({ params: [id] }) => (id === UNFINISHED ? json({ error: 'not_found' }, 404) : { id, repo: `open-hai/${id}`, title: 't', verdict: 'passed', review: 'excluded', can_publish: false }),
  ],
  [/^\/jobs\/[^/]+\/verdict$/, json({ error: 'no_findings' }, 404)],
  [/\/artifacts$/, { files: [] }],
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', profile],
  ['/projects', [{ ...project, session_count: 2, running: 0, latest_phase: 'done', published_sessions: 2 }]],
  [/^\/projects\/[^/]+$/, { project, sessions: [sessionOf(FLAGGED), sessionOf(UNFINISHED)], papers: [paper], may_submit: true, role: 'owner' }],
  [/^\/projects\/[^/]+\/papers$/, [paper]],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/projects\/[^/]+\/credits$/, { wallet: 20, start_reserve: 5 }],
  [/^\/jobs\/([^/]+)$/, ({ params: [id] }) => sessionOf(id)],
  [/^\/jobs\/[^/]+\/transcript$/, { events: [], usage: null, live: false }],
  [/^\/(public|shared)$/, []],
]

const h = await start()
try {
  const page = await h.page({ viewport: { width: 1440, height: 1000 }, routes })
  const read = (sel) => page.$eval(sel, (el) => el.textContent.replace(/\s+/g, ' ').trim())

  // press clicks the wizard button whose label matches, once it is live: step
  // 2's Continue is disabled until the profile it shows has been read, and the
  // sign-off until the affirmation is ticked. A click on a disabled button is
  // swallowed, and the walk would stall where it stood.
  const press = async (label) => {
    const btn = await page.waitForFunction(
      (re) => {
        const b = [...document.querySelectorAll('.modal button')].find((b) => new RegExp(re, 'i').test(b.textContent))
        return b && !b.disabled ? b : null
      },
      { timeout: 5000 },
      label,
    )
    await btn.evaluate((el) => el.click())
  }

  /* ── Walk the wizard to the refusal ── */

  await page.open(`/p/${PROJECT}`)
  await page.waitForSelector('[data-test="publish-project"]', { timeout: 15000 })
  await page.click('[data-test="publish-project"]')
  await page.waitForSelector('.modal', { timeout: 5000 })
  // Three steps: what becomes public, who stands behind it, responsibility. The
  // profile is prefilled and publishable, so both Continues come live; each
  // step is waited for by the dot it lights.
  for (let step = 2; step <= 3; step++) {
    await press('Continue')
    await page.waitForFunction((n) => document.querySelectorAll('.modal .step-dot.on').length === n, { timeout: 5000 }, step)
  }
  await page.evaluate(() => document.querySelector('.modal .check, .modal [role="checkbox"]')?.click())
  await press('Sign off')

  await page.waitForSelector('[data-test="publish-blockers"]', { timeout: 8000 })

  const refused = await page.evaluate(() => ({
    message: document.querySelector('.modal .err')?.textContent.replace(/\s+/g, ' ').trim() ?? '',
    rows: [...document.querySelectorAll('.blocker-row')].map((r) => r.textContent.replace(/\s+/g, ' ').trim()),
  }))

  // Both sessions, not the first one: an owner who fixes one must not walk into
  // the same refusal with nothing more to go on.
  if (refused.rows.length !== 2) fail(`the refusal named ${refused.rows.length} sessions, want both`)
  if (!refused.rows.some((r) => /cannot be redistributed/i.test(r))) fail(`no row explains the flagged tree: ${JSON.stringify(refused.rows)}`)
  if (!refused.rows.some((r) => /never finished/i.test(r))) fail(`no row explains the unfinished run: ${JSON.stringify(refused.rows)}`)
  // And the headline is about content, because one of them is.
  if (!/redistribut/i.test(refused.message)) fail(`the refusal message does not name the reason: ${refused.message}`)

  /* ── Follow a blocker to the session it names ── */

  await page.evaluate(() => document.querySelectorAll('[data-test="open-blocker"]')[0]?.click())
  await page.waitForSelector('[data-test="run-record"]', { timeout: 8000 })
  // The notice is what is measured next, so its absence falls through to the
  // assertion rather than ending the run here.
  await page.waitForSelector('[data-test="blocks-publishing"]', { timeout: 5000 }).catch(() => {})

  const landed = page.url()
  if (!landed.includes(`/s/${FLAGGED}/verdict`)) fail(`opening the flagged blocker landed at ${landed}`)

  // The place it landed says what is wrong, in words. A pill reading "excluded"
  // beside "Redistribution" is a field name, not an explanation.
  const blocks = await read('[data-test="blocks-publishing"]').catch(() => '')
  if (!/blocks publishing/i.test(blocks)) fail(`the flagged session does not say it blocks publishing: ${blocks}`)
  if (!/third-party data/i.test(blocks)) fail(`the flagged session does not say what was found: ${blocks}`)
  const warn = await page.$eval('[data-test="blocks-publishing"]', (el) => getComputedStyle(el).color)
  if (!warn || warn === 'rgb(0, 0, 0)') fail(`the blocking notice is not toned: ${warn}`)

  h.ok('a refused publish names its blockers and opens them')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
