# 035 — A paper can move to another project

**Status:** built. Every acceptance criterion holds. `project.Store.MovePaper` with the `SessionMover`/`PGSessionMover` seam (Postgres does it in one transaction; the in-memory store moves the sessions first and puts the paper back if they fail), `job.MovePaperSessions`/`MovePaperSessionsTx`, `POST /projects/{id}/papers/move` with the two-project authorisation, and the paper row's Move action with its dialog. Covered by `runMovePaperContract` against both stores, `runMoveSessionsContract` against both movers, `internal/httpapi/paper_move_test.go`, the extended `cmd/server` e2e, and `frontend/test/paper-move.test.mjs`. Go (including the Postgres contracts), the 130 unit tests and the 18-file browser suite are green.

Five guards were verified to fail with their fix reverted: the running-session refusal (the move returned 204 mid-run), the destination lead check (a lead of one end could move), the in-memory rollback (a failed session move split the paper from its runs), `updated_at` (bumping it in the SQL moved months-old spend into the window), and the deleted-session filter (the thrown-away run's charge stayed under the source). The browser test was checked against three separate regressions: Move gated behind "never run", the destination list unfiltered, and the refresh dropped after a successful move.

Two things came out of the build.

**The column the design started with was not needed, and the terminal-only rule is why.** The first draft added `jobs.pot_project_id` so a moved session's charge kept naming the project that paid — because `Hold`/`Settle` key on `credit.Pot(j.ProjectID)` and would follow the row. But a move is refused while any session is non-terminal, so every session that moves is already settled and neither is ever called on it again. The ledger keeps the charge under the pot that made it with no help from a column. What remained was the rail's 30-day figure, and that is answered by argument rather than schema — see the decision below.

**`removeProjectPaper`'s lead check became `rt.leads`.** The move needs the same rule twice, on two projects, and leaving the original inline would have been the third copy of `proj.Owner != principal(r) && !rt.isLead(...)` — three places for one rule to drift in.

**Scope:** moving one paper, and every session that is a run of it, from one project to another. A lead of both projects picks the destination from the paper's row; the paper record and its runs land there together, and the money stays where it was spent.

**Depends on:** [012](012-a-project-holds-papers.md) (the paper row, and the rule that removing one is the lead's), [011](011-what-a-session-costs.md) (the project pot a session charges), [015](015-a-verdict-belongs-to-a-session.md) (sessions nest under the paper they are runs of), [019](019-a-project-can-be-thrown-away.md) (the trash a move must refuse), [021e](021e-the-project-rail.md) (the 30-day spend figure this must not disturb), [032](032-a-session-can-be-thrown-away.md) (terminal-only, and why).

## Problem

A paper is filed under a project once and can never be filed anywhere else.

`project_papers` is keyed `(project_id, paper_key)` (`internal/project/migrate.go:102`), and the only writes against it are an insert, a title update, and a delete (`internal/project/pgstore.go:232-292`). There is no `UPDATE project_papers SET project_id`, and nothing in the HTTP surface, the frontend, or the CLI offers one.

What exists instead is a removal that refuses the moment the paper has been run:

```go
for _, j := range sessions {
	if j.PaperKey == key {
		writeError(w, http.StatusConflict, "paper_has_sessions")   // handlers.go:267
	}
}
```

So the only correction available — delete the paper, add it to the right project — stops working exactly when somebody notices the mistake, which is after a run has produced something worth reading. The result is a project that permanently contains work that does not belong to it, and no way to gather one paper's runs beside the papers they belong with. A re-run under the correct project does not fix it: it opens a *second* session and leaves the first where it was, so the reproduction record splits across two containers.

## The sessions move too

Moving a paper without moving its sessions would orphan them: `listProjectPapers` joins the two through `job.ListByProject` on the paper key (`handlers.go:178-197`), and the frontend's `groupByPaper` drops a session whose key matches no paper row into `Grouped.other` (`frontend/src/papers.ts:19-24`). A session under a container that is not about its paper is the bug [015](015-a-verdict-belongs-to-a-session.md) was written to fix. So the sessions move, and the whole change is two updates in one transaction:

```sql
UPDATE project_papers SET project_id = $to WHERE project_id = $from AND paper_key = $key;
UPDATE jobs           SET project_id = $to WHERE project_id = $from AND paper_key = $key;
```

## Decisions

**No column records which pot paid, because the ledger already does.** `Hold` and `Settle` key on `credit.Pot(j.ProjectID)` (`internal/job/pgstore.go:67,188`), which would follow a session to its new project — except that a move is refused while any session is non-terminal, so every session that moves is already settled and neither is ever called on it again. The charge sits in `credit_entries` under the pot that made it, permanently and correctly. `Advance` is the only writer of `project_id` after insert, and it writes back the value it just read under `FOR UPDATE`, so a transition cannot revert a move with a stale copy.

What does follow the paper is `SpendSince` — the project rail's 30-day figure, which sums `jobs` by `project_id` (`internal/job/pgstore.go:377`). After a move the source project reads as having spent less and the destination as having spent more, while neither pot balance changed. That is deliberate, and it is the honest reading of the tile: the rail answers *what has this project been costing lately*, and after a reorganisation those runs are the destination's work. The number that does not move is the authoritative one — the pot's own ledger still shows the charge under the project that paid it, which is where an owner looks when a balance and a rolling estimate disagree.

The alternative was a `pot_project_id` column and a `SpendSince` grouped on it. It was rejected as a second answer to a question the ledger already answers once — which is migration 4's own argument for dropping `charged_to` (`internal/job/migrate.go:63-66`), and it survives here because the money never moves.

**Terminal sessions only.** A move refuses with `409 paper_running` while any of the paper's sessions is non-terminal. Three reasons, and each alone is enough:

- `jobs_active_paper_project` is `UNIQUE (project_id, paper_key) WHERE phase NOT IN ('done','failed')` (`migrate.go:45`). Rewriting `project_id` on a live row happens *inside* that index; the `paper_exists` check upstream reads `project_papers`, which is a different table and cannot see a stray live job. The collision would surface as a raw `23505` mid-transaction.
- The worker holds `ProjectID` in memory for a run it is driving, and `FinalizeLive` may be mid-publish.
- It is [032](032-a-session-can-be-thrown-away.md)'s rule, for 032's reason: a session in flight raises the identical hazard as a dead one, and without the guard a move against a slow publish kills it.

Deleted sessions *do* move. They are hidden from every read but one, and `SpendSince` deliberately still counts them; leaving them behind would strand a charge under a container that no longer holds the paper.

**Lead or owner, on both projects.** `removeProjectPaper` is already the lead's, tied to who may move credits out (`handlers.go:251-256`), and a move is a remove plus an add. Owner-only would be *stricter* than the removal it composes, so a promoted lead who may delete a paper outright could not move it. The destination is checked by the same rule, from the request body rather than the path — `projectForWork` resolves exactly one project, so the move needs a second resolution beside it.

**Refused while either project is public.** Tested on `Visibility`, never on the presence of a sign-off: [006o](006o-withdrawing-a-publication.md) leaves the sign-off standing on a withdrawn project, so a sign-off proves nothing about current openness, and `Visibility` is the only field a read path may ask (`project.go:26-27`). Moving a paper out of a published project would change what a signed attestation covers. The owner withdraws first, moves, and publishes again.

**Refused while either project is trashed**, which `projectForWork` already enforces on the source and the new destination check repeats.

**One transaction, through the seam job/credit already uses.** `project.Store` gains

```go
// MovePaper moves a paper, and every session that is a run of it, to another
// project. The caller authorises; this store only records.
MovePaper(ctx context.Context, fromID, toID, key string) error
```

`project.PGStore` opens the transaction, updates `project_papers`, and calls the session mover inside it — exactly as `job.PGStore` passes its own transaction into `credit.SettleTx`. The mover is a locally-declared interface so `project` never imports `job`; the composition root wires `*job.PGStore` into `project.NewPGStore` and `*job.MemStore` into the memory store. The two implementations carry different method names (`MovePaperSessionsTx` takes a `pgx.Tx`, `MovePaperSessions` does not), which is the same asymmetry job/credit already lives with and is why they are not one interface on `job.Store`.

A store with no mover wired **fails loudly** rather than moving the paper and stranding its sessions: a composition-root mistake has to surface in a test, not in production data.

**The move does not touch `updated_at` on the sessions.** `SpendSince` filters `updated_at >= since`, so bumping it would drag months-old sessions into the 30-day window and inflate both rails. The SQL says so, because this is the kind of omission somebody later "fixes".

**The paper keeps its `added_at` and `added_by`.** They record when somebody wrote the paper down and who did; the move does not un-write it. Consequence, stated rather than discovered: `ListPapers` orders oldest-first, so a moved paper lands in the destination in the position its original addition earns it, not at the bottom.

## The surface

```
POST /projects/{id}/papers/move
  { "paper_key": "doi:10.1145/…", "to_project": "…" }
  → 204
```

The key travels in the body, not the path, for `startSession`'s reason: `paper.Key` returns `doi:10.1145/3544548.3581433`, which spans several path segments and routes nowhere.

| Status | Code | When |
| --- | --- | --- |
| 400 | `missing_key` | no `paper_key` |
| 400 | `missing_target` | no `to_project` |
| 400 | `same_project` | source and destination are one project |
| 403 | `not_lead` | the caller does not lead or own one of them |
| 404 | `project_not_found` | source missing, or the caller has no stake |
| 404 | `target_not_found` | destination missing, or the caller has no stake |
| 404 | `paper_not_found` | the source does not hold that key |
| 409 | `project_trashed` | either project is in the trash |
| 409 | `project_public` | either project is published |
| 409 | `paper_running` | a session for this paper is not terminal |
| 409 | `paper_exists` | the destination already holds that key |

`ErrPaperNotFound` is new on the store; `ErrPaperExists` is 012's and keeps its meaning.

## The screen

`PapersCard` (`App.tsx:3263`) gains a **Move** action on each paper row, beside Remove and under the same lead-or-owner condition. It opens an in-app dialog — not a native `<select>`, per the standing rule that browser widgets are rebuilt in-app and overlays portal to `<body>` — listing the caller's other projects that are private and not trashed. The picker offers **owned** projects only, which is the plain reading of the request; the server's rule stays lead-or-owner so a lead is not locked out of an API the product does not yet surface to them.

On success the project screen refreshes its papers and sessions, so the moved row and its runs leave the list in place rather than after a reload. Every refusal maps through `errors.ts` to a sentence, in `en` and `zh` together.

## Acceptance criteria

1. A lead of two private projects moves a paper with two finished sessions; the paper row and both sessions read under the destination afterwards, and neither reads under the source.
2. Both pot balances and every ledger entry are unchanged by the move: the charge stays under the pot that paid it. The 30-day spend figure follows the paper — down on the source, up on the destination, by the same amount.
3. A move is refused while any session for that paper is running, with `409 paper_running`.
4. A move into a project that already holds that paper key is refused with `409 paper_exists`, and nothing has moved.
5. A move is refused when either project is trashed or public.
6. A caller who is neither lead nor owner of the destination gets `403 not_lead`, and the paper stays put.
7. A failure inside the move leaves both tables as they were — the paper is never in two projects, nor in neither.
8. A deleted session belonging to the moved paper moves with it, stays hidden from every read that hides it, and counts in the destination's spend figure the way it counted in the source's.
9. The frontend moves a paper from the project screen and the row disappears from the source and appears in the destination without a reload.

## Test plan

- **`internal/project/store_contract_test.go`** — `runMovePaperContract`, run against both stores with a recording fake mover: the happy move, `ErrPaperNotFound`, `ErrPaperExists` at the destination with the source untouched, idempotence of a repeated move, and the loud failure when no mover is wired. The Postgres half runs under `make` with `TEST_DATABASE_URL`.
- **`internal/job`** — the real movers, both stores: `project_id` rewritten for exactly the matching `(project_id, paper_key)` rows including deleted ones, `updated_at` untouched, and every other column — `repo_name`, `rate_card`, `phase_at` — left alone.
- **`internal/httpapi/paper_move_test.go`** — every row of the status table, plus the two-project authorisation, against the in-memory fakes.
- **`cmd/server/e2e_test.go`** — a real server: two projects, a paper run to `done` in the first, moved to the second, then read back through `GET /projects/{id}/papers` on both.
- **`frontend/src/`** — the error mapping under bun; **`frontend/test/paper-move.test.mjs`** drives the dialog in a browser and is registered in `package.json`'s `test:browser`.

Each suite is verified to fail with the change reverted.

## Not in scope

- **Moving a paper while a session runs.** Refused, above.
- **Moving credits with the paper.** The money stays in the pot that spent it, and no ledger entry is rewritten. A lead who wants the destination funded allocates to it, which is already a route.
- **Any schema change.** Both tables have the columns this needs; the move is two `UPDATE`s.
- **Moving a paper out of a published project.** Withdraw first.
- **Moving between owners.** Both projects are the caller's (or led by them); a paper does not change hands here, and a transfer of ownership is a different act with a different consent question.
- **A move that merges into a paper the destination already holds.** Refused as a conflict; merging two papers' session histories is not a move.
