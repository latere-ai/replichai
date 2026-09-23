---
title: "Withdrawing a publication"
status: built
---

# 006o — Withdrawing a publication

**Status:** built. `project.SetPrivate` takes a `Withdrawal` and records the retraction beside the sign-off (migration 5: `withdrawn_at`/`withdrawn_by`/`withdrawn_reason`); `SetPublic` clears it, so a row never claims to be both live and withdrawn. `github.Client.SetPrivate` and the `RepoPublisher` half close the repositories, sourced from the project's sessions rather than the publish gate so an unclear tree's repository is still closed. `POST /projects/{id}/private` is owner-only and best-effort on the forge. The frontend has the withdraw action on the signed-off card and a one-step confirmation carrying the reason and the "this is not a recall" statement. Covered by the project store contract (both implementations, including that the sign-off survives, that a repeated withdrawal does not overwrite the first, and the republish round-trip) and four handler tests (owner-only, forge failure, unclear-project repo closure, republish re-running the gate). The Postgres half was run against a real database, and the route was verified live: a public project withdrawn returns `visibility: private` with its `signed_off_*` intact, leaves `/public`, and a second withdrawal keeps the original timestamp.
**Scope:** making the public flip reversible. An owner may take a public project back to private, the forge repository follows, and the withdrawal is recorded beside the sign-off it retracts. A child of [006f](006f-taking-a-reproduction-out.md).
**Depends on:** [006f](006f-taking-a-reproduction-out.md) (the one-way public flip and its recorded attestation), [006n](006n-who-signed-off.md) (the signer snapshot the withdrawal retracts), [006g](006g-projects-and-replication-sessions.md) (the project that owns visibility).

## Problem

006f made the public flip one-way and said so everywhere: `Visibility`'s doc comment, the `Store.SetPublic` contract, `github.Client.SetPublic` ("deliberately one-way: this package offers no `SetPrivate`"), the publish wizard's step 1, and acceptance criterion 6. The reasoning was sound for the act of *signing*: an attestation is a record, and a record you can silently delete is not a record.

But visibility and the attestation are not the same thing, and treating them as one leaves an owner with no move at all. A published project turns out to contain an author's unreleased figure. A co-author objects after the fact. A license question surfaces on a dataset the manifest could not see. Today the only remedy is to ask an operator to touch the database, and nothing takes the GitHub repository back: it stays open under `open-hai` with the app showing no trace of the problem.

The gap is not a missing toggle. *Publishing* was modeled as a state change when it is a declaration with a duration. The fix is not to make the flip erasable; it is to give it an end.

Granting this to the owner is safe for a stated reason: **withdrawal is not deletion, and an owner has no deletion.** Removing a project cascades (sessions, transcripts, artifacts, manifests, and the ledger entries recording what its runs cost), so it stays an admin action. Withdrawal touches one column and takes nothing away; it is the strongest reversal an owner can be trusted with precisely because it destroys nothing. Everything published remains on the row, readable by its owner and by an operator, and republishable in one step.

## Decisions

**Withdrawal is a retraction, not an erasure.** `SetPrivate` returns `visibility` to `private` and records who withdrew, when, and why, and leaves every `signed_off_*` field exactly where it is. The project no longer appears in the public zone and its artifacts are no longer readable by strangers, but the row still says who signed the v2 attestation on 30 July and that it was withdrawn on 2 August. This is the same rule 006n already follows for the signer snapshot and 002 for the manifest: a verdict is an artifact, not a view of the present. An erasing un-publish would let a project be published, withdrawn, and re-published under a different signer with no evidence any of it happened.

New columns on `projects`, all empty for a project that was never withdrawn:

| column | meaning |
|---|---|
| `withdrawn_at` | when the retraction was recorded; `NULL` while the project has never been withdrawn |
| `withdrawn_by` | the deciding principal (006c) |
| `withdrawn_reason` | free text the owner types, capped; the *why*, for the owner's own record |

**Nothing new gates the artifacts.** Session reads already resolve visibility through the project on every request (`viewableSession` → `mayViewProject`), so a withdrawal closes the transcript, the artifacts, and the reproduction with the project, and no read path caches a decision taken at publish time. This spec adds no second gate; it verifies the existing one holds in the closing direction.

**Withdrawal clears nothing but visibility.** In particular it does not clear the sign-off, so `signed_off_at` alone no longer means "this is public". Every read path that asks *is this public* must ask `visibility == "public"`, never "does it have a sign-off". The frontend's signed-off card is rendered on that condition today and must not start showing a withdrawn attestation to a private-project viewer as though it were live.

