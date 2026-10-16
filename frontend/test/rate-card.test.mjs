// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser test for the price list in the Admin console's platform policy.
//
// It guards that an admin can add, edit and remove a model's prices, since a
// model id with no row is charged the card's dearest rates, and that the
// policy's Save writes the whole card through PUT /admin/settings.
//
// What needs a browser is the round trip: the fields the admin types become the
// card the request carries, in micro-USD per million tokens, with a renamed
// row's ref renamed with it and every field the console does not edit carried
// through untouched; a card that cannot be priced from is never sent; and the
// table fits a phone.
//
// When the server follows a price list, the rows for the models the list
// prices are the list's: the server rewrites them every hour, so the console
// shows them as text under the list's name, says when the list was last
// applied, and keeps the admin's other rows editable. Run it with
// `bun run test:browser`.
import { fail, settled, start, waitUntil } from './harness.mjs'

const EMAIL = 'admin@example.com'
const M = 1_000_000

// What the server holds: three rows, the first with a provider-qualified ref
// and a meter the console does not edit, which must survive a save.
const card = {
  models: [
    {
      model: 'claude-opus-5',
      model_ref: { provider: 'anthropic', id: 'claude-opus-5' },
      input: 5 * M,
      output: 25 * M,
      cache_read: M / 2,
      cache_write_5m: 6.25 * M,
      cache_write_1h: 10 * M,
      extra: [{ meter: 'container_second', rate: 50 }],
    },
    { model: 'claude-sonnet-5', input: 3 * M, output: 15 * M, cache_read: 300_000, cache_write_5m: 3.75 * M, cache_write_1h: 6 * M },
    { model: 'claude-haiku-4-5', input: M, output: 5 * M, cache_read: 100_000, cache_write_5m: 1.25 * M, cache_write_1h: 2 * M },
  ],
}
let saved = null

const routes = [
  ['/me', { authenticated: true, principal: 'admin', email: EMAIL, tier: 'admin', credits: { wallet: 0, start_reserve: 5 * M } }],
  ['GET /admin/access', [{ email: EMAIL, tier: 'admin', bound: true, wallet: 0 }]],
  ['GET /admin/settings', { start_reserve: 5 * M, overdraft_cap: 20 * M, rates: card }],
  [
    '/admin/settings',
    ({ body }) => {
      saved = body
      return { ...body, updated_by: EMAIL }
    },
  ],
]

