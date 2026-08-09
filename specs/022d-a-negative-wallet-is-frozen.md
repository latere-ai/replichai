# 022d — A negative wallet is frozen

**Status:** validated — ready to build. Leaf of [022](022-money-comes-in-without-an-admin.md).

**Scope:** the account freeze. A mutable override store (recorded state, never the append-only ledger), a `FreezeGate` middleware that refuses mutating requests for a negative-wallet account, thaw-by-arithmetic, a one-shot admin override cleared on return to positive, the freeze email, the admin freeze flag + negative filter + unfreeze action, and the frontend banner with disabled spend controls.

**Depends on:** [022c](022c-refunds-and-the-solvency-read.md) (the negative wallet a refund produces, and the webhook path the freeze email is sent from).

## Goal

Stop a clawed-back account from costing more compute before it makes the debt good — enforced server-side, mirrored in the UI, reversible by a top-up or an admin.

## What to do — server

1. **Override store** — recorded, mutable, so **not** the append-only ledger. Add to `internal/access` (parallel to `Revoke`/`RevokedAt`): a table `freeze_overrides(email TEXT PRIMARY KEY, granted_by TEXT, granted_at TIMESTAMPTZ, cleared_at TIMESTAMPTZ)` and an interface
   ```go
   type Overrides interface {
       Active(ctx context.Context, email string) (bool, error) // row present, not cleared
       Grant(ctx context.Context, byAdmin Principal, email string) error
       Clear(ctx context.Context, email string) error
   }
   ```
   PG + mem impls, keyed by normalized email (the wallet's key rule).

2. **Frozen derivation** — one helper: `frozen(email) = credits.Balance(Wallet(email)) < 0 && !overrides.Active(email)`. Used by the gate and the admin list so they cannot disagree.

3. **Clear-on-positive** — the override is one-shot: whenever a wallet credit brings the balance to `≥ 0`, clear it. Centralize in one helper called by the three wallet-crediting paths — `TopupPaid` (022a), admin `Topup`, `Reclaim` — after posting: if `Balance ≥ 0`, `overrides.Clear(email)`. So a later chargeback re-freezes, a fresh decision.

4. **FreezeGate** — `webauth.FreezeGate(credits, overrides) func(http.Handler) http.Handler`, composed in `buildHandler` **after** the auth middleware sets identity: `authMiddleware(FreezeGate(mux))`. Rule: allow `GET`/`HEAD`; allow `POST /me/credits/checkout`, `POST /stripe/webhook`, and sign-out; for any other mutating method, if `frozen(id.Email)` ⇒ `httpapi.WriteError(w, 402, "account_frozen")`. Gating by method covers every mutating operator/credit route uniformly while reads (incl. the project archive download) stay open. Keep the ledger-level guards intact — the gate is the coarse net, the store still refuses server-side.

5. **Admin surface** (`internal/webauth/admin.go`):
   - Append `{POST /admin/access/{email}/unfreeze}` → `a.Unfreeze` (records `overrides.Grant`); a `DELETE` on the same path re-freezes a still-negative account (`overrides.Clear`).
   - Add `Frozen bool` to `GrantView`, populated in `List` from the balance already merged in plus `overrides.Active`.
   - Add a `?filter=negative` query to `List` returning only negative-wallet rows.

6. **Freeze email** — `notify.AccountFrozen(ctx, to)` + `renderAccountFrozen` (escape every interpolated value, link the new `/wallet` route via `n.baseURL`), following `ProjectInvitation`. Extend the `httpapi.Notifier` interface with the method. Send it from the 022c webhook refund path when the `adjust` drives the balance from `≥ 0` to `< 0` (the crossing into frozen), guarded `rt.notifier != nil`, failures logged via `notify.LogFailure`.

## What to do — frontend (`frontend/src`)

7. **Me.frozen** — add `frozen bool` to the `Me` type (`api.ts`) and compute it in `webauth.Me` (`GET /me`) from balance + override.
8. **Banner** — a global `banner warn` (frozen-banner) in `Shell` above `.main` when `me.frozen`, explaining the freeze and carrying a **Top up** button that opens `TopUpModal` (022b).
9. **Disable spend/create** when `me.frozen`: the "New project" button (`App.tsx:587`), `PapersCard` start/add, `ProjectCreditsCard` allocate — disabled with a tooltip; the only enabled money action is Top up.
10. **Console** — show each row's state (normal/frozen/banned), a filter toggle to negative-wallet accounts, and an **Unfreeze** button calling `POST /admin/access/{email}/unfreeze`.
11. **i18n** — new `en` keys (frozen banner, unfreeze, filter label, `account_frozen` branch in `startErrLabel`).

## Tests

- **Gate**: a negative wallet has a representative `POST` (start session / allocate / create project) refused `402 account_frozen`, while a `GET` and the archive download still `200`, and `POST /me/credits/checkout` is allowed.
- **Thaw**: after a `TopupPaid` crosses `≥ 0`, the same `POST` succeeds with no unfreeze call; the override is cleared on the crossing.
- **Override**: `unfreeze` lets a still-negative account `POST`; a **further** negative adjust keeps it writable; a top-up to `≥ 0` clears the override; a subsequent negative **re-freezes**.
- **Admin**: `GrantView.Frozen` matches the gate; the `?filter=negative` returns exactly the owing rows; the unfreeze route records the override and is refused below `TierAdmin`.
- **notify**: `AccountFrozen` sends once with the reason and the top-up remedy, HTML-escaped; a nil `Notifier` is silent.
- **Frontend**: the banner shows when `me.frozen`, spend/create controls are disabled, and Top up still works.

## Boundaries

- Freeze on a negative **wallet** only, never a negative pot (a named deferred spec in the parent).
- The override store is its own recorded state, never an entry in the ledger.
- No dunning beyond the freeze and the one email — no retry schedules, no escalation.
