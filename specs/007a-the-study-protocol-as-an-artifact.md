# 007a — The study protocol as an artifact

**Status:** draft
**Scope:** the audit writes `instrument.json` — the paper's study protocol, the analysis entrypoint contract for `src/`, and a servability assessment — as declared, machine-readable data beside the prose reports. A child of [007](007-from-reproduction-to-replication.md).
**Depends on:** [004](004-go-port.md) (the method is data; the auditor's grade-and-revise loop).

## Problem

The audit already knows the protocol. To place a component on the inner/outer boundary at all, the worker must read how the study was run — its conditions, how participants were assigned, what was measured — and it writes that down, in prose, in `REPRODUCIBILITY.md`'s boundary table. It also writes a working analysis implementation into `src/` and records the exact command that ran it.

Both are stranded. A human can read the boundary table and reconstruct the study; nothing else can. `src/` runs once, on whatever data the worker had, behind a command recorded in a Markdown file. So the reproduction is a document about an instrument rather than an instrument, and every capability [007](007-from-reproduction-to-replication.md) plans — re-running the analysis on a new dataset, driving a synthetic cohort, serving the study to participants — begins by needing the same three declarations that the audit is already in the best possible position to make and currently does not.

This spec adds the declaration. It is the only spec in the 007 track that touches the method, and it touches it only additively.

## Decisions

**`instrument.json` sits beside `replication.json`, and refusal is a first-class answer.** Three top-level fields, each independently nullable with a stated reason:

```
protocol     the study design, machine-readable        | null + reason
analysis     the entrypoint contract for src/          | null + reason
servability  can a machine serve this protocol?        | never null
```

A paper that describes its procedure too vaguely to extract yields `protocol: null, protocol_absent_reason: "..."`. A paper whose analysis could not be recovered runnably yields `analysis: null` with the blocker. This is `UNVERIFIED.md`'s posture applied to structured data: **the wall is the finding**, and a stated reason is a passing outcome. What is *not* passing is an invented protocol, and that is what the criterion below grades for.

**The protocol carries what the paper reported, not only how it ran.** Alongside design and measures, `protocol.reported` records the paper's own headline statistics — the effect, its test, its value, its interval, its N. This is the field that makes the rest of the track checkable: it is the ground truth a `released`-source re-analysis is compared against ([007b](007b-experiments-over-instruments.md)) and the only thing a synthetic pilot can be calibrated on ([007c](007c-the-synthetic-pilot.md)). Every entry cites a table or figure, as the honesty criterion already requires of all claims about the paper.

**The analysis entrypoint is a contract, not a recorded command.** `src/` today is runnable in the sense that it ran once. To be runnable *on new data* it must declare what it consumes:

- the command, with the input path as a named parameter rather than baked in;
- the **input schema** — the columns it expects, their types, their units, and which are required — so a researcher's dataset can be validated before execution instead of failing inside a script;
- the outputs it produces and where.

This is a small method change with a large consequence: it is the difference between an implementation and an instrument, and it is what lets [007b](007b-experiments-over-instruments.md) run the same pipeline over four different data sources without knowing anything about the paper.

**Servability is a closed criteria list, answered with a justification.** "Highly replicable" becomes a decision the audit makes against declared, mechanical criteria rather than a judgement call. A protocol is servable only if all hold:

| Criterion | Excludes |
|---|---|
| Single session, no follow-up | longitudinal and diary studies |
| Screen-based, no physical apparatus | eye trackers, EEG, VR rigs, tangible devices |
| No experimenter or confederate in the loop | interviews, think-aloud, Wizard-of-Oz, moderated tasks |
| Every measure capturable in a browser | physiological signals, observation, video coding |
| The apparatus is recovered in `src/` or specified completely enough to build | studies of a system the reproduction could not rebuild |

`servability` records the verdict plus which criteria failed. Most HCI papers will answer `false`, and that is the correct and useful answer — it is the same value the boundary table provides by refusing to overclaim. The last criterion is the one that most often decides it: a paper's mechanism may be reproducible while the *interface* participants actually saw is not, and a study cannot be served without the thing being studied.

**The rubric gains a seventh criterion, and the six existing ones do not move.** The new criterion grades the *declaration*, never the extraction:

> **7. Instrument.** `instrument.json` is present and validates against the schema. Each of `protocol` and `analysis` is either populated from the paper with section, table, or figure references, or explicitly null with a specific reason. `servability` is answered against every criterion. A fabricated protocol, an unstated null, or a schema violation fails.

So a paper with no extractable protocol passes criterion 7 by saying so. This keeps the bar on honesty, where the other six already are, and keeps the audit's *verdict and rate* untouched — criterion 7 is graded like the others, but the reproducibility rate it feeds is still the inner loop's, computed exactly as today.

**A malformed instrument is a revision trigger, not a failed audit.** The auditor validates `instrument.json` against the schema after the run and folds the result into the existing grade-and-revise loop, so an invalid or dishonest instrument is fed back to the worker within its revision budget — the machinery [004](004-go-port.md) already built for every other criterion. Rejected: rejecting the run, which would throw away a good reproduction over a bad JSON file; and accepting silently, which would let unvalidated agent-written JSON reach the executors downstream.

**Both golden cases gain instrument expectations, and they discriminate.** The eval pair is chosen to fail in opposite directions, and it does so here too: case-02's protocol is a screen-based single-session task whose measures are trial-level reaction time and accuracy — the shape servability was written for — while case-01's outer loop is a study *of a visual interface* the reproduction does not rebuild, so it must answer `servability: false` on the apparatus criterion however cleanly its protocol extracts. A change that makes the audit credulous about servability shows up in case-01; one that makes it uselessly conservative shows up in case-02.

## Interface (sketch)

```go
// internal/instrument: the schema, its validation, and nothing else. No
// execution, no serving — those are 007b and 007d.
package instrument

type Instrument struct {
	Protocol    *Protocol    `json:"protocol"`              // nil when not extractable
	Analysis    *Analysis    `json:"analysis"`              // nil when not recovered
	Servability Servability  `json:"servability"`           // always present
	Absent      map[string]string `json:"absent_reasons,omitempty"` // field -> why
}

type Protocol struct {
	Design   Design     `json:"design"`   // between/within, factors and levels, counterbalancing
	Trials   Trials     `json:"trials"`   // structure, counts, stimuli, timing
	Measures []Measure  `json:"measures"` // what is recorded, per trial or per participant
	Plan     []Model    `json:"plan"`     // the declared statistical models
	Reported []Reported `json:"reported"` // the paper's own results, each with a citation
}

type Analysis struct {
	Command string   `json:"command"` // input path as a named parameter
	Input   Schema   `json:"input"`   // columns, types, units, required
	Outputs []string `json:"outputs"`
}

type Servability struct {
	Servable bool     `json:"servable"`
	Failed   []string `json:"failed_criteria"` // the closed list above
	Why      string   `json:"why"`
}

// Validate reports every schema violation, so one revision can fix all of them.
func Validate(raw []byte) ([]Violation, error)
```

Field names are implementation's to settle; this fixes what must be declared. The package holds the schema alone — the executors that consume it live in later specs, so nothing downstream can grow a dependency on how the audit produced it.

## Acceptance criteria

1. `instrument.json` is written by every audit, and `Validate` accepts it. A run that omits it fails criterion 7 and is revised.
2. The six existing rubric criteria are unchanged in text and effect; both golden cases pass their existing expectations byte-for-byte, with only additive instrument expectations added.
3. A null `protocol` or `analysis` with a stated reason passes criterion 7; the same null without a reason fails. Asserted directly against `Validate` and against a graded run.
4. Every `reported` entry carries a section, table, or figure citation — a fabricated or uncited statistic fails, on the same terms as the existing honesty criterion.
5. `analysis.command` parameterises its input path: a test substitutes a different data path and the command is well-formed without editing.
6. `analysis.input` validates a conforming dataset and rejects a non-conforming one with a message naming the offending column — the check [007b](007b-experiments-over-instruments.md)'s `uploaded` source runs before execution.
7. Servability is answered against every criterion in the closed list, with failures enumerated. Case-02 answers `true`; case-01 answers `false` on the apparatus criterion.
8. A schema-invalid `instrument.json` triggers a revision rather than failing the run, and the revision receives every violation at once.
9. No network and no credential: schema and validation tests are pure; the graded-run tests use the in-process provider.

## Out of scope

Executing anything — running the analysis (007b), driving a cohort (007c), serving the page (007d). Writing the `study/` participant bundle, which needs the protocol schema this spec defines and is therefore 007d's. Inferring a protocol the paper does not describe: extraction reads the paper, and where the paper is silent the field is null. Any change to how the reproducibility rate is computed. A protocol schema general enough for every study design in HCI — this one covers what the servability criteria admit, and says so.
