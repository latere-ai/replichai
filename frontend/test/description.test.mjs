// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for a project's description and the draft that
// offers to write one.
//
// A title is either what somebody typed or the first paper's reference, so it
// rarely explains a project's purpose to its owner returning after a month, to
// a collaborator arriving, or to a stranger reading the public zone. The
// description does.
//
// Six things have to hold:
//
//   1. An owner whose project has no description is invited to write one; a
//      reader of the same project is shown nothing at all. An empty description
//      is a state, not a placeholder a reader has to skip past.
//   2. Writing one calls PATCH /projects/{id} with the description alone, and
//      the head re-reads without a reload.
//   3. It is edited in place, on the screen it is read on, not on a settings
//      page.
//   4. The list shows it under the title, on one line.
//   5. A draft lands in the editor and nowhere else: asking for one stores
//      nothing, and what is finally saved is what the person left in the field.
//   6. A deployment with no drafting model offers no control at all, and its
//      descriptions are still typed.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { fail, start } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const ME = 'p'
const PROJECT = 'proj-1'
const TEXT = 'Two papers on whether a segmentation overlay changes what a driver trusts.'

let description = ''
let patches = []
let drafted = 0
let drafts = true
const DRAFTED = 'A model wrote this sentence about the project.'

const project = () => ({ id: PROJECT, owner: ME, title: 'Trust in automation', description, visibility: 'private', created_at: at(0) })

