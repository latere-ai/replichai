# 006l — The session quota *(superseded by [011](011-what-a-session-costs.md))*

**Status:** superseded by [011](011-what-a-session-costs.md), which replaces the session counter with money. Nothing described below is in the build: the counter, `jobs.charged_to`, `projects.sponsored`, the `charge_to` picker and the `quota_exceeded` code are all removed, and what limits a run is now what it costs.

It stays here, unarchived, because it is the record of *why* the counter was built the way it was — and 011 inherits most of that argument rather than overturning it. The check that lives inside `Enqueue` rather than in a handler, serialised in the writing transaction because a read-then-insert races; the refusal that is a 402 rather than a 500; the derived count rather than a stored one. 011 changes the unit and the payer, not those. What it does overturn is stated there and stated plainly: the payer moves from a person to the project, which deletes sponsorship outright, and "failed is refunded" dies because a run that burned real tokens has to be paid for.

**Built beyond the original scope, and still standing:** submitting into a project was owner-only before this spec, which left sponsorship with no consumer. A project's collaborators may open sessions in it (`mayWorkInProject`); a stranger still cannot, and viewing a public project confers nothing. `GET /projects/{id}` reports `may_submit` so the client can hide the form it would be refused for. Sponsorship is gone; this outlived it, and 011 leans on it — the project pays, so anyone who may work in a project spends its credits.

**Scope (as built, now superseded):** how many replication sessions an invited person may run, who pays for a session started in someone else's project, and where the remaining allowance is shown. One structural check inside `Enqueue`, in the transaction that inserts the row. A child of [006](006-web-application.md).
**Depends on:** [006k](006k-the-platform-admin-console.md) (the allowance an admin sets), [006b](006b-job-model-and-persistence.md) (the job store and its enqueue guard), [006g](006g-projects-and-replication-sessions.md) (projects own sessions), [006c](006c-identity-and-access.md) (the tier, project membership).

## Problem

One shared credential pays for every run. 006c closed the door on *who* may run, and nothing bounds *how much* they run once through it: the only per-user constraint in the system is the double-trigger guard, which forbids two live sessions on the same paper and says nothing about a hundred sessions on a hundred papers.

The seed phase needs a number. It also needs the number to survive the two ways a session is born — `POST /projects` starting several papers at once (006j) and `POST /papers` appending one — and the fact that a collaborator can submit into a project they do not own, spending an allowance that is not obviously theirs.

## Decisions

**The check lives inside `Enqueue`, in the same transaction as the insert.** Not in a handler, not in a middleware. Two reasons, and the second is the load-bearing one:

- A handler check is bypassable. `Enqueue` is the single narrowest point every session passes through — the two HTTP doors and any third one added later — so a new submit path inherits the cap instead of quietly escaping it. This is the same structural argument 006c makes for the gate.
- A read-then-insert check races. Two concurrent submits both read "4 of 5 used" and both insert. Counting inside the writing transaction, next to the double-trigger guard that already lives there, makes the cap exact rather than approximate.

The store learns the allowance through a function field, so `internal/job` does not import `internal/platform`:

```go
// Allowance reports what a payer may spend. Injected by the composition root;
// nil means unlimited, which is what a test or an offline run wants.
type Allowance func(ctx context.Context, principal string) (n int, unlimited bool, err error)
```

An over-quota enqueue returns `job.ErrQuotaExceeded`, mapped to `402` with code `quota_exceeded`, mirroring how `ErrActiveSession` is already surfaced.

**Consumed = sessions that finished `done`, plus sessions currently in flight.** The literal reading — count only `done` — is exploitable, because nothing is `done` at submit time and a hundred simultaneous submits would all pass. So a running session *holds* a unit of allowance, and the hold is released when the session ends in `failed` or cancelled. The user pays for work that worked, is refunded for work that broke, and cannot burst past the cap while nothing has finished yet.

| Phase | Counts |
|---|---|
| `submitted` … any non-terminal | yes — held |
| `done` | yes — spent |
| `failed` (including cancelled, which lands there) | no — refunded |

This is a *derived* count over `jobs`, never a stored counter: a materialised balance is a second source of truth that drifts the first time a session is deleted or a phase is corrected. The index that makes it cheap is `(charged_to, phase)`.

