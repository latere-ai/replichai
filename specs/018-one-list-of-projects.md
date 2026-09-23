---
title: "One list of projects"
status: built
---

# 018 — One list of projects

**Status:** built. Every acceptance criterion holds. `GET /shared` and `listShared` are gone; `GET /projects` walks the owned projects and the bound memberships and decorates both through one `projectViews`. `job.Store.StatusByProject(owner)` became `StatusByProjects(ids)` and `project.Store` gained `ModifiedAt(ids)`, both with contract tests. `frontend/src/when.ts` renders the age (5 unit tests); `frontend/test/projects-list.test.mjs` drives the merged list, the Owner and Last-modified columns, the order, the tiles, and the narrow-screen fold in Chrome, and was verified to fail with the change stashed. The Go suite and the 9-file browser suite are green.

Three things came out of the build. **A summary read that fails now returns 500 for the list** rather than rendering zeros. The graceful-degradation version was written and reverted, because "0 sessions" is a claim about the project, and a list that makes a false claim is worse than one that reports it could not load. `store_failure_test.go` holds that. **`signerOf` stopped printing the opaque subject**: on the public registry a v1 sign-off with no recorded name now falls back to the owner's profile name and then to an em dash. The spec implied this by giving `/public` the owner fields but did not say it. **`projectViews` does two profile reads per row** (the display name, then the handle) on top of the signer's. This is an N+1 traded for the one it removed; it is acceptable at a page of projects and should be revisited before the list is hundreds of rows.

**Scope:** the Projects screen's list, and the three facts each row must carry: the title, who owns it, and when it last changed. Merges "My projects" and "Shared with me" into one list, replaces `GET /shared` with a single `GET /projects` that answers both, and shows a human name where an opaque OIDC subject used to be. No delete, no download: those are [019](019-a-project-can-be-thrown-away.md) and [020](020-download-the-whole-project.md).

**Depends on:** [006g](006g-projects-and-replication-sessions.md) (projects as records), [006f](006f-taking-a-reproduction-out.md) (membership, the shared list), [006r](006r-the-public-profile.md) (the profile a name resolves through), [013](013-the-address-bar-is-the-state.md) (the screen's address).

## Problem

The Projects screen splits the reader's work in two by a fact they did not choose: whether they created the project or were invited to it. A project's tab depends on who created it, and the reader has to remember which side a given piece of work is on before they can find it. Nothing else on the screen is organized that way: the public registry is one list, and the sessions inside a project are one list.

Three problems follow from the split, all visible in the current screen:

**The two halves disagree about the same project.** The owned list summarizes through `jobs.StatusByProject`, whose SQL is `WHERE principal = $1` (`internal/job/pgstore.go:325`); it counts only the sessions the caller submitted. The shared list summarizes through `ListByProject`, which counts every session in the project. So a project where a collaborator ran a session reports a different session count depending on which list it appears in. Today the two counts never appear side by side; merged, they would.

**There is no owner to show.** `Project.Owner` is an opaque OIDC subject. The shared tab works around it with `signerOf` (`App.tsx:105`), which falls back to that subject for any project that was never published, which is most of them. A merged list needs an owner column, and there is nothing to put in it.

**There is no modified time.** A project carries `created_at` and nothing else. The list sorts by creation, so a project somebody worked in this morning sits below one created last week and never touched. "Last modified" is the column that makes a list of work scannable, and the record cannot currently answer it.

## Decisions

**One list, one endpoint.** `GET /projects` returns every project the caller owns *or* collaborates on. `GET /shared` and its client are deleted rather than kept alongside; a second endpoint answering a subset of the first is a second source of truth for the same question. The owner is a column, so ownership is still visible without splitting the list.

**One summary query, over project ids.** `job.Store.StatusByProject(owner)` becomes `StatusByProjects(ids []string)`, grouped by `project_id` over the set the list already resolved. That is one query for the merged list instead of one plus N, and it removes the disagreement above by construction: there is exactly one way a project's sessions are counted, and it counts all of them. `summarize` and its per-project `ListByProject` in the shared and public paths are removed with it.

**Last modified is derived, never stored.** It is `max(created_at, newest session's updated_at, newest paper's added_at)`, computed at read time. `ProjectStatus` gains `LatestAt` (the newest session's `updated_at`); `project.Store` gains `ModifiedAt(ids)` returning `max(projects.created_at, project_papers.added_at)` per project in one query. Nothing to migrate, nothing to drift, and no write site to forget. The alternative, an `updated_at` column touched by rename, add-paper, remove-paper, publish, withdraw and every credit move, is a list of write sites that is easy to get wrong and hard to check. A session finishing counts as a modification, which matches what a reader means by the word.

**The owner is named through their profile, never by their email address.** `ProjectView` gains `owner_name` and `owner_handle`, resolved per read the way `signer_handle` already is (`handlers.go:1193`) and for the same reason: it is a view of who somebody is *now*, not a snapshot, and a snapshot would be a second, staler copy of a person. The name falls back to the handle, then to `—`.

It is deliberately **not** the owner's email. `SignedOffEmail` is stored already redacted so a full address never reaches a client (`project.go:70`); putting the owner's raw address on every shared row would reverse that policy on a much wider surface. The client renders `You` when `p.owner === me.principal`, so the common case needs no lookup.

**Sort is last-modified descending.** A displayed column that does not order the list forces the reader to sort by eye.

**The stat tiles count what the list now shows.** `PROJECTS` counts the merged list, not the owned half. `REPLICATION SESSIONS` sums every row's session count. The screen's loading gate becomes "the list has loaded", one state instead of two.

## The row

| Column | Content |
|---|---|
| Project | the title, and the session count beneath it |
| Owner | `You`, or the owner's profile name linking to `/u/{handle}` when they have a public page |
| Last modified | a relative time (`3 hours ago`), with the absolute time as its tooltip |
| — | the status and visibility badges the row already carries, then the chevron |

## API

`GET /projects` → `[]ProjectView`, owned and shared together, newest-modified first. `ProjectView` gains:

```json
{ "owner_name": "Changkun Ou", "owner_handle": "changkun", "modified_at": "2026-08-08T21:00:00Z" }
```

`GET /shared` is removed. `GET /public` keeps its shape and gains the same three fields, since the public registry lists other people's projects and has the same naming problem.

## Acceptance criteria

1. `GET /projects` returns a project the caller owns and a project they collaborate on, in one response, newest-modified first; `GET /shared` returns 404.
2. A project where a collaborator ran a session reports the same `session_count` to the owner and to the collaborator.
3. `modified_at` is newer than `created_at` for a project whose session was updated after creation, and for one that had a paper added after creation.
4. `owner_name` is the owner's profile display name; `owner_handle` is present only when that profile is public; neither field ever carries an email address.
5. The Projects screen renders one list with Project, Owner and Last-modified columns, and no segmented control.
6. The owner cell reads `You` for a project the signed-in user owns, and links to `/u/{handle}` for one whose owner has a public page.
7. Both stat tiles count the merged list.
8. A browser test drives 5 and 6 against a mocked `/projects` and `/me`, and fails without the change.

## Not in scope

Deleting or downloading a project (019, 020); a sort control or column headers the reader can click; filtering by owner; pagination; a project search box.
