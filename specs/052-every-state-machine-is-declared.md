---
title: "Every state machine is declared"
status: designed
---

# 052 — Every state machine is declared

**Status:** designed.

**Scope:** every place in ReplicHAI where a record moves through states: the server's stores, the runtimes, the browser and the command line. How each is declared, how its writers are held to the declaration, and how the declaration is checked with TLA+ and Lean. The shared package and the gates are not designed here; [`latere.ai/x/pkg/statemachine`](https://github.com/latere-ai/pkg/tree/main/statemachine) describes them.

**Depends on:** [006b](006b-job-model-and-persistence.md) (the session phase), [006o](006o-withdrawing-a-publication.md) (publishing and withdrawing), [022c](022c-refunds-and-the-solvency-read.md) (refunds), [022d](022d-a-negative-wallet-is-frozen.md) (the freeze).

## Problem

A survey on 2026-11-08 found 31 places where a record moves through states. Only the session phase had a transition table, and three writers went around it. The others kept their state in nullable columns, pairs of flags, ledger entries, a remote event log or one process's memory, and each writer applied its own rule. The gaps were real. A refund reversed more credit than it refunded. Revoking an address that had never signed in recorded nothing. A replica that had lost a session's lease could still finish it. New work on a published project skipped the publish check.

## Decisions

**A machine with a stored state is a table.** It is declared once with `statemachine.New`, and every write of the state is checked against it in the transaction that makes the write. A write either reads the state under a row lock and calls `Check`, or it writes with `state = ANY(Sources(to))` and requires one affected row. No SQL names a list of states by hand; a test scans the tree for one.

**Writers are driven through the table in tests.** Each table has a test that runs `statemachinetest.Exhaustive` and `Race` against the real Postgres store, through the store's own writers. A step the table has must land, anything else must be refused, and two racing writers must end in a state the table explains.

**The tables are rendered and checked.** `formal/` holds the Lean and TLA+ models `statemachinetest.Models` renders from the tables, kept current by a test with `-update`. Beside them are hand-written claims: Lean theorems in `formal/Proofs/` and TLA+ protocol specs with their own `.cfg`. The `lean` and `tla` gates of lateregate build and model-check all of it on every push.

**The browser reads the server's table.** The session phases and which of them are final are rendered from the same table into `frontend/src/generated/job-phase.ts`, so a phase the server adds is not shown as running.

### Three decisions taken without the owner

Each is the choice that is cheaper to be wrong about, and each is one change in one place to reverse.

1. **A refund reverses its share of the credit.** A reversal takes back `floor(credited × refunded so far / charged)` less what earlier reversals of the same purchase took back. The total never exceeds what the purchase credited, and it reaches that exactly at a full refund, whatever order the refunds arrive in. A dispute takes back whatever is left. `pay` reports the charged and refunded amounts so this can be computed. A Lean theorem in `formal/Proofs/Refund.lean` proves the bound.
2. **A public project takes no new work.** Starting a session, adding, editing or removing a paper, and importing a session are refused on a public project with `project_public`, the error moving a paper already returns. To change a published project, the owner withdraws it, changes it, and publishes it again with a new sign-off. Hiding new sessions from readers until a new sign-off would have touched every public read route.
3. **A frozen account can still stop spending and retract.** Cancelling a session, withdrawing a publication, reclaiming credits from a project and moving a project to trash are let through the freeze. None of them spends.

## What each machine gets

The numbers are the survey's. "Table" means a `statemachine` table with the tests above. "Fix" means the confirmed or reported gap is fixed in code. "Left" carries the reason.

