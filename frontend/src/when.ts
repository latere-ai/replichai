// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Lang, T } from './i18n'
import { dateOnly, dateTime } from './locale'

// When something last changed, in the words a reader scanning a list uses. A
// row shows a relative time and keeps the exact one in its tooltip: scanning a
// list is a question about recency, reading one row is a question about a
// moment. Every word comes through t() and every date through the app's language.

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

// relativeTime renders an ISO timestamp as an age, or '' for a missing or
// unparseable one, so the row can render a dash rather than "Invalid Date". Ages
// round down to one unit and stop at a week, past which a date is shown instead.
export function relativeTime(t: T, lang: Lang, iso: string | undefined, now: number = Date.now()): string {
  const at = iso ? Date.parse(iso) : NaN
  if (Number.isNaN(at)) return ''
  const ms = now - at
  // A clock that disagrees with the server by a few seconds must not produce "in
  // 4 seconds" on a row that was just written.
  if (ms < MINUTE) return t('justNow')
  if (ms < HOUR) return ago(t, Math.floor(ms / MINUTE), 'agoMinute', 'agoMinutes')
  if (ms < DAY) return ago(t, Math.floor(ms / HOUR), 'agoHour', 'agoHours')
  if (ms < 7 * DAY) return ago(t, Math.floor(ms / DAY), 'agoDay', 'agoDays')
  return dateOnly(lang, at)
}

// ago picks the singular or the plural line for a count. Two keys rather than
// one with a rule, so a language whose plural is not "add an s" writes both.
function ago(t: T, n: number, one: 'agoMinute' | 'agoHour' | 'agoDay', many: 'agoMinutes' | 'agoHours' | 'agoDays'): string {
  return n === 1 ? t(one) : t(many, { n: String(n) })
}

// absoluteTime is the exact moment, for the tooltip behind the relative one.
export function absoluteTime(lang: Lang, iso: string | undefined): string {
  const at = iso ? Date.parse(iso) : NaN
  return Number.isNaN(at) ? '' : dateTime(lang, at)
}

// elapsed renders how long a run has been going, for a row that is still going.
// Unlike relativeTime it keeps the minutes: a run 89 minutes in reads "1h 29m"
// rather than "1 hour ago", because on a session still spending credits the
// minutes are what the reader is watching.
export function elapsed(t: T, iso: string | undefined, now: number = Date.now()): string {
  const at = iso ? Date.parse(iso) : NaN
  if (Number.isNaN(at)) return ''
  return duration(t, Math.max(0, Math.floor((now - at) / 1000)))
}

// duration renders a count of seconds the way the whole client does: seconds
// under a minute, minutes under an hour, then hours and minutes. `seconds`
// keeps the seconds at minute scale, for a timeline phase or a transcript
// offset, which are read to the second. Callers do their own rounding.
export function duration(t: T, seconds: number, opts?: { seconds?: boolean }): string {
  if (seconds < 60) return t('durSeconds', { s: String(seconds) })
  const m = Math.floor(seconds / 60)
  if (m < 60) return opts?.seconds ? t('durMinutesSeconds', { m: String(m), s: String(seconds % 60) }) : t('durMinutes', { m: String(m) })
  const h = Math.floor(m / 60)
  return t('durHoursMinutes', { h: String(h), m: String(m % 60) })
}
