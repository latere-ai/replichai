import { expect, test } from 'bun:test'
import { ledgerKindLabel } from './App'
import { ApiError } from './api'
import { creditMoveErrLabel, emptyWallet, sessionErrLabel, submitErrLabel } from './errors'
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
// money they already hold.
test('a refused submit tells a new account something different from a spent project', () => {
  expect(submitErrLabel(t, new ApiError(402), { wallet: 0, start_reserve: 10 * MICRO })).toBe('errNoCreditsAtAll')
  expect(submitErrLabel(t, new ApiError(402), { wallet: 20 * MICRO, start_reserve: 10 * MICRO })).toBe('errNoCredits')
  // With nothing known, the message that does not accuse anybody of anything.
  expect(submitErrLabel(t, new ApiError(402), undefined)).toBe('errNoCredits')
  // Every other refusal is unchanged.
  expect(submitErrLabel(t, new ApiError(409), undefined)).toBe('activeSession')
  expect(submitErrLabel(t, new ApiError(500), undefined)).toBe('submitError')
})

// The same distinction on the per-paper path a batch create reports through.
test('a per-paper credit refusal makes the same distinction', () => {
  expect(sessionErrLabel(t, 'no_credits', { wallet: 0, start_reserve: 10 * MICRO })).toBe('errNoCreditsAtAll')
  expect(sessionErrLabel(t, 'no_credits', { wallet: 20 * MICRO, start_reserve: 10 * MICRO })).toBe('errNoCredits')
  expect(sessionErrLabel(t, 'active_session')).toBe('errActiveSession')
  expect(sessionErrLabel(t, 'unresolvable_paper')).toBe('errUnresolvable')
  expect(sessionErrLabel(t, undefined)).toBe('errEnqueue')
})

// Money is micro-USD in flight and dollars on screen, and the conversion is in
// one place so a balance in the header and the same balance in the admin
// console cannot round differently.
test('amounts render in dollars', () => {
  expect(formatUSD(0)).toBe('$0.00')
  expect(formatUSD(12_340_000)).toBe('$12.34')
  expect(formatUSD(200 * MICRO)).toBe('$200.00')
  // A negative balance is a real state: a session may overrun what its project
  // held, and the pot goes below zero rather than the run being killed.
  expect(formatUSD(-1_500_000)).toBe('-$1.50')
})

// A charge smaller than a cent is not nothing. Rendering it as $0.00 would tell
// somebody a session that cost real money cost them nothing.
test('a sub-cent amount says so rather than rounding to zero', () => {
  expect(formatUSD(500)).toBe('<$0.01')
  expect(formatUSD(-500)).toBe('-<$0.01')
  expect(formatUSD(10_000)).toBe('$0.01')
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
  expect(creditMoveErrLabel(t, new ApiError(400, 'no_credits'))).toBe('errMoveInsufficient')
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
