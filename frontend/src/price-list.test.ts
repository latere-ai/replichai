// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'bun:test'
import { followsList, listName, listedKeys } from './price-list'

const list = {
  url: 'https://openrouter.ai/api/v1/models',
  applied_at: '2026-10-16T12:00:00Z',
  models: [
    { provider: 'anthropic', id: 'claude-opus-5-5' },
    { provider: 'anthropic', id: 'claude-sonnet-5' },
  ],
}

// A row is the list's when the server's sync would write into it, which it
// decides by the row's provider-qualified ref, exactly. The console must agree
// with it: a row it shows as the admin's would lose their edit within the hour,
// and one it shows as the list's could never be edited at all.
describe('followsList', () => {
  const listed = listedKeys(list)

  it('marks a row whose ref the list priced', () => {
    expect(followsList({ model: 'claude-opus-5-5', model_ref: { provider: 'anthropic', id: 'claude-opus-5-5' } }, listed)).toBe(true)
  })

  it('reads a row written before refs as an Anthropic model of its name', () => {
    expect(followsList({ model: ' claude-sonnet-5 ' }, listed)).toBe(true)
  })

  it('leaves a model the list does not price to the admin', () => {
    expect(followsList({ model: 'claude-opus-4-1', model_ref: { provider: 'anthropic', id: 'claude-opus-4-1' } }, listed)).toBe(false)
  })

  // The sync matches a whole ref. A dated snapshot is priced from its base
  // model's row but keeps its own row, and another provider's model of the same
  // name, or a billing variant, is another row.
  it('matches the whole ref and nothing near it', () => {
    expect(followsList({ model: 'claude-opus-5-5-20260921' }, listed)).toBe(false)
    expect(followsList({ model: 'claude-opus-5-5', model_ref: { provider: 'bedrock', id: 'claude-opus-5-5' } }, listed)).toBe(false)
    expect(followsList({ model: 'claude-opus-5-5', model_ref: { provider: 'anthropic', id: 'claude-opus-5-5', billing_variant: 'batch' } }, listed)).toBe(false)
  })

  it('leaves a row the admin just added, which has no model yet, to the admin', () => {
    expect(followsList({}, listed)).toBe(false)
    expect(followsList({ model: '' }, listed)).toBe(false)
  })

  // Before the server has read the list there is nothing to match, and with
  // the list off there is no list: every row is the admin's.
  it('marks nothing when the list has not been applied or is off', () => {
    expect(followsList({ model: 'claude-opus-5-5' }, listedKeys({ url: list.url }))).toBe(false)
    expect(followsList({ model: 'claude-opus-5-5' }, listedKeys(undefined))).toBe(false)
  })
})

describe('listName', () => {
  it('calls OpenRouter by its name and any other list by its host', () => {
    expect(listName('https://openrouter.ai/api/v1/models')).toBe('OpenRouter')
    expect(listName('https://prices.example.com/models.json')).toBe('prices.example.com')
  })

  it('shows an address with no host as the server gave it', () => {
    expect(listName('prices.json')).toBe('prices.json')
    expect(listName('')).toBe('')
    expect(listName(undefined)).toBe('')
  })
})
