// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { ledgerKindLabel, ledgerLabel } from './ledger'
import { ApiError, type LedgerEntry } from './api'
import { creditMoveErrLabel, emptyWallet, startErrLabel } from './errors'
import { formatUSD, MICRO, parseUSD, toDollars } from './money'

// The identity function for the label helpers: they take a translate function,
// and asserting the key is what matters here, not the English copy.
const t = ((key: string) => key) as never

// Most people's first encounter with credits is at zero. That is a starting
// state, not a fault — and its remedy is different from a funded project that
// has run down, so the two must not share a message.
test('emptyWallet separates a new account from a project that ran out', () => {
  expect(emptyWallet({ wallet: 0, start_reserve: 10 * MICRO })).toBe(true)
  // Has credits of their own: whatever was refused, it was the project's pot.
  expect(emptyWallet({ wallet: 5 * MICRO, start_reserve: 10 * MICRO })).toBe(false)
  // Not enough for a session but not nothing — still their own money to move.
  expect(emptyWallet({ wallet: 1 * MICRO, start_reserve: 10 * MICRO })).toBe(false)
  // No credits reported at all: say nothing rather than guess.
  expect(emptyWallet(undefined)).toBe(false)
})

// One refusal, two problems: only one of them the person can fix by moving
// money they already hold. It is asked on the one act that spends — starting a
// session — where the project's pot is on screen to answer it with.
test('a refused start tells a new account something different from a spent project', () => {
  expect(startErrLabel(t, new ApiError(402), { wallet: 0, start_reserve: 10 * MICRO })).toBe('errNoCreditsAtAll')
  expect(startErrLabel(t, new ApiError(402), { wallet: 20 * MICRO, start_reserve: 10 * MICRO })).toBe('errNoCredits')
  // With nothing known, the message that does not accuse anybody of anything.
  expect(startErrLabel(t, new ApiError(402), undefined)).toBe('errNoCredits')
  // Every other refusal is unchanged.
  expect(startErrLabel(t, new ApiError(409), undefined)).toBe('activeSession')
  expect(startErrLabel(t, new ApiError(500), undefined)).toBe('startFailed')
})

// Money is micro-USD in flight and dollars on screen, and the conversion is in
// one place so a balance in the header and the same balance in the admin
// console cannot round differently.
test('amounts render in dollars', () => {
  expect(formatUSD('en', 0)).toBe('$0.00')
  expect(formatUSD('en', 12_340_000)).toBe('$12.34')
  expect(formatUSD('en', 200 * MICRO)).toBe('$200.00')
  // A negative balance is a real state: a session may overrun what its project
  // held, and the pot goes below zero rather than the run being killed.
  expect(formatUSD('en', -1_500_000)).toBe('-$1.50')
})

// A charge smaller than a cent is not nothing. Rendering it as $0.00 would tell
// somebody a session that cost real money cost them nothing.
test('a sub-cent amount says so rather than rounding to zero', () => {
  expect(formatUSD('en', 500)).toBe('<$0.01')
  expect(formatUSD('en', -500)).toBe('-<$0.01')
  expect(formatUSD('en', 10_000)).toBe('$0.01')
})

// What an admin types is dollars; what the ledger holds is micro-USD.
test('typed dollars become micro-USD', () => {
  expect(parseUSD('10')).toBe(10 * MICRO)
  expect(parseUSD('  $12.34 ')).toBe(12_340_000)
  expect(parseUSD('0.000001')).toBe(1)
  // Rounded to a whole micro-dollar: the ledger has no way to hold a fraction.
  expect(parseUSD('0.0000004')).toBe(0)
  expect(parseUSD('')).toBeNull()
  expect(parseUSD('abc')).toBeNull()
})

// A field shows what parseUSD reads back, so editing a value and saving it
// unchanged cannot move it.
test('a field round-trips through the parser', () => {
  for (const micro of [0, 1, 10 * MICRO, 12_340_000, 200 * MICRO]) {
    expect(parseUSD(toDollars(micro))).toBe(micro)
  }
})

