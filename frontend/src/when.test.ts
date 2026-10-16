// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'bun:test'
import { translator } from './i18n'
import { absoluteTime, duration, elapsed, relativeTime } from './when'

const { t } = translator('en')
const now = Date.parse('2026-08-08T12:00:00Z')
const ago = (ms: number) => new Date(now - ms).toISOString()

describe('relativeTime', () => {
  it('reads a fresh row as just now, including a clock a little ahead of ours', () => {
    expect(relativeTime(t, 'en', ago(0), now)).toBe('just now')
    expect(relativeTime(t, 'en', ago(30_000), now)).toBe('just now')
    expect(relativeTime(t, 'en', new Date(now + 4_000).toISOString(), now)).toBe('just now')
  })

  it('counts in the largest unit that fits, singular at one', () => {
    expect(relativeTime(t, 'en', ago(60_000), now)).toBe('1 minute ago')
    expect(relativeTime(t, 'en', ago(5 * 60_000), now)).toBe('5 minutes ago')
    expect(relativeTime(t, 'en', ago(3 * 3_600_000), now)).toBe('3 hours ago')
    expect(relativeTime(t, 'en', ago(24 * 3_600_000), now)).toBe('1 day ago')
    expect(relativeTime(t, 'en', ago(6 * 24 * 3_600_000), now)).toBe('6 days ago')
  })

  // The date is written in the app's language, not the browser's.
  it('falls back to a date past a week, in the app’s language', () => {
    const old = ago(43 * 24 * 3_600_000)
    expect(relativeTime(t, 'en', old, now)).toBe(new Date(Date.parse(old)).toLocaleDateString('en'))
  })

  it('renders nothing for a missing or unparseable time', () => {
    expect(relativeTime(t, 'en', undefined, now)).toBe('')
    expect(relativeTime(t, 'en', '', now)).toBe('')
    expect(relativeTime(t, 'en', 'not a time', now)).toBe('')
  })
})

describe('absoluteTime', () => {
  it('is the exact moment in the app’s language, and empty for a missing one', () => {
    const iso = '2026-08-08T09:30:00Z'
    expect(absoluteTime('en', iso)).toBe(new Date(Date.parse(iso)).toLocaleString('en'))
    expect(absoluteTime('en', undefined)).toBe('')
    expect(absoluteTime('en', 'nope')).toBe('')
  })
})

describe('elapsed', () => {
  it('keeps the minutes a running session is measured in', () => {
    const start = '2026-08-08T10:00:00Z'
    const at = (ms: number) => Date.parse(start) + ms
    expect(elapsed(t, start, at(12_000))).toBe('12s')
    expect(elapsed(t, start, at(34 * 60_000))).toBe('34m')
    expect(elapsed(t, start, at(107 * 60_000))).toBe('1h 47m')
    expect(elapsed(t, start, at(60 * 60_000))).toBe('1h 0m')
  })

  // An age would have rounded this to "1 hour ago", which answers the wrong
  // question imprecisely on a row that is still spending.
  it('does not round away the minutes relativeTime drops', () => {
    const start = '2026-08-08T10:00:00Z'
    expect(elapsed(t, start, Date.parse(start) + 89 * 60_000)).toBe('1h 29m')
  })

  it('is empty for a session with no recorded start, and never negative', () => {
    expect(elapsed(t, undefined)).toBe('')
    expect(elapsed(t, 'nonsense')).toBe('')
    expect(elapsed(t, '2026-08-08T10:00:00Z', Date.parse('2026-08-08T09:59:50Z'))).toBe('0s')
  })
})

// duration is the one formatter behind elapsed, the transcript's offsetOf and
// the timeline's phase lengths. Each of those pins its own output, above and
// in transcript.test.ts; this checks the shared function, at every scale and
// in both modes.
describe('duration', () => {
  it('counts seconds under a minute, whether or not seconds were asked for', () => {
    expect(duration(t, 0)).toBe('0s')
    expect(duration(t, 59)).toBe('59s')
    expect(duration(t, 12, { seconds: true })).toBe('12s')
  })

  it('keeps the seconds at minute scale only when asked', () => {
    expect(duration(t, 60)).toBe('1m')
    expect(duration(t, 60, { seconds: true })).toBe('1m 0s')
    expect(duration(t, 34 * 60 + 7)).toBe('34m')
    expect(duration(t, 34 * 60 + 7, { seconds: true })).toBe('34m 7s')
    expect(duration(t, 59 * 60 + 59, { seconds: true })).toBe('59m 59s')
  })

  it('reads hours with their minutes and never the seconds', () => {
    expect(duration(t, 3600)).toBe('1h 0m')
    expect(duration(t, 107 * 60 + 30)).toBe('1h 47m')
    expect(duration(t, 107 * 60 + 30, { seconds: true })).toBe('1h 47m')
  })
})
