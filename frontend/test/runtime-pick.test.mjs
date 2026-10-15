// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser test for the runtime and model picker on the papers card (031,
// 048).
//
// What needs testing is that the choice a person makes is the one the start
// request carries, that the levels' models and prices are the server's, and
// that a run the person pays on the Latere platform is not held back by the
// project's credits. None of that is visible to a test that only reads the
// DOM, so this one drives the picker and inspects the request that left the
// browser.
import { fail, settled, start, waitUntil } from './harness.mjs'

const at = (i) => new Date(1754_215_200_000 - i * 86_400_000).toISOString()
const PROJECT = 'proj-1'
const project = { id: PROJECT, owner: 'p', title: 'Point of no Undo', visibility: 'private', created_at: at(0) }
const paper = { key: 'doi:x', ref: '10.1145/3654777.3676417', title: 'Point of no Undo', sessions: 0 }
// The project holds nothing: a project-paid run cannot start, a person-paid one can.
const credits = { balance: 0, available: 0, start_reserve: 20_000_000 }

// What the server would answer. Micro-USD per million tokens.
const runtimes = [
  {
    name: 'anthropic-managed', label: 'Anthropic', default: true, payer: 'project',
    efforts: [
      { tier: 'best', effort: 'sustained', model: 'claude-opus-5', input: 5_000_000, output: 25_000_000 },
      { tier: 'balanced', effort: 'thorough', model: 'claude-sonnet-5', input: 3_000_000, output: 15_000_000 },
      { tier: 'fast', effort: 'quick', model: 'claude-haiku-4-5', input: 1_000_000, output: 5_000_000 },
    ],
  },
  {
    name: 'latere', label: 'Latere', default: false, payer: 'person',
    efforts: [
      { tier: 'best', effort: 'sustained', model: 'claude-opus-5.5', input: 4_000_000, output: 20_000_000 },
      { tier: 'balanced', effort: 'thorough', model: 'claude-sonnet-5.5', input: 2_000_000, output: 10_000_000 },
      { tier: 'fast', effort: 'quick', model: 'gemma-4-26b-a4b-it' },
    ],
    models: ['anthropic/claude-sonnet-4.6', 'openai/gpt-6.1-sol', 'qwen/qwen3-coder-480b'],
  },
]

let started = null

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'e@x', tier: 'user', credits: { wallet: 0, start_reserve: 20_000_000 } }],
  ['/me/profile', {}],
  ['/models', { tiers: [], runtimes }],
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

const startButton = (page) =>
  page.evaluate(() => {
    const b = [...document.querySelectorAll('.paper-group .btn')].find((x) => /Start replication/i.test(x.textContent))
    return b ? { disabled: b.disabled } : null
  })

const h = await start()
try {
  const page = await h.page({ routes })
  await page.open(`/p/${PROJECT}`)
  await page.waitForSelector('.rp-btn', { timeout: 15000 })
  await settled(page)

  /* ── The default is the deployment's runtime, at the default level ── */

  const summary = await page.evaluate(() => document.querySelector('.rp-btn').textContent.trim())
  if (summary !== 'Anthropic · Sustained') fail(`the picker reads "${summary}", want "Anthropic · Sustained"`)
  if (!(await startButton(page))?.disabled) fail('a project-paid run started with no project credits')

  /* ── The panel names each runtime, and each level's model and price ── */

  await page.click('.rp-btn')
  await page.waitForSelector('.rp-pop', { timeout: 5000 })
  const seg = await page.evaluate(() => [...document.querySelectorAll('.rp-seg-opt')].map((b) => b.textContent.trim()))
  if (seg.join(',') !== 'Anthropic,Latere') fail(`the runtimes offered are ${seg}, want Anthropic and Latere`)
  await page.evaluate(() => [...document.querySelectorAll('.rp-seg-opt')].find((b) => b.textContent.trim() === 'Latere').click())
  await page.waitForFunction(() => document.querySelector('.rp-seg-opt.on')?.textContent.trim() === 'Latere')
  const levels = await page.evaluate(() => [...document.querySelectorAll('.rp-level')].map((b) => b.textContent.replace(/\s+/g, ' ').trim()))
  for (const [i, want] of [
    ['Quick', 'gemma-4-26b-a4b-it'],
    ['Thorough', 'claude-sonnet-5.5', '$2.00', '$10.00'],
    ['Sustained', 'claude-opus-5.5', '$4.00', '$20.00'],
  ].entries()) {
    for (const part of want) {
      if (!(levels[i] ?? '').includes(part)) fail(`level ${i} reads "${levels[i]}"; it must name ${part}, the server's`)
    }
  }
  const note = await page.evaluate(() => document.querySelector('.rp-note').textContent)
  if (!/your own Latere wallet/.test(note)) fail(`the panel says "${note}"; a Latere run must say the person's own wallet pays`)

  /* ── A specific model, found by search, is the one Start sends ── */

  await page.type('.rp-search', 'gpt')
  await page.waitForFunction(() => document.querySelectorAll('.rp-model').length === 1)
  await page.evaluate(() => document.querySelector('.rp-model').click())
  await page.waitForFunction(() => !document.querySelector('.rp-pop'))
  const chosen = await page.evaluate(() => document.querySelector('.rp-btn').textContent.trim())
  if (chosen !== 'Latere · openai/gpt-6.1-sol') fail(`after the pick the picker reads "${chosen}"`)

  const button = await startButton(page)
  if (!button || button.disabled) fail('a run the person pays for was held back by the project\'s credits')
  await page.evaluate(() => [...document.querySelectorAll('.paper-group .btn')].find((b) => /Start replication/i.test(b.textContent))?.click())
  await waitUntil(() => started, { what: 'the start request to be sent' }).catch(() => {})
  if (!started) fail('pressing Start sent no request')
  else {
    if (started.runtime !== 'latere') fail(`Start sent runtime ${JSON.stringify(started.runtime)}, want "latere"`)
    if (started.model !== 'openai/gpt-6.1-sol') fail(`Start sent model ${JSON.stringify(started.model)}`)
    if (started.paper_key !== paper.key) fail(`Start sent paper_key ${JSON.stringify(started.paper_key)}`)
  }

  /* ── Escape closes the panel and gives focus back to the trigger ── */

  await page.click('.rp-btn')
  await page.waitForSelector('.rp-pop')
  await page.keyboard.press('Escape')
  await page.waitForFunction(() => !document.querySelector('.rp-pop'))
  const focused = await page.evaluate(() => document.activeElement?.classList.contains('rp-btn'))
  if (!focused) fail('Escape did not return focus to the picker')

  /* ── The control is inside its card ── */

  const clipped = await page.evaluate(() => {
    const pick = document.querySelector('.tier-pick')
    const card = pick.closest('.card')
    return pick.getBoundingClientRect().right > card.getBoundingClientRect().right + 0.5
  })
  if (clipped) fail('the picker is clipped at the card edge')

  h.ok('the picker offers each runtime and level at the server\'s prices, and Start sends the runtime and model chosen')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
