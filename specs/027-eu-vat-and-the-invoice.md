---
title: "EU VAT, and the invoice that comes with it"
status: designed
---

# 027 — EU VAT, and the invoice that comes with it

**Status:** designed, not built. **Blocked on the operator, not the code:** VAT registration (OSS), a Stripe Tax configuration, and one legal classification (below) are prerequisites an accountant confirms before this ships. The design is Option A: let Stripe compute the tax and emit the invoice; the platform owns registration, filing, and classification. Do not build until the prerequisites are answered.

**Scope:** when a deployment is registered to charge VAT, enable **Stripe Tax** (automatic per-country calculation, VIES-validated B2B reverse charge) and **Checkout invoicing** on the top-up flow, show the tax in replichai's own UI *before* the redirect, and surface the resulting invoice in the wallet. Behind a flag, so a deployment that is not yet registered keeps 022b's exact behaviour (charge equals quote, no tax).

**Depends on:** [022b](022b-checkout-and-the-webhook.md) (the checkout session, the webhook, the spread, and the tax it deliberately turned *off*), [022c](022c-refunds-and-the-solvency-read.md) (the webhook that credits), [022e](022e-buying-credits.md) (the modal that must show the tax before Stripe), [006k](006k-the-platform-admin-console.md) (where a VAT flag and config live), [011](011-what-a-session-costs.md) (the micro-USD ledger the tax must stay *out* of).

## Problem

022 shipped card payments with VAT **deliberately deferred**: 022b turns Stripe's automatic tax and Managed Payments *off* so the charge equals exactly what the modal quoted, and the parent spec names EU VAT as a flagged, out-of-scope follow-up. That was the right call to ship. But selling a digital service to EU consumers **legally requires** charging VAT at the buyer's country rate and remitting it (through OSS), and issuing a compliant invoice. So the deferred piece is real and owed, and this spec is it.

The reason it waited is that the hard parts are not code. Stripe Tax will do the rate table, the VIES lookups, and the invoice PDF. But **registration, filing, and how *this* product is taxed** are legal acts and legal judgements that belong to the operator and an accountant. This spec builds the integration and states the prerequisites; it cannot decide the tax treatment.

## The one classification everything hinges on

Prepaid credits are a **voucher**, and EU VAT law taxes two kinds differently:

- **Single-purpose voucher (SPV)**: redeemable only for a service at a *known* place of supply and VAT rate. VAT is due **at purchase**. Option A (Stripe Tax on the checkout) implements exactly this.
- **Multi-purpose voucher (MPV)**: redeemable for things at *different* VAT treatments. VAT is due **at redemption** (when a session spends the credit), not at top-up.

**This design assumes SPV.** replichai credits buy one thing (replication compute) at one treatment, so VAT at purchase is the natural fit. If an accountant classifies them MPV, the whole approach inverts (tax at spend, not at top-up) and this spec does not apply. **Confirming SPV is the gate.** It is stated first because getting it wrong is expensive to unwind, unlike a code change.

## Decisions

### Option A: Stripe computes the tax and the invoice; we own the rest

Chosen over a hand-rolled VAT engine (maximum code, maximum liability) and over VAT-inclusive flat pricing (not destination-compliant). On the Checkout session, set:

- `automatic_tax: { enabled: true }`: Stripe computes the buyer's-country rate.
- `tax_id_collection: { enabled: true }`: collects a VAT ID; a valid one triggers **B2B reverse charge** (no VAT, buyer self-accounts), validated against VIES by Stripe.
- `invoice_creation: { enabled: true }`: a compliant invoice PDF per payment.
- a real **product tax code** on the line item (the general electronically-supplied-services code, or what the accountant specifies), replacing the "no tax code / Managed Payments off" that 022b set to keep tax off.

This **reverses what 022b disabled** (automatic tax back on, Managed Payments left to its default, the tax code restored), but only when the deployment is registered (next decision). Stripe then also produces the **filing-ready reports**; the operator (or accountant) still submits the OSS return and pays. Stripe calculates and reports; it does **not** file or remit. This is stated so "Stripe Tax handles VAT" is not misread as "Stripe files my taxes".

### A VAT flag, so an unregistered deployment stays exactly as 022b

A platform setting (006k), `vat_enabled`, gates the whole thing:

- **Off (default):** precisely 022b's behaviour: automatic tax off, no invoice, the charge equals the quote. A deployment that is not registered must not collect tax it cannot remit.
- **On:** the Stripe Tax + tax-ID + invoice params above, and the modal's tax line (next decision).

The operator flips it **after** OSS registration and Stripe Tax configuration are in place, as a settings change rather than a redeploy. This is what lets the code ship before the operator is registered.

### The tax is shown in replichai's UI before the redirect

