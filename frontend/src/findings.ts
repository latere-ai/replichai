// The shape of verdict.json (021a) as the app reads it, and the three questions
// the panes ask of it.
//
// The types mirror `internal/verdict` field for field. The server validates
// before serving, so anything that reaches here has already been refused if an
// outer-loop claim carried an outcome or a decision omitted its sensitivity —
// which is why nothing below re-checks those rules. What it does instead is
// answer questions the schema deliberately does not encode: which claims are
// still open, and how a rate should be read.

export type Loop = 'inner' | 'outer'
export type ClaimOutcome = 'verified' | 'partial' | 'blocked' | 'not_scored'
export type MismatchKind = 'numeric' | 'ordering' | 'contradiction' | 'typo'
export type FindingsVerdict = 'reproducible' | 'partial' | 'not_reproducible'
export type Criterion = 'code' | 'data' | 'license' | 'preregistration' | 'supplement'
export type ReleaseResult = 'found' | 'partial' | 'none' | 'dead'

export type Mismatch = { kind: MismatchKind; reported: string; observed: string; delta?: string }

export type Claim = {
  claim: string
  loop: Loop
  outcome: ClaimOutcome
  evidence: string
  citation: string
  mismatch?: Mismatch
}

export type Decision = { question: string; where: string; assumed: string; sensitivity: string }
export type Score = { criterion: Criterion; found: boolean; url?: string; searched?: string[] }
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

// inner and outer split the boundary. They are separate calls rather than one
// grouping so a caller cannot accidentally render outer rows through a code
// path that assumes an outcome means something.
export const inner = (f: Findings): Claim[] => (f.boundary ?? []).filter((c) => c.loop === 'inner')
export const outer = (f: Findings): Claim[] => (f.boundary ?? []).filter((c) => c.loop === 'outer')

// unresolved is what did not cleanly reproduce: an inner-loop claim that is
// blocked, only partly verified, or verified while still disagreeing with the
// paper. That last case is the one a table of outcomes hides — a run can verify
// a claim and still have found a number the paper got wrong — so a mismatch
// counts as unresolved whatever the outcome beside it says.
//
// Outer rows are never included. They were never scored, so there is nothing
// about them to resolve, and listing them under a heading about what failed
// would read as an accusation the product does not make.
export const unresolved = (f: Findings): Claim[] =>
  inner(f).filter((c) => c.outcome !== 'verified' || !!c.mismatch)

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

// The five, in the order a reader reads them — the same order the schema lists.
export const CRITERIA: Criterion[] = ['code', 'data', 'license', 'preregistration', 'supplement']

// tones map a value to the semantic token that carries it. They live here, next
// to the types, so a new outcome cannot be added without a colour being chosen
// for it.
export const outcomeTone = (o: ClaimOutcome): string =>
  o === 'verified' ? 'var(--ok)' : o === 'partial' ? 'var(--warn)' : o === 'blocked' ? 'var(--err)' : 'var(--fg-3)'

export const verdictTone = (v: FindingsVerdict): string =>
  v === 'reproducible' ? 'var(--ok)' : v === 'partial' ? 'var(--warn)' : 'var(--err)'

// A dead link and nothing published are different findings — the first is one
// an author can still fix — so they never share a tone.
export const releaseTone = (r: ReleaseResult): string =>
  r === 'found' ? 'var(--ok)' : r === 'partial' ? 'var(--warn)' : r === 'dead' ? 'var(--err)' : 'var(--fg-3)'
