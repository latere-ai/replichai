# 022 — Money comes in without an admin

**Status:** designed, not built. A design to refine into implementation leaves (a suggested breakdown is at the foot).

**Scope:** let a person buy credits into their own wallet with a card, so an admin is no longer the only way money exists. A Stripe **hosted Checkout** session creates the charge; a signed **webhook** is the one thing that posts the credit; the ledger dedupes the post on an external reference so a retried delivery cannot double it; a **spread** at purchase leaves margin for fees and overhead; a **refund** is a correcting ledger entry, not a reversal engine. Adds one migration (an `external_ref` column and its unique index), a payment port with a Stripe adapter, three routes (`POST /me/credits/checkout`, `POST /webhooks/stripe`, and a solvency read for the console), and the top-up modal the `402` already promises.

**Depends on:** [011](011-what-a-session-costs.md) (the ledger, `KindTopup`, micro-USD, the wallet the credit lands in and the `402 no_credits` that sends a person to buy some), [006c](006c-identity-and-access.md) (the signed-in email a wallet is keyed by, and the tier guard the webhook must sit *outside* of), [006k](006k-the-platform-admin-console.md) (platform settings, where the spread lives beside the rate card it complements), [010](010-deployment-and-release.md) (the secret the Stripe keys need, and the public ingress path the webhook needs reachable).

**Supersedes:** 011's closing line — *"an admin is still how credits come into existence"* — and only that line. Admin top-up ([`POST /admin/access/{email}/credits`](../internal/webauth/admin.go)) stays: it is how a grant, a comp, or a correction enters a wallet, and a card is not the only reason money should. This spec adds a second door, it does not close the first.

## Problem

011 built a ledger with two levels, priced sessions from it, and gated every start on it — and then left the front door to it locked. `KindTopup` is admin-only (`ErrNotAdmin`), so the only way a wallet holds anything is a platform admin typing a number into the console. That is fine for a seed and impossible for a product: every account arrives empty (006q), the start gate refuses it `402 no_credits`, and the only person who can lift that refusal is an operator. A researcher who wants to run a reproduction cannot pay to.

The ledger already anticipated the fix. `KindTopup`'s own comment reads *"Only an admin, and later a payment,"* and micro-USD was chosen partly because *"a payment provider charges in minor units of the same currency."* So the shape is known: a purchase is a `topup` posted by something other than an admin. What is missing is the three things that make posting it *safe* — who is allowed to post it, how a doubled delivery is refused, and where the money to run the compute the credit will buy actually comes from.

There is also a question this spec answers by **not** building it. The prompt that started this work asked whether a customer's payment should also top up the platform's own Anthropic API account. It should not — see the non-goal below — but the concern behind it is real and gets a small answer.

## Decisions

### A purchase is a webhook-posted entry, never a redirect-confirmed one

The money-moving fact is **the webhook**, `checkout.session.completed` (with its `payment_status: paid`), verified against the endpoint signing secret. The browser's return from Checkout is a UX event only: it navigates the user back and re-reads their wallet, and it may arrive before, after, or never relative to the webhook. Nothing about the credit is decided from a redirect query parameter, because a redirect parameter is client-supplied and the amount credited must come from the Stripe event object the server verified. This is the instruction-source boundary applied to money: the browser is data, the signed webhook is the instruction.

Concretely, three moves:

```
POST /me/credits/checkout   {amount_usd}   → creates a Checkout Session,
                                              returns its hosted URL
  (browser redirects to Stripe, pays, redirects back to /wallet?topup=…)
POST /webhooks/stripe        (Stripe → us)  → verify signature, on
                                              checkout.session.completed
                                              post the topup, return 200
```

`POST /webhooks/stripe` **mounts outside the tier guard** (006c): Stripe is not a signed-in principal and carries no `TierUser`. Its authentication is the signature, not the session cookie. This is the one route in the app whose caller is a machine, and getting it out from under `RequireTier` is a deliberate seam, not an oversight — stated here so a later refactor that "tidies up" by moving all routes under one guard does not silently start rejecting every payment.

