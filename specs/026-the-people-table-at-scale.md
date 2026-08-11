# 026 — The people table at scale

**Status:** designed, not built. A deferred follow-up to [022f](022f-the-frozen-account-in-the-ui.md) / [006k](006k-the-platform-admin-console.md); the current table is fine at beta scale (tens of accounts) and does not hold at 1000+. Recorded so the fix is known before the numbers force it.

**Scope:** make the admin People table hold a thousand-plus accounts — pagination, search, one aggregated balance query instead of an N+1, and a server-side owing filter — without a stored-balance column or a new user table until one is actually warranted.

**Depends on:** [006k](006k-the-platform-admin-console.md) (`GET /admin/access`, the `GrantView`), [022f](022f-the-frozen-account-in-the-ui.md) (the frozen flag, the "Only owing" filter, unfreeze), [011](011-what-a-session-costs.md) (the ledger a balance is folded from, and its argument against a stored balance), [006c](006c-identity-and-access.md) (the grants the list is over).

## Problem

`GET /admin/access` (`Admin.List`) is a beta-scale surface wearing a production hat. Three limits, none of which bite at ten accounts and all of which bite at a thousand:

1. **N+1 balance reads.** `List` loads every grant, then folds a wallet balance **per row** — one `SUM(amount)` query per email. At 1000 accounts that is 1000 round-trips on a single page load, and the frozen flag adds an override lookup per row on top.
2. **No pagination.** The whole grant set is returned and rendered at once. A thousand `member-row`s is a slow response and an unusable scroll.
3. **The owing filter is client-side.** "Only owing" (022f) narrows the already-loaded list. At scale the interesting rows — the handful of accounts in the red — are buried in a payload that had to fetch everyone to find them.

The beta has a handful of users, so this is correct-to-defer. But the growth path that makes payments worth building is exactly the growth path that breaks this screen, so the fix is written down now.

## Decisions

### One aggregated balance query, never N

The per-row fold is the first thing to go. Balances come from **one** pass over the ledger, joined to the grants:

```sql
SELECT g.email, g.tier, g.principal, g.granted_by, g.granted_at,
       COALESCE(b.balance, 0) AS wallet
FROM access_grants g
LEFT JOIN (
  SELECT holder, SUM(amount) AS balance
  FROM credit_entries
  WHERE kind NOT IN ('hold','release')
  GROUP BY holder
) b ON b.holder = 'user:' || g.email
-- + WHERE / cursor / ORDER BY / LIMIT below
```

The frozen flag joins `freeze_overrides` the same way, so the whole row — identity, balance, frozen — is one query per page rather than `1 + 2N`.

### Pagination, and the owing filter, are the query's job

- **Pagination.** A page size and a stable cursor, not offset — offset skips or repeats rows as the set shifts, and grants are inserted continuously (every first sign-in). Cursor on `(granted_at, email)` or plain `email` is stable. `GET /admin/access?cursor=…&limit=…` returns a page plus a next-cursor.
- **Search.** `?q=` matches an email prefix/substring, indexed, so finding one account is a query, not a scroll. This is what an operator actually does at scale — look someone up, not browse.
- **Owing, server-side.** `?filter=negative` becomes a `HAVING SUM(amount) < 0` (or a predicate over the aggregated balance), returning only the accounts in the red — the debts, found without paging through everyone.
- **Sort.** Optionally `?sort=balance` to surface the biggest debtors or holders first; the who-owes-most view an operator wants during a chargeback wave.

### No stored balance, and no user table — yet

011 rejected a materialised balance column outright: *"a materialised total is a second source of truth, and it drifts the first time a row is corrected."* That argument still holds, and the aggregated query above honours it — the balance is still a fold, computed on read, just once instead of N times. It carries comfortably into the low thousands.

**When the aggregate itself gets slow** (a ledger of millions of entries, a hot admin screen), the next step is a **materialised view** or a maintained per-holder aggregate — which *does* reintroduce 011's drift risk and therefore must carry a rebuild-from-ledger reconciliation, so the derived total can always be checked against the truth. That is deferred *within* this spec: name the trigger (row count / p95 latency), do not build it speculatively.

A **dedicated user/account table** — a read-model projection of identity + balance + frozen — is heavier still and is **not** proposed. Today a "user" is `access_grants` joined to a folded balance; that projection is a query, not a table, until the query cannot keep up. Introducing a table before then is a second source of truth to keep in sync for no measured gain.

## Open questions (close before building)

1. **Cursor key** — `(granted_at, email)` (chronological) or `email` (alphabetical, matches a lookup habit)? Affects the default order the operator scrolls.
2. **Materialisation trigger** — what row count or query latency justifies moving from the live aggregate to a maintained one? Pick a number to watch rather than a guess.
3. **Owing at scale** — is `HAVING SUM < 0` over the full ledger fast enough with the right index, or does *the owing view specifically* want the materialised aggregate first (few rows, but scans everything to find them)?

## Out of scope (deliberately)

- **A stored balance column.** 011's rejected design; the aggregated fold replaces the N+1 without it.
- **A dedicated user table.** Not until the aggregate query is measured too slow.
- **Analytics / cohorts / retention.** This is an operations table — find an account, see its balance, act on a debt — not a dashboard.
- **Bulk actions.** Revoke/unfreeze stay per-row; a select-many surface is its own thing if ever needed.

## Acceptance criteria

- `GET /admin/access` returns a **page** (cursor + limit), computes every row's balance in **one** aggregate query (no per-row `SUM`), and joins the frozen flag in the same pass — asserted by a test that counts queries or drives a few hundred accounts and checks latency/shape.
- `?q=` finds an account by email substring; `?filter=negative` returns only negative-wallet accounts **from the query**, not a client filter; both are tested.
- The frontend paginates (load-more or pages) and offers search, with the "Only owing" toggle mapped to the server filter.
- Balances remain a fold over the ledger — no stored balance introduced (a test or a note pins the invariant), and the materialisation path is documented with its trigger rather than built.
