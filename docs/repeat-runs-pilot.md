# What repeat runs of one paper showed

Three papers were audited ten to twenty-one times each on 10 and 11 September
2026, all under one method, meaning one set of instructions and one rubric
(digest `sha256:f9af95bb…`). This page records what
those runs show about the idea that run-to-run variation measures what a paper
left unsaid. The data was analyzed on 9 October 2026.

**The short answer:** the runs vary a lot, but most of the variation traced so
far comes from the agent system, the network, how the agent labels its own
results, and the grader's revisions. The paper's own silence is a real part of
it, and a small one. A design that reads the spread as a property of the paper
has to remove or measure those other sources first.
[`dispersion-model.md`](dispersion-model.md) proposes the measurement, and
[spec 046](../specs/046-one-run-is-one-draw.md) the platform changes.

## The runs

| Paper | Called below | Runs started | With a verdict |
|---|---|---|---|
| Johnson, Sengupta, Schöning and Hecht (2016). *The Geography and Importance of Localness in Geotagged Social Media.* CHI '16, 515–526. [10.1145/2858036.2858122](https://doi.org/10.1145/2858036.2858122) | Geography | 10 | 10 |
| Ou (2026). *Progressive Autonomy as Preference Learning: A Formalization of Trust Calibration for Agentic Tool Use.* [arXiv:2605.19151](https://arxiv.org/abs/2605.19151) | Progressive autonomy | 18 | 6 |
| Kay, Matuszek and Munson (2015). *Unequal Representation and Gender Stereotypes in Image Search Results for Occupations.* CHI '15, 3819–3828. [10.1145/2702123.2702520](https://doi.org/10.1145/2702123.2702520) | Unequal representation | 21 | 10 |

Each run is its own private repository under `open-hai`, named after the paper
with a four-character suffix. The suffixes below refer to those repositories.

**The runs with no verdict failed at launch.** Eight repositories on the third
paper were created within 20 seconds of each other and hold no commits. Most of
the rest hold only the system's identity commit, written seconds after the
repository was created, with no agent commit after it. Runs started later the
same day completed 16 of 16. The completion rates in the table therefore say
when each paper was run, not how hard it is. No published record says why a
run stopped, so this reading rests on timing, not on a recorded cause.

## Two agent systems ran every paper

Each run's `environment.json` names its harness and model. The pilot mixed
Claude Code on `claude-opus-5` with Codex on `gpt-6-astra`, under the same
method digest. The manifest the registry reads, `replication.json`, records
neither, so the mix is invisible to anything that reads only the manifest.

| Paper | System | Runs | Inner components | Verified rate, mean and SD | Hidden decisions | Outer-loop rows |
|---|---|---|---|---|---|---|
| Geography | Claude Code | 5 | 14 to 22 | 38%, SD 18 pp | 12 to 22 | 2 to 5 |
| Geography | Codex | 5 | 18 to 21 | 28%, SD 5 pp | 17 to 28 | 2 to 4 |
| Progressive autonomy | Claude Code | 3 | 14 to 21 | 61%, SD 10 pp | 10 to 22 | 4 to 5 |
| Progressive autonomy | Codex | 3 | 16 to 18 | 67%, SD 14 pp | 21 | 1 |
| Unequal representation | Claude Code | 5 | 11 to 16 | 73%, SD 9 pp | 13 to 22 | 5 to 6 |
| Unequal representation | Codex | 4 | 12 to 15 | 61%, SD 9 pp | 17 to 24 | 5 |

The verified rate is the share of a run's inner-loop components that the run
marked verified. One run on the third paper left no `environment.json` and is
not in this table.

- **Spread depends on the system.** Within one paper, the component count
  varies about eight times more under Claude Code than under Codex (mean
  variance 10.6 against 1.3).
- **So does the hidden-decision count.** Codex lists four to five more hidden
  decisions per run on the same paper. The paper means barely differ (17.8 to
  19.4), so the count tracks the agent, not the paper.
- **So does the boundary.** On progressive autonomy, Codex puts one component
  in the outer loop and Claude Code puts four or five. The two systems slice
  the paper differently: Codex lists pipeline modules, Claude Code lists claims.
- **The ranking of papers by spread is not stable across systems.** A review
  pass found that the order of the three papers flips between systems on four
  of six measures. With three to five runs per cell this is suggestive, not
  established.

## The same number, read and labeled differently

On *Unequal representation*, the runs mostly computed the same values. What
differed was the paper they compared against and the label they gave the result.

- **Which copy of the paper a run could read.** The Claude Code runs were
  refused by the sandbox's network allowlist when they tried the publisher and
  the authors' pages (m9kk records "NOT FETCHED - the allowlist refused both
  domains"; 5nbs and k38p record the same). They took reference values from the
  authors' rendered notebook instead. The Codex run aysp fetched an author PDF
  through its web reader. The two copies do not agree in every number, so the
  same computation was compared against different reported values.
- **Identical disagreements, different labels.** Three runs (k38p, qzfk, r82q)
  recorded the same disagreement on the "trustworthy" coefficient in the
  paper's Table 1: reported −0.1117, observed −0.0463. Two labeled the claim *partial*, one
  *verified*.
- **Factual items answered differently.** Whether the paper's data was
  released is a fact, yet on progressive autonomy three of six runs say yes and
  three say no. Two of them (c9ks and k46m) describe the same release and
  still answer differently.

A difference of this kind is not underspecification. It would disappear if
every run read the same copy of the paper and the system, not the agent,
decided whether a number matched.

## The grader changes what is measured

Every run is graded inside the loop, and a run that falls short is revised. In
four of the six runs where a revision happened and the history kept both
versions, the revision changed fields the analysis would measure:

- *released* rows grew from 23 to 30 (2mzg), 29 to 30 (grfb) and 36 to 37 (akwe);
- one run's component count went from 11 to 12 and its decisions from 16 to 17 (kpam);
- one run's mismatch count went from 6 to 8 (akwe).

A review pass found that Codex runs took two grading rounds in 10 of 12 cases
and Claude Code runs one round in 10 of 13, so the push toward the rubric is
itself system-dependent. The grader always runs on the worker's model.

## The findings file was often unreadable to the system

The system rejected 14 of the 27 findings files it read when finalizing the
pilot runs: all 12 from Codex and 2 of the 12 from Claude Code. The agents
wrote mismatch values as objects and numbers, or a fetch the sandbox refused as
`not_fetched`, and the schema allowed neither. One mistyped field discarded the
whole file, with a message saying the file was not valid JSON, which was false.
Every file was valid JSON. The model grader passed the schema criterion on all
14.

Schema 2 of `verdict.json` reads what the agents wrote. Run over the 29
findings files the pilot runs published, it accepts 27 and reads all 214 of
their mismatches. The two it still refuses break a real rule, a search recorded
as found with no address, and each is reported at its row with the rest of the
file kept. The schema is described in
[`../method/instructions.md`](../method/instructions.md).

## Freezing the denominator does not remove the swing

[`consultations.md`](consultations.md) §8 proposed fixing the denominator of
the verified rate, because the agent picks its own component count. Holding
each paper's denominator at its mean leaves the spread as large as before
(SD 17, 10 and 12 points across the three papers, against 14, 11 and 10
observed). The numerator moves as much as the denominator.

The runs already extract the paper's own headline results, in
`instrument.json`. Those lists have a stable core and a long tail:

| Paper | Runs with a list | Entries per run | Distinct numbers | In every run | In at least half | In one run only |
|---|---|---|---|---|---|---|
| Geography | 7 of 10 | 9 to 16 | 83 | 10 | 36 | 21 |
| Progressive autonomy | 0 of 6 | none | none | | | |
| Unequal representation | 10 of 10 | 10 to 18 | 94 | 3 | 36 | 24 |

A claim list taken from one extraction would freeze one arbitrary draw. And
*Progressive autonomy* reports simulation means and formal statements rather
than tests, so a list limited to "a test carrying a number" would be empty for
it.

## What is the paper's own silence

A review pass reading the runs side by side found real gaps the paper leaves
open: the tail of a Vuong test, a bootstrap seed, whether the first or the last
selected image was used (5.307 against 5.300), and one contrast. These are the
target of the measurement. In this pilot they are a small share of what moved
between runs.

## What this changes

- One run is one draw. A verdict from a single run should say so.
- Record every facet that can vary between runs in the manifest: system, model,
  network policy, paper copy, grading rounds, and why a run stopped.
- Give every run the same inputs, and let the system decide whether a number
  matched.
- Measure dispersion per claim, against a claim list coded by people.
- Treat the environment as a facet to record and vary on purpose, because it
  moves the result as much as anything else measured here.

## How the numbers were checked

The tables on this page come from
[`tools/repeat-runs/analyze.py`](../tools/repeat-runs/analyze.py), which
re-fetches the runs with `gh` and prints them:

```bash
python3 -I tools/repeat-runs/analyze.py fetch /tmp/pilot \
  geography-and-importance-of-localness-in \
  progressive-autonomy-as-preference-learning-a \
  unequal-representation-and-gender-stereotypes-in
python3 -I tools/repeat-runs/analyze.py report /tmp/pilot
```

The fetch-refusal quotes, the labels on that coefficient, and the
before-and-after revision counts were read from the files directly. Three statements come from
independent review passes and were not recomputed: the ranking flips between
systems, the grading-round counts by system, and the list of genuine gaps.