### The credit is posted at most once, and the ledger insert is what guarantees it

Stripe retries webhook deliveries by design, and a network hiccup can deliver the same `checkout.session.completed` twice. The current ledger has no defense: a balance is a fold over entries, so two `topup` rows silently double the money. The fix is a unique reference on the entry, and the *insert* — not a read-then-insert check — is what dedupes, for exactly the reason 011 gives for holding inside the enqueue transaction: a check on a second connection races the write it is trying to protect.

Migration `v2` of the credit schema:

```sql
ALTER TABLE credit_entries ADD COLUMN external_ref TEXT;
CREATE UNIQUE INDEX credit_entries_external_ref
  ON credit_entries (external_ref) WHERE external_ref IS NOT NULL;
```

A purchased `topup` carries `external_ref = <stripe payment_intent id>`; the insert is `ON CONFLICT (external_ref) DO NOTHING`. A duplicate delivery hits the index, inserts nothing, and the webhook still returns `200` so Stripe stops retrying. At-most-once insert over a folded balance is exactly-once crediting, with no stored counter to drift. Every other entry (`allocate`, `hold`, `charge`, an admin `topup`) leaves `external_ref` NULL, and the partial index ignores them — the column is dead weight for internal moves and load-bearing only where an outside system's identifier is the idempotency key.

This is the single highest-risk part of the spec. It is also the smallest: one column, one index, one `ON CONFLICT`.

### The payment path is its own store method, and `ErrNotAdmin` keeps its meaning

`Topup(ctx, byAdmin access.Principal, email, amount)` takes a principal and returns `ErrNotAdmin` — a webhook has neither. Rather than widen that signature and weaken what `ErrNotAdmin` asserts, add a sibling:

```go
// TopupPaid posts a purchased credit into a wallet, idempotent on ref.
// It has no principal to check: authority came from the verified webhook
// signature, upstream. actor records the payment, e.g. "stripe:pi_123".
TopupPaid(ctx context.Context, email string, credited int64, ref, actor string) error
```

The `actor` field then does real work in the wallet statement 011 built: a granted credit reads `actor = admin@…`, a purchased one reads `actor = stripe:pi_…`, and a person reading *why does my wallet hold this?* sees the difference between a comp and a purchase without a new column. Two doors into `KindTopup`, distinguished by who posted and recorded on the row itself — which is the ledger's whole thesis (011): a balance is the rows, and each names its cause.

### Hosted Checkout, and margin is a visible spread at purchase

**Hosted Checkout** (Stripe-hosted page, redirect out and back) over an embedded Payment Element: the card fields never touch this origin, so the PCI surface is the smallest one on offer, and Link and the wallet buttons ride along for free. The app holds a publishable key and a secret key; it renders no card input.

**The spread lives at the till, not in the rate card.** 011's rate card is raw vendor pricing, and its own text leans on that: an unknown model is charged *"at the highest rate the card holds,"* and a session's cost is meant to reconcile against Anthropic's published rates. Baking a margin multiplier into that card would quietly break the reconciliation — every session would cost more than the vendor charges, for a reason invisible on the session. So the margin is a purchase spread instead: **you pay $20 and your wallet gains $18** (or whatever the spread resolves to). The rate card stays honest, the session cost still matches the vendor, and the place the platform takes its cut is the one place a customer expects one — the moment of sale.

The spread is a platform-settings number beside the rate card (006k), in basis points, admin-editable like everything else there:

```
credited_micro = paid_micro − round(paid_micro × spread_bps / 10_000)
paid_micro     = amount_cents × 10_000            # cents → micro-USD, exact
```

Two rules make it trustworthy:

- **The credited amount is shown before the charge.** The modal states *"$18.00 in credits for $20.00"* using a public spread, so nobody is surprised at the statement. `GET /me/credits` (or a small quote endpoint) exposes the spread so the client can compute it without a round-trip per keystroke.
- **The spread is snapshotted onto the Checkout session at create time**, in its metadata, exactly as 011 snapshots the rate card onto the job at enqueue and for the same reason: an admin editing the spread while a payment is in flight must not change what a paid session credits. The webhook reads the snapshotted spread from the session metadata, not the live settings row.

