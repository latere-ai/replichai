---
title: "What the authors released"
status: built
---

# 021c — What the authors released

**Status:** built. Every acceptance criterion holds. `ReleasedByAuthors` renders above the file browser, `none` and `dead` are toned apart and asserted to be, the scorecard renders all five criteria with the fetched URL or the places searched, and a session without findings renders the browser alone. Covered by `frontend/test/findings.test.mjs` alongside 021b, since both panes read one file and a single mocked session exercises both.

**Scope:** the session's Artifacts pane gains a section above the file browser: every place the run searched for the authors' own artifacts, what it found, and the result. Reads [021a](021a-the-findings-as-data.md)'s `Released` and `Scorecard`.

**Depends on:** [021a](021a-the-findings-as-data.md), [015](015-a-verdict-belongs-to-a-session.md) (the pane's address), [002](002-publishing-a-reproduction.md) (the repository the browser reads).

## Problem

`SessionArtifacts` (`frontend/src/App.tsx:1737`) browses the reproduction repository — what the *agent* produced. The first question an auditor asks is the other one: what did the authors release, and where did the run look? `SOURCES.md` answers it in the repository, in prose, one click away on GitHub and invisible in the app. So the pane presents the reproduction as if it were the only artifact in the story, and a paper whose dataset link 404s looks identical to one that released everything.

## Decisions

**Two sections, authors first.** `WHAT THE AUTHORS RELEASED` sits above `REPRODUCTION REPOSITORY`. The order is the argument: the reproduction exists because of what was and was not released, and reading the repository first inverts that.

**A result is a fact about a search, not a judgement.** Each row is where it looked, what was there, and one of `FOUND · PARTIAL · NONE · DEAD`. `NONE` and `DEAD` are different findings — nothing published versus something published that no longer resolves — and the pane never collapses them, because the second is the one an author can still fix.

**The scorecard sits beside it, not inside it.** Code, data, license, preregistration, and supplement are the five the rubric scores; they render as a compact list with the fetched URL when found and the places searched when not. A criterion marked not-found with no places searched cannot occur — 021a's validator refuses the file.

**The file browser is untouched.** It works, 015 put it at the right address, and this spec adds above it rather than rewriting it.

**No findings means no first section.** The pane renders the repository browser alone, under the same one-line reason 021b uses. It does not render an empty "nothing released" table, which would be a claim the run never made.

## Acceptance criteria

1. With findings, the pane renders the released list above the repository browser, each row carrying where, what, and one of the four results, with the URL linked when present.
2. `NONE` and `DEAD` render distinctly.
3. The scorecard renders all five criteria, with the URL when found and the searched places when not.
4. Without findings, only the repository browser renders, with the stated reason and no empty table.
5. The existing artifact browser behaviour is unchanged, and its tests still pass.
6. A browser test drives 1–4, and fails without the change.

## Not in scope

Re-checking a dead link from the app. Fetching the authors' repository. Anything about the reproduction repository's own contents.
