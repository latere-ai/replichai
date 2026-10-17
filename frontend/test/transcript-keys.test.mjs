// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for what a transcript card remembers when the
// filters move under it.
//
// It guards that a card is keyed by the event it shows, not by its position in
// the filtered list. A tool result's disclosure holds its own open/closed
// state, so with positional keys a search that removed the card above it would
// hand the open state to whichever event moved into that position. Run it with `bun run test:browser`.
import { check, fail, start } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()
const project = { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }
const job = { id: 'job-1', kind: 'replicate', ref: 'arxiv:2301.03711', phase: 'done', created_at: at(0), phase_at: { submitted: at(0), running: at(1000), done: at(9000) } }

// A message first, then two tool results. The search below keeps both results
// and drops the message, so every result moves up one position.
const events = [
  { role: 'user', kind: 'message', content: 'Reproduce this paper.', time: at(1000) },
  { role: 'assistant', kind: 'tool_result', content: 'alpha result\nfirst line of alpha', time: at(2000) },
  { role: 'assistant', kind: 'tool_result', content: 'beta result\nfirst line of beta', time: at(3000) },
]

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', {}],
  [/^\/(projects|public|shared)$/, []],
  ['/projects/proj-1', { project, sessions: [job], may_submit: true, role: 'owner' }],
  ['/jobs/job-1', job],
  ['/jobs/job-1/transcript', { events, usage: null, live: false }],
]

const h = await start()
try {
  const page = await h.page({ routes })
  await page.open('/p/proj-1/s/job-1')
  await page.waitForSelector('.tx-stream .event')
  await page.waitForFunction(() => document.querySelectorAll('.tx-stream .event').length === 3)

  // The state of each result's disclosure, by the result it belongs to.
  const disclosures = () =>
    page.evaluate(() =>
      Object.fromEntries(
        [...document.querySelectorAll('.tx-stream .event')]
          .filter((c) => c.querySelector('.disc'))
          .map((c) => [c.textContent.includes('alpha') ? 'alpha' : c.textContent.includes('beta') ? 'beta' : '?', c.querySelector('details.disc').open]),
      ),
    )

  // Open alpha's disclosure and nothing else.
  await page.evaluate(() => [...document.querySelectorAll('.tx-stream .event')].find((c) => c.textContent.includes('alpha')).querySelector('.disc-head').click())
  await page.waitForFunction(() => [...document.querySelectorAll('.tx-stream details.disc')].some((d) => d.open))
  const before = await disclosures()
  if (!check(before.alpha === true && before.beta === false, `the test needs alpha open and beta shut to start, got ${JSON.stringify(before)}`)) throw new Error('setup')

  // Drop the message above them. Both results stay, one position up each.
  await page.type('.tx-search', 'result')
  await page.waitForFunction(() => document.querySelectorAll('.tx-stream .event').length === 2)
  const after = await disclosures()
  check(after.alpha === true, `alpha's disclosure was open before the filter and is ${after.alpha ? 'open' : 'shut'} after it`)
  check(after.beta === false, `beta's disclosure was shut before the filter and is ${after.beta ? 'open' : 'shut'} after it — the open state moved to a different event`)

  h.ok('a transcript card keeps its own state when the filters move')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
