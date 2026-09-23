---
title: "A project can start empty"
status: built
---

# 036 — A project can start empty

**Status:** built. All five criteria hold. `internal/httpapi/empty_project_test.go` drives the three cases the rule now is (titled with no papers is a 201 that reads back empty, neither is a `missing_title` 400 that creates nothing, and a paper still names an untitled project), and `TestCreateProjectNeedsAPaper` was removed with a note pointing at them, because it asserted exactly the requirement this drops.

Criterion 5 needed no work: the project screen has had an empty papers state and its own add-a-paper form since 012, which is the same reason the requirement was obsolete.

**Scope:** creating a project stops requiring a paper. `POST /projects` accepts an empty paper list, and the New-project screen lets somebody name a project and open it with nothing in it yet. What replaces the requirement is a narrower one: a project with no paper must be *named*, because there is nothing to name it after. Backend and frontend. No schema.

**Depends on:** [012](012-a-project-holds-papers.md) (which separated recording a paper from starting a session), [006j](006j-the-new-project-flow.md) (the New-project screen this relaxes), [035](035-a-paper-can-move-to-another-project.md) (a paper can arrive from somewhere else, which is one reason to want an empty project first).

## Problem

`POST /projects` refuses an empty paper list with `missing_papers`, and the New-project screen refuses before it gets that far. That requirement is left over from when creating a project *was* starting work: 006j fused the two acts, so a project with no paper was a run with nothing to run.

012 separated them. A create records papers and enqueues nothing; a member starts a session per paper afterwards, from the project screen, which is also where papers are added. So the requirement now guards nothing: it refuses a container for want of contents it does not act on, and the paper it insists on can be removed a second later from inside the project it just gated.

It also gets in the way of two things that are now normal. Somebody setting up a project for collaborators before deciding what goes in it has to invent a paper. And 035 made a paper something that can *move* into a project, which means the natural order is sometimes "make the project, then move the paper into it", an order this refusal makes impossible.

## Decisions

**The paper list may be empty, and the title then may not be.** The two are not independent: the title falls back to the first paper's reference when nobody names the project, so with no paper there is no fallback and the project would have no name at all. The refusal moves rather than disappearing: `missing_papers` becomes `missing_title`, raised only when *both* are absent.

**The screen says which of the two is missing.** A create with neither is one mistake with two fixes (name it, or add a paper to name it after), and the message names both, because either one resolves it.

**Nothing downstream changes.** A project with no papers already renders: the project screen's papers card has its own empty state and its own add-a-paper flow, because 012 made adding a paper a thing done from inside a project rather than at its creation. Publishing already gates on sessions, not papers. This spec removes a check; it does not teach anything new about emptiness.

## Acceptance criteria

1. `POST /projects` with a title and no papers creates the project and returns 201.
2. `POST /projects` with neither a title nor a paper is refused with `missing_title`, and creates nothing.
3. `POST /projects` with papers and no title keeps 006j's behavior: the title is the first paper's reference, and enrichment may still replace it.
4. The New-project screen submits with no paper rows filled in, provided the project is named, and says what is missing when it is not.
5. The project opens with an empty papers card that offers the add-a-paper flow.
