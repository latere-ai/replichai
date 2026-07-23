# Procedure

What one audit does, in order, and why each step is shaped the way it is.

## 1. Acquire the paper

Fetch the reference. If only an abstract comes back, download the PDF and extract the full text. Read all of it — appendices, footnotes, the data-availability statement, acknowledgements.

The artifact pointer is frequently *not* where you would expect. In one audited paper the only code link was a page-one footnote; the landing page carried no availability statement at all. A procedure that reads landing pages and stops will report "no code released" for papers that did release code.

## 2. Hunt for artifacts, and record where you looked

Search the paper's own footnotes and availability statement, the preprint page and its ancillary files, the publisher's artifact and supplementary tabs, the authors' personal and lab pages, code-hosting sites by author name and by system name, and the open research repositories.

Two rules make this useful rather than decorative:

- **"Nothing found" is a finding**, but only after a search you can list. The search log is part of the deliverable.
- **Existence is not availability.** A repository that resolves may still be empty, may contain an unmerged draft unrelated to the paper, or may be code that no longer runs. Each of those is a different score.

## 3. Split the inner loop from the outer loop

Decompose the paper into its substantive components — each algorithm, module, dataset, and evaluation — and place each on one side of the line, with a justification and a section or figure reference.

A component is **outer loop** only if it genuinely requires human participants. Not because it is expensive, not because it is underspecified, not because the data originally came from people. The frequent subtle case: an evaluation that reads like human-in-the-loop but whose "user" is a deterministic oracle answering from gold labels. That is a simulation over data, and it belongs to the inner loop.

Getting this wrong in either direction ruins the verdict. Too much marked outer, and the paper looks unreproducible when it is not. Too much marked inner, and the audit implies a machine could have validated a claim only people can validate.

## 4. Enumerate the hidden decisions

List the choices a reimplementation must make but the paper never states: hyperparameters, prompt text, model versions and snapshot dates, data filtering and splits, thresholds, tie-breaks, normalisation, seeds, stopping conditions, interface defaults.

Each entry carries three things — where the paper leaves it open, what the reproduction assumed instead, and how sensitive the result is to that choice. The third is what makes the list useful: a dozen low-sensitivity gaps matter less than one high-sensitivity gap that silently decides the headline result.

## 5. Implement and run the inner loop

Write real code. Run it at least once and record the exact command and its real output. A smoke test on synthetic or small public data is sufficient when the paper's own data is unavailable.

Where reproduction hits a wall, the wall is the finding: "blocked because the paper does not specify which benchmark split" is a legitimate result. "Did not attempt" is not.

## 6. Score open science

Code, data, licence, preregistration, supplementary artifacts — each with a URL that was actually fetched, or an explicit "none found" naming where the search went.

Worth scoring separately: whether released code still *runs*. Code that exists but has rotted against its own dependencies is a different, and much more common, condition than code that was never published.

## 7. Write the verdict

A reproducibility rate for the inner loop, with a stated denominator: which components were counted, how they were counted, what each scored. A rate that cannot be decomposed back into its components is not a measurement, and the rubric fails it.

The verdict must also separate **re-analysis** from **re-observation**. Reproducing a paper's statistics from its released data does not re-validate its claim about the world; that claim rests on the outer loop and on the sample that was collected. An audit that blurs the two overstates what it proved.

## Grading

Every run is graded against `method/rubric.md` — six binary criteria covering the verdict, the boundary, the hidden decisions, the open-science score, evidence that code was actually run, and honesty of citation. The worker revises and is re-graded up to its revision budget.

## Regression

`evals/` holds papers whose true answer is known. The two current cases test opposite failure modes:

| Case | Condition | Failure it catches |
|---|---|---|
| 01 | Repository exists but is empty | Crediting code that was promised but not delivered |
| 02 | Data and analysis genuinely released | Penalising a study paper for being a study |

Run both after any change to the method. A change that games one case tends to show up in the other, which is why they are chosen to pull in opposite directions.
