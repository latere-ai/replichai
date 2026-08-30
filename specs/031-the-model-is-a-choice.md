---
title: "The model is a choice"
status: built
---

# 031 — The model is a choice

**Status:** built.

**Scope:** the tier a session runs on, chosen by the person who starts it. One field on the submission, carried durably on the job, read by the runner; one route that prices the tiers; one control beside Start. Audit sessions only.

**Depends on:** [011](011-what-a-session-costs.md) (the rate card, the enqueue snapshot, and `credit.Preferences` — the tier vocabulary this spec exposes), [006b](006b-job-model-and-persistence.md) (the job row and its stores), [012](012-a-project-holds-papers.md) (the Start button, per paper, on the project screen), [006e](006e-frontend.md) (the shell and its tokens).

## Problem

Every managed session this product has ever run has run on Opus, and nobody chose it.

```
audit.DefaultModelPreference = "best"        internal/audit/audit.go:34
  → modelTier("best") = "opus"               internal/provider/anthropic/anthropic.go:38
  → first listed model containing "opus"     internal/provider/anthropic/anthropic.go:186
  → $5 / $25 per Mtok                        internal/credit/price.go:178
```

`audit.WithModelPreference` exists (`audit.go:135`) and is called from exactly one place in the tree: `audit_test.go:548`. The server never calls it — `auditOptions` (`cmd/server/runner.go:177`) passes an observer and `WithoutTimeout()` and nothing else — so the default is not a default, it is the only value. A session that would have been served perfectly well by Sonnet at $3/$15, or by Haiku at $1/$5, is billed at Opus rates against the owner's project pot, and the owner has no way to say otherwise before pressing Start.

The odd part is that everything needed to offer the choice is already built, and built for this reason. `credit.Preferences = {"best", "balanced", "fast"}` (`coverage.go:15`) is described in its own comment as *the model tiers the engine asks for*. `Card.Covers` (`coverage.go:39`) already resolves each of them against the backend and prices it against the card. Both are reachable from only two callers — a boot-time log line (`cmd/server/main.go:367`) and a CLI report (`cmd/replichai/rates.go:58`). The vocabulary is there and priced; there is no door.

Three things constrain how the door gets cut.

**An unvalidated string is two failures, not one.** `modelTier` ends `default: return preference` (`anthropic.go:46`), so anything it does not recognise is passed through as a literal model id to `BetaManagedAgentsModelConfigParams{ID: spec.Model}` (`anthropic.go:237`). That is correct for the adapter — an operator naming a concrete model on the CLI means it — but it means a user-supplied string reaches the vendor unexamined. And a model the card does not name is priced by `Card.For` at the **dearest rates on the card** (`price.go:104`), deliberately, so a vendor's new id is never free compute. Together: a typo would request a model that may not exist and, if it did, would be charged Opus rates for it. The boundary that accepts the choice has to close the set.

**A tier that stops at the handler reaches nothing.** `job.Submission` does not outlive `Enqueue` — it is an argument, not a record. Nobody who runs a session ever sees it: the worker claims a row, and `Execute` may be a different process on a different pod minutes later. So the tier has to be on the job, or the run has no way to learn it. The restart case makes this visible rather than causing it: a pod that dies between enqueue and the backend session existing leaves only the row for the next worker to re-claim.

`Resume` is *not* the reason, and it is worth saying so because it looks like one. It rejoins a session that already exists, and a session's model was fixed when it was created — the auditor resolves a model in `Start` (`audit.go:235`) and nowhere else, so `finish` has nothing left to choose. The tier is passed on that path anyway, inert, so both paths stay one expression.

**The reserve does not move.** `takeReserveTx` calls `s.reserve(ctx)` with no model input (`pgstore.go:181`), so the hold taken at the door is the same whichever tier is chosen. This changes burn rate, not admission.

## Decisions

