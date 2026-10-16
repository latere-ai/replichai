// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Money is micro-USD everywhere it travels: an integer count of millionths of a
// dollar, never a float and never a formatted string. A rate is quoted per
// million tokens, so the unit multiplies cleanly where a session is priced.
// Formatting lives only here, so the same balance cannot round differently on
// two screens.

import type { Lang } from './i18n'
import { formatCurrency } from './locale'

// MICRO is one dollar, in micro-USD.
export const MICRO = 1_000_000

// formatUSD renders an amount for a person to read, in cents and in the app's
// language: "$12.34". A non-zero amount that would round to $0.00 shows as
// "<$0.01", because a session that cost a fraction of a cent did cost something.
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
// spread. It is the same arithmetic the server applies at checkout, so the
// modal's quote is exactly what the webhook will credit: round down to whole
// cents first, then take the percentage and the fixed cut, never below zero.
export function creditedMicro(grossMicro: number, spreadBps: number, spreadFixedMicro: number): number {
  const perCent = MICRO / 100 // 10_000 micro-USD to a cent
  const cents = Math.floor(grossMicro / perCent)
  const gross = cents * perCent
  const cut = Math.round((gross * spreadBps) / 10_000)
  return Math.max(0, gross - cut - spreadFixedMicro)
}

// typedDollars shapes an in-progress entry in a money field: digits, at most one
// point, and at most two places after it. Unlike parseUSD it accepts partial
// input such as "12." on the way to "12.50", and leaves validity to the caller.
// Two places, because a field that accepted tenths of a cent would invite a
// rounding gap between what was typed and what was charged.
export function typedDollars(text: string): string {
  const cleaned = text.replace(/[^0-9.]/g, '')
  const [whole, ...rest] = cleaned.split('.')
  if (rest.length === 0) return whole
  return whole + '.' + rest.join('').slice(0, 2)
}

// stepDollars moves a money field by `by`, from `from` when it holds a number
// and from `min` when it does not, so a step out of an empty field lands on the
// floor rather than on NaN. The result carries cents only when it has any: a
// field showing "17" after a step from "16" should not read "17.00".
export function stepDollars(from: string, by: number, min = 0): string {
  const n = Number.parseFloat(from)
  const base = Number.isFinite(n) ? n : min
  const next = Math.max(min, base + by)
  return String(Math.round(next * 100) / 100)
}
