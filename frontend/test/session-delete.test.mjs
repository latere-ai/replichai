// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for removing a session (032).
//
// It guards against a dead end. A run that began publishing and never finished
// holds a repository with no reproduction record, and the publish gate blocks on
// exactly that, so one cancelled or crashed session made its project
// unpublishable forever, and nothing in the product could clear it. The refusal
// named the session and offered one thing to do with it: open it, and land on a
// transcript that does not mention publishing at all.
//
// So two surfaces have to work, and the second is the one that matters:
//
//   1. A finished session on the project screen carries a delete, behind the
//      app's own dialog, which says what survives it: the cost, and the
//      repository, which is closed rather than destroyed.
//   2. The blocker row inside the publish wizard carries the same delete, and
//      using it clears the way: the very next sign-off makes the project public.
//
// The second is asserted through to the end state, not to the row
// disappearing. A test that stops at the disappearing row passes on a stale list.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { fail, json, noContent, start } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const ME = 'p'
const PROJECT = 'proj-1'
const GOOD = 'job-good' // finished, cleared, publishable
const STUCK = 'job-stuck' // a repository, and no manifest it will ever gain

// The server's state, mutated by the calls the page makes, so this drives the
// real delete → publish round trip rather than two static fixtures.
const deleted = new Set()
let visibility = 'private'

const project = () => ({ id: PROJECT, owner: ME, title: 'Posture-Aware Pen+Touch', visibility, created_at: at(0) })
const paper = { key: 'doi:10.1145/3544548.3581433', ref: '10.1145/3544548.3581433', title: 'Sensing Posture-Aware Pen+Touch Interaction', sessions: 2 }
const sessionOf = (id) => ({
  id,
  project_id: PROJECT,
  paper_key: paper.key,
  ref: paper.ref,
  phase: id === STUCK ? 'failed' : 'done',
  error: id === STUCK ? 'interrupted by a restart in publishing; no live run to rejoin' : '',
  repo_name: `open-hai/${id}`,
  created_at: at(0),
  updated_at: at(6e6),
  cost: 3842,
})
const live = () => [GOOD, STUCK].filter((id) => !deleted.has(id)).map(sessionOf)

// What the server refuses with, recomputed from what is left, exactly as
// internal/httpapi does: a session with a repository and no manifest blocks, and
// a deleted one is not read at all.
const refusal = () => ({
  error: 'sessions_unfinished',
  blockers: live()
    .filter((s) => s.id === STUCK)
    .map((s) => ({ session_id: s.id, repo: s.repo_name, ref: s.ref, reason: 'unfinished' })),
})

const profile = { display_name: 'Ada Lovelace', contact_email: 'ada@example.org', handle: 'ada', public: true }
const notFound = json({ error: 'not_found' }, 404)

