---
title: "The project rail"
status: built
---

# 021e — The project rail

**Status:** built. The rail renders one `LATEST SESSION · INNER LOOP` card per paper with a graded session, `Publish…` sits on the project header, and `SPEND · LAST 30 DAYS` reads a server-computed windowed aggregate. `job.Store.SpendSince` is implemented in both stores with a contract test that was mutation-checked two ways — a store filtering on `created_at` and one summing zero each produce their own named failure.

Two things came out of the build. **The rail's predicate had to be `done` and a repository, not *terminal* and a repository.** `RepoName` marks that publishing began and `publishing → failed` is legal (015), so the first version selected a newer session that had died mid-publish, found no findings on it, and rendered nothing while a graded run sat underneath. The browser test now carries exactly that fixture. And **the spend contract test was initially vacuous** — every session in it was freshly enqueued with no usage, so it asserted `0 == 0` and would have passed against a store that summed nothing; it now observes real usage through a priced card and asserts a session created before the window and updated inside it still counts.

**Scope:** the Sessions tab gains the right rail the operator design gives it — the latest session's inner-loop decomposition per paper, the open-science scorecard, and the collaborators — plus `Publish…` on the project header and a `SPEND · LAST 30 DAYS` tile on the projects screen.

**Depends on:** [021a](021a-the-findings-as-data.md) (the decomposition and the scorecard), [021d](021d-watching-a-session-run.md) (the running rows beside it), [018](018-one-list-of-projects.md) (the projects screen and its tiles), [006n](006n-who-signed-off.md) (the publish wizard the button opens), [011](011-what-a-session-costs.md) (the spend).

## Problem

The Sessions tab is a list and nothing else. Everything a reader needs in order to act on it lives one or two clicks away: how the latest run decomposed, whether the authors released anything, who else is on the project, and how to publish. The publish wizard in particular is reachable only from the Collaborators tab, which is where it was put when that tab was the only one with room — publishing is not a fact about collaborators.

On the projects screen, [018](018-one-list-of-projects.md) left three tiles. The fourth thing an operator asks — what this is costing — is metered per session by [011](011-what-a-session-costs.md) and shown nowhere in aggregate.

## Decisions

**The rail summarises the latest session per paper, and says so.** A project holds papers ([012](012-a-project-holds-papers.md)); a paper has sessions; only a session has findings ([015](015-a-verdict-belongs-to-a-session.md)). So the rail is not "the project's rate" — it is one card per paper with more than one session's worth of interest, headed `LATEST SESSION · INNER LOOP` and naming the session it read. On a project holding one paper it looks exactly like the design; on a project holding three it does not lie, which is the failure 015 was written to fix.

**The decomposition is the bar, not the number.** Verified, partial and blocked as three counts and one stacked bar, with the rate beside them rather than above them.

**`Publish…` moves to the project header.** It opens the wizard 006n already built. The Collaborators tab keeps the sign-off card, because the record of who signed belongs with the people, but the *action* belongs where an operator looks for it.

**`SPEND · LAST 30 DAYS` is one windowed aggregate, computed server-side.** Cost is derived per job from usage and the rate card; the tile sums the sessions the caller may see whose settled time falls inside the window. It goes on `GET /projects` alongside the counts that tile already carries, not as a second round trip.

**A window needs a stated edge.** Thirty days back from now, by the time the session settled, not when it started — a run that began 31 days ago and finished yesterday spent yesterday's money. Sessions still running count what they have burned so far, because that is what the wallet has already committed.

## Acceptance criteria

1. The Sessions tab renders a rail with one `LATEST SESSION · INNER LOOP` card per paper that has a graded session, each naming its session and linking to it.
2. A paper with no findings renders no card rather than a zeroed one.
3. The scorecard card renders the five criteria from the latest graded session's findings.
4. The collaborators card lists members and their roles, and links to the Collaborators tab.
5. `Publish…` appears on the project header for an owner, opens the existing wizard, and is absent for a non-owner.
6. `SPEND · LAST 30 DAYS` renders on the projects screen from a server-computed aggregate over settled-in-window sessions, including in-flight spend.
7. Go tests cover the window's edges (a session settled 31 days ago is out; one settled yesterday but started 31 days ago is in; a running one counts its spend so far). A browser test drives 1–6 and fails without the change.

## Not in scope

Comparing sessions. A spend chart or per-day breakdown. Changing what a session costs or how it is metered.
