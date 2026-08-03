// Money is micro-USD everywhere it travels: an integer count of millionths of a
// dollar, never a float and never a formatted string (011). A rate is quoted per
// million tokens, so the unit multiplies cleanly at the one place a session is
// priced, and a payment provider charges in minor units of the same currency.
//
// Formatting belongs here and nowhere else, so a balance rendered in the header
// and the same balance rendered in the admin console cannot round differently.

// MICRO is one dollar, in micro-USD.
export const MICRO = 1_000_000

// formatUSD renders an amount for a person to read: "$12.34".
//
// Cents, not micro-dollars, because that is the resolution a human decides
// anything at. A non-zero amount that would round to $0.00 shows as "<$0.01"
// instead — a session that cost a fraction of a cent did cost something, and
// showing it as nothing is the one rounding a reader would be misled by.
export function formatUSD(micro: number): string {
  const neg = micro < 0
  const abs = Math.abs(micro)
  if (abs > 0 && abs < MICRO / 100) return (neg ? '-' : '') + '<$0.01'
  const s = (abs / MICRO).toLocaleString(undefined, { style: 'currency', currency: 'USD' })
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