const routes = [
  // The route under test.
  [
    'DELETE',
    /^\/jobs\/([^/]+)$/,
    ({ params: [id] }) => {
      if (deleted.has(id)) return notFound
      deleted.add(id)
      return noContent()
    },
  ],
  [
    'POST',
    /^\/projects\/[^/]+\/public$/,
    () => {
      const r = refusal()
      if (r.blockers.length) return json(r, 409)
      visibility = 'public'
      return project()
    },
  ],
  ['PATCH /me/profile', profile],
  ['/me', { authenticated: true, principal: ME, email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', profile],
  ['/projects', () => [{ ...project(), session_count: live().length, running: 0, latest_phase: 'done', published_sessions: live().length }]],
  [`/projects/${PROJECT}`, () => ({ project: project(), sessions: live(), papers: [paper], may_submit: true, role: 'owner' })],
  [`/projects/${PROJECT}/papers`, [paper]],
  [`/projects/${PROJECT}/members`, []],
  [`/projects/${PROJECT}/credits`, { wallet: 20, start_reserve: 5 }],
  [/^\/jobs\/([^/]+)$/, ({ params: [id] }) => (deleted.has(id) ? notFound : sessionOf(id))],
  [/^\/jobs\/[^/]+\/transcript$/, { events: [], usage: null, live: false }],
  [/^\/jobs\/([^/]+)\/reproduction$/, ({ params: [id] }) => (id === STUCK ? notFound : { id, repo: `open-hai/${id}`, title: 't', verdict: 'passed', review: 'clear', can_publish: true })],
  [/^\/jobs\/[^/]+\/verdict$/, json({ error: 'no_findings' }, 404)],
  [/\/artifacts$/, { files: [] }],
  [/^\/(public|shared)$/, []],
]

const h = await start()
try {
  const page = await h.page({ viewport: { width: 1440, height: 1000 }, routes })
  const rows = () => page.$$eval('.blocker-row', (els) => els.map((r) => r.textContent.replace(/\s+/g, ' ').trim()))

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

  // The wizard, walked to its sign-off: three steps, namely what becomes public,
  // who stands behind it, and responsibility. The profile is prefilled and
  // publishable, so both Continues come live; each step is waited for by the
  // dot it lights.
  const walkToSignOff = async () => {
    await page.click('[data-test="publish-project"]')
    await page.waitForSelector('.modal', { timeout: 5000 })
    for (let step = 2; step <= 3; step++) {
      await press('Continue')
      await page.waitForFunction((n) => document.querySelectorAll('.modal .step-dot.on').length === n, { timeout: 5000 }, step)
    }
    await page.evaluate(() => document.querySelector('.modal .check, .modal [role="checkbox"]')?.click())
  }
  const signOff = () => press('Sign off')

  /* ── 1. The row action, behind the app's own dialog ── */

  await page.open(`/p/${PROJECT}`)
  await page.waitForSelector('[data-test="delete-session"]', { timeout: 15000 })

  // Both finished sessions offer it, and it is not a native confirm: clicking it
  // has to raise the app's modal.
  const offered = await page.$$eval('[data-test="delete-session"]', (b) => b.length)
  if (offered !== 2) fail(`${offered} sessions offer a delete, want both finished ones`)

  await page.click('[data-test="delete-session"]')
  await page.waitForSelector('[data-test="delete-session-confirm"]', { timeout: 5000 })
  const dialog = await page.$eval('.modal', (el) => el.textContent.replace(/\s+/g, ' ').trim())
  // The dialog states what does not go. That is the reason it is a dialog and
  // not a bare button.
  if (!/ledger/i.test(dialog)) fail(`the dialog does not say the cost survives: ${dialog}`)
  if (!/made private, not deleted/i.test(dialog)) fail(`the dialog does not say the repository survives: ${dialog}`)

  // Escape out: a dialog that cannot be declined is not a confirmation.
  await page.keyboard.press('Escape')
  await page.waitForFunction(() => !document.querySelector('[data-test="delete-session-confirm"]'), { timeout: 2000 }).catch(() => {})
  if (await page.$('[data-test="delete-session-confirm"]')) fail('the delete dialog would not close')
  if (deleted.size) fail('a cancelled dialog deleted the session anyway')

  /* ── 2. The blocker row, and through to a published project ── */

  await page.waitForSelector('[data-test="publish-project"]', { timeout: 8000 })
  await walkToSignOff()
  await signOff()
  await page.waitForSelector('[data-test="publish-blockers"]', { timeout: 8000 })

  const blocked = await rows()
  if (blocked.length !== 1) fail(`the refusal named ${blocked.length} sessions, want the one unfinished run`)
  if (!/never finished/i.test(blocked[0])) fail(`the row does not explain the unfinished run: ${blocked[0]}`)
  // The row carries its own remedy. Before 032 its only offer was "Open session",
  // which led to a transcript that says nothing about publishing.
  if (!(await page.$('[data-test="delete-blocker"]'))) fail('the unfinished blocker offers no way to remove it')

  // It confirms in the row, not in a second Modal: two modals would share the
  // Escape key, and one press would take the list of blockers with it.
  await page.click('[data-test="delete-blocker"]')
  await page.waitForSelector('[data-test="delete-blocker-confirm"]', { timeout: 5000 })
  if ((await page.$$eval('.modal', (m) => m.length)) !== 1) fail('the blocker delete opened a second modal over the wizard')
  await page.click('[data-test="delete-blocker-confirm"]')
  await page.waitForFunction(() => document.querySelectorAll('.blocker-row').length === 0, { timeout: 8000 })

  if (!deleted.has(STUCK)) fail('confirming the blocker delete did not call DELETE /jobs/{id}')
  if (deleted.has(GOOD)) fail('the delete removed the wrong session')

  // The screen behind the wizard is now stale, and this is the path that has
  // no reload to hide it: the owner deletes a blocker and then closes the wizard
  // instead of signing off again. Without a refresh on the delete, the project
  // screen keeps listing a session that is gone, and opening it 404s. Asserted
  // in place; a page.goto here would pass whether or not anything refetched.
  await page.evaluate(() => document.querySelector('.modal-backdrop')?.click())
  await page.waitForFunction(() => !document.querySelector('.modal'), { timeout: 8000 })
  // The refetch is given time to land; a row that is still there after it is
  // the bug, so the wait falls through to the count rather than failing on it.
  await page.waitForFunction(() => document.querySelectorAll('[data-test="delete-session"]').length === 1, { timeout: 3000 }).catch(() => {})
  const stillShown = await page.$$eval('[data-test="delete-session"]', (b) => b.length)
  if (stillShown !== 1) fail(`${stillShown} sessions on the project screen after closing the wizard, want only the one kept`)

  // The end state, which is the point: a sign-off now publishes. Asserting only
  // that the blocker row went away would pass on a list that never refetched.
  await walkToSignOff()
  await signOff()
  await page.waitForFunction(() => !document.querySelector('.modal'), { timeout: 8000 })

  if (visibility !== 'public') fail(`the project is ${visibility} after the blocker was removed, want public`)
  if (!page.calls.includes(`DELETE /jobs/${STUCK}`)) fail(`no DELETE reached the server: ${JSON.stringify(page.calls.slice(-8))}`)

  h.ok('an unfinished session can be deleted, and its project then publishes')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
