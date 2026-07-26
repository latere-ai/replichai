# Known answer — case-02 · Short-Form Videos / Prospective Memory

**Paper.** Chiossi, Haliburton, **Ou**, Butz, Schmidt — *Short-Form Videos Degrade Our Capacity to Retain Intentions: Effect of Context Switching On Prospective Memory*. CHI '23, DOI 10.1145/3544548.3580778, arXiv 2302.03714.

**Status of this file.** Drafted 2026-07-22 from run 2's independently verified findings. Changkun Ou is an author of this paper — **his confirmation or correction supersedes everything below.**

---

## Why this case exists

It is the mirror of [case-01](../case-01/expected.md). Case-01 tests whether the agent over-credits a repository that exists but is empty. This case tests the opposite: whether the agent **under-credits a paper that genuinely practises open science** just because most of the paper is a human study.

A run **fails** if it:

- mechanically penalises the reproducibility rate because the paper is a user study;
- classifies the statistical analysis as outer loop merely because the *data* came from people;
- fails to locate the released artifacts (they exist on three hosts and are not hard to find);
- claims to have reproduced the analysis without actually running it against the released data.

A run **passes** if it reaches a **high** inner-loop rate, correctly separates re-analysis from re-observation, and grounds the rate in numbers it actually recomputed.

## The discriminating facts

### 1. The artifacts are real and plural

- Code: `github.com/mimuc/media-prospective-memory` (GPLv3, `LICENSE` present) and OSF `kzxy7`.
- Data: trial-level `rt.csv` (21,480 trials, 60 participants), plus `acc.csv`, `ddm.csv`, `q.csv` — on GitHub, OSF, **and** a DOI-minted DaRUS archive (`10.18419/darus-3327`).
- Supplementary: questionnaire PDF and the YouTube interruption video list on OSF.

Expected open-science score: **high** — code ✅, data ✅, code license ✅, supplementary ✅.

### 2. The two genuine gaps

- **The PsychoPy experiment program was never released.** So the data-*generating* step is un-auditable even though everything downstream is open. A good run names this.
- **No preregistration.** The directional hypothesis is stated a priori in the text but was not registered. The paper does not claim otherwise.

### 3. The boundary

- **Outer loop** — recruitment (N=60), random assignment to Rest/Twitter/YouTube/TikTok, the 10-minute interruption on participants' own feeds, behavioural data collection, questionnaire self-report, and the central claim itself ("TikTok degrades PM"), which can only be re-established with a new sample.
- **Inner loop** — everything from the released data onward: accuracy preprocessing, the accuracy LMM, the RT GLMM, the DDM fitting, the Table 1 ANOVA, the questionnaire ANOVAs.

The subtle call: the **PsychoPy program is software but its output is human data**. Either classification is defensible if justified; what matters is that the run *notices the ambiguity* rather than sorting it silently.

Reproducing the analysis is **not** the same as re-validating the effect. A run that conflates the two fails criterion 1, however good its arithmetic.

### 4. What reproduces, component by component

The load-bearing expectation is the per-component conclusion, not a single number: accuracy preprocessing, the accuracy LMM, the DDM fitting, the Table 1 ANOVA, and the questionnaire ANOVAs all reproduce from the released data; the reaction-time GLMM is the one that resists — Run 2 could not recover the reported null without the exact Gamma/log crossed-random-effects specification chosen by BIC. A run that reaches those component-level conclusions passes, whatever overall rate it reports. Do not treat any particular rate (Run 2's was 5/6) as a target; the number depends on how the analysis is sliced and is not comparable across papers or runs. The shortfall must come from specific model-specification gaps, never from "it's a study paper".

## Open items for the author to confirm

1. Is the RT GLMM's reported null robust, or is it genuinely conditional on the Gamma/log crossed-random-effects specification chosen by BIC? Run 2 found a naive Gaussian LMM recovers significant terms.
2. Data license: GitHub/OSF say **CC-0**, DaRUS says **CC BY 4.0**. Which is authoritative?
3. The text says "one video out of ten"; the OSF list contains **11** URLs.
4. Was the omission of the PsychoPy program deliberate, or an oversight?
