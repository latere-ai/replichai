# 007a — The study protocol as an artifact

**Status:** implemented
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

**Servability is a closed criteria list, answered per component.** "Highly replicable" becomes a decision the audit makes against declared, mechanical criteria rather than a judgement call. A component is servable only if all hold:

| Criterion | Excludes |
|---|---|
| Single session, no follow-up | longitudinal and diary studies |
| Screen-based, no physical apparatus | eye trackers, EEG, VR rigs, tangible devices |
| No experimenter or confederate in the loop | interviews, think-aloud, Wizard-of-Oz, moderated tasks |
| Every measure capturable in a browser | physiological signals, observation, video coding |
| **The manipulation is administrable by the page** | conditions the participant must carry out elsewhere, in software we neither control nor can verify |
| The apparatus is recovered in `src/` or specified completely enough to build | studies of a system the reproduction could not rebuild |

**Per component, not per protocol**, because the answer is almost never uniform and the uniform answer throws away the useful part. `REPRODUCIBILITY.md`'s boundary table is already component-wise; servability is the same decomposition carried one step further, and it tells a researcher exactly which parts of their study a hosted page can run and which they must administer themselves. A roll-up field says whether *every* outer-loop component is servable — that is what [007d](007d-hosting-a-study.md) gates a fully hosted run on — but the per-component answer is the deliverable.

Two criteria do most of the deciding. The apparatus one: a paper's mechanism may be reproducible while the *interface* participants actually saw is not, and a study cannot be served without the thing being studied. And the manipulation one, which is easy to miss because the *measures* look servable — a task whose reaction times a browser captures perfectly may still assign participants to spend ten minutes in a third-party application, which a page can ask for and can neither deliver nor verify.

Most components will answer `false`. That is the correct and useful answer, and it is the same value the boundary table provides by refusing to overclaim.

**The rubric gains a seventh criterion, and the six existing ones do not move.** The new criterion grades the *declaration*, never the extraction:

> **7. Instrument.** `instrument.json` is present and validates against the schema. Each of `protocol` and `analysis` is either populated from the paper with section, table, or figure references, or explicitly null with a specific reason. `servability` is answered against every criterion. A fabricated protocol, an unstated null, or a schema violation fails.

So a paper with no extractable protocol passes criterion 7 by saying so. This keeps the bar on honesty, where the other six already are, and leaves the audit's *inner-loop verdict* untouched — criterion 7 is graded like the others, and the inner-loop reproduction table it sits beside is unchanged.

**A malformed instrument is a revision trigger, not a failed audit — and the revision is the backend's.** There is no auditor-side grade-and-revise loop: criteria 1–6 are enforced by the backend grading against `rubric.md`, with `MaxRevisions` handed to the run. Criterion 7 joins them, so the same loop [004](004-go-port.md) built feeds an invalid or dishonest instrument back to the worker within its revision budget — no new machinery. `instrument.Validate` is the Go half, and it plays two roles neither of which is that loop: the schema [007b](007b-experiments-over-instruments.md) consumes, and a **finalize-time report** the auditor runs over the written file (and the live path over the committed tree) and surfaces to the caller. Rejected: rejecting the run, which would throw away a good reproduction over a bad JSON file; accepting silently, which would let unvalidated agent-written JSON reach the executors downstream; and folding the finalize report into `replication.json` or the public-eligibility gate, since a malformed JSON file is not a redistribution risk and must not become a publication control.

**Both golden cases gain instrument expectations, and neither is a fully servable study.** This is worth stating plainly rather than discovering during implementation:

- **case-01** — the outer loop is a study *of a visual interface* the reproduction does not rebuild, so its components fail the apparatus criterion however cleanly the protocol extracts.
- **case-02** — the measures are trial-level reaction time and accuracy, exactly what a browser captures, so the prospective-memory task's *apparatus* is servable. But assignment sends participants to spend ten minutes on their own feeds in Rest / Twitter / YouTube / TikTok ([`evals/case-02/expected.md`](../evals/case-02/expected.md):39), which fails the manipulation criterion: a page can ask and can neither administer nor verify. So the roll-up is `false` and the per-component answer is the informative one — *this task we can host; this manipulation you must administer*.

Which means the pair discriminates in only one direction here. A change making the audit credulous about servability shows up immediately in both. A change making it uselessly conservative shows up in neither, because there is no case in the golden set whose outer loop is servable end to end. **That gap is a third eval case to add**, chosen for a fully hostable study, and until it exists the conservative direction is unguarded — which is the honest state of the regression set, not a property to paper over by asserting a `true` the papers do not support.

**`instrument.json` must join the publish gate's worker-written allowlist.** `publish.Classify` is an *allowlist*: `internal/publish/gate.go` includes `OriginWorker` and withholds everything else, and `originOf` recognises exactly four fixed report names plus `src/`. A new top-level deliverable is therefore withheld from every publication by default — silently, because withholding is the safe direction and produces no error. So `originOf` gains `instrument.json`, and a test asserts the file survives a publish. Overlooking this would ship an audit whose new artifact never reaches a single published repository.

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
	// Per component, keyed as the boundary table names it. The roll-up below is
	// derived, never asserted independently.
	Components []ComponentServability `json:"components"`
	AllServable bool                  `json:"all_servable"` // every outer-loop component
}

type ComponentServability struct {
	Component string   `json:"component"`
	Servable  bool     `json:"servable"`
	Failed    []string `json:"failed_criteria"` // from the closed list above
	Why       string   `json:"why"`
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
7. Servability is answered for every outer-loop component in the boundary table against every criterion in the closed list, with failures enumerated. `all_servable` is derived from the components, never set independently — asserted by constructing a mixed set and checking the roll-up.
8. The golden cases assert the answers the papers support: case-01 fails the apparatus criterion; case-02's PM task is servable while its feed manipulation fails the manipulation criterion, so its roll-up is `false`. Neither is asserted `true` end to end, and the missing positive-direction case is recorded as an open eval gap rather than fabricated.
9. `instrument.json` survives a publish on both paths — `originOf` classifies it `OriginWorker` on the offline path, and the mount-path verifier `VerifyTree` does not flag it — asserted against `internal/publish` directly, because an allowlist withholds a new file silently.
10. `Validate` reports every schema violation in one pass, so a single revision can fix them all — asserted directly. A schema-invalid `instrument.json` is surfaced at finalize as a report, never a failed run; criterion 7 in the rubric is what triggers the backend's revision.
11. No network and no credential: schema and validation tests are pure; the graded-run tests use the in-process provider.

## Out of scope

Executing anything — running the analysis (007b), driving a cohort (007c), serving the page (007d). Writing the `study/` participant bundle, which needs the protocol schema this spec defines and is therefore 007d's. Inferring a protocol the paper does not describe: extraction reads the paper, and where the paper is silent the field is null. Any change to how the reproducibility rate is computed. A protocol schema general enough for every study design in HCI — this one covers what the servability criteria admit, and says so.
