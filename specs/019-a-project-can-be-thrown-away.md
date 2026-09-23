---
title: "A project can be thrown away"
status: built
---

# 019 — A project can be thrown away

**Status:** built. Every acceptance criterion holds. `projects.deleted_at` (migration 9, with a partial index per read), `Trash`/`Restore`/`ListTrashed` on the project store and `DeleteByProject` on the job store, all under the shared contract tests; `DELETE /projects/{id}`, `?purge=1`, `POST /projects/{id}/restore` and `GET /projects/trash` in `internal/httpapi`, with `trash_test.go` covering the guards, the cascade, the read rules and the route precedence. The frontend has the row action, the two dialogs, the `/trash` screen and the banner; `frontend/test/trash.test.mjs` drives the whole round trip in Chrome and was verified to fail with the change stashed. Go and the 10-file browser suite are green.

Four things came out of the build. **The trashed check is enforced at three gates rather than decided caller by caller.** The spec said `Get` answers and "the caller decides". What shipped puts the check in `mayViewProject` (which closes the sessions, transcripts and artifacts under a project in one edit, because that predicate is already documented as the decision everything below a project inherits), in `projectForWork` (start, add-paper, credits) and in `ownedProject` (rename, members, publish, withdraw), with `trashableProject` as the deliberate opt-out the trash routes resolve through. That is a better answer than the spec's, and it is why the read-path audit the spec worried about was not needed.

**`signedPublicProjects` needed no change.** It reads `ListPublic` and filters by signer, so a trashed project left `/u/{handle}` as soon as it left the zone. This follows from 006o's one-source-of-truth choice.

**`trashableProject` hides existence where `ownedProject` does not.** A collaborator's delete is 403 and a stranger's is 404, which is `getProject`'s rule rather than the members routes' (403 for both). A stranger should not learn whether a project exists, and a route added now should not inherit the older leak.

**Two follow-on fixes the first pass missed**, both found by review rather than by a test: the trash screen inherited `projectViews`' modified-time sort and so did not order by the column it displays (now sorted on `deleted_at`, with a test that discriminates); and the project screen still rendered the rename pencil, the People tab and the credits card on a trashed project, all of which the server now refuses (`isOwner` now includes `!deleted_at`). `closeRepos` was also extracted so a withdrawal and a delete close repositories through one helper instead of two copies.

