// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for three writes on a project that used to fail in
// silence: renaming it, restoring it from the trash, and removing a collaborator.
//
// The bug it guards: each of the three awaited its request and did nothing with
// a rejection. A refused rename left the input open with no word on why; a
// refused restore left the trash banner exactly as it was; a refused removal was
// swallowed, the list re-read, and the person still on it — which looks, to the
// owner who clicked, like a removal that worked. Every other write on the screen
// names its refusal through the error copy in errors.ts; these three now do the
// same. Run it with `bun run test:browser`.
import { check, fail, json, settled, start, waitUntil } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const project = { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }
let trashed = false

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', {}],
  [/^\/(projects|public|shared)$/, []],
  // The three refusals under test.
  ['PATCH /projects/proj-1', json({ error: 'internal' }, 500)],
  ['POST /projects/proj-1/restore', json({ error: 'internal' }, 500)],
  ['DELETE', /^\/projects\/proj-1\/members\/.+$/, json({ error: 'not_owner' }, 403)],
  ['/projects/proj-1', () => ({ project: trashed ? { ...project, deleted_at: at(5000) } : project, sessions: [], may_submit: !trashed, role: 'owner' })],
  ['/projects/proj-1/papers', []],
  ['/projects/proj-1/members', [{ email: 'ada@example.org', bound: true }]],
]

const h = await start()
try {
  const page = await h.page({ routes })
  const sent = (prefix) => page.calls.some((c) => c.startsWith(prefix))

  // A rename the server refuses: the input stays, and says why.
  await (async () => {
    await page.open('/p/proj-1')
    await page.waitForSelector('.detail-title')
    await settled(page)
    await page.click('.detail-title button[aria-label="Rename"]')
    await page.waitForSelector('.title-input')
    await page.type('.title-input', ' (renamed)')
    await page.keyboard.press('Enter')
    await waitUntil(() => sent('PATCH /projects/proj-1'), { what: 'the rename to be sent' })
    await page.waitForSelector('.detail-title-edit .err', { timeout: 3000 }).catch(() => {})
    const shown = await page.evaluate(() => ({
      err: document.querySelector('.detail-title-edit .err')?.textContent.trim() ?? null,
      editing: !!document.querySelector('.title-input'),
      disabled: document.querySelector('.title-input')?.disabled ?? null,
    }))
    check(shown.err?.startsWith('Couldn’t rename'), `a refused rename should say so beside the input, got ${JSON.stringify(shown)}`)
    check(shown.editing && shown.disabled === false, `a refused rename should hand the input back for another try, got ${JSON.stringify(shown)}`)
  })()

  // A restore the server refuses: the banner says so.
  await (async () => {
    trashed = true
    await page.open('/p/proj-1')
    await page.waitForSelector('[data-test="restore"]')
    await settled(page)
    await page.click('[data-test="restore"]')
    await waitUntil(() => sent('POST /projects/proj-1/restore'), { what: 'the restore to be sent' })
    await page.waitForSelector('[data-test="trashed-banner"] .err', { timeout: 3000 }).catch(() => {})
    const err = await page.evaluate(() => document.querySelector('[data-test="trashed-banner"] .err')?.textContent.trim() ?? null)
    check(err?.startsWith('Couldn’t restore'), `a refused restore should say so in the banner, got ${JSON.stringify(err)}`)
    trashed = false
  })()

  // A removal the server refuses: the person stays on the list, and the card
  // says the removal did not happen.
  await (async () => {
    await page.open('/p/proj-1/people')
    await page.waitForSelector('.member-row button[aria-label="Remove"]')
    await settled(page)
    await page.click('.member-row button[aria-label="Remove"]')
    await waitUntil(() => sent('DELETE /projects/proj-1/members/'), { what: 'the removal to be sent' })
    await page.waitForFunction(() => [...document.querySelectorAll('.card .err')].some((e) => e.textContent.includes('remove')), { timeout: 3000 }).catch(() => {})
    const shown = await page.evaluate(() => ({
      errs: [...document.querySelectorAll('.card .err')].map((e) => e.textContent.trim()),
      rows: document.querySelectorAll('.member-row').length,
    }))
    check(shown.errs.some((e) => e.startsWith('Couldn’t remove')), `a refused removal should be named on the card, got ${JSON.stringify(shown.errs)}`)
    check(shown.rows === 1, `the person the server kept should still be listed, saw ${shown.rows} rows`)
  })()

  h.ok('a refused rename, restore or removal says so')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