// Each refusal of a credit move has a different remedy, so each gets its own
// sentence. One "that did not work" would tell somebody who mistyped an amount
// the same thing it tells somebody who is not a lead.
test('a refused credit move says which refusal it was', () => {
  // no_credits means different things by direction: an allocate is short in the
  // wallet (top up), a reclaim is held by a running session (wait to settle).
  expect(creditMoveErrLabel(t, new ApiError(400, 'no_credits'))).toBe('errAllocateShort')
  expect(creditMoveErrLabel(t, new ApiError(400, 'no_credits'), true)).toBe('errReclaimHeld')
  expect(creditMoveErrLabel(t, new ApiError(400, 'not_lead'))).toBe('errMoveNotLead')
  expect(creditMoveErrLabel(t, new ApiError(400, 'not_member'))).toBe('errMoveNotMember')
  expect(creditMoveErrLabel(t, new ApiError(400, 'bad_amount'))).toBe('errMoveAmount')
  // An unknown code is a generic failure rather than a wrong specific one.
  expect(creditMoveErrLabel(t, new ApiError(400, 'something_new'))).toBe('errMoveFailed')
  expect(creditMoveErrLabel(t, new ApiError(400, ''))).toBe('errMoveFailed')
})

// A wallet only ever sees three of the ledger's seven kinds: holds, releases
// and charges happen to a project's pot, never to a person. The rest fall
// through to a neutral label rather than leaking vocabulary nobody here needs.
test('a wallet statement names only the kinds a wallet can hold', () => {
  expect(ledgerKindLabel(t, 'topup')).toBe('ledgerTopup')
  expect(ledgerKindLabel(t, 'allocate')).toBe('ledgerAllocate')
  expect(ledgerKindLabel(t, 'reclaim')).toBe('ledgerReclaim')
  for (const kind of ['hold', 'release', 'charge', 'adjust'] as const) {
    expect(ledgerKindLabel(t, kind)).toBe('ledgerOther')
  }
})

// A move that captured a project names it (024b). The three cases are the whole
// point: a live project links, a purged one reads as plain text but still names
// itself, and a pre-024b move that never captured a project keeps the old label.
const entry = (e: Partial<LedgerEntry>): LedgerEntry => ({ id: 'e', holder: 'user:a@b', amount: -1, kind: 'allocate', actor: 'a@b', created_at: '2026-08-09', ...e })

test('an allocate to a live project links to it', () => {
  const label = ledgerLabel(t, entry({ kind: 'allocate', project_id: 'p1', project_name: 'Smith et al.', project_alive: true }))
  expect(label).toEqual({ kind: 'project', prefix: 'ledgerAllocateTo', name: 'Smith et al.', href: '/p/p1' })
})

test('a reclaim from a purged project names it as plain text, no link', () => {
  const label = ledgerLabel(t, entry({ kind: 'reclaim', amount: 1, project_id: 'p1', project_name: 'Smith et al.', project_alive: false }))
  expect(label).toEqual({ kind: 'project', prefix: 'ledgerReclaimFrom', name: 'Smith et al.', href: undefined })
})

test('a pre-024b move with no captured project keeps the generic label', () => {
  expect(ledgerLabel(t, entry({ kind: 'allocate' }))).toEqual({ kind: 'plain', text: 'ledgerAllocate' })
  expect(ledgerLabel(t, entry({ kind: 'topup', amount: 1 }))).toEqual({ kind: 'plain', text: 'ledgerTopup' })
})

test('a card purchase and refund name their source, not an admin', () => {
  // The actor distinguishes a Stripe purchase from an admin grant (022b/022c).
  expect(ledgerLabel(t, entry({ kind: 'topup', amount: 1, actor: 'stripe:pi_1' }))).toEqual({ kind: 'plain', text: 'ledgerCardTopup' })
  expect(ledgerLabel(t, entry({ kind: 'adjust', amount: -1, actor: 'stripe:refund:re_1' }))).toEqual({ kind: 'plain', text: 'ledgerRefund' })
  // An admin grant still reads as an admin grant.
  expect(ledgerLabel(t, entry({ kind: 'topup', amount: 1, actor: 'admin@x.io' }))).toEqual({ kind: 'plain', text: 'ledgerTopup' })
})