One thing deliberately left: purging a project **orphans its `project_members` rows**. There is no foreign key (the ledger's `job_id` is unconstrained on purpose, because "deleting a project must not delete the record of what its sessions cost", and `project_members.project` is a plain column), so nothing breaks: a membership whose project is gone is already skipped by the merged list. It is untidy, not wrong.

**Scope:** removing a project. An owner moves one to the trash, sees it in a Trash screen reached from the rail, restores it, or deletes it permanently. Soft delete is a `deleted_at` column and a filter on every read path; permanent delete is the cascade `project.Store.Delete` already performs, plus the two things it does not cover.

**Depends on:** [006g](006g-projects-and-replication-sessions.md) (the project record), [006o](006o-withdrawing-a-publication.md) (withdrawal, which is what deleting a *public* project has to do first), [011](011-what-a-session-costs.md) (the pot that blocks a permanent delete), [013](013-the-address-bar-is-the-state.md) (the Trash screen's address), [018](018-one-list-of-projects.md) (the row the action sits on).

## Problem

Nothing in the product removes a project. `project.Store.Delete` exists and cascades to `project_papers` (`project.go:171`), and no route has ever called it; the only callers are contract tests. A project created by a typo, a paper resolved wrong, a duplicate created twice: all of them stay on the list forever, and the list is the screen the reader works from every day.

The reason it was never built is visible in 006o's argument: withdrawal was given to owners because it is not deletion, and "deletion cascades and stays an admin action" was the sentence that made that safe. The concern is real: a delete that silently drops sessions, forge repositories and ledgered credits should not be a button. But the conclusion that an owner can never remove their own work does not follow.

## Decisions

**Two steps, because the dangerous one has to be deliberate.** The row's action moves a project to the trash: reversible, instant, and the only removal an owner performs in ordinary work. Permanent delete lives on a separate screen, behind its own confirmation, and carries the guards.

**Soft delete is `deleted_at` on the project row.** Not a second table: moving a row means every foreign key pointing at it needs the same move, and `project_papers` and `jobs.project_id` both point at it. A nullable timestamp is the whole mechanism.

**Its cost is that every read path has to filter, so the list is written down here and tested.** Missing one is how a trashed project reappears somewhere its owner does not expect:

| Read path | Behavior |
|---|---|
| `ListByOwner` | excludes trashed |
| `ListPublic` | excludes trashed |
| `listShared`'s membership walk | excludes trashed |
| `signedPublicProjects` (`handlers.go:998`) | excludes trashed; a trashed project must leave `/u/{handle}` |
| `Get` | **returns** the project; the caller decides |

`Get` keeps answering because the trash screen, the restore, and the permanent delete all need to read a trashed project. A new `ListTrashed(owner)` is the only read that returns them as a set.

**A trashed project's URL still resolves, for its owner, read-only.** 013's rule is that the address is the state, and a link that 404s as soon as somebody tidies up misreports why it stopped working. The owner gets the project screen with a banner saying it is in the trash and a Restore button; every write route on it is refused `project_trashed`. Everybody else (collaborator, public reader) gets the same 404 they would get for a project that never existed. A trashed project is not public, whatever its `visibility` column still says.

**Trashing is refused while a session is running.** A running session would spend money into a project the owner has just hidden from themselves. `409 sessions_running`; stop the sessions first.

**Trashing a public project withdraws it first**, through the existing 006o path, with the reason recorded as the deletion. A publication that vanishes without a retraction is the failure 006o was written to prevent, and a delete does not skip it.

**Permanent delete is refused while the pot holds credits.** `409 credits_remaining`; a lead reclaims first. The ledger exists to prevent a balance nobody can account for (`api.ts:146`), and a delete that drops a funded pot creates one. Auto-reclaiming was the alternative and is worse: it makes a delete a money movement, which should be a separate, deliberate decision.

**Permanent delete cascades to what the store misses.** `Delete` covers the project row and `project_papers`. It does not cover:

- **the sessions**: `jobs` rows live in their own store, joined by `project_id`, and there is a `jobs_project_fk`. `job.Store` gains `DeleteByProject(projectID)`, called first.
- **the forge repositories**: every session's `repo_name`. They are closed on the forge the way withdrawal already closes them (made private, not deleted): a repository the agent authored is evidence, and this product does not destroy evidence on a click. The delete removes the project's claim on the repository.

The transcript and the manifest live in those repositories and go private with them. Permanent delete is therefore the app forgetting a project, not an erasure across the forge, and the confirmation says so in those words.

**Only the owner may trash or delete.** A collaborator must not be able to remove a project they were invited to; leaving a project is a membership question and is not in this spec.

**The confirmation is an in-app modal.** `ui.tsx` already owns `Modal` and `PromptDialog`; `window.confirm` is not used anywhere in this app and is not introduced here.

## Routes

| Route | Meaning |
|---|---|
| `DELETE /projects/{id}` | move to trash. 409 `sessions_running`. Owner only. |
| `POST /projects/{id}/restore` | back to the list. Owner only. |
| `DELETE /projects/{id}?purge=1` | permanent. 409 `credits_remaining`, 409 `not_trashed`. Owner only. |
| `GET /projects/trash` | the caller's trashed projects, newest-trashed first. |

Permanent delete is a query parameter on the same verb rather than a second path because it is the same act at a different depth, and because `not_trashed` enforces the order: a project cannot be purged without being trashed first.

## The screen

Trash is a rail item in a group of its own at the bottom of `rail-nav`, above the user button, at `/trash`. It lists title, owner, and *deleted* time, with Restore and Delete permanently per row. Empty state: "Nothing in the trash."

The projects list gains a trash-can button per row, on owned rows only, beside the chevron.

## Acceptance criteria

1. `DELETE /projects/{id}` by its owner removes it from `GET /projects`, from `GET /public`, from a collaborator's list, and from `/u/{handle}`; `GET /projects/trash` returns it.
2. The same call by a collaborator is 403, and by a stranger 404.
3. `DELETE /projects/{id}` on a project with a running session is 409 `sessions_running` and changes nothing.
4. Trashing a public project records a withdrawal and leaves `visibility` private.
5. `POST /projects/{id}/restore` returns it to `GET /projects` and removes it from the trash.
6. `GET /p/{id}` of a trashed project answers its owner and 404s a collaborator; a write route on it is refused `project_trashed`.
7. `DELETE /projects/{id}?purge=1` on a trashed project with a funded pot is 409 `credits_remaining`; after the pot is reclaimed it succeeds, and the project, its papers and its sessions are gone from every store.
8. `?purge=1` on a project that is not trashed is 409 `not_trashed`.
9. Purging a project with published repositories leaves each repository present on the forge and private.
10. A browser test drives the row action, the modal, the trash screen, and restore, and fails without the change.

## Not in scope

An admin trash across all owners; auto-purge after N days; leaving a project you were invited to; deleting one session; deleting a forge repository; undo as a toast rather than a screen.
