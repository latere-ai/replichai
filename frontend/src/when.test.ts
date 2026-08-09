import { describe, expect, it } from 'bun:test'
import { absoluteTime, elapsed, relativeTime } from './when'

const now = Date.parse('2026-08-08T12:00:00Z')
const ago = (ms: number) => new Date(now - ms).toISOString()

describe('relativeTime', () => {
  it('reads a fresh row as just now, including a clock a little ahead of ours', () => {
    expect(relativeTime(ago(0), now)).toBe('just now')
    expect(relativeTime(ago(30_000), now)).toBe('just now')
    expect(relativeTime(new Date(now + 4_000).toISOString(), now)).toBe('just now')
  })

  it('counts in the largest unit that fits, singular at one', () => {
    expect(relativeTime(ago(60_000), now)).toBe('1 minute ago')
    expect(relativeTime(ago(5 * 60_000), now)).toBe('5 minutes ago')
    expect(relativeTime(ago(3 * 3_600_000), now)).toBe('3 hours ago')
    expect(relativeTime(ago(24 * 3_600_000), now)).toBe('1 day ago')
    expect(relativeTime(ago(6 * 24 * 3_600_000), now)).toBe('6 days ago')
  })

  it('falls back to a date past a week, where an age stops being useful', () => {
    const old = ago(43 * 24 * 3_600_000)
    expect(relativeTime(old, now)).toBe(new Date(Date.parse(old)).toLocaleDateString())
  })

  it('renders nothing for a missing or unparseable time', () => {
    expect(relativeTime(undefined, now)).toBe('')
    expect(relativeTime('', now)).toBe('')
    expect(relativeTime('not a time', now)).toBe('')
  })
})

describe('absoluteTime', () => {
  it('is the exact moment, and empty for a missing one', () => {
    const iso = '2026-08-08T09:30:00Z'
    expect(absoluteTime(iso)).toBe(new Date(Date.parse(iso)).toLocaleString())
    expect(absoluteTime(undefined)).toBe('')
    expect(absoluteTime('nope')).toBe('')
  })
})

describe('elapsed', () => {
  it('keeps the minutes a running session is measured in', () => {
    const start = '2026-08-08T10:00:00Z'
    const at = (ms: number) => Date.parse(start) + ms
    expect(elapsed(start, at(12_000))).toBe('12s')
    expect(elapsed(start, at(34 * 60_000))).toBe('34m')
    expect(elapsed(start, at(107 * 60_000))).toBe('1h 47m')
    expect(elapsed(start, at(60 * 60_000))).toBe('1h 0m')
  })

  // An age would have rounded this to "1 hour ago" — the wrong question,
  // answered imprecisely, on a row that is still spending.
  it('does not round away the minutes relativeTime drops', () => {
    const start = '2026-08-08T10:00:00Z'
    expect(elapsed(start, Date.parse(start) + 89 * 60_000)).toBe('1h 29m')
  })

  it('is empty for a session with no recorded start, and never negative', () => {
    expect(elapsed(undefined)).toBe('')
    expect(elapsed('nonsense')).toBe('')
    expect(elapsed('2026-08-08T10:00:00Z', Date.parse('2026-08-08T09:59:50Z'))).toBe('0s')
  })
})
