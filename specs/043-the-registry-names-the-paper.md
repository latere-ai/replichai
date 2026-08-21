# 043 — The registry names the paper it reproduces

**Status:** designed.

**Scope:** the public list — `GET /public`, and the two screens that read it: the landing's published strip and the `/registry` screen. No new storage: every fact this puts on a card is already in `project_papers`.

**Depends on:** [037](037-a-paper-carries-its-bibliography.md) (the bibliography this reads), [028](028-the-registry-opens.md) (the list it goes on), [033](033-the-landing-rebuilt-from-the-design.md) (the cards).

## Problem

A registry card says three things: a title, who signed the reproduction, and how many sessions it ran. A reader arriving from nowhere is deciding whether to open one, and the question they are deciding on — *which paper is this, and whose* — is the one thing the card does not answer.

The answer is already stored. 037 gave every paper a `Record`: authors, venue, year, and the rest of a citation. The project screen renders it under each paper's title. The list does not, for one reason: papers are read a project at a time (`GET /projects/{id}/papers`), and `GET /public` returns `ProjectView`, which has never carried them. Six cards on an anonymous landing page cannot each open a second request to find out what they are about.

The project's own title is not a substitute. It is a name somebody typed. Several read as the paper's title today because the owner pasted one in, and one of them reads `Replication "Point of no Undo" and "PinchCatcher"` — a project of two papers, which is exactly the case a single citation line under a single title would misreport.

## Decisions

**The list carries every paper, trimmed.** `ProjectView.papers` is a list, because a project holds a list (012) and one citation under a two-paper project is a false attribution. Each entry is four fields — title, authors, venue, year — and not `project.Paper` verbatim: `abstract` is paragraphs, and `ref`, `added_by`, `added_at`, `edited` are bookkeeping for the owner's screen. A landing page must not ship six abstracts to say six citations.

**One query for the whole list.** `PapersByProjects(ctx, ids)` on the project store, returning a map keyed by project id, joined in `projectViews` beside `StatusByProjects` and `ModifiedAt`. The alternative — `ListPapers` in a loop — is an N+1 on the one route the internet hits unauthenticated.

**A bibliography is a public fact.** It is populated outside the `stake` branch that gates `spend_30d`. The redaction in `projectViews` withholds the owner's opaque subject; what a published project is a reproduction *of* is the thing publishing published. The anonymous reader is the reader this is for, so the guarding test calls `GET /public` with no principal — an owner-authenticated test would pass while the landing rendered blank.

**A card names the first paper and counts the rest.** One citation line, then `+N more` when there are others. The line is the same shape the project screen already uses: up to three authors then *et al.*, venue, year, separated by dots.

**A record nobody could fill in renders nothing.** No line, no dots, no "unknown". `NeedsDetails` exists because an empty record is ordinary, and the strip's own rule is that a thing which cannot make a true claim makes none. The project screen's "add the details" invitation stays there: it is addressed to the owner, who can act on it, and a stranger cannot.

**The join becomes a function.** `citation(record)` moves into `papers.ts` with unit tests, and the existing `Citation` component renders what it returns. Three surfaces formatting a citation inline in JSX is how one of them drifts.

## Criteria

1. `GET /public` returns each project's papers as `{title, authors, venue, year}`, and never an abstract or an `edited` list.
2. A caller with no session gets them — asserted by a test that sends no principal.
3. The list is built with one store call for all projects, not one per project.
4. A landing card shows the first paper's authors, venue and year under the project title, and `+N more` when the project holds more than one.
5. A `/registry` row shows the same line, beside the signer and the session count it already carries.
6. A project whose papers have no record renders no citation line and no stray separators.
7. `citation()` lives in `papers.ts` and is unit-tested for: no record, authors only, four authors (*et al.*), venue and year only, year only.
8. `frontend/test/landing.test.mjs` asserts 4 and 6 in a real browser.

## Not in scope

The registry's JSON-LD. `reportLD` already describes a project page's papers to a crawler (042a); the collection page describes itself and not its members, and changing that is 042's follow-up, not this one.