| # | Machine | Outcome |
|---|---|---|
| 1 | Wallet purchase and refund | Fix: proportional reversal (decision 1), a refund that arrives before its payment is retried rather than dropped, a won dispute restores what it took, unconfigured payments answer 503 |
| 2 | Session credit hold | Left: a hold after its settle is unreachable, since the job store takes the hold once inside the enqueue transaction; a draft takes no hold by design |
| 3 | Account freeze | Fix: decision 3, and the override is retired on any return to a non-negative balance, not only a top-up |
| 4 | Access grant | Table and fix: revoking an unknown address records the revocation, the test store refuses revoked rows as Postgres does, a tier-0 row is not granted, the last admin cannot be revoked or demoted |
| 5 | Stored platform sign-in | Fix: forgotten on logout and on revoke, stored only after access is granted, a forget after a refusal removes only the refused token, the cache follows the database |
| 6 | Browser sign-in | Left: the cookie is stateless by design; logout now forgets the platform sign-in, which is what acts for the person |
| 7 | Project membership | Fix: the owner's lead row cannot be removed or demoted, and the test store refuses what Postgres refuses |
| 8 | CLI sign-in | Fix: refresh holds a lock other processes honor, and rereads before clearing |
| 9 | Project publication | Table and fix: decision 2, publish only from private, a republish keeps the record of each publication and withdrawal, restore only from trash, trash and publish lock the project against a session starting |
| 10 | GitHub repository visibility | Fix: the wanted visibility is recorded and a failed flip is retried until GitHub matches |
| 11 | Imported sessions | Fix: refused on a public project, the repository name is checked, a finished row is not rewritten to another phase |
| 12 | Listing and sitemap caches | Fix: every write that changes the listing clears the cache, and the sitemap's cache lifetime matches it |
| 13 | Profile handle | Left: whether a released handle may be taken again is a product call; the sign-off keeps the handle it was signed under |
| 14 | Repository names | Fix: an existing repository is never pushed into unless this session created it |
| 15 | Paper details | Fix: a lookup that found nothing is recorded, and the sweep waits before trying again |
| 16 | Session phase | Table and fix: `Observe` checks phase and lease, SQL takes its state lists from the table, a CHECK constraint on `jobs.phase`, an explicit stop reason replaces matching error text |
| 17 | Session ownership lease | Fix and TLA+: `Advance` and `Observe` require the lease, the periodic sweep fails a stale session it cannot rejoin, a cancel on one replica stops the run on the other, one error no longer stops the start-up pass |
| 18 | Server start and stop | Fix: shutdown waits for the worker before closing the database, live streams close when the server drains, readiness checks the database |
| 19 | Live stream, server side | Fix: access is checked again while the stream is open, and a stream that cannot read the session ends with a final event |
| 20 | Local runtime session | Table and fix: one lock per session across poll, cancel and release, a stage whose locator write failed is discarded, events are not appended twice, only a missing container counts as gone |
| 21 | Latere hosted runtime | Table and fix: a finished session stays finished when a cancel arrives late, a session is created under a key derived from the job so a retry after a crash finds it, release is retried |
| 22 | Run controller | Fix: a session that goes silent keeps the timed-out reason, a resumed graded session is checked as graded |
| 23 | Legacy Anthropic runtime | Fix: an ungraded idle session is done, and a failed locator write closes the run |
| 24 | Session view | Fix: the stream recovers from an error by polling, Stop ends, phases come from the server's table, a finished session refreshes what depends on it |
| 25 | Live transcript poll | Fix: retries with backoff and keeps the last transcript |
| 26 | Publish wizard | Fix: the attestation resets when the sign-off changes, the dialog stays open while publishing, a failed profile load is shown as one |
| 27 | Wallet top-up return | Fix: Pay waits for the quote, the page recovers on Back, polling waits longer and says when it stops |
| 28 | Admin console | Fix: actions show progress and refuse a second click, unsaved edits survive a reload, revoke asks first, a top-up carries an idempotency key |
| 29 | Start session button | Fix: stays busy until the new session is listed |
| 30 | Projects list cache | Fix: a write during a read reloads after it, a failed load says so, an expired sign-in asks to sign in again |
| 31 | Batch, experiment, audit | Table for the batch entry; the experiment outcome and the audit flags are left, as nothing outside their packages writes them |

## Acceptance criteria

- Every machine marked "Table" is declared with `statemachine`, its writers pass `Exhaustive` and `Race` against Postgres, and no SQL in the tree names a list of its states.
- `formal/` holds the rendered models and `Proofs/Refund.lean`, `Lease.tla` and `PublishGate.tla`; `go tool lateregate` runs `lean` and `tla` over them and passes.
- Each fix above has a test that fails on the code before it.
- The release carrying this is live, and a refund, a revoke and a cancel are checked against it.
