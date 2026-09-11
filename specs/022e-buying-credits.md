---
title: "Buying credits"
status: built
---

# 022e — Buying credits

**Status:** built. Leaf of [022](022-money-comes-in-without-an-admin.md). Frontend (`frontend/src`). `api.startCheckout` posts `{amount_micro}` and returns the hosted URL; `money.creditedMicro` computes the quote with the same arithmetic the server applies (unit-tested to match); `TopUpModal` reads the spread/floor from `/me/credits`, shows the live "$Y in credits for $X" quote, refuses below the floor, and redirects to the processor; `WalletScreen` at `/wallet` shows the statement, a Top up button, and polls `/me` five times over ~7.5s on a `?checkout=success` return so the credit appears without a manual refresh. Typecheck clean, 119 frontend tests pass (including the new `creditedMicro` and route cases).

**Two deviations, both from the 022b currency decision and the app's shape:** (1) **no USD/EUR toggle**: the checkout is USD-only and EU customers see euros via Stripe Adaptive Pricing, so the modal takes a USD amount, with no currency picker. (2) **The trigger is the clickable wallet balance** in the top bar (available on every screen, including a project where a submit was refused) plus a `Wallet` nav item and the `/wallet` screen, rather than an inline button spliced into the `no_credits` error. The modal's open state lives in `Shell` (`setTopUp`), so [022f](022f-the-frozen-account-in-the-ui.md)'s frozen banner reuses the one dialog.

**Scope:** the payer's surface for a top-up. A `startCheckout` client call, a `TopUpModal` (USD/EUR toggle, live *"€X → $Y"* quote from the public spread), the *Top up* button that opens it from the `402 no_credits` path, a `/wallet` route that polls after returning from Stripe, and the `en` copy for all of it.

**Depends on:** [022b](022b-checkout-and-the-webhook.md) (the `POST /me/credits/checkout` route and the spread/floor quote fields on `GET /me/credits`).

## Goal

Turn "you're out of credits" into a two-click purchase, and show the bought credits landing without a manual refresh.

## What to do

1. **api.ts.** `startCheckout(amountMinor, currency): {url}` calling `POST /me/credits/checkout`; extend the `Wallet`/`Credits` types with the `spread_bps`, `spread_fixed_micro`, and per-currency floor fields 022b exposes.
2. **TopUpModal.** A new component on `Modal`/`PromptDialog` (`ui.tsx`): a USD/EUR toggle, an amount field (`parseUSD`-style, minor units), a live *"you'll get $Y in credits"* computed from the public spread, and a **Continue to payment** button that calls `startCheckout` and sets `window.location = url`. Refuse below the floor before calling the server.
3. **Trigger from 402.** Where `startErrLabel` (`errors.ts`) / `PapersCard.start` (`App.tsx`) shows `no_credits` inline today, add a **Top up** button that opens `TopUpModal`. Export the modal open-handler so 022f's frozen banner can reuse it.
4. **/wallet route.** Add a `wallet` variant to the `View` union and `SCREENS` (`routes.ts`), a nav item and render branch in `Shell`. The screen reads `window.location.search`; on `checkout=success`, poll `refreshMe`/`getWallet` a few times over a few seconds (the `setTimeout(refresh, 1500)` precedent), with a *"credits will appear shortly"* fallback.
5. **i18n.** Add the new keys to the `en` block (`i18n.ts`); only `en` exists today, so there is no other locale to touch.

## Tests

- vitest/RTL: a `402 no_credits` opens `TopUpModal`; the quote renders *"€X → $Y"* from a stubbed spread; the CTA calls `startCheckout` with the entered amount + currency; a below-floor amount is refused before any request.
- The `/wallet` screen with `?checkout=success` polls `getWallet`/`refreshMe` and reflects the new balance; without the param it does not poll.

## Boundaries

- No backend; the routes and the quote fields are 022b.
- No frozen banner or disabled controls (022f); this leaf is the buying flow, reused *by* the freeze UI, not the freeze UI itself.
- Do not add a `zh` locale; add `en` keys only.