// serve is the mocked API with `me` as the identity. The project is read afresh
// on every answer, so a description once saved is what the head re-reads.
const serve = (me = ME) => [
  [
    'PATCH',
    /^\/projects\/[^/]+$/,
    ({ body = {} }) => {
      patches.push(body)
      if (body.description !== undefined) description = body.description
      return project()
    },
  ],
  ['/me', { authenticated: true, principal: me, email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', { display_name: 'Ada Lovelace', contact_email: 'ada@example.org', handle: 'ada', public: true }],
  ['/projects', () => [{ ...project(), session_count: 0, running: 0, modified_at: at(0) }]],
  [/^\/projects\/[^/]+$/, () => ({ project: project(), sessions: [], papers: [], may_submit: me === ME, role: me === ME ? 'owner' : 'member' })],
  [/^\/projects\/[^/]+\/papers$/, []],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/projects\/[^/]+\/credits$/, { wallet: 20, start_reserve: 5, available: 20 }],
  ['/models', () => ({ tiers: [], drafting: drafts })],
  [
    'POST',
    /^\/projects\/[^/]+\/description\/draft$/,
    () => {
      drafted++
      return { description: DRAFTED, model: 'small-1', cost_micro: 1200 }
    },
  ],
  [/^\/(public|shared)$/, []],
]

const h = await start()
const open = (me) => h.page({ viewport: { width: 1440, height: 1000 }, routes: serve(me) })
try {
  /* ── 1, 2, 3. The owner writes one, in place ── */

  const page = await open()
  await page.open(`/p/${PROJECT}`)
  await page.waitForSelector('[data-test="describe-add"]', { timeout: 15000 })
  if (await page.$('[data-test="description"]')) fail('a project with no description rendered one')

  await page.click('[data-test="describe-add"]')
  await page.waitForSelector('[data-test="describe-input"]', { timeout: 5000 })
  // In place: the editor is in the project's own head, not on another screen.
  const inHead = await page.$eval('[data-test="describe-editor"]', (e) => !!e.closest('.detail-head'))
  if (!inHead) fail('the description is edited somewhere other than the project head')

  await page.type('[data-test="describe-input"]', TEXT)
  await page.click('[data-test="describe-save"]')
  await page.waitForSelector('[data-test="description"]', { timeout: 10000 })

  if (patches.length !== 1) fail(`saving sent ${patches.length} requests`)
  if (patches[0].description !== TEXT) fail(`the description sent was ${JSON.stringify(patches[0])}`)
  if ('title' in patches[0]) fail('describing a project also sent its title, which it must not touch')

  const shown = await page.$eval('[data-test="description"]', (e) => e.textContent.trim())
  if (!shown.startsWith(TEXT)) fail(`the head shows ${JSON.stringify(shown)}`)

  /* ── 4. On the list, under the title ── */

  await page.open('/')
  await page.waitForSelector('[data-test="row-description"]', { timeout: 10000 })
  const row = await page.$eval('[data-test="row-description"]', (e) => ({
    text: e.textContent.trim(),
    lines: e.getBoundingClientRect().height,
    underTitle: e.previousElementSibling?.className.includes('row-title') ?? false,
  }))
  if (row.text !== TEXT) fail(`the list row shows ${JSON.stringify(row.text)}`)
  if (!row.underTitle) fail('the description is not under the title in the list')
  if (row.lines > 24) fail(`the list row's description is ${row.lines}px tall; it must stay one line`)
  await page.close()

  /* ── 5. A draft is a suggestion, not a save ── */

  {
    const p = await open()
    description = ''
    patches = []
    await p.open(`/p/${PROJECT}`)
    await p.waitForSelector('[data-test="describe-add"]', { timeout: 15000 })
    await p.click('[data-test="describe-add"]')
    await p.waitForSelector('[data-test="describe-draft"]', { timeout: 5000 })

    await p.click('[data-test="describe-draft"]')
    await p.waitForFunction((want) => document.querySelector('[data-test="describe-input"]')?.value === want, { timeout: 10000 }, DRAFTED)
    if (drafted !== 1) fail(`the draft control called the endpoint ${drafted} times`)
    // Into the editor, and nowhere else: nothing is stored until somebody saves.
    if (patches.length !== 0) fail('asking for a draft wrote the description without anybody accepting it')
    if (await p.$('[data-test="description"]')) fail('the draft was rendered as the project’s description before it was saved')

    // Saving is the person's act, and it saves what is in the field.
    await p.click('[data-test="describe-save"]')
    await p.waitForSelector('[data-test="description"]', { timeout: 10000 })
    if (patches.length !== 1 || patches[0].description !== DRAFTED) fail(`saving after a draft sent ${JSON.stringify(patches)}`)
    await p.close()
  }

  /* ── 5, the other half. A deployment that cannot draft offers no control ── */

  {
    drafts = false
    description = ''
    const p = await open()
    // The control appears only once /models has said there is a model to draft
    // with; the measurement is taken after that answer has landed and said no.
    const models = p.waitForResponse((r) => new URL(r.url()).pathname === '/models', { timeout: 15000 })
    await p.open(`/p/${PROJECT}`)
    await p.waitForSelector('[data-test="describe-add"]', { timeout: 15000 })
    await p.click('[data-test="describe-add"]')
    await p.waitForSelector('[data-test="describe-input"]', { timeout: 5000 })
    await models
    if (await p.$('[data-test="describe-draft"]')) fail('a deployment with no drafting model offered a control that can only fail')
    // And the field still works, which is what the fallback is for.
    await p.type('[data-test="describe-input"]', 'Typed by hand.')
    await p.click('[data-test="describe-save"]')
    await p.waitForSelector('[data-test="description"]', { timeout: 10000 })
    await p.close()
    drafts = true
  }

  /* ── 1, the other half. A reader is shown nothing to fill in ── */

  const reader = await open('somebody-else')
  description = ''
  await reader.open(`/p/${PROJECT}`)
  // The head renders once the project, and with it the reader's role, is known.
  await reader.waitForSelector('.detail-head', { timeout: 15000 })
  if (await reader.$('[data-test="describe-add"]')) fail('a reader who cannot edit was invited to write a description')
  if (await reader.$('[data-test="description"]')) fail('an empty description rendered as an empty paragraph')

  h.ok('a project says what it is about, written in place and read in the list')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
