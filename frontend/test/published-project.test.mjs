// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for what a published project offers.
//
// A signed attestation covers the papers and sessions a project held when it
// was signed, so the server refuses a new paper or session on a published
// project. The screen has to say so where those actions would have been, and
// not offer them: an owner who clicked Start and got a refusal is told the
// reason after the fact.
//
// Two things have to hold:
//
//   1. A published project the owner opens shows the refusal sentence and no
//      control that adds a paper or starts a session.
//   2. A private project beside it still offers both, so the check is on the
//      project's visibility and not on every project.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { fail, start } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const ME = 'p'
const PUBLIC = 'proj-public'
const PRIVATE = 'proj-private'
const KEY = 'doi:10.1145/3544548.3581433'
const SENTENCE = 'A published project can’t take new papers or sessions. Withdraw the publication first.'

const projects = () => [
  { id: PUBLIC, owner: ME, title: 'Already published', visibility: 'public', created_at: at(0) },
  { id: PRIVATE, owner: ME, title: 'Still private', visibility: 'private', created_at: at(1) },
]
const project = (id) => projects().find((p) => p.id === id)
const paper = { key: KEY, ref: '10.1145/3544548.3581433', title: 'Sensing Posture-Aware Pen+Touch Interaction', added_by: ME, added_at: at(0), sessions: 0 }

const routes = [
  ['/me', { authenticated: true, principal: ME, email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', { display_name: 'Ada Lovelace', contact_email: 'ada@example.org', handle: 'ada', public: true }],
  ['/projects', () => projects().map((p) => ({ ...p, session_count: 0, running: 0, latest_phase: 'done', published_sessions: 0 }))],
  [/^\/projects\/([^/]+)$/, ({ params: [id] }) => ({ project: project(id), sessions: [], papers: [paper], may_submit: true, role: 'owner' })],
  [/^\/projects\/[^/]+\/papers$/, [paper]],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/projects\/[^/]+\/credits$/, { wallet: 20, start_reserve: 5, available: 20 }],
  ['/models', { tiers: [] }],
  [/^\/(public|shared)$/, []],
]

// The controls that add work, found by what they say, since the card marks
// neither of them.
const offers = (page) =>
  page.evaluate(() => ({
    addForm: !!document.querySelector('input[placeholder][spellcheck="false"]') && [...document.querySelectorAll('button')].some((b) => /^\s*Add\b/.test(b.textContent)),
    start: [...document.querySelectorAll('button')].some((b) => /^\s*Start\b/.test(b.textContent)),
    note: document.querySelector('[data-test="published-note"]')?.textContent.trim() ?? null,
  }))

const h = await start()
try {
  const page = await h.page({ viewport: { width: 1440, height: 1000 }, routes })

  /* ── 1. A published project offers neither, and says why ── */

  await page.open(`/p/${PUBLIC}`)
  await page.waitForSelector('.paper-group', { timeout: 6000 })
  const published = await offers(page)
  if (published.note !== SENTENCE) fail(`the published project shows ${JSON.stringify(published.note)}, want the refusal sentence`)
  if (published.addForm) fail('a published project still offers to add a paper')
  if (published.start) fail('a published project still offers to start a session')

  /* ── 2. A private project still offers both ── */

  await page.open(`/p/${PRIVATE}`)
  await page.waitForSelector('.paper-group', { timeout: 15000 })
  const open = await offers(page)
  if (open.note) fail(`a private project shows the published sentence: ${open.note}`)
  if (!open.addForm) fail('a private project no longer offers to add a paper')
  if (!open.start) fail('a private project no longer offers to start a session')

  h.ok('a published project says it takes no new work, and offers none')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
