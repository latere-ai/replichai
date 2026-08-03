import { expect, test } from 'bun:test'
import { noBudgetYet, sessionErrLabel, submitErrLabel } from './App'

// The identity function for the label helpers: they take a translate function,
// and asserting the key is what matters here, not the English copy.
const t = ((key: string) => key) as never

// 006q: with sign-in open, most people's first encounter with the quota is at
// 0/0. That is a starting state, not a fault — telling somebody they have "no
// sessions left" when they never had any reads as something taken away.
test('noBudgetYet separates a new account from an exhausted one', () => {
  expect(noBudgetYet({ used: 0, limit: 0, unlimited: false })).toBe(true)
  // Spent it: had budget, used it.
  expect(noBudgetYet({ used: 3, limit: 3, unlimited: false })).toBe(false)
  // Has budget left.
  expect(noBudgetYet({ used: 0, limit: 3, unlimited: false })).toBe(false)
  // An admin is unlimited and never in either state.
  expect(noBudgetYet({ used: 0, limit: 0, unlimited: true })).toBe(false)
  // No quota reported at all: say nothing rather than guess.
  expect(noBudgetYet(undefined)).toBe(false)
})

// One refusal, two problems: only one of them is the person's own doing, and
// the remedy differs (ask for budget vs wait for a session to be refunded).
test('a refused submit tells a new account something different from a spent one', () => {
  expect(submitErrLabel(t, '402', { used: 0, limit: 0, unlimited: false })).toBe('errQuotaNone')
  expect(submitErrLabel(t, '402', { used: 3, limit: 3, unlimited: false })).toBe('quotaExhausted')
  // With no quota known, the older message is the safe one.
  expect(submitErrLabel(t, '402', undefined)).toBe('quotaExhausted')
  // Every other refusal is unchanged.
  expect(submitErrLabel(t, '409', undefined)).toBe('activeSession')
  expect(submitErrLabel(t, '403', undefined)).toBe('notSponsored')
  expect(submitErrLabel(t, '500', undefined)).toBe('submitError')
})

// The same distinction on the per-paper path a batch create reports through.
test('a per-paper quota refusal makes the same distinction', () => {
  expect(sessionErrLabel(t, 'quota_exceeded', { used: 0, limit: 0, unlimited: false })).toBe('errQuotaNone')
  expect(sessionErrLabel(t, 'quota_exceeded', { used: 1, limit: 1, unlimited: false })).toBe('errQuota')
  expect(sessionErrLabel(t, 'active_session')).toBe('errActiveSession')
  expect(sessionErrLabel(t, 'unresolvable_paper')).toBe('errUnresolvable')
  expect(sessionErrLabel(t, undefined)).toBe('errEnqueue')
})
