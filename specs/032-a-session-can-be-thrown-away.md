# 032 — A session can be thrown away

**Status:** ready.

**Scope:** removing one session. An owner deletes a session that ended without a reproduction, from the session list or from the publish refusal that names it, and the project becomes publishable. Soft delete is a `deleted_at` column on `jobs` and a filter on four read paths; the session's repository is closed the way [019](019-a-project-can-be-thrown-away.md) closes one, not destroyed.

**Depends on:** [006b](006b-job-model-and-persistence.md) (the job row and its two stores behind one contract), [006f](006f-taking-a-reproduction-out.md) (the publish gate this unblocks), [019](019-a-project-can-be-thrown-away.md) (the soft-delete pattern, the repo-closing rule, and `closeRepos` itself), [021d](021d-watching-a-session-run.md) (the session row the action sits on).

## Problem

A project can be permanently unpublishable, and nothing in the product can fix it.

`publishBlockers` (`internal/httpapi/handlers.go:1550`) decides on one predicate, and it never reads `Phase`:

```go
if s.RepoName == "" { continue }                      // never began publishing — not a blocker
repro, err := rt.repro.Get(ctx, repoName(s.RepoName))
if errors.Is(err, ErrReproNotFound) {
	block(BlockUnfinished, "")                         // repository, no manifest
}
```

So the blocker is **a repository with no manifest**, whatever became of the run that made it. `RepoName` is set the moment publishing begins (`internal/job/job.go:180`), and finalizing can fail after it: a cancel, a publish error, or a restart with no live run to rejoin (`internal/job/worker.go:255`). Each of those leaves a `failed` row holding a repository that will never gain a manifest.

That row blocks its project forever, because:

- **It cannot be finished.** `transitions` has `PhaseFailed: {}` (`job.go:232`), and `CanTransition` is the single writer no store may bypass. `Worker.drivePublish` — the finalize step, and idempotent — is reachable only from `Reconcile`, which requires `Phase == PhasePublishing` (`worker.go:248`).
- **It cannot be removed.** The only deletion in the job store is `DeleteByProject`, the 019 purge cascade. There is no per-session delete, and 019 named "deleting one session" in its *Not in scope* list.
- **It cannot be worked around.** Re-running the paper opens a *second* session; the first one still holds its repository and still blocks.

