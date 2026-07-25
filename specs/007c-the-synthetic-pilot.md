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
| Distribution and sensitivity exploration | Sweeping the instrument across parameter ranges, condition orderings, and response distributions the paper never sampled, to find where its analysis becomes unstable. A finding about the *instrument's* behaviour, cheap to run because no one is inconvenienced by it. |
| Protocol comprehensibility | Instructions a model cannot follow are a weak but real signal that participants will struggle too. |
| Calibration as its own research question | How closely model cohorts track human data is a question worth measuring, and a platform holding both is well placed to measure it. The answer is about models, not about the paper. |

Every row is a claim about the **instrument**, never about the paper or about people, and every one of them happens *after* an audit is finished and graded. Nothing here flows back into an audit: [007](007-from-reproduction-to-replication.md) makes that a package-direction rule the import graph enforces, precisely because a synthetic cohort quietly informing a boundary classification would be the same violation as writing `verdict` outright.

And what it is **not** for: estimating the paper's effect, standing in for the study, powering a sample-size calculation off a synthetic effect size, or producing anything that appears in a claim about people. [007](007-from-reproduction-to-replication.md) fixed the name for this reason — a **pilot**, which by construction precedes a study and never is one.

## Decisions

**Two sub-kinds, and the cheap deterministic one comes first.**

- `synthetic:generated` — a data generator that reads `protocol.measures` and emits conforming trial data, optionally carrying a specified effect. No model, no credits, deterministic from a recorded seed. This delivers the shakedown and the positive control, which are the two uses that actually carry weight.
- `synthetic:model` — a model cohort: N participants, each a session driven through the protocol's trials, answering as instructed. This is the pilot proper, and its marginal claim over the generator is only about *how a plausible responder behaves*.

Separating them keeps the strong uses from borrowing credibility they do not need from a language model, and the weak use from lending credibility it does not have to the strong ones.

**The label is indelible and travels.** `cohort_kind` is a required field on the experiment record — and a `NOT NULL` column when 006b's schema lands — present on every artifact, every export, and every API response. The report is `PILOT.md` — never `RESULTS.md` — and it opens with what it does not establish before it shows a single number. A synthetic result that reaches a reader stripped of its label is the one failure mode this spec exists to prevent, so the test is not "the field is set" but "the label survives every path out of the system" ([007](007-from-reproduction-to-replication.md), criterion 5).

**A synthetic comparison is a calibration, and it is a statement about the model.** `Compare` from [007b](007b-experiments-over-instruments.md) is reused — comparing a cohort's statistics against `protocol.reported` is exactly the right operation — but its rendering is not. For a `released` source the comparison reads as re-analysis of the paper. For a synthetic one it reads as *how close a model cohort came to what people did*, which measures the cohort and never the paper. Same arithmetic, different subject, and the `Source` on the `Comparison` is what distinguishes them.

**A model cohort requires ground truth, in the first version.** `synthetic:model` is available only for instruments that have both released data and `protocol.reported` — so every model cohort the platform runs at first can be checked against a real answer immediately.

This is the sequencing argument from [007](007-from-reproduction-to-replication.md) made structural rather than advisory. A model cohort on a paper with no released data produces numbers no one can falsify, which is how this feature would fail. The restriction is a gate to relax deliberately once calibration has been measured on papers where it can be, not a permanent property.

**The gate binds `synthetic:model` only, and that is why it costs little.** `synthetic:generated` is ungated: it is declared synthetic data from a declared seed, claiming nothing about responders, so there is nothing to falsify and nothing to calibrate. Which means the distribution and sensitivity sweeps — the use with the widest appetite for runs — are served by the *unrestricted, free, deterministic* half of this spec, and the gate applies only where a model's behaviour is being read as informative. The restriction lands on the one use that needs ground truth and leaves the others alone.

**A model cohort requires servable *components*, not a servable protocol.** [007a](007a-the-study-protocol-as-an-artifact.md)'s servability is per component, and that resolution matters here: gating on the roll-up would make the feature unreachable. If a component cannot be presented on a screen without an experimenter or a physical rig, a model cannot be driven through it either — but a study with one unservable manipulation and a perfectly servable task still has a task worth piloting.

So a model cohort covers the servable components and refuses the rest, citing their failed criteria, and the pilot states which components it did not cover. A manipulation the page cannot administer is one a model cannot undergo either: describing it in a prompt and recording what the model then does is precisely the fabrication this spec exists to prevent.

This is also what keeps the feature testable. The gate is an intersection — released data, `protocol.reported`, and servability — and gating on `all_servable` would empty it: 007a establishes that *neither* golden case is servable end to end. Per component, case-02 qualifies on its prospective-memory task, so 007c has a real instrument with a real ground truth to be exercised against in CI. A gate that admits nothing is not a strict gate, it is an unbuilt feature.

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
3. A sweep over `synthetic:generated` runs without a gate, a model, or a credential, and reports where the instrument's analysis becomes unstable across the swept range. Every finding is stated about the instrument; none is stated about the paper or about people.
4. No pilot of any sub-kind reaches an audit. Asserted by [007](007-from-reproduction-to-replication.md)'s import-graph test and by re-auditing a paper after pilots have run against its instrument and getting the same verdict.
5. The label survives every path out: report, artifact, export, and API response each carry `cohort_kind`, and a test enumerates the output paths rather than checking one of them.
6. `PILOT.md` states what it does not establish before its first number, and no synthetic artifact is named or shaped like a result.
7. A `Pilot` cannot be consumed where a `Result` is expected — asserted by the type, so the boundary cannot erode.
8. A synthetic `Comparison` renders as calibration of the cohort and never as re-analysis of the paper; the two renderings are distinguished by `Source` and both are tested.
9. `synthetic:model` is refused for an instrument lacking released data or `protocol.reported`, and drives only the servable components — an unservable component is skipped with its failed criteria cited and named in the pilot as not covered. Refusals are server-side.
10. The gate admits case-02's prospective-memory task, so the model-cohort path is exercised in CI against an instrument with real ground truth. A gate that admits no golden case fails this criterion.
11. No demographic or persona attribute reaches a model cohort's prompt, and no subgroup statistic appears in a pilot — asserted against the prompt template and the report.
12. Every pilot records model, snapshot, prompt template, per-participant ordering seeds, N, and caps; a pilot missing any of them is not written.
13. A run exceeding its cap is refused before it starts, and usage is reported on every pilot.
14. `verdict` and `rate` in the 002 manifest are untouched, and no synthetic artifact is eligible for publication into a reproduction repository.
15. Every test runs against the in-process provider — no network, no credential, no model calls in CI.

## Out of scope

Any use of a pilot to plan a real study's sample size. Demographic personas and subgroup analysis — refused above, not deferred. Fine-tuning or prompt-optimising a cohort to match human data more closely: that is training a model to produce a target result, and the target is the thing under study. Multi-turn or conversational protocols, and anything the servability criteria exclude. Publishing calibration findings as research — the platform records them; whether they are a paper is the researchers' business. Relaxing the ground-truth gate, which is a later decision to make on evidence.
