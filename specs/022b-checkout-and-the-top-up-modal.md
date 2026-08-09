# 022b — Checkout, the webhook, and the top-up modal

**Status:** validated — ready to build. Leaf of [022](022-money-comes-in-without-an-admin.md).

**Scope:** the visible half of a purchase. The Stripe adapter behind 022a's port, a signed-in `POST /me/credits/checkout` (a `usd`/`eur` allow-list, the spread and its snapshot, a per-currency floor), the signed `POST /stripe/webhook` that credits on `checkout.session.completed` — reading the USD a EUR charge is worth from Stripe's own rate — and the frontend top-up modal, the `/wallet` return, and its brief poll. Refunds, the solvency read, and the freeze are later leaves.

**Depends on:** [022a](022a-the-idempotent-paid-topup.md) (the port, `TopupPaid`, `external_ref` idempotency).

## Goal

Let a signed-in person buy credits with a card and see them land, with the webhook — not the redirect — as the source of truth, and EUR converted once at Stripe's rate.

## What to do — server

1. **Stripe adapter** — `internal/payment/stripe`, implementing 022a's `Provider` over `stripe-go`. `CreateCheckout` opens a Checkout Session (`mode=payment`, `currency` from the allow-list, one line item of `amountMinor`, `success_url=<base>/wallet?checkout=success`, `cancel_url=<base>/wallet`, `metadata`: `email`, `spread_bps`, `spread_fixed_micro`, `currency`). `ParseWebhook` verifies the signature with `STRIPE_WEBHOOK_SECRET`. Config from env: `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_PUBLISHABLE_KEY`; **absent ⇒ a refusing adapter** returning `payment.ErrUnconfigured`.

2. **Settings** — extend `platform.Settings` and `PUT /admin/settings` (`internal/webauth/admin.go` `SetSettings`) with `spread_bps int64`, `spread_fixed_micro int64`, and a per-currency floor `min_topup map[string]int64` (usd/eur). Rendered beside the rate card in the console. The spread is **snapshotted onto the Checkout session metadata** at create time, so a mid-flight edit cannot change what a paid session credits.

3. **Checkout route** — `rt.gatedHandle("POST", "/me/credits/checkout", rt.startCheckout)` (`internal/httpapi`). Decode `{amount_minor int64, currency string}`; reject a currency outside `usd`/`eur` (`400 bad_currency`) and an amount below the floor (`400 below_min`); create the session via the injected provider; return `{url}`. Provider unconfigured ⇒ `501 payments_unconfigured` (not a panic).

4. **Webhook route** — `rt.openHandle("POST", "/stripe/webhook", rt.stripeWebhook)` (off the operator `Gate`, still enumerated in `OpenRoutes()`; Stripe carries no session cookie). Read the raw body, `ParseWebhook`; a bad/missing signature ⇒ `400`, nothing posted. On `checkout.session.completed` with `payment_status == "paid"`: resolve the charge's **balance transaction** to get the charge's USD value (`gross_micro`); apply the **snapshotted** spread → `credited_micro = gross_micro − round(gross_micro×spread_bps/10_000) − spread_fixed_micro`; call `credits.TopupPaid(email, credited_micro, payment_intent, "stripe:"+payment_intent)`. Return `200` (also on a duplicate). Any other event type ⇒ `200`, ignored.

5. **Wire it** — construct the adapter in `cmd/server/main.go run()` and thread it into `httpapi.Deps` (checkout + webhook) and `buildHandler`. A nil/refusing adapter keeps the app booting unmetered.

6. **Quote** — expose `spread_bps`, `spread_fixed_micro`, and the floor on `GET /me/credits` (extend the `Wallet`/`Credits` payload) so the modal computes *"€X → $Y"* client-side without a round-trip per keystroke.

## What to do — frontend (`frontend/src`)

7. **api.ts** — `startCheckout(amountMinor, currency): {url}`; extend `Credits`/`Wallet` types with the spread + floor fields.
8. **TopUpModal** — a new component on `Modal`/`PromptDialog` (`ui.tsx`): a USD/EUR toggle, an amount field (`parseUSD`-style, minor units), a live *"you'll get $Y in credits"* from the public spread, and a **Continue to payment** button that calls `startCheckout` and sets `window.location = url`.
9. **Trigger from 402** — where `startErrLabel` (`errors.ts:78`) / `PapersCard.start` (`App.tsx:2920`) shows `no_credits` inline today, add a **Top up** button that opens `TopUpModal`.
10. **/wallet route** — add a `wallet` variant to the `View` union and `SCREENS` (`routes.ts`), a nav item and render branch in `Shell`. The screen reads `window.location.search`; on `checkout=success`, poll `refreshMe`/`getWallet` a few times over a few seconds (the `setTimeout(refresh, 1500)` precedent), with a *"credits will appear shortly"* fallback.
11. **i18n** — add the new keys to the `en` block (`i18n.ts`); only `en` exists today, so no other locale to touch.

## Tests

- **Go**, driving the `MemProvider` fake: `startCheckout` returns a URL and refuses a bad currency / below-floor amount / unconfigured provider; a synthesized `checkout.session.completed` posts exactly one `TopupPaid`; a **EUR** event with a known exchange rate credits `gross_micro − spread`; a **bad signature** ⇒ `400`, nothing posted; a **duplicate** event ⇒ one credit and `200` twice; a return URL carrying an **inflated `amount` param** leaves the wallet unchanged (credited only from the event).
- **Frontend**: the 402 opens `TopUpModal`, the quote renders, the CTA calls `startCheckout`; the `/wallet` return polls and reflects the new balance.

## Boundaries

- No refund/dispute handling, no solvency read (022c). No freeze/banner (022d).
- Do not change the ledger schema (022a owns it).
- Do not add a `zh` locale; add `en` keys only.