**A tier, never a model id.** The choice offered is one of the three preferences the engine already asks for, and `credit.Preferences` becomes a closed set of a named `credit.Preference` rather than bare strings — the typed-enum convention, and the thing the boundary validates against. It lives in `credit` because that is where the list already lives and where the comment already explains why it is the pricing unit; declaring a second list somewhere more obviously "about models" would be a second answer to which tiers exist, and the two would drift the first time one gained a member. `job` already imports `credit` (it carries a `credit.Card`), so this adds no edge to the graph.

It is `Preference` and not `Tier` for two reasons. `access.Tier` already means *what a principal may do*, and `internal/credit/memstore.go:283` reads `access.TierAdmin` — a second `Tier` in the same package would make a permission level and a model choice look alike at the call site. And *preference* is the word the engine has used since 004: `modelPref`, `WithModelPreference`, `ResolveModel(ctx, pref)`. On the wire the field is `tier`, because that is the word the person choosing it uses; projecting a domain enum to a plain string under whatever name the API surface wants is what the wire-DTO convention is for.

**The tier is a column, not an argument.** Migration 6 adds `tier TEXT NOT NULL DEFAULT ''` to `jobs`, in `jobColumns`, scanned in `scanJob`, written by both `Enqueue` implementations, and read through one `auditOptions(j, observe)` that cannot be called without a job — so the fresh and resumed paths cannot come to disagree, whether or not the resumed one acts on it. Empty means the engine default, resolved in exactly one place (`Tier.OrDefault()`), and existing rows are left empty rather than backfilled to `best`: a backfill would put a choice on record that nobody made, and this product's whole argument is that the record says what actually happened.

**Validation is the HTTP boundary's job, and `modelTier` keeps its passthrough.** `startSession` refuses an unrecognised tier with `400 bad_tier` before `Enqueue`, so no job row and no credit hold can exist for a tier the engine cannot serve. The adapter's `default:` branch stays exactly as it is — it is how `replichai audit --model` names a concrete model, and the CLI operator is not the surface being defended.

**Prices come from the server or they do not appear.** A gated `GET /models` returns `Card.Covers` projected to the wire — one entry per tier with its resolved model, its per-Mtok input and output rates, and whether the card names it. The frontend renders those numbers and computes none of them: the card is the admin's policy, and a second implementation of it in TypeScript would be a second answer to what a run costs, which is the failure 011 built the snapshot to prevent. `Covers` calls `ResolveModel` once per tier, which is a backend round trip each, so the router memoises the whole answer for five minutes — a vendor's catalogue changes on release days, not per request, and an admin's price edit appearing within five minutes is acceptable where three network calls per page load is not.

**The choice is one control on the papers card, and Start stays one tap.** The picker sits in the card's toolbar beside the add-paper form, showing each tier with its price, and applies to whichever paper you then press Start on — so the flow 012 established is unchanged and the common case costs no extra interaction.

