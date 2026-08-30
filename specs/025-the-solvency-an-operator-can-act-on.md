---
title: "The solvency an operator can act on"
---

# 025 — The solvency an operator can act on

**Status:** designed, not built. A deferred follow-up to [022](022-money-comes-in-without-an-admin.md); not urgent at beta scale, recorded so the signal is right when volume grows. Open questions below to close before implementation.

**Scope:** turn 022c's raw outstanding-liability number into a signal an operator acts on — a comparison against what the vendor account holds, and an alert before the platform is under-funded — while keeping the *funding itself* out of the platform, exactly as 022's non-goal decided.

**Depends on:** [022c](022c-refunds-and-the-solvency-read.md) (the `TotalOutstanding` fold and `GET /admin/solvency`), [006k](006k-the-platform-admin-console.md) (the console and its settings row), [022d](022d-a-negative-wallet-is-frozen.md) / [006p](006p-the-invitation-that-arrives.md) (`notify`, if an alert emails).

## Problem

022c put one number on the console: **outstanding liability = Σ wallet balances + Σ pot balances** — credits the platform has sold or granted and not yet delivered as compute. It is correct and it is inert. An operator reading "$81.82" cannot tell whether that is comfortable or dangerous, because the number that gives it meaning — **what the Anthropic account actually holds** — is nowhere on the screen. And there is no *warning*: the platform learns it has sold more compute than it can deliver the way a user learns it — when a session fails to run.

022 deliberately made **coupling a customer payment to a vendor top-up a non-goal**, for two reasons that still hold and that this spec does not reopen:
1. It would make Stripe a dependency of serving traffic — a webhook outage would degrade everyone's ability to run sessions, not just the payer's.
2. There is no clean vendor API for it: Anthropic funds a pay-as-you-go account through **console auto-reload** (a threshold that charges the card automatically), not a public "add these dollars" call keyed to an external event.

022c named the replacement — *"surface sold-liability so an operator can watch it against what the Anthropic account holds and decide when to reload"* — but shipped only half of it (the liability side). This spec builds the other half: the comparison and the alarm.

## Decisions

### The number needs a denominator, not just a value

Liability alone is half a fraction. The console should show **liability, the vendor balance, and the headroom between them** — so "am I solvent?" is answered at a glance rather than computed in the operator's head. The blocker is whether the vendor balance is *readable*.

**Open question — does Anthropic's Admin API expose the account's credit balance?** The Admin API is known to report **usage and cost** (what was spent); a live **credit balance** read is uncertain. This decides the shape:

- **A — Live (if readable).** Read the vendor balance via the Admin API on the console load; show `liability / balance / headroom` live. Best, if the endpoint exists.
- **B — Operator-entered.** An admin types the current vendor balance into a settings field with a `last checked` timestamp; the console shows the comparison and how stale it is. Crude, but it gives the denominator with zero API dependency, and a stale-badge keeps it honest.
- **C — Derived estimate.** Track vendor top-ups (an admin records each reload) and subtract cumulative spend (which the app already prices per session) → an estimated balance. More moving parts, and it drifts from the real account; probably not worth it over B.

Recommendation pending the API answer: **A if the balance is readable, else B.** C only if B proves too manual.

### An alert fires before the platform is short, not after

A configurable **threshold** on the platform settings row (006k), and a warning when liability crosses it. Fail-safe direction: warn **early** — a threshold set to "liability within $X of the vendor balance" or "headroom below $Y" trips while there is still time to reload, not once a session has already failed.

**Open question — the alert channel.** A console banner is the minimum (an admin sees it next visit). An **email** via `notify` (022d's `AccountFrozen` pattern) reaches an operator who is not looking — worth it, since the whole point is to act *before* the failure. Likely both: banner always, email on the crossing (once, like the freeze notice).

### The funding stays the vendor's auto-reload — restated, not reopened

This spec adds the **signal**, never the **transfer**. The documented mechanism remains: set Anthropic **auto-reload** (console) with a threshold and amount sized to cover peak liability plus margin. Stating it here so "the solvency console" is not later mistaken for "the platform tops up Anthropic," which 022 ruled out for good reasons.

### Liability counts granted credits too

`TotalOutstanding` already folds every holder regardless of how the credit entered — a purchase and an admin comp are both future compute the platform will pay Anthropic for. This is correct and stated so nobody later "cleans it up" to count only purchased credits: a granted credit is just as much a delivery obligation as a bought one.

## Open questions (close before building)

1. **Vendor balance readability** — is there an Anthropic Admin API endpoint for the account credit balance? Decides Option A vs B above. The single highest-leverage question.
2. **Threshold semantics** — absolute liability ceiling, or headroom-below-Y, or a ratio (liability / balance > 0.8)? A ratio travels best across scale.
3. **Alert channel + cadence** — banner only, or banner + one email on the crossing? Who receives it (all admins, or a designated billing contact)?

## Out of scope (deliberately)

- **Automated vendor funding.** The non-goal above; auto-reload is the mechanism.
- **Per-user or per-project profitability.** This is a solvency gauge, not analytics.
- **Currency conversion.** Liability is micro-USD (011); the vendor account is USD.

## Acceptance criteria

- The console shows outstanding liability **beside** a vendor-balance figure (read live, or operator-entered with a staleness badge) and the headroom between them.
- An admin sets a solvency threshold on the platform settings row; a test asserts it round-trips like the other policy numbers.
- Crossing the threshold raises a console warning (and, if chosen, one email via `notify`); a test drives liability across the line and asserts the warning, and that it does not re-fire every read.
- The spec's copy documents that vendor funding is Anthropic auto-reload, not a platform action.
- The liability figure continues to fold granted and purchased credits alike (a test pins this).
