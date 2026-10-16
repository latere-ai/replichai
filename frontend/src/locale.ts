// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Lang } from './i18n'

// How a number or a moment is written in the app's language. Each formatter
// takes the language as an argument, because a bare toLocale*String() formats
// in the browser's language, so a reader who chose English in an OS set to
// German would see "12,34 $" beside English copy. i18n-coverage.test.ts fails
// any call that leaves the language out.

// dateOnly is a calendar date: "8/8/2026".
export function dateOnly(lang: Lang, at: string | number | Date): string {
  return new Date(at).toLocaleDateString(lang)
}

// dateTime is a moment to the second, for a tooltip behind a rounded age.
export function dateTime(lang: Lang, at: string | number | Date): string {
  return new Date(at).toLocaleString(lang)
}

// clockTime is a time of day to the minute, on a 24-hour clock, which is the
// granularity a transcript gutter has room for.
export function clockTime(lang: Lang, at: string | number | Date): string {
  return new Date(at).toLocaleTimeString(lang, { hour: '2-digit', minute: '2-digit', hour12: false })
}

// formatCount is a whole number with its thousands grouped: "12,345".
export function formatCount(lang: Lang, n: number): string {
  return n.toLocaleString(lang)
}

// formatCurrency is an amount in dollars, with the symbol the language uses.
export function formatCurrency(lang: Lang, dollars: number): string {
  return dollars.toLocaleString(lang, { style: 'currency', currency: 'USD' })
}
