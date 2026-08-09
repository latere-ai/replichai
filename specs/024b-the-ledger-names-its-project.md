# 024b — The ledger names its project

**Status:** built. A child of [024](024-settings-is-a-place.md).

**Scope:** make a wallet's `allocate` and `reclaim` entries name the project they moved credits to or from, and link to it while it exists. The name is snapshotted onto the entry at the moment of the move, so a project that is later purged still reads in the history. Carries the `credit.Store` interface change, a new additive migration on `credit_entries`, and the wallet-statement rendering. Forward-only.

**Depends on:** [024a](024a-settings-as-a-destination.md) (the Credits tab this renders into), [011](011-what-a-session-costs.md) (the ledger, `transfer`, and the allocate/reclaim moves), [019](019-a-project-can-be-thrown-away.md) (trash then purge — why a link is conditional and a snapshot is required).

## Problem

A wallet-side `allocate` entry stores no project reference. `transfer(from, to, amount, kind, actor)` writes two rows — the wallet side (negative on allocate) and the pot side (`Pot(project)`, positive) — and only the pot side carries the project id, in its holder string. The wallet owner reads their own side, which names nothing. So the statement says `Allocated to a project` and stops there: no id to link by, and after a purge no name to fall back to. The ledger is deliberately standalone — it depends on no other package and holds no foreign keys, because "a ledger must outlive the thing it is about" — so the project's *name* cannot be looked up at read time and must be captured when the move happens.

## Decisions

**An entry gains an optional project id and a snapshot of its name.** `credit.Entry` gains `ProjectID string` and `ProjectName string` (both `json:",omitempty"`), set on `allocate` and `reclaim` rows and empty on every other kind. The name is the project's title *at the moment of the move* — a snapshot, not a reference — which is what lets a purged project still read. This keeps the ledger's no-foreign-key rule: it stores strings it will never resolve, exactly as it already does for `holder` and `job_id`.

**The snapshot is passed in from the handler, not looked up in the ledger.** `Store.Allocate` and `Store.Reclaim` gain a `projectName string` parameter (`Allocate(ctx, by, project, projectName string, amount int64)`). The `/projects/{id}/credits` handler already loads the project row for its membership check, so the title is in hand — no extra fetch. `transfer` stamps both rows with the id and name, so the wallet side and the pot side agree. `MemStore`, `PgStore`, and every construction site of `KindAllocate`/`KindReclaim` are updated together; `store_contract_test.go` gains a case asserting the snapshot is persisted, so the two stores cannot drift.

**A new migration adds the columns; the initial `CREATE` is untouched.** A second versioned step runs `ALTER TABLE credit_entries ADD COLUMN project_id TEXT, ADD COLUMN project_name TEXT` — editing the initial `CREATE` would touch only fresh databases, and production holds live rows. The columns are nullable, which is what makes forward-only work: old rows read as empty and fall back to the generic label. `PgStore`'s entry `SELECT` reads the two new columns with `COALESCE(…, '')`, mirroring `job_id`.

**The wallet API carries both, plus whether the project is still reachable.** `LedgerEntry` gains `project_id`, `project_name`, and `project_alive` (bool). `project_alive` is the wallet handler's answer — a `LEFT JOIN` (or existence check) against the projects table for the entry's `project_id` — because "deleted" is two states: a trashed project's row still exists and should link ([019](019-a-project-can-be-thrown-away.md)), a purged one is gone and reads as plain text. The client cannot infer this; the server states it.

**The statement names the project, and links it when alive.** On an `allocate`/`reclaim` row with a `project_name`, the label reads `Allocated to {name}` / `Reclaimed from {name}`. When `project_alive`, the name is a link to `/p/{project_id}`; otherwise it is plain text. A row with no `project_name` (a pre-024b entry) keeps today's generic label. The link is the only new affordance; the row's date and amount are unchanged.

## Acceptance criteria

- A new allocate names its project in the wallet statement and links to `/p/{id}`; a reclaim reads `Reclaimed from {name}`.
- After the project is trashed, the row still links (its row exists); after it is purged, the row shows the name as plain text and does not link.
- A pre-migration `allocate` row still renders, with the generic `Allocated to a project` label.
- `store_contract_test.go` fails a store that does not persist `ProjectID`/`ProjectName` on allocate and reclaim.
- The migration applies additively over a database holding existing `credit_entries` rows, leaving them intact with empty project columns.
- `credits.test.ts` covers the three render cases (linked, plain-text, legacy). The new `ledgerAllocateTo`/`ledgerReclaimFrom` strings are added to the `en` locale — the app is English-only today (`Lang = 'en'`), so there is no second locale to keep in step yet.

## Leaves out

- Backfilling pre-024b rows. The pairing between a wallet entry and its pot entry is implicit (same actor, amount, timestamp) and heuristic; recovering it risks mis-attribution, and the generic label is honest. Named here as the deliberate non-goal it is.
- The pot-side (project) statement; this is the wallet owner's view only.
