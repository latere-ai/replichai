# 007c — The synthetic pilot

**Status:** draft
**Scope:** the `synthetic` data source — driving a cohort through an extracted protocol without any people, in two sub-kinds: a deterministic data generator that shakes the pipeline down, and a model cohort that answers the protocol's task. Plus the labelling that keeps neither from ever being read as evidence about people. A child of [007](007-from-reproduction-to-replication.md).
**Depends on:** [007b](007b-experiments-over-instruments.md) (the experiment model and runner), [007a](007a-the-study-protocol-as-an-artifact.md) (the protocol and its reported ground truth).

## Problem

A researcher who reads a reproduction and decides to run the study is about to spend money and other people's time on an apparatus nobody has ever exercised, an analysis pipeline that has only seen the original authors' data, and a protocol reconstructed from a paper that was not written to be reconstructed. The first real participant is a terrible place to discover that trial four never records a response.

That is the honest case for a cohort with no people in it, and it is a smaller case than the phrase "simulate the study" suggests. So this spec is unusually explicit about what a synthetic cohort is for:

| Legitimate use | Why it holds |
|---|---|
| Apparatus and pipeline shakedown | An end-to-end run producing data of the declared shape debugs the page and the analysis before a human sees either. This is the primary value. |
| Positive control on the analysis | A cohort built to carry a *stated* effect tests whether the pipeline detects an effect it is known to contain. This is a claim about the instrument, not the paper. |
| Protocol comprehensibility | Instructions a model cannot follow are a weak but real signal that participants will struggle too. |
| Calibration as its own research question | How closely model cohorts track human data is a question worth measuring, and a platform holding both is well placed to measure it. The answer is about models, not about the paper. |

And what it is **not** for: estimating the paper's effect, standing in for the study, powering a sample-size calculation off a synthetic effect size, or producing anything that appears in a claim about people. [007](007-from-reproduction-to-replication.md) fixed the name for this reason — a **pilot**, which by construction precedes a study and never is one.

## Decisions

**Two sub-kinds, and the cheap deterministic one comes first.**

- `synthetic:generated` — a data generator that reads `protocol.measures` and emits conforming trial data, optionally carrying a specified effect. No model, no credits, deterministic from a recorded seed. This delivers the shakedown and the positive control, which are the two uses that actually carry weight.
- `synthetic:model` — a model cohort: N participants, each a session driven through the protocol's trials, answering as instructed. This is the pilot proper, and its marginal claim over the generator is only about *how a plausible responder behaves*.

Separating them keeps the strong uses from borrowing credibility they do not need from a language model, and the weak use from lending credibility it does not have to the strong ones.

**The label is indelible and travels.** `cohort_kind` is a non-null column, present on the experiment, on every artifact, on every export, and on every API response. The report is `PILOT.md` — never `RESULTS.md` — and it opens with what it does not establish before it shows a single number. A synthetic result that reaches a reader stripped of its label is the one failure mode this spec exists to prevent, so the test is not "the field is set" but "the label survives every path out of the system" ([007](007-from-reproduction-to-replication.md), criterion 4).

**A synthetic comparison is a calibration, and it is a statement about the model.** `Compare` from [007b](007b-experiments-over-instruments.md) is reused — comparing a cohort's statistics against `protocol.reported` is exactly the right operation — but its rendering is not. For a `released` source the comparison reads as re-analysis of the paper. For a synthetic one it reads as *how close a model cohort came to what people did*, which measures the cohort and never the paper. Same arithmetic, different subject, and the `Source` on the `Comparison` is what distinguishes them.

**A model cohort requires ground truth, in the first version.** `synthetic:model` is available only for instruments that have both released data and `protocol.reported` — so every model cohort the platform runs at first can be checked against a real answer immediately.

This is the sequencing argument from [007](007-from-reproduction-to-replication.md) made structural rather than advisory. A synthetic pilot on a paper with no released data produces numbers no one can falsify, which is how this feature would fail. The restriction is a gate to relax deliberately once calibration has been measured on papers where it can be, not a permanent property.

**A model cohort requires a servable protocol.** `servability` from [007a](007a-the-study-protocol-as-an-artifact.md) is the same gate: if the protocol cannot be presented on a screen without an experimenter or a physical rig, a model cannot be driven through it either, and the refusal cites the failed criteria. One gate protects both cohorts, which is the point of having written it as a closed list.