const h = await start()
try {
  const page = await h.page({ routes })
  await page.open('/console')
  await page.waitForSelector('[data-test="rate-row"]', { timeout: 15000 })
  await settled(page)

  const rows = () =>
    page.evaluate(() =>
      [...document.querySelectorAll('[data-test="rate-row"]')].map((r) =>
        Object.fromEntries([...r.querySelectorAll('input[data-field]')].map((i) => [i.dataset.field, i.value])),
      ),
    )
  // The nth row of the card, counted from one; the column heads are not a row.
  const row = async (n) => (await page.$$('[data-test="rate-row"]'))[n - 1]
  const type = async (n, name, value) => {
    const input = await (await row(n)).$(`input[data-field="${name}"]`)
    // Select the whole value, then clear it with a key a person would press:
    // a triple click selects only a word in a value with hyphens.
    await input.focus()
    await input.evaluate((el) => el.select())
    await page.keyboard.press('Backspace')
    if (value !== '') await input.type(value)
  }
  const canSave = () => page.$eval('[data-test="save-policy"]', (b) => !b.disabled)
  const error = () => page.evaluate(() => document.querySelector('[data-test="rate-error"]')?.textContent.trim() ?? '')

  /* ── The card is a form, filled with what the server holds ── */

  const first = await rows()
  if (first.length !== 3) fail(`the card shows ${first.length} rows, want 3`)
  if (first[0]?.model !== 'claude-opus-5' || first[0]?.input !== '5' || first[0]?.cache_read !== '0.5') {
    fail(`the first row reads ${JSON.stringify(first[0])}; the fields are the server's prices in dollars`)
  }
  if (await canSave()) fail('Save is enabled before anything changed')

  /* ── Add a row: it starts empty, and an empty row cannot be saved ── */

  await page.click('[data-test="rate-add"]')
  const added = await rows()
  if (added.length !== 4) fail(`adding a model left ${added.length} rows, want 4`)
  // No price is chosen for the admin: a new row is blank until they type one.
  if (Object.values(added[3] ?? {}).some((v) => v !== '')) fail(`a new row arrives filled in: ${JSON.stringify(added[3])}`)
  const focused = await page.evaluate(() => document.activeElement?.dataset.field)
  if (focused !== 'model') fail(`adding a model put the cursor in ${focused}, want the new row's model id`)
  if (await canSave()) fail('a card with a blank row can be saved')
  if (!(await error())) fail('a blank row says nothing about why the card cannot be saved')

  await type(4, 'model', 'claude-opus-5-5')
  await type(4, 'input', '5')
  await type(4, 'output', '25')
  await type(4, 'cache_read', '0.5')
  await type(4, 'cache_write_5m', '6.25')
  if (await canSave()) fail('a row missing a price can be saved')
  await type(4, 'cache_write_1h', '10')
  if (!(await canSave())) fail(`a complete new row still cannot be saved: ${await error()}`)

  /* ── A card that cannot be priced from is never sent ── */

  await type(2, 'model', 'claude-opus-5-5')
  if (await canSave()) fail('a card naming one model twice can be saved')
  if (!/twice/i.test(await error())) fail(`a model listed twice says "${await error()}"`)
  await type(2, 'model', 'claude-sonnet-5')
  await type(2, 'output', '-1')
  if (await canSave()) fail('a negative price can be saved')
  await type(2, 'output', '16')

  /* ── Edit, rename and remove ── */

  // A rename carries the row's ref with it: the ref is what pricing matches.
  await type(1, 'model', 'claude-opus-5-legacy')
  await (await (await row(3)).$('[data-test="rate-remove"]')).click()
  const after = await rows()
  if (after.map((r) => r.model).join() !== 'claude-opus-5-legacy,claude-sonnet-5,claude-opus-5-5') {
    fail(`after the edits the card lists ${after.map((r) => r.model).join(', ')}`)
  }
  if (!(await canSave())) fail(`the edited card cannot be saved: ${await error()}`)
  await page.click('[data-test="save-policy"]')
  await page.waitForFunction(() => document.querySelector('[data-test="save-policy"]')?.disabled === true, { timeout: 5000 }).catch(() => {})
  if (!saved) fail('Save sent nothing')
  else {
    const got = saved.rates?.models ?? []
    const want = [
      {
        model: 'claude-opus-5-legacy',
        model_ref: { provider: 'anthropic', id: 'claude-opus-5-legacy' },
        input: 5 * M,
        output: 25 * M,
        cache_read: M / 2,
        cache_write_5m: 6.25 * M,
        cache_write_1h: 10 * M,
        extra: [{ meter: 'container_second', rate: 50 }],
      },
      { model: 'claude-sonnet-5', input: 3 * M, output: 16 * M, cache_read: 300_000, cache_write_5m: 3.75 * M, cache_write_1h: 6 * M },
      { model: 'claude-opus-5-5', input: 5 * M, output: 25 * M, cache_read: M / 2, cache_write_5m: 6.25 * M, cache_write_1h: 10 * M },
    ]
    if (JSON.stringify(got) !== JSON.stringify(want)) fail(`Save sent the card ${JSON.stringify(got)}, want ${JSON.stringify(want)}`)
    if (saved.start_reserve !== 5 * M) fail(`Save changed the start reserve to ${saved.start_reserve}`)
  }
  await page.close()

  /* ── A card left alone never blocks the rest of the policy ── */

  // The server may hold a row this form would refuse to save, here one priced
  // at zero. An admin who only edits the reserve must still be able to save,
  // and the card goes back exactly as the server gave it.
  {
    const odd = { models: [...card.models, { model: 'claude-zero', input: 0, output: 0, cache_read: 0, cache_write_5m: 0, cache_write_1h: 0 }] }
    saved = null
    const p = await h.page({ routes: [['GET /admin/settings', { start_reserve: 5 * M, overdraft_cap: 20 * M, rates: odd }], ...routes] })
    await p.open('/console')
    await p.waitForSelector('[data-test="rate-row"]', { timeout: 15000 })
    await settled(p)
    if (await p.evaluate(() => document.querySelector('[data-test="rate-error"]')?.textContent.trim())) {
      fail('a card nobody edited shows a problem')
    }
    await p.evaluate(() => {
      const input = document.querySelector('#start-reserve')
      Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set.call(input, '7')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await p.evaluate(() => document.querySelector('[data-test="save-policy"]')?.click())
    await waitUntil(() => saved, { what: 'the policy save to be sent' }).catch(() => {})
    if (!saved) fail('a card nobody edited blocked saving the start reserve')
    else if (JSON.stringify(saved.rates) !== JSON.stringify(odd)) fail(`a card nobody edited went back changed: ${JSON.stringify(saved.rates)}`)
    await p.close()
  }

  /* ── The rows a price list sets are the list's; the rest stay the admin's ── */

  // The server writes the list's prices into the rows for the models it lists
  // every hour, so a field there would take an edit the next sync undoes. Those
  // rows are text under the list's name and cannot be removed; the admin's own
  // rows are fields, and Save still sends the card in the server's
  // order with the list's rows exactly as the server gave them.
  {
    const appliedAt = new Date(Date.now() - 12 * 60_000).toISOString()
    const priceList = {
      url: 'https://openrouter.ai/api/v1/models',
      applied_at: appliedAt,
      models: [
        { provider: 'anthropic', id: 'claude-opus-5' },
        { provider: 'anthropic', id: 'claude-haiku-4-5' },
      ],
    }
    const listedRoutes = [['GET /admin/settings', { start_reserve: 5 * M, overdraft_cap: 20 * M, rates: card, price_list: priceList }], ...routes]
    saved = null
    const p = await h.page({ routes: listedRoutes })
    await p.open('/console')
    await p.waitForSelector('[data-test="rate-row"]', { timeout: 15000 })
    await settled(p)
    const shown = await p.evaluate(() =>
      [...document.querySelectorAll('[data-test="rate-row"]')].map((r) => ({
        source: r.dataset.source,
        model: r.querySelector('input[data-field="model"]')?.value ?? r.querySelector('.rate-model .rate-value')?.textContent.trim(),
        fields: r.querySelectorAll('input').length,
        remove: !!r.querySelector('[data-test="rate-remove"]'),
      })),
    )
    const want = [
      { source: 'list', model: 'claude-opus-5', fields: 0, remove: false },
      { source: 'list', model: 'claude-haiku-4-5', fields: 0, remove: false },
      { source: 'admin', model: 'claude-sonnet-5', fields: 6, remove: true },
    ]
    if (JSON.stringify(shown) !== JSON.stringify(want)) fail(`with a price list the card shows ${JSON.stringify(shown)}, want ${JSON.stringify(want)}`)
    const listedPrices = await p.evaluate(() => [...document.querySelectorAll('[data-source="list"]')[0].querySelectorAll('.rate-cell:not(.rate-model) .rate-value')].map((v) => v.textContent.trim()))
    if (listedPrices.join() !== '5,25,0.5,6.25,10') fail(`the list's row for claude-opus-5 reads ${listedPrices.join(', ')}, want its prices in dollars`)
    const group = await p.evaluate(() => document.querySelector('[data-test="rate-group-list"]')?.textContent ?? '')
    if (!/OpenRouter/.test(group)) fail(`the list's rows are not marked as OpenRouter's: "${group}"`)
    if (!/12 minutes ago/.test(group)) fail(`the list's rows do not say when the list was applied: "${group}"`)
    if (!(await p.$('[data-test="rate-group-own"]'))) fail("the admin's own rows have no heading of their own")

    // Edit the admin's row; the list's rows go back untouched, in the server's order.
    const sonnet = await p.$('[data-source="admin"] input[data-field="output"]')
    await sonnet.focus()
    await sonnet.evaluate((el) => el.select())
    await p.keyboard.press('Backspace')
    await sonnet.type('16')
    await p.click('[data-test="save-policy"]')
    await waitUntil(() => saved, { what: 'the policy save to be sent' }).catch(() => {})
    if (!saved) fail('Save sent nothing with a price list')
    else {
      const sent = saved.rates?.models ?? []
      const expected = [card.models[0], { ...card.models[1], output: 16 * M }, card.models[2]]
      if (JSON.stringify(sent) !== JSON.stringify(expected)) fail(`with a price list Save sent ${JSON.stringify(sent)}, want ${JSON.stringify(expected)}`)
    }
    await p.close()

    // On a phone the list's rows restack too, and nothing spills.
    const phone = await h.page({ viewport: { width: 390, height: 844, isMobile: true, hasTouch: true }, routes: listedRoutes })
    await phone.open('/console')
    await phone.waitForSelector('[data-source="list"]', { timeout: 15000 })
    await settled(phone)
    const spill = await phone.evaluate(() => {
      const card = document.querySelector('.rate-card')
      const out = [...card.querySelectorAll('.rate-value, input')].filter((el) => el.getBoundingClientRect().right > window.innerWidth + 1).length
      return { doc: document.documentElement.scrollWidth - window.innerWidth, card: card.scrollWidth - card.clientWidth, out }
    })
    if (spill.doc > 1 || spill.card > 1 || spill.out) fail(`on a phone the card with a price list spills: ${JSON.stringify(spill)}`)
    await phone.close()

    // Before the server has read the list, every row is the admin's, and the
    // console says the list has not been read yet.
    const pending = await h.page({ routes: [['GET /admin/settings', { start_reserve: 5 * M, overdraft_cap: 20 * M, rates: card, price_list: { url: priceList.url } }], ...routes] })
    await pending.open('/console')
    await pending.waitForSelector('[data-test="rate-row"]', { timeout: 15000 })
    await settled(pending)
    const sources = await pending.evaluate(() => [...document.querySelectorAll('[data-test="rate-row"]')].map((r) => r.dataset.source).join())
    if (sources !== 'admin,admin,admin') fail(`before the list is read the rows are ${sources}, want every one the admin's`)
    const note = await pending.evaluate(() => document.querySelector('[data-test="rate-list-pending"]')?.textContent ?? '')
    if (!/OpenRouter.*not been read/.test(note)) fail(`before the list is read the console says "${note}"`)
    await pending.close()
  }

  /* ── The card fits a phone ── */

  const phone = await h.page({ viewport: { width: 390, height: 844, isMobile: true, hasTouch: true }, routes })
  await phone.open('/console')
  await phone.waitForSelector('[data-test="rate-row"]', { timeout: 15000 })
  await settled(phone)
  const spill = await phone.evaluate(() => {
    const card = document.querySelector('.rate-card')
    const inputs = [...card.querySelectorAll('input')].filter((i) => i.getBoundingClientRect().right > window.innerWidth + 1).length
    return { doc: document.documentElement.scrollWidth - window.innerWidth, card: card.scrollWidth - card.clientWidth, inputs }
  })
  if (spill.doc > 1 || spill.card > 1 || spill.inputs) fail(`on a phone the card spills: ${JSON.stringify(spill)}`)
  await phone.close()

  h.ok("an admin adds, edits and removes a model on the card, the price list's rows are its own, and Save sends a card that can be priced from")
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
