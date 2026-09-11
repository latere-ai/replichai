// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// When something last changed, in the words a reader scanning a list uses.
//
// A project row shows a relative time and keeps the exact one in its tooltip
// (018): scanning a list is a question about recency ("did I touch this today?"),
// and reading one row is a question about a moment. Both are wanted, but only one
// fits in a column.

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR

// relativeTime renders an ISO timestamp as an age. Empty for a missing or
// unparseable one, so a row with no recorded time renders a dash rather than
// "Invalid Date" — an absent record is not an error to report at the reader.
//
// Ages round down to the unit below them and stop at a week, past which a date is
// what somebody actually wants: "43 days ago" is arithmetic nobody asked for.
export function relativeTime(iso: string | undefined, now: number = Date.now()): string {
  const at = iso ? Date.parse(iso) : NaN
  if (Number.isNaN(at)) return ''
  const ms = now - at
  // A clock that disagrees with the server by a few seconds must not produce "in
  // 4 seconds" on a row that was just written.
  if (ms < MINUTE) return 'just now'
  if (ms < HOUR) return plural(Math.floor(ms / MINUTE), 'minute')
  if (ms < DAY) return plural(Math.floor(ms / HOUR), 'hour')
  if (ms < 7 * DAY) return plural(Math.floor(ms / DAY), 'day')
  return new Date(at).toLocaleDateString()
}

// absoluteTime is the exact moment, for the tooltip behind the relative one.
export function absoluteTime(iso: string | undefined): string {
  const at = iso ? Date.parse(iso) : NaN
  return Number.isNaN(at) ? '' : new Date(at).toLocaleString()
}

function plural(n: number, unit: string): string {
  return `${n} ${unit}${n === 1 ? '' : 's'} ago`
}

// elapsed renders how long a run has been going, for a row that is still going.
//
// It is not relativeTime with different words. An age answers "how stale is
// this" and rounds down to one unit, so a run 89 minutes in reads "1 hour ago" —
// which, on a session that is still burning credits, is the wrong question
// answered imprecisely. A duration answers "how long has this been running" and
// keeps the minutes, because the minutes are what the reader is watching.
export function elapsed(iso: string | undefined, now: number = Date.now()): string {
  const at = iso ? Date.parse(iso) : NaN
  if (Number.isNaN(at)) return ''
  return duration(Math.max(0, Math.floor((now - at) / 1000)))
}

// duration renders a count of seconds the way the whole client does: seconds
// under a minute, minutes under an hour, then hours and minutes. `seconds`
// keeps the seconds at minute scale — a phase on the timeline and an offset
// into a transcript are read to the second; a session still running is not
// (see elapsed). Callers do their own rounding, so a floor and a round each
// stay where they were.
export function duration(seconds: number, opts?: { seconds?: boolean }): string {
  if (seconds < 60) return `${seconds}s`
  const m = Math.floor(seconds / 60)
  if (m < 60) return opts?.seconds ? `${m}m ${seconds % 60}s` : `${m}m`
  const h = Math.floor(m / 60)
  return `${h}h ${m % 60}m`
}
