# Procedure

What one audit does, in order, and why each step is shaped the way it is.

Steps 1–7b are the agent's work, and they are the audit whether or not it is published. Publishing wraps them: identity and a repository before, verification and attestation after.

## 0. Before the agent starts (publishing runs only)

Resolve the paper to a stable identity, ask the registry whether this paper has been reproduced before — a prior reproduction is reported with its date, its method digest, and its verdict, so an operator knows whether they are re-running or duplicating — allocate a name that cannot collide with a sibling, and create the repository private.

The repository is then mounted into the sandbox, and the agent commits into it as it works. Its history is part of the result: a reproduction that arrives as one squashed drop says nothing about how it was reached.

That makes the workspace boundary load-bearing. The agent writes its reproduction inside the mounted repository and keeps every download — the paper PDF, datasets, benchmarks, anything third-party — outside it, in `/tmp`. Nothing filters the push, so this discipline is what keeps the repository publishable, and it is checked afterwards rather than assumed.

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

List the choices a reimplementation must make but the paper never states: hyperparameters, prompt text, model versions and snapshot dates, data filtering and splits, thresholds, tie-breaks, normalisation, seeds, stopping conditions, interface defaults, exclusion rules, scale scoring, covariates entered into a model.

Each entry carries three things — where the paper leaves it open, what the reproduction assumed instead, and how sensitive the result is to that choice. The third is what makes the list useful: a dozen low-sensitivity gaps matter less than one high-sensitivity gap that silently decides the headline result.

## 5. Implement and run the inner loop

Write real code. Run it at least once and record the exact command and its real output. A smoke test on synthetic or small public data is sufficient when the paper's own data is unavailable.

Where reproduction hits a wall, the wall is the finding: "blocked because the paper does not specify which benchmark split" is a legitimate result. "Did not attempt" is not.

## 6. Score open science

Code, data, licence, preregistration, supplementary artifacts — each with a URL that was actually fetched, or an explicit "none found" naming where the search went.

Worth scoring separately: whether released code still *runs*. Code that exists but has rotted against its own dependencies is a different, and much more common, condition than code that was never published.

## 7. Write the verdict

A per-component reproduction table for the inner loop: each component the boundary marked inner, scored *reproduced*, *partial*, or *blocked*, with the evidence or the specific blocker. This table is the load-bearing result, not a single number.

How many components an inner loop has is itself a judgment, so the scalar rate is treated as a summary, not a measurement. The same paper, audited twice under an identical method, honestly decomposes into different counts — in one pair of runs, five and seven — and any rate derived from it swings with the slicing (there, 40% and 79%) while the verdict, the boundary, and the component-level conclusions stay put. A rate is therefore reported only as a derived footnote to the table, labelled as a summary of one particular decomposition, and never compared across papers or across runs. The rubric grades the table, not the number.

The verdict must also separate **re-analysis** from **re-observation**. Reproducing a paper's statistics from its released data does not re-validate its claim about the world; that claim rests on the outer loop and on the sample that was collected. An audit that blurs the two overstates what it proved.

## 7b. Write the findings as data

Everything in step 7 is also written as `verdict.json` — the boundary rows with their loop, outcome, evidence or blocker and citation; the hidden decisions with their sensitivity; the open-science scorecard; and every artifact search with its result — and the study half as `instrument.json`: the protocol, the analysis entrypoint contract for `src/`, and a per-component servability assessment.

The rule between the two forms is that the prose is not authoritative. If `verdict.json` and `REPRODUCIBILITY.md` disagree, the markdown is wrong. Writing the finding as data is what lets a reader get the result without opening a file, and it is what the app's verdict screen renders; the prose is the argument around it.

Both are graded, and both grade the *declaration* only. A paper with no extractable protocol passes `instrument.json` by saying so with a reason, and a missing or invalid `verdict.json` is recorded without changing the inner-loop verdict — the run still publishes, and it still fails that criterion.

## Grading

Every run is graded against `method/rubric.md` — eight binary criteria covering the verdict, the boundary, the hidden decisions, the open-science score, evidence that code was actually run, honesty of citation, and the two machine-readable files that carry the same findings as data. The worker revises and is re-graded up to its revision budget.

## 8. Finalize (publishing runs only)

Everything so far was authored by the agent. What follows is authored by the system, outside the sandbox, and that separation is the whole reason the record is worth anything.

Read the tree the agent actually committed and check it for third-party content. Fetch the run's event log from the backend and commit it — the evidence behind the verdict, not the agent's account of it — as `transcript.jsonl`, the canonical stream, and `TRANSCRIPT.md`, an index over chunked narrative parts, all in one atomic commit. Write `replication.json`: the paper's identity, the repository, the digest of the method that produced the run, the verdict, the decomposed rate when one could be read out of the report, and whether the redistribution check cleared — the manifest is what the registry reads, so this is also how the next reproduction of this paper learns that this one happened. Then invite the requesting login as a collaborator, which is how ownership is granted — by invitation, never by handing the service someone's credentials.

Finalizing is idempotent. The manifest and the transcript are compared against what is already committed and rewritten only when they differ, so re-running it costs nothing and leaves no redundant commit.

Because the agent commits independently of the process that started it, finalizing does not have to happen in the same breath as the run. `audit --publish --detach` starts the run and prints the `finish` command; `finish` reattaches to the session, waits for it to settle, and performs this step. That is also the recovery path when a long run outlives its CLI.

## Regression

`evals/` holds papers whose true answer is known. The two current cases test opposite failure modes:

| Case | Condition | Failure it catches |
|---|---|---|
| 01 | Repository exists but is empty | Crediting code that was promised but not delivered |
| 02 | Data and analysis genuinely released | Penalising a study paper for being a study |

Run both after any change to the method. A change that games one case tends to show up in the other, which is why they are chosen to pull in opposite directions.
