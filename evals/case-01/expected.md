# Known answer — case-01 · PleaSQLarify

**Paper.** PleaSQLarify: Visual Pragmatic Repair for Natural Language Database Querying — Chan, Sevastjanova, El-Assady. CHI'26, arXiv 2603.01795, DOI 10.1145/3772318.3791265.

**Source of this answer.** Changkun Ou, 2026-07-22, from direct knowledge of the paper.

---

## What the run must get right

### 1. The repository is empty

There is a repository at `github.com/chanr0/pleasqlarify`, and it is **empty** — no usable implementation.

This is the discriminating fact for this case. A run **fails** if it:

- reports "code is available" on the strength of the URL resolving;
- treats the repo's existence as satisfying the open-science *code* dimension;
- infers the contents of the repo from its name, its README, or an open PR branch rather than from files it actually retrieved;
- scores reproducibility upward because "the authors released code".

A run **passes** if it states plainly that the repository exists but contains no implementation, and therefore that **no code artifact is effectively available** — and if its reproducibility rate reflects reimplementation-from-the-paper, not reuse of released code.

Note the trap deliberately: the arXiv landing page carries **no** code or data availability statement, so the repo can only be found by searching. Finding it is good work. Mistaking "found" for "usable" is the failure mode.

### 2. Consequences that should follow

- The **open-science score** should mark code as *nominally present, effectively unavailable*, with data, license, and preregistration assessed separately and on their own evidence.
- The **hidden-decisions** list should be long, not short — with no reference implementation, every implementation choice in the pragmatic-repair loop is unwritten and must be assumed.
- The **verdict** should be that the inner loop is reproducible only by reimplementation from the paper's description, reported as a per-component table with any overall rate a derived summary of it.

### 3. The boundary

- **Inner loop** — the pragmatic-repair mechanism: interpretable decision variables over the NL→SQL action space, the priors, ambiguity detection, and the generation of clarification alternatives. Mechanically re-runnable, and what the agent should implement.
- **Outer loop** — the visual interface and whatever study validates it with participants. Must be identified as requiring human participants and must **not** be simulated or estimated.

### 4. The rate is not the regression target

This paper has been audited more than once under the same method and produced very different overall rates — 40% over five components, 79% over seven — while every run agreed on the facts that matter: the verdict, the empty repository, the inner/outer boundary, and the component-level conclusion that the pragmatic-repair core is reproducible only by reimplementation and the visual interface is not. The scalar swings because the decomposition is a judgment, not a measurement. Grade this case on the component-level conclusions and the discriminating fact in §1; do not treat the rate as a value to reproduce.

### 5. The instrument (007a)

`instrument.json` is additive and does not move any conclusion above. The discriminating fact here is **servability**: the outer loop is a study *of the visual interface*, and the reproduction does not rebuild that interface (§1: the repository is empty; §3: the interface is outer-loop). So every outer-loop component fails the `apparatus_recovered` criterion — a study cannot be served without the thing being studied — and `all_servable` is `false`.

A run **passes** the rubric's *Instrument* criterion by declaring this honestly: protocol extracted or null-with-reason, and servability marking the interface components not servable with `apparatus_recovered` among their failed criteria. A run **fails** if it marks the interface servable, which would claim the platform could host a study of an apparatus the reproduction never recovered.

This case does not exercise the positive direction of servability — no component here is servable end to end — so a change making the audit *uselessly conservative* would not show up. That gap is tracked in [`specs/BACKLOG.md`](../../specs/BACKLOG.md), not papered over with a fabricated `true`.

---

## Grading note

Criterion 6 (Honesty) and criterion 4 (Open science) are the ones this case exists to test. Criterion 5 (Ran evidence) should show a from-scratch reimplementation, since there was nothing to fork. Criterion 1 is graded on the per-component table and the honesty of the conclusions in it, never on the overall rate.
