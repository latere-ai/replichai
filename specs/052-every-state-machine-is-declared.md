---
title: "Every state machine is declared"
status: built
---

# 052 — Every state machine is declared

**Status:** built.

**Scope:** every place in ReplicHAI where a record moves through states: the server's stores, the runtimes, the browser and the command line. How each is declared, how its writers are held to the declaration, and how the declaration is checked with TLA+ and Lean. The shared package and the gates are not designed here; [`latere.ai/x/pkg/statemachine`](https://github.com/latere-ai/pkg/tree/main/statemachine) describes them, and [`formal/README.md`](../formal/README.md) says what ReplicHAI keeps under it.

**Depends on:** [006b](006b-job-model-and-persistence.md) (the session phase), [006o](006o-withdrawing-a-publication.md) (publishing and withdrawing), [022c](022c-refunds-and-the-solvency-read.md) (refunds), [022d](022d-a-negative-wallet-is-frozen.md) (the freeze).

## Problem

A survey on 2026-11-08 found 31 places where a record moves through states. Only the session phase had a transition table, and three writers went around it. The others kept their state in nullable columns, pairs of flags, ledger entries, a remote event log or one process's memory, and each writer applied its own rule, so two writers could disagree about which steps exist. Release v0.39.0 fixed the most visible gaps one by one; this spec gives every machine one declaration and checks the declarations themselves.

## Decisions

**A machine with a stored state is a table.** It is declared once with `statemachine.New`, and every write of the state is checked against it in the transaction that makes the write: the writer reads the state under a row lock and calls `Check`, or writes with `state = ANY(Sources(to))` and requires one affected row. Five machines are tables: `job.Phases`, `access.Grants`, `project.Publications`, `managed.Sessions` and `batch.Entries`.

**Writers are driven through the table in tests.** Each table has a test that runs `statemachinetest.Exhaustive` and `Race` through the store's own writers, against Postgres where the store is Postgres. A step the table has must land, anything else must be refused, and two racing writers must end in a state the table explains.

**The tables are rendered and checked.** `TestFormalModels` in `internal/formal` renders every table into `formal/` and fails when a table changed without its models. Beside them are hand-written claims: a Lean theorem file per table, `Proofs/Refund.lean`, and the TLA+ protocols `Lease.tla` and `PublishGate.tla`, each with a configuration that must fail when the rule it depends on is removed. The `lean` and `tla` gates of lateregate build and model-check all of it.

**The schema and the switches hold the same set.** `jobs.phase` carries a CHECK constraint that a test compares with the table. The `enum-go` gate keeps every switch over the five state types exhaustive, and text from outside the store becomes a state only through `job.ParsePhase` and `managed.ParseState`.

**The browser reads the server's table.** `frontend/src/generated/job-phase.ts` is rendered from `job.Phases`, so a phase the server adds is known by name and by whether it is final, and an unknown one is shown in a neutral tone. A failed session carries `stop_reason` (`canceled`, `interrupted` or `error`), which replaced matching the error text.

### Three decisions taken without the owner

Each is the choice that is cheaper to be wrong about, and each is one change in one place to reverse.

1. **A refund reverses its share of the credit.** A reversal takes back `floor(credited × refunded so far / charged)` less what earlier reversals of the same purchase took back, in the charge's own currency, so the total never exceeds what the purchase credited and reaches it exactly at a full refund, whatever order the refunds arrive in. A dispute takes back whatever is left, and a won dispute gives back what it took. v0.39.0 shipped the rule; `credit.RefundReversal` now states it as one function, and `formal/Proofs/Refund.lean` proves the bound.
2. **A public project takes no new work.** Starting a session, adding a paper and importing a session were refused on a public project in v0.39.0. Editing or removing a paper and allocating a repository for a local run are refused the same way, and the session store checks the project row in the transaction that inserts the session, so a publish that commits first is always seen. To change a published project, the owner withdraws it, changes it, and publishes it again with a new sign-off.
3. **A frozen account can still stop spending and retract.** Cancelling a session, withdrawing a publication, reclaiming credits from a project and moving a project to the trash pass the freeze. None of them spends.

## What each machine got

The numbers are the survey's. "Table" is one of the five tables above.