A hard requirement from the product: a person sees the full total, tax included, **before** they leave for Stripe, not as a surprise on the payment page. The difficulty is that the exact rate depends on the buyer's country and B2B status, which Stripe collects *at* checkout. Two ways to meet the requirement:

- **A: Estimated in the modal (simpler).** Show an *"Estimated VAT"* line at the deployment's home-country rate (the common case), labelled as estimated and finalized at checkout, the pattern Anthropic's own top-up uses. Transparent that tax applies and roughly how much; the exact figure lands on Stripe's page where the address/VAT-ID is entered.
- **B: Exact via a country step (fuller).** Collect the buyer's country (and optionally VAT ID) in the modal, call Stripe Tax's **calculation preview** server-side, and show the *exact* tax and total before redirect. More surface (a selector, a preview endpoint) but no "estimated".

Recommendation: **B if the preview call is cheap enough, else A.** Either way the modal's breakdown gains a VAT row above the total when `vat_enabled`, so 022e's *"You pay / Fee / Credits"* becomes *"Credits / Fee / VAT / Total"*.

### VAT is on top of the credit, and never enters the ledger

Buying $20 of credits means paying **$20 + VAT**; the wallet still gains the same USD credit ($20 less the spread). The VAT is money the platform collects for the tax authority; it is **not credit**, and `credited_micro` (022b) does not change. The ledger stays micro-USD and tax-free (011); a session's cost still reconciles against vendor rates. Stated so nobody later folds VAT into a balance: a credit is prepaid compute, and tax is a separate obligation attached to the *purchase*, not the *credit*.

### The invoice is captured and surfaced in the wallet

`invoice_creation` produces a Stripe invoice with a hosted URL and a PDF. On the completed-checkout webhook, retrieve the invoice and record its URL/number keyed by the `payment_intent` (the same reference 022a dedupes on), so the wallet's Recent-activity row for a card top-up can link to *"Invoice"*. Storage is an open question below (a side table, a column, or just the Stripe-hosted link); the ledger entry itself stays as it is.

### What stays the operator's

Code cannot do these; an accountant or authority does:

1. **OSS registration** with the German BZSt (or home-country equivalent), and the **€10k threshold** decision: below it, charge home-country VAT and skip OSS (Stripe Tax config, not a code difference).
2. **Stripe Tax setup**: origin address, tax registrations, the product tax code.
3. **The SPV/MPV classification** above.
4. **Filing and remittance** of the OSS return: Stripe reports, the operator files.

## Open questions (close before building)

1. **SPV vs MPV.** The gate. Everything here assumes SPV (VAT at purchase). Accountant confirms.
2. **Estimated vs exact in the modal.** Decision A vs B above; depends on whether a Stripe Tax preview per keystroke/selection is acceptable.
3. **Invoice storage.** A `purchase_invoices` side table (payment_intent → url/number), a column on the entry, or store nothing and re-fetch the hosted URL from Stripe on demand?
4. **Threshold handling.** Is the €10k / home-vs-destination distinction purely a Stripe Tax registration config, or does the UI copy need to change with it?
5. **Refund + VAT.** When a purchase is refunded (022c), Stripe reverses the tax too; confirm the credit reversal (which reverses only the `credited_micro`) needs no change, and that a credit note is issued.

## Out of scope (deliberately)

- **Filing/remitting the OSS return.** Stripe reports; the operator files. Not automatable here.
- **Non-EU tax**: US sales tax, UK VAT post-Brexit specifics, etc. This spec is EU VAT via OSS; other jurisdictions are their own work if the audience spreads.
- **Re-pricing or discounts.** The spread (022b) is unchanged; VAT sits on top of it.
- **A tax treatment for *granted* credits.** An admin comp is not a sale, so no VAT event; only purchased credits carry tax.

## Acceptance criteria

- With `vat_enabled` **off**, the checkout is byte-for-byte 022b: automatic tax off, no invoice, charge equals quote. A test pins that flipping the flag off restores today's behaviour exactly.
- With it **on**, the Checkout session carries `automatic_tax`, `tax_id_collection`, `invoice_creation`, and the product tax code; a test drives a session and asserts the params.
- A consumer in a VAT country is charged VAT on top; a business with a valid VAT ID is reverse-charged (no VAT). Verified against Stripe test-mode tax behaviour.
- The **credited amount excludes VAT**: the wallet gains `credited_micro`, tax is separate; a test asserts the ledger entry is the pre-tax credit regardless of tax charged.
- The modal shows a VAT line (estimated or exact per the chosen option) **before** the redirect, and the total matches what Stripe charges (for the exact option) or is labelled estimated (for the estimated option).
- A completed purchase's **invoice is retrievable from the wallet** (a link to the Stripe-hosted invoice / PDF), keyed by payment_intent.
- The spec's copy makes explicit that filing/remittance and registration are the operator's, not the platform's.