**No demographic personas, and this is a refusal rather than a deferral.** The platform does not assign a model cohort ages, genders, cultures, occupations, or abilities, and does not report subgroup differences over them. Inventing a population and reporting how its strata differ is fabricated participant data with extra steps — the exact thing [`method/rubric.md`](../method/rubric.md):15 fails a run for. Rejected outright, not scoped out for later.

**A pilot has its own unwritten decisions, and it enumerates them.** Every run records the model and its snapshot, the prompt template, the trial ordering and seed per participant, N, and the caps that applied — because a pilot whose configuration is unrecorded is unreproducible in exactly the way the audit's hidden-decisions list exists to expose. The platform holds itself to the standard it grades papers against.

**Cost is capped per run and reported.** N participants times trials is many model calls. Each run declares its N and trial count against a cap, and `provider.Usage` — already carried through the run — is reported on the pilot. A pilot that would cost more than the audit that produced the instrument is refused rather than silently billed to the single shared credential.

## Interface (sketch)

```go
// internal/experiment: the synthetic sources, behind one seam so 007d's human
// cohort is a third implementation rather than a parallel mechanism.
type Cohort interface {
	Kind() string // synthetic:generated | synthetic:model | human
	// Collect produces trial data conforming to the protocol's measures.
	Collect(ctx context.Context, p instrument.Protocol) (Dataset, error)
}

type Generated struct { // no model, no credits
	N      int
	Effect *SpecifiedEffect // nil for a null cohort
	Seed   int64            // recorded; the run is reproducible from it
}

type ModelCohort struct {
	N        int
	Provider provider.Provider // the existing seam; no new backend
	Config   PilotConfig       // model, snapshot, prompt template, ordering seeds, caps
}

// Pilot is what a synthetic run emits. It is not a Result, and the type
// difference is deliberate — nothing that consumes a Result can be handed one.
type Pilot struct {
	Kind        string
	Config      PilotConfig
	Data        Dataset
	Calibration *Comparison // against protocol.reported; a statement about the cohort
	Usage       provider.Usage
}
```

`Pilot` being a distinct type from `Result` is the structural half of the labelling decision: the compiler, not a code review, is what stops a pilot from flowing into a path that reports findings about people.

## Acceptance criteria

1. `synthetic:generated` emits data conforming to `analysis.input`, deterministic from its recorded seed, and the instrument's own pipeline runs on it end to end without a model or a credential.
2. A cohort carrying a specified effect is detected by the instrument's analysis, and a null cohort is not — the positive and negative control, asserted as a pair.
3. The label survives every path out: report, artifact, export, and API response each carry `cohort_kind`, and a test enumerates the output paths rather than checking one of them.
4. `PILOT.md` states what it does not establish before its first number, and no synthetic artifact is named or shaped like a result.
5. A `Pilot` cannot be consumed where a `Result` is expected — asserted by the type, so the boundary cannot erode.
6. A synthetic `Comparison` renders as calibration of the cohort and never as re-analysis of the paper; the two renderings are distinguished by `Source` and both are tested.
7. `synthetic:model` is refused for an instrument lacking released data or `protocol.reported`, and refused for a non-servable protocol with the failed criteria cited. Both refusals are server-side.
8. No demographic or persona attribute reaches a model cohort's prompt, and no subgroup statistic appears in a pilot — asserted against the prompt template and the report.
9. Every pilot records model, snapshot, prompt template, per-participant ordering seeds, N, and caps; a pilot missing any of them is not written.
10. A run exceeding its cap is refused before it starts, and usage is reported on every pilot.
11. `verdict` and `rate` in the 002 manifest are untouched, and no synthetic artifact is eligible for publication into a reproduction repository.
12. Every test runs against the in-process provider — no network, no credential, no model calls in CI.

## Out of scope

Any use of a pilot to plan a real study's sample size. Demographic personas and subgroup analysis — refused above, not deferred. Fine-tuning or prompt-optimising a cohort to match human data more closely: that is training a model to produce a target result, and the target is the thing under study. Multi-turn or conversational protocols, and anything the servability criteria exclude. Publishing calibration findings as research — the platform records them; whether they are a paper is the researchers' business. Relaxing the ground-truth gate, which is a later decision to make on evidence.
