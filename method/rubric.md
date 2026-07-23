# Definition of done — reproduction audit

Grade the run against each criterion independently. A criterion passes only if the evidence for it is present in the output files.

1. **Verdict.** `REPRODUCIBILITY.md` opens with an explicit inner-loop reproducibility verdict *and* a rate, and the rate is derived from a stated denominator — which components were counted, how they were counted, what each scored. A rate asserted without that decomposition fails.

2. **Boundary.** Every substantive component of the paper is classified **inner loop** (mechanically reproducible) or **outer loop** (requires human participants), each with a one-line justification and a section or figure reference. The document also states explicitly what a follow-up human study would have to cover for the outer-loop parts.

3. **Hidden decisions.** At least five unwritten decisions are enumerated. Each one gives: where the paper leaves it open, what the reproduction assumed instead, and how sensitive the result is to that choice.

4. **Open science.** Code, data, license, preregistration, and supplementary artifacts are each scored with either a URL that was successfully fetched, or an explicit "none found" naming the places searched.

5. **Ran evidence.** `src/` contains a best-effort implementation of the inner loop that was actually executed at least once, with the exact command and its real output recorded. Anything not runnable appears in `UNVERIFIED.md` with a specific blocker — "the paper does not specify which benchmark split" is a passing blocker; "did not attempt" is not.

6. **Honesty.** Every claim about the paper's contents cites a section, figure, or table. No fabricated numbers, no invented participant data, no simulated user-study results, no artifact claimed without a verified URL.
