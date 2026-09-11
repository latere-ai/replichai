// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Lang, T } from './i18n'
import { dateOnly, dateTime } from './locale'

// When something last changed, in the words a reader scanning a list uses.
//
// A project row shows a relative time and keeps the exact one in its tooltip
// (018): scanning a list is a question about recency, such as whether it was
// touched today, and reading one row is a question about a moment. Both are wanted, but only one
// fits in a column.
//
// Every word here comes through t(), and every date through the app's language:
// "3 minutes ago" was English whatever the reader had chosen, and the date past
// a week was in whatever language the browser happened to run in.

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

// relativeTime renders an ISO timestamp as an age. Empty for a missing or
// unparseable one, so a row with no recorded time renders a dash rather than
// "Invalid Date"; an absent record is not an error to report to the reader.
//
// Ages round down to the unit below them and stop at a week, past which a date is
// what somebody actually wants: "43 days ago" is arithmetic nobody asked for.
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
//
// It is not relativeTime with different words. An age answers "how stale is
// this" and rounds down to one unit, so a run 89 minutes in reads "1 hour ago",
// which on a session that is still spending credits answers the wrong question
// imprecisely. A duration answers "how long has this been running" and
// keeps the minutes, because the minutes are what the reader is watching.
export function elapsed(t: T, iso: string | undefined, now: number = Date.now()): string {
  const at = iso ? Date.parse(iso) : NaN
  if (Number.isNaN(at)) return ''
  return duration(t, Math.max(0, Math.floor((now - at) / 1000)))
}

// duration renders a count of seconds the way the whole client does: seconds
// under a minute, minutes under an hour, then hours and minutes. `seconds`
// keeps the seconds at minute scale: a phase on the timeline and an offset
// into a transcript are read to the second, but a session still running is
// not, see elapsed. Callers do their own rounding, so a floor and a round each
// stay where they were.
export function duration(t: T, seconds: number, opts?: { seconds?: boolean }): string {
  if (seconds < 60) return t('durSeconds', { s: String(seconds) })
  const m = Math.floor(seconds / 60)
  if (m < 60) return opts?.seconds ? t('durMinutesSeconds', { m: String(m), s: String(seconds % 60) }) : t('durMinutes', { m: String(m) })
  const h = Math.floor(m / 60)
  return t('durHoursMinutes', { h: String(h), m: String(m % 60) })
}
