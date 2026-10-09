// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The shape of verdict.json (021a) as the app reads it, and the three questions
// the panes ask of it.
//
// The types mirror `internal/verdict` field for field. The server validates
// before serving and refuses a verdict in which an outer-loop claim carries an
// outcome or a decision omits its sensitivity, so nothing below re-checks those
// rules. It answers the questions the schema deliberately does not encode:
// which claims are still open, and how a rate should be read.

export type Loop = 'inner' | 'outer'
export type ClaimOutcome = 'verified' | 'partial' | 'blocked' | 'not_scored'
type MismatchKind = 'numeric' | 'ordering' | 'contradiction' | 'typo'
type FindingsVerdict = 'reproducible' | 'partial' | 'not_reproducible'
export type Criterion = 'code' | 'data' | 'license' | 'preregistration' | 'supplement'
export type ReleaseResult = 'found' | 'partial' | 'none' | 'dead' | 'not_fetched'

// Mismatch is one quantity on which the run and the paper disagree. reported
// and observed are the two values as text, as the paper states one and the run
// got the other; the two numeric values repeat them as numbers when each is a
// single number, and are absent or null when it is not.
export type Mismatch = {
  kind: MismatchKind
  what?: string
  reported: string
  observed: string
  reported_value?: number | null
  observed_value?: number | null
  unit?: string
  note?: string
}

// mismatches is the only form the app receives. The server reads an older
// file's single mismatch into this list, so no screen handles two shapes.
export type Claim = {
  claim: string
  loop: Loop
  outcome: ClaimOutcome
  evidence: string
  citation: string
  mismatches?: Mismatch[]
}

type Decision = { question: string; where: string; assumed: string; sensitivity: string }
type Score = { criterion: Criterion; found: boolean; url?: string; searched?: string[] }
export type Release = { where: string; what: string; result: ReleaseResult; url?: string }
export type Components = { verified: number; partial: number; blocked: number; total: number }

export type Findings = {
  schema: number
  verdict: FindingsVerdict
  summary: string
  graded_at: string
  components: Components
  boundary: Claim[]
  decisions?: Decision[]
  scorecard?: Score[]
  released?: Release[]
}

// inner is the half of the boundary an outcome means something for. There is no
// matching outer(): the one place that shows outer rows renders the whole
// boundary in one table and marks them per row, so a list of them has never
// been needed.
export const inner = (f: Findings): Claim[] => (f.boundary ?? []).filter((c) => c.loop === 'inner')

// unresolved is what did not cleanly reproduce: an inner-loop claim that is
// blocked, only partly verified, or verified while still disagreeing with the
// paper. A table of outcomes hides that last case, because a run can verify a
// claim and still have found a number the paper got wrong, so a mismatch counts
// as unresolved whatever the outcome beside it says.
//
// Outer rows are never included. They were never scored, so there is nothing
// about them to resolve, and listing them under a heading about what failed
// would read as an accusation the product does not make.
export const unresolved = (f: Findings): Claim[] =>
  inner(f).filter((c) => c.outcome !== 'verified' || !!c.mismatches?.length)

// rate is the derived summary the rubric permits only beside its decomposition,
// so this returns null rather than 0 when there is nothing to divide: a run with
// no components has no rate, and printing 0.00 would state a finding it does not
// have.
export const rate = (c: Components | undefined): number | null => {
  if (!c || c.total <= 0) return null
  return (c.verified + c.partial * 0.5) / c.total
}

// scoreOf finds one scorecard row. The server guarantees all five are present
// when the scorecard is non-empty, but a run that scored nothing has none, so
// the caller gets undefined rather than a fabricated "not found" row.
export const scoreOf = (f: Findings, c: Criterion): Score | undefined =>
  (f.scorecard ?? []).find((s) => s.criterion === c)

// The five, in the order the schema lists them.
export const CRITERIA: Criterion[] = ['code', 'data', 'license', 'preregistration', 'supplement']

// tones map a value to the semantic token that carries it. They live here, next
// to the types, so a new outcome cannot be added without a color being chosen
// for it.
export const outcomeTone = (o: ClaimOutcome): string =>
  o === 'verified' ? 'var(--ok)' : o === 'partial' ? 'var(--warn)' : o === 'blocked' ? 'var(--err)' : 'var(--fg-3)'

export const verdictTone = (v: FindingsVerdict): string =>
  v === 'reproducible' ? 'var(--ok)' : v === 'partial' ? 'var(--warn)' : 'var(--err)'

// A dead link and nothing published are different findings, and only the first
// is one an author can still fix, so they never share a tone. A page the run
// could not fetch is neither: it is a limit of the run and says nothing about
// the authors, so it is neutral, and darker than nothing published so the two
// are not read as one. A record rather than a chain of conditions, so a result
// added to the type does not compile until it has a tone.
const RELEASE_TONE: Record<ReleaseResult, string> = {
  found: 'var(--ok)',
  partial: 'var(--warn)',
  none: 'var(--fg-3)',
  dead: 'var(--err)',
  not_fetched: 'var(--fg-2)',
}

export const releaseTone = (r: ReleaseResult): string => RELEASE_TONE[r] ?? 'var(--fg-3)'
