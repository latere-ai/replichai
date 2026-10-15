---
title: "Checkout and the webhook"
status: built
---

# 022b — Checkout and the webhook

**Status:** built. One of six specs, 022a to 022f, for credits a person buys by card. Backend only; the buying UI is [022e](022e-buying-credits.md). Three commits: `platform` gained `spread_bps`/`spread_fixed_micro`/`min_topup_micro` (migration v6, seeded + admin-editable, `Settings.Credited(gross)`); `latere.ai/x/pay/stripe` implements the port over `stripe-go/v82` (USD-only checkout, `webhook.ConstructEventWithOptions` verifying the signature and tolerating API-version drift, unconfigured ⇒ refusing adapter); and `internal/httpapi` added `POST /me/credits/checkout` (gated), the open `POST /stripe/webhook`, the spread applied and snapshotted into session metadata at create, and the spread/floor quote on `GET /me/credits`. Wired in `cmd/server` via `paystripe.New()`. Tests green on mem + real Postgres 16, including the checkout quote, the below-floor refusal, the `payments_unconfigured` path, and the webhook crediting once on a retried delivery and refusing a bad signature. Env: `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`.

**Two things the build settled:** the webhook is signature-verified with `IgnoreAPIVersionMismatch: true`; it reads only stable fields, so a webhook endpoint on a different API version than the pinned SDK must not crash crediting. And the checkout request is `{amount_micro}` snapped to whole cents, with the credited amount computed from the spread at create and carried in metadata, so the webhook never recomputes.

**Hardening (follow-up, commit `aa22f0c`):** the adapter also credits on `checkout.session.async_payment_succeeded`, because EU methods (SEPA Direct Debit, iDEAL, Bancontact) leave `completed` unpaid and confirm asynchronously; the ledger's payment_intent dedupe keeps the two events for one purchase crediting once. **The webhook endpoint must subscribe to** `checkout.session.completed`, `checkout.session.async_payment_succeeded`, `checkout.session.async_payment_failed`, and (022c) `charge.refunded`, `charge.dispute.created`. Deferred: an idempotency key on session *creation* (needs a client nonce; lands with 022e); auto receipts (a Stripe dashboard toggle, not code).

**Scope:** the server half of a purchase. The Stripe adapter behind 022a's port, a signed-in `POST /me/credits/checkout` (an amount in USD, the spread applied and snapshotted at create), and the signed `POST /stripe/webhook` that credits on `checkout.session.completed`, plus the wiring and the quote fields the modal will read. All Go, provable with the `MemProvider` fake; no frontend, no refund handling, no freeze.

**Depends on:** [022a](022a-the-idempotent-paid-topup.md) (the `payment.Provider` port, `TopupPaid`, `external_ref` idempotency).

## The currency mechanic (decided)

The ledger is micro-USD and a credit is prepaid USD compute (011), so **the checkout is always created in USD**, for the amount of credit the person is buying. EU customers see and pay the euro equivalent through Stripe's **Adaptive Pricing**, which is a dashboard toggle, not code: this leaf never charges, reads, or converts EUR. The USD we grant is exactly the USD we put on the session, so there is **no balance-transaction read and no self-managed FX rate**; the settlement problem that a "charge EUR, read the USD back" design would hit does not arise. `Event.GrossUSDMicro` from 022a's port is unused on this path; the credited amount is decided at create.

The spread is applied **at create**, not in the webhook: we compute `credited_micro = gross − round(gross×spread_bps/10_000) − spread_fixed_micro` when the session is made and store it in the session **metadata** (with the email). This *is* the snapshot 011's argument asks for (an admin editing the spread mid-flight cannot change what an in-flight session credits), and it makes the webhook trivial: read `credited_micro` and `email` from the verified event, post them. Metadata is trustworthy because the webhook signature is verified; it is never a redirect parameter.

## Goal

Let a signed-in person's card payment land as credits, with the webhook, not the redirect, as the source of truth. Everything testable without a browser.

## What to do

1. **Stripe adapter.** `latere.ai/x/pay/stripe`, implementing 022a's `Provider` over `stripe-go`. `CreateCheckout` opens a Checkout Session (`mode=payment`, `currency=usd`, one line item of `AmountMinor` USD cents, `success_url=<base>/wallet?checkout=success`, `cancel_url=<base>/wallet`, `metadata`: `email`, `credited_micro`). `ParseWebhook` verifies the signature with `STRIPE_WEBHOOK_SECRET` and reduces a `checkout.session.completed` to an `Event` carrying the metadata and the `payment_intent`. Config from env: `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`; **absent ⇒ a refusing adapter** returning `pay.ErrUnconfigured`. Hosted Checkout redirects, so the browser never talks to Stripe and no publishable key is read. (EUR presentment is the account's Adaptive Pricing setting; no code.)

2. **Settings.** Extend `platform.Settings` and `PUT /admin/settings` (`internal/webauth/admin.go` `SetSettings`) with `spread_bps int64`, `spread_fixed_micro int64`, and a single USD floor `min_topup_micro int64`, seeded with sensible defaults and validated non-negative, rendered beside the rate card.

3. **Checkout route.** `rt.gatedHandle("POST", "/me/credits/checkout", rt.startCheckout)`. Decode `{amount_micro int64}` (USD); reject an amount below `min_topup_micro` (`400 below_min`); compute `credited_micro` from the current spread and pass both the USD cents to charge and `credited_micro` (in metadata) to `CreateCheckout`; return `{url}`. Provider unconfigured ⇒ `501 payments_unconfigured` (not a panic).

4. **Webhook route.** `rt.openHandle("POST", "/stripe/webhook", rt.stripeWebhook)` (off the operator `Gate`, still enumerated in `OpenRoutes()`; Stripe carries no session cookie). Read the raw body, `ParseWebhook`; a bad/missing signature ⇒ `400`, nothing posted. On `EventCheckoutCompleted`: read `email` and `credited_micro` from the event metadata and call `credits.TopupPaid(email, credited_micro, payment_intent, "stripe:"+payment_intent)`. Return `200` (also on a duplicate). Any other event kind ⇒ `200`, ignored.

5. **Wire it.** Construct the adapter in `cmd/server/main.go run()` and thread it into `httpapi.Deps` (checkout + webhook) and `buildHandler`. A nil/refusing adapter keeps the app booting unmetered.

6. **Quote fields.** Expose `spread_bps`, `spread_fixed_micro`, and `min_topup_micro` on `GET /me/credits` (extend the `Wallet` payload) so 022e's modal computes *"$X buys $Y"* client-side without a round-trip per keystroke.

## Tests

Driving the `MemProvider` fake (`internal/httpapi`):
- `startCheckout` returns a URL; refuses a below-floor amount (`400 below_min`) and an unconfigured provider (`501 payments_unconfigured`); the `credited_micro` it puts in the session metadata equals `gross − spread` from the current settings.
- A synthesized `EventCheckoutCompleted` posts exactly one `TopupPaid` for the `credited_micro` in its metadata.
- A **bad signature** ⇒ `400`, nothing posted; a **duplicate** event ⇒ one credit and `200` twice.
- A return URL carrying an **inflated `amount` param** leaves the wallet unchanged (credited only from the event metadata, never the redirect).
- Settings round-trip: `PUT /admin/settings` persists the spread + floor and a failed read falls back to the seeded defaults.

## Boundaries

- No frontend; the modal, `/wallet`, and i18n are 022e.
- No refund/dispute handling, no solvency read (022c). No freeze (022d).
- Do not change the ledger schema (022a owns it).
- No EUR in code: presentment is the Stripe account's Adaptive Pricing setting.
