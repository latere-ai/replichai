// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Money is micro-USD everywhere it travels: an integer count of millionths of a
// dollar, never a float and never a formatted string (011). A rate is quoted per
// million tokens, so the unit multiplies cleanly at the one place a session is
// priced, and a payment provider charges in minor units of the same currency.
//
// Formatting belongs here and nowhere else, so a balance rendered in the header
// and the same balance rendered in the admin console cannot round differently.

import type { Lang } from './i18n'
import { formatCurrency } from './locale'

// MICRO is one dollar, in micro-USD.
export const MICRO = 1_000_000

// formatUSD renders an amount for a person to read: "$12.34".
//
// Cents, not micro-dollars, because that is the resolution a human decides
// anything at. A non-zero amount that would round to $0.00 shows as "<$0.01"
// instead — a session that cost a fraction of a cent did cost something, and
// showing it as nothing is the one rounding a reader would be misled by.
//
// It is written in the app's language, not the browser's: the same balance
// used to read "$12.34" in one OS and "12,34 $" in another, beside copy that
// had not changed.
export function formatUSD(lang: Lang, micro: number): string {
  const neg = micro < 0
  const abs = Math.abs(micro)
  if (abs > 0 && abs < MICRO / 100) return (neg ? '-' : '') + '<$0.01'
  const s = formatCurrency(lang, abs / MICRO)
  return neg ? '-' + s : s
}

// parseUSD reads what somebody typed in dollars into micro-USD, or null when it
// is not a number. It rounds to the nearest micro-dollar so a typed "0.0000001"
// cannot become a fraction the ledger has no way to hold.
export function parseUSD(text: string): number | null {
  const n = Number.parseFloat(text.trim().replace(/^\$/, ''))
  if (!Number.isFinite(n)) return null
  return Math.round(n * MICRO)
}

// toDollars renders an amount for an input field: a plain decimal with no
// currency symbol, so what the field shows is what parseUSD reads back.
export function toDollars(micro: number): string {
  return String(micro / MICRO)
}

// creditedMicro is what a gross top-up lands in a wallet after the purchase
// spread (022b): the same arithmetic the server applies at checkout, so the
// modal's quote is exactly what the webhook will credit. It snaps to whole cents
// (the processor's unit) first, then takes the percentage and the fixed cut, and
// never returns less than zero.
export function creditedMicro(grossMicro: number, spreadBps: number, spreadFixedMicro: number): number {
  const perCent = MICRO / 100 // 10_000 micro-USD to a cent
  const cents = Math.floor(grossMicro / perCent)
  const gross = cents * perCent
  const cut = Math.round((gross * spreadBps) / 10_000)
  return Math.max(0, gross - cut - spreadFixedMicro)
}

// typedDollars keeps what somebody is typing into a money field typeable.
//
// It is not parseUSD: that reads a finished value, this shapes an in-progress
// one, and the two have opposite obligations. "12." is not a number, but a
// person on their way to "12.50" must be allowed to have typed it, so this
// returns strings parseUSD will reject and lets the caller's validity check
// decide when to enable the button.
//
// The rules are the ones a currency has: digits, at most one point, and at most
// two places after it — the ledger holds micro-dollars, but nobody allocates
// tenths of a cent, and a field that accepts them invites a rounding surprise
// between what was typed and what was charged.
export function typedDollars(text: string): string {
  const cleaned = text.replace(/[^0-9.]/g, '')
  const [whole, ...rest] = cleaned.split('.')
  if (rest.length === 0) return whole
  return whole + '.' + rest.join('').slice(0, 2)
}

// stepDollars moves a money field by `by`, from `from` when it holds a number
// and from `min` when it does not — so a step out of an empty field lands on the
// floor rather than on NaN. The result carries cents only when it has any: a
// field showing "17" after a step from "16" should not read "17.00".
export function stepDollars(from: string, by: number, min = 0, max?: number): string {
  const n = Number.parseFloat(from)
  const base = Number.isFinite(n) ? n : min
  const next = Math.min(max ?? Number.MAX_SAFE_INTEGER, Math.max(min, base + by))
  return String(Math.round(next * 100) / 100)
}
