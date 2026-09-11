// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Browser regression for an admin adding credits to their own wallet.
//
// The admin people table and the header are fed by different endpoints. The
// table refresh alone used to leave the header's /me snapshot stale until a
// reload, so this test requires both surfaces to show the completed top-up.
import { fail, start, waitUntil } from './harness.mjs'

const EMAIL = 'admin@example.com'
let wallet = 20_000_000
// What the policy card actually wrote, so the test can check the request
// rather than the button's label.
let savedPolicy = null

const routes = [
  ['/me', () => ({ authenticated: true, principal: 'admin', email: EMAIL, tier: 'admin', credits: { wallet, start_reserve: 5_000_000 } })],
  ['GET /admin/access', () => [{ email: EMAIL, tier: 'admin', bound: true, wallet }]],
  [
    `POST /admin/access/${encodeURIComponent(EMAIL)}/credits`,
    ({ body }) => {
      wallet += body.amount
      return { email: EMAIL, wallet }
    },
  ],
  ['GET /admin/settings', { start_reserve: 5_000_000, overdraft_cap: 20_000_000, rates: { models: [] } }],
  // A PUT or a POST: whichever the card sends, its body is what it wrote.
  [
    '/admin/settings',
    ({ body }) => {
      savedPolicy = body
      return { ...savedPolicy, updated_by: EMAIL }
    },
  ],
]

const h = await start()
try {
  const page = await h.page({ routes })
  await page.open('/console')
  await page.waitForSelector('.member-row')
  const before = await page.$eval('.quota-pill', (el) => el.textContent.trim())
  if (before !== '$20.00') fail(`header started at ${JSON.stringify(before)}, want "$20.00"`)

  await page.evaluate(() => [...document.querySelectorAll('.member-row .btn')].find((button) => /add credits/i.test(button.textContent))?.click())
  await page.waitForSelector('.modal[role="dialog"]')
  await page.evaluate(() => [...document.querySelectorAll('.modal-foot .btn')].find((button) => /^save$/i.test(button.textContent.trim()))?.click())

  // First prove the top-up finished and the admin table received its refresh.
  await page.waitForFunction(() => [...document.querySelectorAll('.member-row')].some((row) => row.textContent.includes('$30.00')))
  // The same completed action must refresh the header without a reload.
  await page.waitForFunction(() => document.querySelector('.quota-pill')?.textContent.trim() === '$30.00')

  // The policy card's Save writes the whole card, not the field its button sits
  // beside. One request writes both numbers, so a Save placed next to the
  // overdraft floor suggested to an admin editing the start reserve that their
  // change was not covered. Position is the claim here, so it is asserted as
  // geometry: below both fields, and the last control in the card.
  const policy = await page.evaluate(() => {
    const card = document.querySelector('[data-test="save-policy"]')?.closest('.card')
    if (!card) return null
    const btn = card.querySelector('[data-test="save-policy"]')
    const rows = [...card.querySelectorAll('.policy-row')]
    const bottom = (el) => el.getBoundingClientRect().bottom
    return {
      fields: rows.length,
      belowEveryField: rows.every((r) => btn.getBoundingClientRect().top >= bottom(r) - 1),
      // Nothing may sit inside a policy row with it: that is what made it read
      // as one field's control.
      insideAField: rows.some((r) => r.contains(btn)),
      last: card.querySelectorAll('button, a').length > 0 && [...card.querySelectorAll('button')].pop() === btn,
    }
  })
  if (!policy) fail('the platform policy card has no save control')
  else {
    // Three since 039: the reserve, the floor, and which model drafts. The
    // count is asserted so that the "Save is below every field" check below has
    // fields to compare against; a card that lost its fields would pass it
    // vacuously.
    if (policy.fields !== 3) fail(`the policy card shows ${policy.fields} fields, want the reserve, the floor and the drafting model`)
    if (policy.insideAField) fail('Save sits inside a field row, so it reads as that one field’s control')
    if (!policy.belowEveryField) fail('Save is not below every field it writes')
    if (!policy.last) fail('Save is not the card’s final control')
  }

  // And it writes both numbers: editing only the reserve must still send the
  // floor, or the request does not match the "save the card" claim.
  await page.evaluate(() => {
    const input = document.querySelector('#start-reserve')
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set
    setter.call(input, '7')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await page.evaluate(() => document.querySelector('[data-test="save-policy"]')?.click())
  await waitUntil(() => savedPolicy, { what: 'the policy save to be sent' }).catch(() => {})
  if (!savedPolicy) fail('editing the start reserve and pressing Save sent nothing')
  else {
    if (savedPolicy.start_reserve !== 7_000_000) fail(`Save sent start_reserve ${savedPolicy.start_reserve}, want the edited 7_000_000`)
    if (savedPolicy.overdraft_cap !== 20_000_000) fail(`Save dropped the overdraft floor (sent ${savedPolicy.overdraft_cap})`)
  }

  h.ok('an admin self-top-up refreshes the header balance, and the policy card saves the card')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