**Re-publishing is a fresh sign-off, and re-runs the gate.** A withdrawn project may be published again: `SetPublic` overwrites the snapshot with a new attestation and clears the withdrawal fields, and the route runs `clearedForPublish` from scratch. A withdrawal never carries a stale clearance forward: if the manifest that flagged the tree is why it was withdrawn, the re-publish is refused `409` like any other unclear project. Withdraw and re-publish are both repeatable; neither is a one-shot.

**The repository follows, best-effort, and every project repo is set private.** `github.Client` gains `SetPrivate`, and `RepoPublisher` gains the matching method. A failure is logged and does not fail the request, mirroring the publish path exactly: the project is already private in-app either way, and a forge outage must not leave an owner stuck mid-withdrawal.

We set *every* repository the project owns private, not only those the publish flipped, because nothing records which ones were flipped: `flip_repo: false` publishes in-app while leaving the repo private, and the repo list comes from `clearedForPublish` regardless. Setting an already-private repository private is a no-op on GitHub, and the direction is the fail-safe one; the worst case is closing something that was already closed. Rejected: a `flipped_repos` record to make the withdrawal precisely symmetric. It is a second schema addition to buy reporting accuracy on a path whose imprecision is inherently safe.

**Taking a repository private is not a recall, and the UI says so.** Forks, existing clones, and GitHub's public event history survive the flip; a repository with forks may refuse the change outright. The withdrawal confirmation states plainly that going private stops new access and does not retrieve copies already taken. Promising erasure would be the one harmful thing this spec could do.

**Owner-only, and confirmed, not one click.** `POST /projects/{id}/private` sits beside the publish route under the same gate and the same `ownedProject` check. The UI is a confirmation dialog naming the consequences and taking the reason. It is deliberately lighter than the three-step publish wizard, because withdrawing is the conservative direction, but it is not a bare toggle in a sidebar either.

Rejected: **withdrawal as a platform-operator takedown only** (006k). Operator takedown is a real and different need (removing something over an owner's objection), and this spec does not build it. But the common case is an owner who found a problem in their own publication, and routing that through a support request is the status quo this spec fixes.

Rejected: **a public tombstone.** A withdrawn project could leave a "this reproduction was withdrawn on …" page in the public zone, as a journal leaves a retraction notice. It is arguably the more scientific behavior, and it is a real design question, but it turns withdrawal into a *different kind of publication*, with its own visibility rules for a project whose contents the owner just decided should not be readable. Out of scope here; the record is kept on the row so a later spec can surface it without a migration.

## Acceptance criteria

1. `POST /projects/{id}/private` flips a public project to `private`, records `withdrawn_at`/`withdrawn_by`/`withdrawn_reason`, and returns the updated project. Non-owner is refused `403`; a missing project `404`.
2. The withdrawal leaves every `signed_off_*` field unchanged, asserted directly on the returned project, for both store implementations via the contract test.
3. A withdrawn project no longer appears in `ListPublic`, and a signed-in stranger who could read it while public is refused afterwards: the project page *and* every session-scoped read under it (the session, its transcript, its reproduction, its artifacts). Closing the project page while an artifact stayed readable by URL would defeat the feature. The owner keeps all of it.
4. Withdrawing an already-private project is accepted and idempotent (no error, no second withdrawal timestamp overwriting a first, or an explicit decision that it does, asserted either way).
5. `SetPublic` on a withdrawn project clears the withdrawal fields and writes a fresh sign-off; the publish route re-runs `clearedForPublish` and still refuses `409` when the manifest is not clear.
6. `github.Client.SetPrivate` sends `private:true` (mirroring `TestSetPublicSendsPrivateFalse`), and a non-200 is an error; a repo flip failure during withdrawal is logged and the request still succeeds.
7. The frontend shows a withdraw action only on a public project the caller owns, confirms with the reason field and the "this is not a recall" statement, and returns the project card to its private state. The signed-off card renders on `visibility === 'public'`, not on the presence of a sign-off.
8. English and Chinese strings land together.

## Out of scope

- **A public retraction notice / tombstone.** The record is stored; nothing renders it publicly.
- **Operator takedown** over an owner's objection (006k's surface, not this one).
- **Recalling distributed copies.** Forks, clones, and caches are outside the platform's reach and the UI says so rather than pretending otherwise.
- **Per-session or per-artifact visibility.** Visibility remains a property of the project.