| # | Machine | Outcome |
|---|---|---|
| 1 | Wallet purchase and refund | Decision 1. A refund or dispute that arrives before its purchase answers 500 so Stripe retries it; unconfigured payments answer 503; a won dispute gives its credit back once; two refunds at once send the freeze mail once |
| 2 | Session credit hold | Left: the hold is taken once inside the enqueue transaction, so a hold after its settle is unreachable; a draft takes no hold by design |
| 3 | Account freeze | Decision 3. The waiver ends whenever the wallet is solvent again, after a reclaim (v0.39.0) or a won dispute |
| 4 | Access grant | Table (`invited`, `bound`, `revoked`). Revoking an address that never signed in holds (v0.39.0); a row with no tier does not pass the gate; an admin cannot revoke themself or demote the last admin |
| 5 | Stored platform sign-in | Logout and revoke end it, and it is stored only after access is granted (v0.39.0). A refused token is removed under the row lock, so a fresh sign-in that lands meanwhile survives; the cache takes a rotated token only after it commits |
| 6 | Browser sign-in | Left: the cookie is stateless by design and lasts 12 hours; ending the stored sign-in stops the server acting for the person |
| 7 | Project membership | The last lead cannot be removed or demoted, the owner's own row is refused the same way, and the memory store refuses what Postgres refuses |
| 8 | CLI sign-in | A refresh holds a lock other processes honor and rereads the file before clearing it |
| 9 | Project publication | Table (`private`, `public`, `trashed`) and `PublishGate.tla`. Publish and trash lock the row and refuse while a session runs; every publication and withdrawal is kept; restore only from the trash; a public project is withdrawn before it is trashed |
| 10 | GitHub repository visibility | The wanted visibility is recorded and a failed flip is retried until GitHub matches |
| 11 | Imported sessions | Refused on a public or trashed project; the repository must be in the deployment's organization; a finished record never changes, and an identical re-import is a no-op |
| 12 | Listing and sitemap caches | Every write that changes the listing clears the cache, and the sitemap is held no longer than the listing. Left: another replica's cache may show a change up to 30 s late |
| 13 | Profile handle | A sign-off keeps the handle it was signed under. Left: whether a released handle may be taken again is a product call |
| 14 | Repository names | Creating the repository is the check that its name is free, on the server and on the command line |
| 15 | Paper details | A lookup that found nothing is recorded, and the sweep waits 7 days before trying again |
| 16 | Session phase | Table and a CHECK constraint. Every query takes its phase list from the table; `stop_reason` replaces matching error text |
| 17 | Session ownership lease | `Lease.tla`. Only the owner moves the phase and the sweep fails a session whose driver is gone (v0.39.0); a cancel on one replica now stops the run on the replica that drives it; the repository name is written as soon as it is allocated |
| 18 | Server start and stop | Shutdown waits for the worker and the visibility loop before closing the database; open streams end when draining starts; `/readyz` pings the database |
| 19 | Live stream, server side | Access is checked again every 30 s, and a stream that cannot read the session ends with an `end` event |
| 20 | Local runtime session | Table `managed.Sessions`, shared by every runtime. One lock per session held across processes; a stage whose record failed to save is discarded; events are not appended twice; only a missing container counts as gone |
| 21 | Latere hosted runtime | A finished session stays finished when a cancel arrives late; a session is created under a key derived from the job, so a retry after a crash finds it; release retries a transient failure |
| 22 | Run controller | A session that goes silent keeps the timed-out reason; a resumed graded session is held to a verdict |
| 23 | Legacy Anthropic runtime | An ungraded run that rested after working is done; a start whose reference cannot be returned closes the run |
| 24 | Session view | The stream recovers from an error by reading the session until it settles; a slow Stop says it is still stopping; a finished session refreshes what depends on it |
| 25 | Live transcript poll | Retries with backoff and keeps the last transcript on screen |
| 26 | Publish wizard | The attestation resets when the sign-off changes, the dialog stays open while publishing, and a failed profile load is shown as one |
| 27 | Wallet top-up return | Pay waits for the quote, the page recovers on Back, and polling says when a payment is not confirmed yet |
| 28 | Admin console | Actions show progress and refuse a second click, unsaved edits survive, revoke asks first, and a top-up carries an idempotency key the server deduplicates |
| 29 | Start session button | Stays busy until the new session is listed |
| 30 | Projects list cache | A write during a read reloads after it, a failed load says so, and an expired sign-in asks to sign in again |
| 31 | Batch, experiment, audit | Table `batch.Entries`: a running entry left by a dead process reads as interrupted, and a resume runs it again. Left: the experiment outcome and the audit flags, which nothing outside their packages writes |

## Acceptance criteria

- Every machine marked "Table" is declared with `statemachine`, and its writers pass `Exhaustive` and `Race`, against Postgres where the store is Postgres.
- `formal/` holds the rendered models of the five tables, a Lean claims file per table, `Proofs/Refund.lean`, `Lease.tla` and `PublishGate.tla`; `go tool lateregate` runs `lean`, `tla` and `enum-go` over them and passes.
- Each fix above has a test that fails on the code before it.
- The release carrying this is live.

## Outcome

Built in v0.40.0, on `latere.ai/x/pkg` v0.116.0 (the shared table and its test harness), `latere.ai/x/pay` v0.3.0 (the won dispute) and lateregate v0.72.0 (the `tla` gate).

The survey was a week old when the work started, and v0.39.0 had fixed a third of its findings in between: the refund share, a revocation that holds for an address that never signed in, the stored sign-in ending at logout and revoke, the lease owner check, the periodic sweep, and refusing new sessions and papers on a published project. This work declared the machines those fixes touched, which is what turns each one from a check in one writer into a rule every writer is tested against, and fixed the rest.

Three things differ from the design. The survey's refund item became a proof and a single function rather than a change of rule, because the rule had shipped. The local runtime's table is the runtime state every runtime reports (`managed.Sessions`), not a separate local one, since the local and hosted runtimes report the same five states. And the plan for a test that scans the tree for SQL naming a list of states was dropped: every query in `internal/job` takes its list from the table, the CHECK constraint is compared with the table by a test, and the only other literal list of phases is inside a historical migration, which never changes once applied.
