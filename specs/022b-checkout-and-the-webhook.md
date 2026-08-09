# 022b — Checkout and the webhook

**Status:** validated — ready to build. Leaf of [022](022-money-comes-in-without-an-admin.md). Backend only; the buying UI is [022e](022e-buying-credits.md).

**Scope:** the server half of a purchase. The Stripe adapter behind 022a's port, a signed-in `POST /me/credits/checkout` (a `usd`/`eur` allow-list, the spread and its snapshot, a per-currency floor), and the signed `POST /stripe/webhook` that credits on `checkout.session.completed` — reading the USD a EUR charge is worth from Stripe's own rate — plus the wiring and the quote fields the modal will read. All Go, provable with the `MemProvider` fake; no frontend, no refund handling, no freeze.

**Depends on:** [022a](022a-the-idempotent-paid-topup.md) (the `payment.Provider` port, `TopupPaid`, `external_ref` idempotency).

## Goal

Let a signed-in person's card payment land as credits, with the webhook — not the redirect — as the source of truth, and EUR converted once at Stripe's rate. Everything testable without a browser.

## What to do

1. **Stripe adapter** — `internal/payment/stripe`, implementing 022a's `Provider` over `stripe-go`. `CreateCheckout` opens a Checkout Session (`mode=payment`, `currency` from the allow-list, one line item of `AmountMinor`, `success_url=<base>/wallet?checkout=success`, `cancel_url=<base>/wallet`, `metadata`: `email`, `spread_bps`, `spread_fixed_micro`, `currency`). `ParseWebhook` verifies the signature with `STRIPE_WEBHOOK_SECRET`, and for a completed checkout follows the charge to its **balance transaction** to fill `Event.GrossUSDMicro` (the USD the charge is worth, at Stripe's rate). Config from env: `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PUBLISHABLE_KEY`; **absent ⇒ a refusing adapter** returning `payment.ErrUnconfigured`.

2. **Settings** — extend `platform.Settings` and `PUT /admin/settings` (`internal/webauth/admin.go` `SetSettings`) with `spread_bps int64`, `spread_fixed_micro int64`, and a per-currency floor `min_topup map[string]int64` (usd/eur), rendered beside the rate card. The spread is **snapshotted onto the Checkout session metadata** at create time, so a mid-flight edit cannot change what a paid session credits.

3. **Checkout route** — `rt.gatedHandle("POST", "/me/credits/checkout", rt.startCheckout)`. Decode `{amount_minor int64, currency string}`; reject a currency outside `usd`/`eur` (`400 bad_currency`) and an amount below the floor (`400 below_min`); create the session via the injected provider; return `{url}`. Provider unconfigured ⇒ `501 payments_unconfigured` (not a panic).

4. **Webhook route** — `rt.openHandle("POST", "/stripe/webhook", rt.stripeWebhook)` (off the operator `Gate`, still enumerated in `OpenRoutes()`; Stripe carries no session cookie). Read the raw body, `ParseWebhook`; a bad/missing signature ⇒ `400`, nothing posted. On `EventCheckoutCompleted`: apply the **snapshotted** spread to `GrossUSDMicro` → `credited_micro = gross − round(gross×spread_bps/10_000) − spread_fixed_micro`; call `credits.TopupPaid(email, credited_micro, payment_intent, "stripe:"+payment_intent)`. Return `200` (also on a duplicate). Any other event kind ⇒ `200`, ignored.

5. **Wire it** — construct the adapter in `cmd/server/main.go run()` and thread it into `httpapi.Deps` (checkout + webhook) and `buildHandler`. A nil/refusing adapter keeps the app booting unmetered.

6. **Quote fields** — expose `spread_bps`, `spread_fixed_micro`, and the floor on `GET /me/credits` (extend the `Wallet` payload) so 022e's modal computes *"€X → $Y"* client-side without a round-trip per keystroke.

## Tests

Driving the `MemProvider` fake (`internal/httpapi`):
- `startCheckout` returns a URL; refuses a bad currency (`400 bad_currency`), a below-floor amount (`400 below_min`), and an unconfigured provider (`501 payments_unconfigured`).
- A synthesized `EventCheckoutCompleted` posts exactly one `TopupPaid`, and a **EUR** event with a known `GrossUSDMicro` credits `gross − spread`.
- A **bad signature** ⇒ `400`, nothing posted; a **duplicate** event ⇒ one credit and `200` twice.
- A return URL carrying an **inflated `amount` param** leaves the wallet unchanged (credited only from the event, never the redirect).
- Settings round-trip: `PUT /admin/settings` persists the spread + floor and the snapshot rides on the session metadata.

## Boundaries

- No frontend — the modal, `/wallet`, and i18n are 022e.
- No refund/dispute handling, no solvency read (022c). No freeze (022d).
- Do not change the ledger schema (022a owns it).