**Each session records who pays, on the row.** `jobs` gains `charged_to TEXT NOT NULL`, backfilled to `principal`. Payer defaults to the submitter; the exception is sponsorship. Keeping it on the row means a refund, an admin's usage table, and the remaining-quota display all read one column rather than re-deriving intent from project ownership months later.

**Sponsorship is standing consent, granted once, revocable, and never implicit.** `projects` gains `sponsored BOOLEAN NOT NULL DEFAULT false`, set by the owner alone. When it is on, a member submitting into that project may choose to charge the owner; when it is off, the choice is not offered and a request asking for it is refused. The submit carries `charge_to: "me" | "project"`, defaulting to `"me"`, and the server validates rather than trusts: `"project"` requires the project to be sponsored, and the payer resolved from it is the project owner and nobody the client named.

Rejected: **per-request approval** — a session that stalls until a sponsor wakes up needs a new job state and an approval inbox, and buys nothing that a revocable standing consent does not. Rejected: **the owner always pays** — a collaborator could drain an allowance nobody offered.

**A platform admin is unlimited, and pays for nothing.** Admins own the whole platform budget; metering them against a number they set themselves is friction with no safety value. `Allowance` reports `unlimited`, `Enqueue` skips the count entirely, and an admin's sessions are still recorded with `charged_to` set, so usage remains readable even where it is uncapped.

**Batch submits need no batch arithmetic.** 006j already enqueues each paper independently and reports `{url, error}` per paper, keeping the project if at least one started. A five-paper submit by someone with three left therefore starts three and reports `quota_exceeded` on the other two — no separate batch-size pre-check, and no path where a batch slips through a per-request check.

**The remaining allowance is visible where the spending happens.** `GET /me` gains `quota: {used, limit, unlimited}`, so the app shell renders a pill beside the "New project" button, and the submit screen's payer picker shows what is left on each side of the choice. The value is refetched after any submit, so it never shows a stale surplus. The admin's grant table (006k) gains the same used/limit column, which is where an admin decides who needs a top-up.

## Acceptance criteria

1. With a limit of N, the N+1st enqueue is refused with `ErrQuotaExceeded` → `402 quota_exceeded`, asserted against both the mem and Postgres stores through the one contract test the double-trigger guard already uses.
2. Concurrency is exact: parallel enqueues at the boundary let through exactly the remaining number and no more.
3. A session that ends `failed` or is cancelled releases its hold — the same principal may then enqueue again; a session that reaches `done` does not.
4. In-flight sessions count while running, so a burst of submits by a user at the limit is refused rather than admitted-then-reconciled.
5. `charge_to: "project"` in a sponsored project charges the owner; in an unsponsored one it is refused (`403 not_sponsored`); a client naming an arbitrary payer is ignored — the payer is always resolved server-side.
6. Only the owner may set `sponsored`; a collaborator's attempt is `403 not_owner`.
7. A platform admin is never refused and never metered, and their sessions still record `charged_to`.
8. A five-paper `POST /projects` by a user with three remaining starts three and reports `quota_exceeded` for the rest, with the project surviving (006j's rule).
9. `GET /me` reports `{used, limit, unlimited}` matching the store's own count, and the header pill renders it; the admin grant table shows the same numbers.
10. The cap cannot be dodged by a new route: the check is inside `Enqueue`, and a test drives every session-creating path (`POST /papers`, `POST /projects`) to the same refusal.

## Out of scope

- **The append path's payer default.** `POST /papers` defaults `charge_to` to `"me"`, and `POST /projects` (create) always charges the creator, who is the owner. The picker appears only where the choice is real: a sponsored project someone else owns.
- **The CLI.** `replichai audit --publish` runs outside the HTTP surface with the operator's own credentials; it is not metered here. Stated so the gap is a decision rather than a bug.
- **Token or cost budgets.** Usage tokens are recorded per session (006h) and displayed; charging against them is a different and much harder policy than counting sessions.
- **Rolling windows and resets.** The cap is lifetime for the seed phase. A monthly plan is a time-bucketed count over the same column, and can arrive without changing the model.
- **Payment, plans, and self-serve top-up.** An admin grants headroom (006k); nobody buys it.
- **Transferring an existing session's payer.** `charged_to` is set at enqueue and never rewritten.
