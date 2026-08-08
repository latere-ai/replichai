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
