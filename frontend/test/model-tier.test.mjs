// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser test for the model-tier picker on the papers card (031).
//
// Every session this product ran before 031 ran on the "best" tier, because
// nothing offered a choice. What needs testing is not that a control renders
// but that the tier a person picks is the tier the request carries, and that
// the prices beside each tier are the server's rather than numbers the client
// computed. Neither is visible to a test that only reads the DOM, so this one
// drives the picker and inspects the request that left the browser.
import { fail, settled, start, waitUntil } from './harness.mjs'

const at = (i) => new Date(1754_215_200_000 - i * 86_400_000).toISOString()
const PROJECT = 'proj-1'
const project = { id: PROJECT, owner: 'p', title: 'Point of no Undo', visibility: 'private', created_at: at(0) }
const paper = { key: 'doi:x', ref: '10.1145/3654777.3676417', title: 'Point of no Undo', sessions: 0 }
const credits = { balance: 100_000_000, available: 100_000_000, start_reserve: 20_000_000 }

// The card the server would serve. Micro-USD per million tokens, which is the
// unit the platform's rate card is kept in.
const tiers = [
  { tier: 'best', model: 'claude-opus-5', input: 5_000_000, output: 25_000_000, named: true },
  { tier: 'balanced', model: 'claude-sonnet-5', input: 3_000_000, output: 15_000_000, named: true },
  { tier: 'fast', model: 'claude-haiku-4-5-20251001', input: 1_000_000, output: 5_000_000, named: true },
]

// What the start request actually carried.
let started = null

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'e@x', tier: 'user', credits: { wallet: 100_000_000, start_reserve: 20_000_000 } }],
  ['/me/profile', {}],
  ['/models', { tiers }],
  [
    `POST /projects/${PROJECT}/sessions`,
    ({ body }) => {
      started = body
      return { job_id: 'j-new', project_id: PROJECT }
    },
  ],
  [/^\/projects\/[^/]+$/, { project, sessions: [], papers: [paper], may_submit: true, role: 'owner', credits }],
  [/^\/projects\/[^/]+\/papers$/, [paper]],
  [/^\/projects\/[^/]+\/members$/, []],
  [/^\/(projects|public|shared)$/, []],
]

const h = await start()
try {
  const page = await h.page({ routes })
  await page.open(`/p/${PROJECT}`)
  await page.waitForSelector('.tier-pick', { timeout: 15000 })
  await settled(page)

  /* ── The picker offers every tier the server named, at the server's prices ── */

  await page.click('.tier-pick .sel-btn')
  await page.waitForSelector('.sel-list', { timeout: 5000 })
  const options = await page.evaluate(() => [...document.querySelectorAll('.sel-list .sel-opt')].map((o) => o.textContent.replace(/\s+/g, ' ').trim()))

  if (options.length !== 3) fail(`the picker offers ${options.length} tiers, want the 3 the server listed`)
  for (const [i, want] of [
    ['Best', '$5.00', '$25.00'],
    ['Balanced', '$3.00', '$15.00'],
    ['Fast', '$1.00', '$5.00'],
  ].entries()) {
    const got = options[i] ?? ''
    for (const part of want) {
      if (!got.includes(part)) fail(`tier ${i} reads "${got}"; it must name ${part} — the prices are the server's, not the client's`)
    }
  }

  /* ── The tier picked is the tier the request carries ── */

  await page.evaluate(() => [...document.querySelectorAll('.sel-list .sel-opt')].find((o) => /Fast/.test(o.textContent))?.click())
  // A pick closes the list in the same render that commits the choice.
  await page.waitForFunction(() => !document.querySelector('.sel-list'))
  await page.evaluate(() => [...document.querySelectorAll('.paper-group .btn')].find((b) => /Start replication/i.test(b.textContent))?.click())
  await waitUntil(() => started, { what: 'the start request to be sent' }).catch(() => {})

  if (!started) fail('pressing Start sent no request')
  else {
    if (started.tier !== 'fast') fail(`Start sent tier ${JSON.stringify(started.tier)}, want "fast" — the choice did not reach the request`)
    if (started.paper_key !== paper.key) fail(`Start sent paper_key ${JSON.stringify(started.paper_key)}, want ${JSON.stringify(paper.key)}`)
  }

  /* ── The control is inside its card ── */

  const clipped = await page.evaluate(() => {
    const pick = document.querySelector('.tier-pick')
    const card = pick.closest('.card')
    return pick.getBoundingClientRect().right > card.getBoundingClientRect().right + 0.5
  })
  if (clipped) fail('the tier picker is clipped at the card edge')

  h.ok('the tier picker prices every tier from the server and sends the one chosen')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
