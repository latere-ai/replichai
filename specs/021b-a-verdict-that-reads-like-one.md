# 021b — A verdict that reads like one

**Status:** designed.

**Scope:** the session's Verdict pane, rebuilt on [021a](021a-the-findings-as-data.md)'s data — the verdict card and its summary, the inner/outer boundary table, a section for what did not reproduce or did not match, and the decisions the paper left unwritten. Carries the app-wide palette retune as its first commit.

**Depends on:** [021a](021a-the-findings-as-data.md) (the data), [015](015-a-verdict-belongs-to-a-session.md) (the pane's address), [006s](006s-reading-a-session-in-time.md) (the session screen it is a tab of).

## Problem

`SessionVerdict` (`frontend/src/App.tsx:1660`) renders a grade word, a rate, and the manifest's metadata. That is the entire answer to "did this paper reproduce". The rubric's per-component table — the thing it says *is* the result — is not on the screen, and the rate that is on the screen is the summary the rubric says must never be read alone.

## Decisions

**The summary leads, the table is the result.** The pane opens with the verdict word, the graded date, and the agent's summary paragraph — then the boundary table immediately below it, because the rubric is explicit that the table is the finding and the rate is derived from it. The rate keeps its place but stops standing alone: it is rendered against `Components` (`8 / 13 verified · 2 partial · 3 blocked`), so the decomposition it depends on is always beside it.

**Inner and outer are typographically different, not just labelled.** An outer row is muted and its outcome column reads `NOT SCORED` — never a tone that could be mistaken for a result. The one rule the product exists to defend has to be visible without reading the label.

**What did not reproduce gets its own section, above the fold of the table.** The design has a boundary table and nothing else, which buries the interesting rows among the verified ones. `MISMATCHES & UNRESOLVED` lists every inner-loop claim that is not cleanly verified — `blocked`, `partial`, or verified-with-a-`Mismatch` — each with its specific blocker or the reported-versus-observed pair and its delta. A run with none says so explicitly rather than rendering an empty box.

**A mismatch prints both numbers.** `91.4% reported · 89.6% observed · −1.8 pp`, in tabular figures, with the kind named. A `typo` or `contradiction` kind renders the same way and is what lets a reader see the paper was wrong rather than the reproduction.

**A session with no findings says what it is.** No `verdict.json` means the pane keeps today's card — verdict, rate, metadata — under one line naming why there is no more: the run predates the findings file, or its file failed validation with the recorded reason. Never an empty table.

**The palette retune lands first, alone.** `--ok --warn --err --run` move to the design's muted family in dark, each with a light twin: the dark values are the design's (`#8fb894`, `#e0bb8c`, `#e0716f`, `#96a5ec`); the light ones are the same hues taken to a usable contrast on paper rather than the dark values reused. One commit, no other change, so the app-wide blast radius is reviewable on its own.

## Acceptance criteria

1. Given findings, the pane renders the verdict word, the graded date, the summary, and `Components` beside the rate.
2. The boundary table renders every claim with its loop, outcome, evidence and citation; outer rows are visually distinct and always read `NOT SCORED`.
3. `MISMATCHES & UNRESOLVED` lists exactly the inner-loop claims that are blocked, partial, or carry a mismatch; a mismatch prints reported, observed, and delta; a run with none renders the explicit empty state.
4. The decisions section renders question, where, assumption, and sensitivity for each decision.
5. A session with no findings renders today's card plus the stated reason, and no empty table.
6. A browser test drives 1–5 against a mocked API in Chrome, and fails without the change.
7. The four semantic tokens are retuned in both themes; the existing browser suite stays green (it asserts geometry and behaviour, not hue).

## Not in scope

Editing findings. Comparing two sessions' boundaries. Linking a claim to the transcript event that produced it (the design's `at:` field — a good idea, and it needs an event id 021a does not record).