The owner sees the refusal, follows *Open session* to a transcript that explains nothing (`errors.ts:242` routes `unfinished` to the transcript tab, and the session pane's "Blocks publishing" banner only fires for `not_clear`), and has no action available: `SessionRow` renders **Stop** only when `!terminal(j.phase)` (`App.tsx:3801`), so a failed session offers nothing at all.

## Decisions

**Delete, not resume, and the state machine is the reason.** A session stuck *in* `publishing` is already handled — `Reconcile` re-drives it at every boot, and finalizing is idempotent. A session that reached `failed` is terminal by the machine's central invariant, and reopening it to finish a publish would make `Terminal()` a suggestion. The product already has the "try again" verb: a re-run, which `ErrActiveSession` permits precisely because the prior session is terminal. What it lacks is the verb for the corpse the prior run left behind.

**Soft delete is `deleted_at` on the job row**, mirroring `projects.deleted_at` exactly (019, migration 9). Not a hard delete: the row is what `credit_entries.job_id` points at, and 019 already settled that *deleting a project must not delete the record of what its sessions cost*. A session that cost money leaves that record standing.

**No trash, no restore.** 019 gave projects a trash because a project is a container of work and losing one by mistake is expensive. A session is one run; the thing being removed is a dead one, and the recovery from a mistaken delete is to run it again. A second restore surface would be a second removal concept beside 019's, for a cheaper object.

**Only terminal sessions.** `409 session_running`; stop it first, which is `POST /jobs/{id}/cancel`, already built and already on the row. This is 019's `sessions_running` rule at session scale, and it is what keeps the delete button on a blocker row from killing a publish that is merely slow — a session legitimately in `publishing` right now produces the *identical* `unfinished` blocker.

**The repository is closed, not deleted.** 019's sentence stands and is quoted here so it is not re-decided: *a repository the agent authored is evidence, and this product does not destroy evidence on a click*. `closeRepos` already takes a `[]job.Job` and makes each one private; a session delete calls it with the one session. The transcript and any artifacts live inside that repository and go private with it. Deleting a session is **the app forgetting a run**, not an erasure across the forge.

**Four read paths filter, and they are written down here because missing one is the bug.** The filter goes in the store, not in callers — 019's read-path audit is the thing not to repeat.

| Read path | Behaviour | Why |
|---|---|---|
| `Get` | `ErrNotFound` | one gate closes every per-session read at once: the job, transcript, verdict, artifacts, events, cancel |
| `ListByProject` | excludes | the session list, `publishBlockers`, the archive (020), and `trashProject`'s running check all read it |
| `ListByPrincipal` | excludes | the caller's own session list |
| `StatusByProjects` | excludes | the project list's count and latest-phase summary |
| `SpendSince` | **includes** | money is money |
| `Accrued`, `Active`, `Claim`, `Advance`, `Cancel` | unchanged | all are scoped to non-terminal rows, and only terminal rows can be deleted |
| `DeleteByProject` | unchanged | the 019 purge takes deleted rows with the rest |

`SpendSince` keeping deleted sessions is the one asymmetry and it is deliberate. It feeds the project rail's spend figure, and the ledger it must agree with still holds the charge. A number that dropped when a session was deleted would be a wallet and a rail reporting different totals — the discrepancy the ledger exists to prevent.

**The double-trigger indexes are untouched.** `jobs_active_paper_project` and `jobs_active_paper_principal` (migration 2) already exclude `done` and `failed`, and only those phases are deletable, so a soft-deleted row was never in either index.

## Routes

| Route | Meaning |
|---|---|
| `DELETE /jobs/{id}` | remove one session. Owner of its project only. 409 `session_running`, 409 `project_trashed`. 204 on success. |

Owner-only, resolved the way every other write route resolves: a collaborator is 403, a stranger 404. `cancelSession` does its own inline owner check and so does not refuse on a trashed project (`handlers.go:2531`); this route does not inherit that, it goes through the shared helper.

## The screen

**On the session row.** A delete action beside the GitHub link, on terminal sessions only, owner only — the slot where **Stop** sits for a running one, so a session always offers exactly one removal verb and never two. Confirmation is an in-app `Modal`; `window.confirm` is not used in this app (019).

**On the blocker row.** The publish refusal lists each session standing in the way (`Blockers`, `App.tsx:4138`) and today offers only *Open session*. An `unfinished` blocker gains **Delete session** on the row itself, because that row is where the owner is when they learn the problem exists, and sending them to a transcript that does not mention publishing is the dead end this spec exists to close. Deleting from there re-runs the publish check.

## Acceptance criteria

1. `DELETE /jobs/{id}` on a terminal session by its project's owner returns 204; the session is gone from `GET /jobs`, from the project's session list, and from `GET /jobs/{id}` (404).
2. The same call by a collaborator is 403 and by a stranger 404; a second call on the same session is 404.
3. `DELETE /jobs/{id}` on a non-terminal session is 409 `session_running` and changes nothing.
4. A project whose only blocker was an unfinished session publishes successfully after that session is deleted, and `POST /projects/{id}/public` no longer returns `sessions_unfinished`.
5. Deleting a session with a repository leaves that repository present on the forge and private.
6. A deleted session's credit entries are unchanged, and the project's spend figure still counts what it cost.
7. Deleting a session does not change the project's status summary count of *running* sessions, and a project whose sessions are all deleted reports no sessions rather than an error.
8. Both stores satisfy the new contract test, memory and Postgres alike.
9. A browser test drives the blocker row's delete through to a successful publish, and fails without the change.

## Not in scope

Resuming a terminal session; restoring a deleted one; an admin view of deleted sessions; deleting a forge repository; deleting a session from a trashed project; a "why this blocks publishing" banner on the unfinished session's own pane.
