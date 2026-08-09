# 021a — The findings as data

**Status:** designed.

**Scope:** `verdict.json` — the reproduction's findings as a validated artifact. A new `internal/verdict` package (schema, parse, validate), a new `method/rubric.md` criterion asking the agent for it, verification at finalize that records rather than blocks, and `GET /jobs/{id}/verdict` so the panes can read it. No frontend.

**Depends on:** [021](021-what-the-run-actually-found.md), [007a](007a-the-study-protocol-as-an-artifact.md) (the package this mirrors), [002](002-publishing-a-reproduction.md) (the manifest and finalize), [006i](006i-server-side-mount-and-publish.md) (`publish.FinalizeLive`, the one finalize both paths share).

## Problem

See [021](021-what-the-run-actually-found.md). The rubric already demands the boundary table, the hidden decisions, and the open-science scorecard; the agent already writes all three; nothing downstream can read any of them because they are prose.

## Decisions

**One file, four lists, at the repository root.** `verdict.json` sits beside `replication.json` and `instrument.json` — the agent writes the first two, the system writes the manifest. It holds the summary the reader sees first, the per-claim boundary, the decisions, the scorecard, and what the authors released.

```go
type Verdict struct {
    Schema     int         `json:"schema"`     // 1
    Verdict    Outcome     `json:"verdict"`    // reproducible | partial | not_reproducible
    Summary    string      `json:"summary"`    // the paragraph a reader reads first
    GradedAt   time.Time   `json:"graded_at"`
    Components Components  `json:"components"` // verified / partial / blocked, and the total
    Boundary   []Claim     `json:"boundary"`
    Decisions  []Decision  `json:"decisions"`
    Scorecard  []Score     `json:"scorecard"`
    Released   []Release   `json:"released"`
}
```

**A claim carries its outcome, its citation, and — when the numbers disagree — the disagreement.**

```go
type Claim struct {
    Claim    string    `json:"claim"`
    Loop     Loop      `json:"loop"`              // inner | outer
    Outcome  ClaimOut  `json:"outcome"`           // verified | partial | blocked | not_scored
    Evidence string    `json:"evidence"`          // the evidence, or the specific blocker
    Citation string    `json:"citation"`          // section, figure, or table — rubric 2 and 6
    Mismatch *Mismatch `json:"mismatch,omitempty"`
}

type Mismatch struct {
    Kind     MismatchKind `json:"kind"`     // numeric | ordering | contradiction | typo
    Reported string       `json:"reported"` // what the paper says
    Observed string       `json:"observed"` // what the run got
    Delta    string       `json:"delta,omitempty"`
}
```

`Mismatch` is the thing the prose could never surface: a claim can be *verified* and still disagree with the paper (89.6% against a reported 91.4%), and a paper can contain a plain error — a transposed number, a figure that contradicts its own caption. Recording the disagreement separately from the outcome is what stops "verified" from meaning "the paper was right" and "blocked" from being the only place a reader learns something went wrong.

**An outer-loop claim is `not_scored`, and the validator enforces it.** `loop: "outer"` with any outcome other than `not_scored` is invalid. This is the product's one hard rule expressed as a schema constraint: the system never scores the study with human participants, so a file that claims it did is refused rather than rendered.

**A decision names where the paper leaves it open and how much the answer matters.** Rubric criterion 3 asks for all three; the schema requires all three, so a decision that omits the sensitivity fails validation rather than reaching a reader as an assumption with no stated risk.

```go
type Decision struct {
    Question    string `json:"question"`
    Where       string `json:"where"`       // where the paper leaves it open
    Assumed     string `json:"assumed"`     // what the reproduction did instead
    Sensitivity string `json:"sensitivity"` // how much the result moves if it is wrong
}
```

**The scorecard and the search log are the same act, recorded twice.** `Score` answers rubric 4 — one row per criterion, either a URL that was fetched or an explicit "none found" listing where it looked. `Release` is the reader-facing version of `SOURCES.md`: each place searched, what was there, and the result.

```go
type Score struct {
    Criterion Criterion `json:"criterion"` // code | data | license | preregistration | supplement
    Found     bool      `json:"found"`
    URL       string    `json:"url,omitempty"`      // required when found
    Searched  []string  `json:"searched,omitempty"` // required when not found
}

type Release struct {
    Where  string  `json:"where"`  // GitHub, OSF, ACM DL supplement, the link in §5
    What   string  `json:"what"`
    Result Found   `json:"result"` // found | partial | none | dead
    URL    string  `json:"url,omitempty"`
}
```

**Validation is a list of specific failures, not a boolean.** `Validate` returns `[]Problem{Field, Message}` like `instrument.Validate` does, so the finalize record can say *which* claim was missing a citation rather than "invalid".

**A missing or invalid file is recorded, never fatal.** `publish.FinalizeLive` reads `verdict.json` from the committed tree; on absence or a validation failure it writes the reason into the manifest (`findings: {present: false, problems: [...]}`) and appends a line to `UNVERIFIED.md`. The run still publishes, the grade is still the system's, and the reader is told the findings could not be read rather than shown an empty table.

**The API serves it under the session, whole.** `GET /jobs/{id}/verdict` returns the parsed file, or `404` with a reason when there is none — read through the same `viewableSession` gate every other session read uses. It is not folded into `/jobs/{id}/reproduction`: that route is the manifest, the manifest is the system's word, and mixing the agent's claims into it would blur exactly the line 021 exists to keep.

## Acceptance criteria

1. `internal/verdict` parses and validates the schema above, with a contract test per rule: outer + scored fails; a claim with no citation fails; a decision with no sensitivity fails; a `found` score with no URL fails; a not-found score with no `searched` fails; a well-formed file passes.
2. `method/rubric.md` gains a criterion requiring `verdict.json`, and the method digest changes with it.
3. `method/instructions.md` tells the agent what the file is and that the boundary rows must match the per-component table it already writes.
4. `publish.FinalizeLive` verifies the file; absence and each validation failure are recorded on the manifest and in `UNVERIFIED.md`, and neither aborts the publish. Tested both ways.
5. `GET /jobs/{id}/verdict` returns the parsed findings for a session that has them, `404` with a reason for one that does not, and `404` for a session the caller may not view.
6. The manifest gains a `findings` block and its schema version rises; the existing manifest tests still pass against the new version.

## Not in scope

Any UI. Backfilling old sessions. Deriving the rate from `Components` (the rate stays what the grader recorded; a disagreement between them is a finding for a later spec, not a fix here).
