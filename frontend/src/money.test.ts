// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'bun:test'
import { creditedMicro, formatUSD, parseUSD, stepDollars, typedDollars } from './money'

// An amount is written in the app's language, not the browser's: the same
// balance used to read "$12.34" in one OS and "12,34 $" in another, beside copy
// that had not changed. The fraction of a cent keeps its own mark.
describe('formatUSD', () => {
  it('writes dollars and cents in the app’s language', () => {
    expect(formatUSD('en', 12_340_000)).toBe('$12.34')
    expect(formatUSD('en', -12_340_000)).toBe('-$12.34')
    expect(formatUSD('en', 0)).toBe('$0.00')
  })

  it('says a fraction of a cent cost something', () => {
    expect(formatUSD('en', 4_000)).toBe('<$0.01')
    expect(formatUSD('en', -4_000)).toBe('-<$0.01')
  })
})

describe('creditedMicro', () => {
  it('applies the percentage and fixed spread, matching the server', () => {
    // $20.00 gross, 5% + $0.30 → $1.00 + $0.30 off → $18.70, exactly what the
    // server's platform.Settings.Credited computes.
    expect(creditedMicro(20_000_000, 500, 300_000)).toBe(18_700_000)
  })

  it('never credits below zero', () => {
    // A gross the fixed cut exceeds: a purchase does not create a debt.
    expect(creditedMicro(100_000, 0, 1_000_000)).toBe(0)
  })

  it('snaps to whole cents before pricing', () => {
    // Sub-cent micro-USD is dropped, the processor's unit being the cent.
    expect(creditedMicro(20_005_000, 0, 0)).toBe(20_000_000)
  })

  it('credits the whole gross with no spread', () => {
    expect(creditedMicro(20_000_000, 0, 0)).toBe(20_000_000)
  })
})

describe('typedDollars', () => {
  it('keeps a value on its way to being typed', () => {
    expect(typedDollars('12')).toBe('12')
    // Not a number yet, and it has to survive: it is the keystroke before 12.5.
    expect(typedDollars('12.')).toBe('12.')
    expect(typedDollars('12.5')).toBe('12.5')
    expect(typedDollars('12.50')).toBe('12.50')
  })

  it('allows one point and two places, and no more', () => {
    expect(typedDollars('12.5.7')).toBe('12.57')
    expect(typedDollars('12.567')).toBe('12.56')
    expect(typedDollars('.5')).toBe('.5')
  })

  it('drops anything that is not a number', () => {
    expect(typedDollars('$12.50')).toBe('12.50')
    expect(typedDollars('12 usd')).toBe('12')
    expect(typedDollars('abc')).toBe('')
    expect(typedDollars('-5')).toBe('5')
  })

  // The defect this exists for: a money field that strips the point can only
  // ever allocate whole dollars.
  it('lets cents be typed at all', () => {
    expect(typedDollars('16.82')).toBe('16.82')
    expect(parseUSD(typedDollars('16.82'))).toBe(16_820_000)
  })
})

describe('stepDollars', () => {
  it('steps from the value, keeping its cents', () => {
    expect(stepDollars('16.82', 1)).toBe('17.82')
    expect(stepDollars('16.82', -1)).toBe('15.82')
  })

  it('steps from the floor when the field holds no number', () => {
    expect(stepDollars('', 1, 0)).toBe('1')
    expect(stepDollars('.', 1, 5)).toBe('6')
  })

  it('does not print cents it does not have', () => {
    expect(stepDollars('16', 1)).toBe('17')
  })

  it('holds the floor', () => {
    expect(stepDollars('0.5', -1, 0)).toBe('0')
  })

  // Floating point: 0.1 + 0.2 must not reach a field as 0.30000000000000004.
  it('never leaks a floating-point tail', () => {
    expect(stepDollars('0.1', 0.2)).toBe('0.3')
  })
})