It is the existing `Select` from `ui.tsx` (a combobox over a portalled listbox, keyboard-driven, already the app's answer to not shipping browser-native widgets), not a new split-button-and-menu: one tested widget beats a second one that would have to re-solve placement, dismissal, and focus return. The price rides in the option's label rather than in a new `Option` field, so the shared component is reused and not reshaped for one caller. And it is one control rather than one per row, because three papers would otherwise mean three identical pickers for a choice nobody makes per paper.

The tier is not remembered between sessions: a per-project default is a different decision with a different owner (below), and remembering a choice nobody was asked to make permanent is how a cheap tier silently becomes a project's policy.

**Settlement is untouched.** The resolved model is already observed onto the job as `Status.Model` and priced from the snapshotted card at settlement (`job.go:126`). A fast-tier run is charged Haiku rates by the machinery that already exists, with no new pricing path — which is the reason this spec adds a tier and not a price.

## Acceptance criteria

1. A submission carrying `tier: "fast"` runs the auditor with that preference: the provider resolves a `haiku` model and the job's `Status.Model` records it.
2. A job's tier survives the store round trip and is read from the row alone, with no submission in hand — because a row is all the worker that runs the session ever has.
3. `POST /projects/{id}/sessions` with an unrecognised tier is `400 bad_tier`, and afterwards the project has no new job and no new hold.
4. A submission that omits the tier behaves exactly as today: `best`, Opus, same resolved model, same charge. Existing rows with an empty tier read the same way.
5. `GET /models` returns one entry per tier with its model and per-Mtok rates from the platform card; a tier whose model the card does not name is still returned, priced at the dearest rates, and flagged as unnamed.
6. A fast-tier run is settled at the card's Haiku rates and a best-tier run at its Opus rates, from the same snapshot, through the existing settlement path.
7. The start reserve is identical for all three tiers — asserted explicitly, so nobody later reads this change as having tightened the gate.
8. A browser test drives the control: the picker sits on the papers card, lists three tiers with prices fetched from `GET /models`, and the tier chosen is the one `POST /projects/{id}/sessions` carries. It fails without the change.

## Not in scope

**Effort.** The obvious second knob — the agent runs at the API's default `high` because `EnsureWorker` sets only the model id — and deliberately deferred: the rate card is per model, so `Covers` can price a tier but cannot describe an effort choice, and folding both into one control would put a number on screen that is not what the run costs.

**A session budget cap**, the platform's hard dollar ceiling per session; adjacent, and a different lever (this one lowers the rate, that one bounds the total).

**A project-level default tier** and **a platform policy over which tiers are allowed** — options B and C of the design discussion, both reasonable follow-ups, both widening the scope from "chosen before a run" to "configured somewhere else".

**The experiment path.** `KindExperiment` is declared (`job.go:49`) and never dispatched from HTTP — `handlers.go:325` is the only enqueue site and hardcodes `KindAudit` — so there is nothing to half-wire. `internal/experiment/runner.go:26` keeps its own `defaultRunnerModel`; the 023 chain owns that surface when it puts a person in front of it.

**A concrete model id as user input**, for the reason the first decision gives.

**Recording the tier in `replication.json`.** The tier rides on `SessionView`, which redacts only `Principal` and `Cost` (028) — so a signed-out reader of a published session sees it. That is deliberate and adds no disclosure: `Status.Model` names the concrete model on the same wire already (`provider.go:221`), and the tier is a coarser view of a fact the public zone has always carried. Putting it in the manifest is a different question — a good one, since which model produced a claim is part of the claim's provenance — but the manifest is 002's schema and a field there is a change to the published record, not to this control.

**A second locale.** `Lang` is `'en'` only (`i18n.ts:8`) and `i18n-coverage.test.ts` keeps it honest; new keys go in the English block.

## Outcome

Built as designed, in six commits — the typed preference, the column, the runner, the boundary and its price route, the picker, and the pricing/gate assertions. Every acceptance criterion has a test; the two that could regress silently were checked against a deliberately reverted change and do fail without it (`TestServerRunsOnTheJobsChosenTier` on the runner wiring, `model-tier.test.mjs` on the request payload). The Postgres store contract was run against a real database, so migration 6 and the new column are exercised rather than assumed.

One claim in the first draft was simply wrong and is corrected above: it said the tier had to be durable because `Resume` would otherwise revert a restarted session to `best`. `Resume` rejoins an existing backend session, and a session's model is fixed at creation — the auditor resolves one in `Start` and nowhere else — so the preference is inert on that path. The column is still required, for a plainer reason the draft missed: `job.Submission` does not outlive `Enqueue`, so without the column the tier would never reach a run at all, restart or no restart. The code was right; the stated mechanism was not.

Three things landed differently from the first draft, each recorded above with its reason: the domain type is `credit.Preference` rather than `credit.Tier`, because `access.Tier` already means what a principal may do and this package reads it; the picker is the existing `ui.tsx` `Select` on the papers card rather than a new split-button menu on Start, because reusing the app's one tested combobox beat writing a second overlay; and `GET /models` memoises for five minutes, which the design did not originally say and the implementation needed once `Covers` turned out to cost one backend round trip per tier.

One thing the design did not anticipate: the client's dev proxy is a hand-written prefix list, and `api-proxy.test.ts` — written for exactly this — failed until `/models` was added to `vite.config.ts`. Worth noting because it is the second time that list has gone stale behind a new route.