Fixed packs versus a free-entry amount is a UI choice left to the breakdown; the spread math is identical either way. A minimum purchase (a spread on $1 is dwarfed by Stripe's fixed `$0.30`) is an open question below.

### A refund is a correcting entry, and a wallet may go negative

Stripe can claw money back after the credit is spent — a refund or a chargeback lands days later, when the wallet has already been allocated into a project and burned. The ledger already has the shape for this: `KindAdjust`, *"either sign … deliberately not unique per session."* A refund posts a **negative `adjust`** to the wallet, `external_ref = <refund/dispute id>` (a *different* id from the purchase, so it dedupes independently and does not collide with the topup it reverses):

- The wallet **may go negative**, and a negative wallet **cannot allocate** — `Allocate` already refuses a move larger than available (`ErrInsufficient`), and a negative available refuses every move. The person cannot spend money that was taken back.
- We **do not** reach into project pots or claw back holds. Money already moved into a pot and possibly spent is gone; the negative wallet is the record that the person owes it, and reconciling that is an operator decision, not an automated reversal. This spec **defines the policy and stops there** — building a dunning or collections flow is out of scope, and saying so keeps "refunds are handled" from being read as "refunds are chased."

Handling `charge.refunded` / `charge.dispute.created` in the same signed webhook is the minimum; the full lifecycle is a breakdown leaf.

### Non-goal: a customer's payment does not top up the Anthropic account

The prompt asked whether a user's payment should also fund the platform's own Claude API balance. It should not, for two reasons that are worth writing down so the question stays answered:

1. **It would make Stripe a dependency of serving traffic.** Coupling "a customer paid" to "the vendor account is funded" means a webhook outage, a Stripe incident, or a single failed transfer degrades the platform's ability to run *anybody's* sessions, not just the payer's. The float that pays the vendor must be independent of any one customer's payment clearing.
2. **There is no clean API for it.** Anthropic's console funds a pay-as-you-go account through **auto-reload**, not a public "add these dollars" call keyed to an external event. The right mechanism already exists and is the vendor's: set an auto-reload threshold on the API account and let it top itself up from the platform's own card.

What the concern behind the question deserves is the **observability half**, and that is cheap. The platform's outstanding liability is a fold it can already compute — the sum of every wallet balance plus every project pot balance is *credits the platform has sold and not yet delivered as compute*. Surface that one number on the admin console (006k) beside the rate card, so an operator can watch sold-liability against what the Anthropic account holds and decide when to reload. A read, not an integration; a solvency gauge, not a coupling. That replaces the automated transfer the prompt reached for with the thing the transfer was really trying to give: knowing the float is healthy.

### Configuration and secrets

Three secrets, injected like every other (010): `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, and the publishable key (public, but carried the same way for parity). Absent all three, the payment adapter is a **no-op that refuses**: `POST /me/credits/checkout` returns a clear "payments not configured" rather than a `nil`-pointer panic, and the admin console still funds wallets. This mirrors 011's `nil` `CardSnapshot` / `Reserve` meaning "unmetered": a deployment without payment keys is a valid deployment (a local `docker compose up`, a test), it just cannot sell credits. The payment provider is a port with a Stripe adapter and a memory adapter, on the same neutral-core rule the audit engine follows — so the whole flow is testable offline, with a fake that posts the webhook the real one would.

## Acceptance criteria

- A signed-in person with an empty wallet opens the top-up modal (reached from the `402 no_credits` path 011 built), sees *"$X in credits for $Y"* computed from the public spread, and is redirected to a Stripe-hosted Checkout page.
- On successful payment, a `checkout.session.completed` webhook posts one `KindTopup` entry to that person's wallet, `external_ref = <payment_intent>`, `actor = stripe:<payment_intent>`, `amount = credited_micro` derived from the **snapshotted** spread — and the wallet balance reflects it on next read.
- Delivering the **same** webhook event twice results in **one** ledger entry and a `200` both times (the unique `external_ref` index + `ON CONFLICT DO NOTHING`). A test posts the identical event twice and asserts the balance moved once.
- A webhook with a **bad or missing signature** posts nothing and returns `400`. A test tampers with the payload and asserts no entry and no `200`.
- The credited amount is derived **only** from the server-verified event/metadata, never from a redirect query parameter. A test hits the return URL with an inflated `amount` param and asserts the wallet is unchanged.
- A `charge.refunded` webhook posts a negative `KindAdjust` to the wallet with the refund's own `external_ref`; the wallet may go negative; a negative-available wallet's `Allocate` is refused `ErrInsufficient`. Tests cover the post, the negative balance, and the refused allocation.
- With no Stripe keys configured, `POST /me/credits/checkout` returns "payments not configured" (not a panic), admin top-up still works, and the app still boots and runs unmetered.
- The admin console shows outstanding sold-liability (`Σ wallet balances + Σ pot balances`) as one micro-USD number. A test asserts it equals the fold over the ledger.
- Every new behavior is covered by an e2e test through the HTTP surface with a fake payment adapter that emits the webhooks the real Stripe would; coverage of the new code stays above the project bar.

## Deliberately not here

- **Automated vendor top-up.** The non-goal above; auto-reload on the Anthropic account plus the solvency read replace it.
- **A margin multiplier in the rate card.** Rejected in favor of the purchase spread, to keep session cost reconcilable against vendor rates.
- **Invoices, receipts, tax/VAT, and Stripe Tax.** A purchased-credit statement line exists in the wallet (011); a compliant invoice/receipt document is its own spec if a jurisdiction requires one.
- **Subscriptions, auto-recharge of a user's wallet, and saved-card management** beyond what Link offers inside Checkout. Top-up is one-shot here.
- **Dunning / collections on a negative wallet.** The negative balance is recorded; chasing it is an operator action, not code in this spec.
- **Per-cell rate-card or spread editors.** Both are typed values on the settings row 006k/011 already render; a wrong cell mis-prices until noticed, which is the same accepted risk 011 states.
- **Currency other than USD.** The ledger holds micro-USD and no notion of conversion (011); Checkout charges USD.

## Open questions (for the breakdown to close)

1. **Minimum purchase.** Stripe's fixed `$0.30` per charge makes tiny top-ups a loss even before the percentage. A floor (e.g. $5 minimum) or pack-only purchasing closes it. Decide floor vs. free-entry.
2. **Spread shape.** Percentage-only, or percentage plus a fixed per-purchase amount that mirrors Stripe's own `2.9% + $0.30`? The latter tracks cost more honestly at small amounts; the former is simpler to display.
3. **Idempotency key: payment_intent vs. checkout_session id.** Both are stable; the payment_intent survives a session that is retried, the checkout_session is what the webhook names first. Pick one and state why.
4. **Return-page freshness.** The webhook may lag the redirect. Does the return page poll the wallet briefly, or just show "processing — your credits will appear shortly"? Affects perceived latency, not correctness.

## Suggested breakdown

Three implementation-ready leaves, in dependency order:

- **022a — the ledger seam and the idempotent post.** Migration `v2` (`external_ref` + unique index), `TopupPaid`/refund-`adjust` on both stores with contract tests, the `ON CONFLICT` dedupe, and the payment **port** with a memory adapter. No Stripe, no HTTP — the money-safety core, fully tested offline. Highest risk, so it lands first and alone.
- **022b — Checkout, the webhook, and the modal.** The Stripe adapter behind the port, `POST /me/credits/checkout`, the signed `POST /webhooks/stripe` mounted outside the tier guard, the spread snapshot, and the top-up modal wired to the `402`. The visible half.
- **022c — refunds and the solvency read.** `charge.refunded` / dispute handling to the negative-`adjust` policy, and the sold-liability number on the admin console.
