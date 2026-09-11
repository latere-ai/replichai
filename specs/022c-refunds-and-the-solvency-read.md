---
title: "Refunds and the solvency read"
status: built
---

# 022c — Refunds and the solvency read

**Status:** built. Leaf of [022](022-money-comes-in-without-an-admin.md). The webhook (022b) now reverses `charge.refunded` and `charge.dispute.created`: the adapter parses each into the original purchase's `payment_intent` plus the reversal's own id, and `reversePurchase` looks up the original top-up via `EntryByExternalRef` and posts a negative `adjust` of the **exact micro-USD it credited** (the FX-drift rule), keyed on the reversal id so it dedupes independently and may take the wallet negative. `credit.TotalOutstanding` folds every wallet and pot (less holds) and `credit.WalletEmail` inverts a wallet holder to the person to reverse against. `GET /admin/solvency` serves the number, tier-gated. Three commits: `4398095` (credit), `c585ae9` (adapter), `a1b1130` (webhook + route). Tests green on mem + real Postgres 16, including a refund reversing exactly once on a retried delivery, an unknown-purchase refund acknowledged and dropped, and the solvency fold.

**Deviation from the sketch:** the reversal derives the wallet's email from the original entry's holder (`credit.WalletEmail`) rather than the refund event, since a `charge.refunded` object carries no email; the original top-up is the source of truth for whose credit is being reversed.

**Scope:** the same signed webhook learns to reverse. `charge.refunded` and `charge.dispute.created` post a negative `adjust` for the exact micro-USD the purchase credited (FX drift borne by the platform), keyed on the refund/dispute id so it dedupes independently. And the backend gains one number, outstanding sold liability, which the operator watches against what the Anthropic account holds. Backend only; the console renders the number in [022f](022f-the-frozen-account-in-the-ui.md).

**Depends on:** [022b](022b-checkout-and-the-webhook.md) (the webhook handler this extends; the purchase entries it reverses).

## Goal

Undo a clawed-back purchase in the ledger, in the unit it was made, and give the operator a solvency gauge, the read that replaces the vendor-coupling non-goal.

## What to do

1. **Refund/dispute in the webhook.** Extend `rt.stripeWebhook` (022b): on `charge.refunded` and `charge.dispute.created` (`funds_withdrawn`), resolve the email and the original purchase from the event's `payment_intent`, look up the original topup via `credits.EntryByExternalRef(payment_intent)` (022a), and post `credits.RefundPaid(email, -entry.Amount, <refund_or_dispute_id>, "stripe:refund:"+id)`. `external_ref` is the **refund/dispute id** (distinct from the purchase `pi`), so it dedupes on its own row. Reversing the original `credited_micro`, not a freshly converted euro amount, is the FX-drift decision from the parent: *claw back the USD credited, not the euros paid*. Unknown or re-delivered ⇒ `200`, no double.

2. **Outstanding liability.** Add `TotalOutstanding(ctx) (int64, error)` to the credit `Store` (both impls): `SELECT COALESCE(SUM(amount),0) FROM credit_entries WHERE kind NOT IN ('hold','release')`, every settled credit in the system, i.e. `Σ wallet balances + Σ pot balances`, the credits sold and not yet delivered as compute.

3. **Solvency route.** `GET /admin/solvency` in `MountAdmin` (`internal/webauth/admin.go`), tier-gated, returning `{outstanding_micro}`. (Or fold the field into the existing `Settings` response; pick one and keep it.) The console renders it in 022f.

## Tests

- A `charge.refunded` event posts one negative `adjust`, `external_ref` = refund id; the wallet goes negative; a **duplicate** refund event is a no-op and still `200`.
- A `charge.dispute.created` event is handled the same way.
- A **EUR** purchase then refund claws back the **same** micro-USD the purchase credited (a changed FX rate does not change the reversal), asserted via the `EntryByExternalRef` reversal path.
- `TotalOutstanding` equals the fold over the ledger (mem + Postgres contract), including after a refund takes a wallet negative.
- `GET /admin/solvency` returns it and is refused below `TierAdmin`.

## Boundaries

- Backend only; the console rendering of the solvency figure is 022f.
- No freeze gate, no unfreeze, no freeze email (022d); this leaf only posts the adjust and reports liability. The negative wallet it produces is what 022d reacts to.
- No new currencies; no ledger schema change (022a owns it).
