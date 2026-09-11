---
title: "The freeze, server-side"
status: built
---

# 022d — The freeze, server-side

**Status:** built. Leaf of [022](022-money-comes-in-without-an-admin.md). Backend only; the frozen account's UI is [022f](022f-the-frozen-account-in-the-ui.md). Four commits: `9605f29` (the `access.FreezeOverrides` store: migration v4, mem + Postgres, the `OverrideActive`/`GrantOverride`/`ClearOverride` recorded state), `68ccb31` (`webauth.FreezeGate` refusing every mutating request from a negative wallet `402 account_frozen`, composed inside the auth middleware in `cmd/server`), `0bc9eef` (`frozen` on `GET /me`, and the admin freeze flag + `?filter=negative` + `POST/DELETE /admin/access/{email}/unfreeze`), `38166d6` (`notify.AccountFrozen`, sent once from the webhook reversal on the crossing into the red). Tests green on mem + real Postgres 16 across all four, including the gate refusing/allowing by method, the one-shot override surviving a further debit and retiring at solvency, the admin freeze-list + unfreeze, and the freeze email firing once on the crossing.

**Deviation from the sketch: where clear-on-positive lives.** The spec called for clearing the override at each wallet-crediting path (`TopupPaid`, admin `Topup`, `Reclaim`). It is instead retired **lazily, inside `webauth.Frozen`**, the one predicate the gate, `/me`, and the admin list all read, which clears a spent override the next time the account is observed solvent. This keeps the override's whole lifecycle in one function rather than spreading a clear across three crediting sites in two packages. The cost is that an override can outlive its debt until the next solvent read; this is harmless because a negative wallet cannot allocate regardless of the freeze (the ledger refuses it independently), so the freeze's purpose, blocking *new* work on a debt, is unaffected. The gate also **fails open** on a balance-read error (log and allow), for the same reason: the ledger is the money-safety backstop, and a database fault must not freeze everyone.

**Scope:** the enforcement half of the account freeze. A mutable override store (recorded state, never the append-only ledger), the `frozen` derivation, clear-on-positive, the `FreezeGate` middleware that refuses mutating requests for a negative-wallet account, the one-shot admin unfreeze route, the `Frozen` flag and negative filter on the admin people list, the freeze email, and `frozen` on `GET /me`. No frontend.

**Depends on:** [022c](022c-refunds-and-the-solvency-read.md) (the negative wallet a refund produces, and the webhook path the freeze email is sent from).

## Goal

Stop a clawed-back account from costing more compute before it makes the debt good, enforced server-side, so the UI (022f) is a mirror over a gate that does not depend on it.

## What to do

1. **Override store.** Recorded, mutable, so **not** the append-only ledger. Add to `internal/access` (parallel to `Revoke`/`RevokedAt`): a table `freeze_overrides(email TEXT PRIMARY KEY, granted_by TEXT, granted_at TIMESTAMPTZ, cleared_at TIMESTAMPTZ)` and an interface
   ```go
   type Overrides interface {
       Active(ctx context.Context, email string) (bool, error) // row present, not cleared
       Grant(ctx context.Context, byAdmin Principal, email string) error
       Clear(ctx context.Context, email string) error
   }
   ```
   PG + mem impls, keyed by normalized email (the wallet's key rule).

2. **Frozen derivation.** One helper: `frozen(email) = credits.Balance(Wallet(email)) < 0 && !overrides.Active(email)`. Used by the gate, `GET /me`, and the admin list so they cannot disagree.

3. **Clear-on-positive.** The override is one-shot: it is retired when the wallet returns to `≥ 0`, so a later chargeback re-freezes as a fresh decision. **Built lazily inside `Frozen`** (see the Status deviation): the predicate clears a spent override the next time the account is read solvent, keeping the lifecycle in one place rather than at each crediting path.

4. **FreezeGate.** `webauth.FreezeGate(credits, overrides) func(http.Handler) http.Handler`, composed in `buildHandler` **after** the auth middleware sets identity: `authMiddleware(FreezeGate(mux))`. Rule: allow `GET`/`HEAD`; allow `POST /me/credits/checkout`, `POST /stripe/webhook`, and sign-out; for any other mutating method, if `frozen(id.Email)` ⇒ `httpapi.WriteError(w, 402, "account_frozen")`. Gating by method covers every mutating operator/credit route uniformly while reads (including the project archive download) stay open. Keep the ledger-level guards intact: the gate is the coarse check, and the store still refuses server-side.

5. **Admin surface** (`internal/webauth/admin.go`):
   - Append `{POST /admin/access/{email}/unfreeze}` → `a.Unfreeze` (records `overrides.Grant`); a `DELETE` on the same path re-freezes a still-negative account (`overrides.Clear`).
   - Add `Frozen bool` to `GrantView`, populated in `List` from the balance already merged in plus `overrides.Active`.
   - Add a `?filter=negative` query to `List` returning only negative-wallet rows.

6. **`GET /me` carries `frozen`.** `webauth.Me` computes `frozen(id.Email)` and returns it, so 022f's banner and disabled controls read one field.

7. **Freeze email.** `notify.AccountFrozen(ctx, to)` + `renderAccountFrozen` (escape every interpolated value, link the `/wallet` route via `n.baseURL`), following `ProjectInvitation`. Extend the `httpapi.Notifier` interface with the method. Send it from the 022c webhook refund path when the `adjust` drives the balance from `≥ 0` to `< 0` (the crossing into frozen), guarded `rt.notifier != nil`, failures logged via `notify.LogFailure`.

## Tests

- **Gate**: a negative wallet has a representative `POST` (start session / allocate / create project) refused `402 account_frozen`, while a `GET` and the archive download still `200`, and `POST /me/credits/checkout` is allowed.
- **Thaw**: after a `TopupPaid` crosses `≥ 0`, the same `POST` succeeds with no unfreeze call; the override is cleared on the crossing.
- **Override**: `unfreeze` lets a still-negative account `POST`; a **further** negative adjust keeps it writable; a top-up to `≥ 0` clears the override; a subsequent negative **re-freezes**.
- **Admin**: `GrantView.Frozen` matches the gate; the `?filter=negative` returns exactly the owing rows; the unfreeze route records the override and is refused below `TierAdmin`.
- **`GET /me`**: reports `frozen` matching the gate.
- **notify**: `AccountFrozen` sends once with the reason and the top-up remedy, HTML-escaped; a nil `Notifier` is silent.

## Boundaries

- No frontend; the banner, disabled controls, and console freeze list are 022f.
- Freeze on a negative **wallet** only, never a negative pot (a named deferred spec in the parent).
- The override store is its own recorded state, never an entry in the ledger.
- No dunning beyond the freeze and the one email: no retry schedules, no escalation.
