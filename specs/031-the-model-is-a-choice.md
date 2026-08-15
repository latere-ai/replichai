# 031 — The model is a choice

**Status:** designed.

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

**A tier consumed at enqueue is a tier that evaporates.** `Resume` (`runner.go:204`) rebuilds the auditor from the job row alone, because a restart has nothing else. A preference that lived only on `job.Submission` and was read at `Execute` time would be correct until the first pod restart and silently revert to `best` after it — an Opus run the owner did not pick and does pay for, with no trace of the substitution anywhere.

**The reserve does not move.** `takeReserveTx` calls `s.reserve(ctx)` with no model input (`pgstore.go:181`), so the hold taken at the door is the same whichever tier is chosen. This changes burn rate, not admission.

## Decisions

**A tier, never a model id.** The choice offered is one of the three preferences the engine already asks for, and `credit.Preferences` becomes a closed set of a named `credit.Preference` rather than bare strings — the typed-enum convention, and the thing the boundary validates against. It lives in `credit` because that is where the list already lives and where the comment already explains why it is the pricing unit; declaring a second list somewhere more obviously "about models" would be a second answer to which tiers exist, and the two would drift the first time one gained a member. `job` already imports `credit` (it carries a `credit.Card`), so this adds no edge to the graph.

It is `Preference` and not `Tier` for two reasons. `access.Tier` already means *what a principal may do*, and `internal/credit/memstore.go:283` reads `access.TierAdmin` — a second `Tier` in the same package would make a permission level and a model choice look alike at the call site. And *preference* is the word the engine has used since 004: `modelPref`, `WithModelPreference`, `ResolveModel(ctx, pref)`. On the wire the field is `tier`, because that is the word the person choosing it uses; projecting a domain enum to a plain string under whatever name the API surface wants is what the wire-DTO convention is for.

**The tier is a column, not an argument.** Migration 6 adds `tier TEXT NOT NULL DEFAULT ''` to `jobs`, in `jobColumns`, scanned in `scanJob`, written by both `Enqueue` implementations, and read by `Execute` *and* `Resume` through the same accessor. Empty means the engine default, resolved in exactly one place (`Tier.OrDefault()`), and existing rows are left empty rather than backfilled to `best`: a backfill would put a choice on record that nobody made, and this product's whole argument is that the record says what actually happened.

**Validation is the HTTP boundary's job, and `modelTier` keeps its passthrough.** `startSession` refuses an unrecognised tier with `400 bad_tier` before `Enqueue`, so no job row and no credit hold can exist for a tier the engine cannot serve. The adapter's `default:` branch stays exactly as it is — it is how `replichai audit --model` names a concrete model, and the CLI operator is not the surface being defended.

**Prices come from the server or they do not appear.** A gated `GET /models` returns `Card.Covers` projected to the wire — one entry per tier with its resolved model, its per-Mtok input and output rates, and whether the card names it. The frontend renders those numbers and computes none of them: the card is the admin's policy, and a second implementation of it in TypeScript would be a second answer to what a run costs, which is the failure 011 built the snapshot to prevent. `Covers` calls `ResolveModel` once per tier, which is a backend round trip each, so the router memoises the whole answer for five minutes — a vendor's catalogue changes on release days, not per request, and an admin's price edit appearing within five minutes is acceptable where three network calls per page load is not.

**The choice is a menu on the Start control, not a field on a form.** Start stays one tap at the default tier — the common case, and the flow 012 established — with a chevron beside it opening an in-app menu of the three tiers, each showing its model and its price, where choosing one starts the session immediately. It is a menu rather than a `<select>` because this app does not ship browser-native widgets, and it portals to the body like every other overlay. The tier is not remembered between sessions: a per-project default is a different decision with a different owner (below), and remembering a choice nobody was asked to make permanent is how a cheap tier silently becomes a project's policy.

**Settlement is untouched.** The resolved model is already observed onto the job as `Status.Model` and priced from the snapshotted card at settlement (`job.go:126`). A fast-tier run is charged Haiku rates by the machinery that already exists, with no new pricing path — which is the reason this spec adds a tier and not a price.

## Acceptance criteria

1. A submission carrying `tier: "fast"` runs the auditor with that preference: the provider resolves a `haiku` model and the job's `Status.Model` records it.
2. A job's tier survives the store round trip and is read by **`Resume`** as well as `Execute` — asserted against a job row alone, with no submission in hand, because that is what a restart has.
3. `POST /projects/{id}/sessions` with an unrecognised tier is `400 bad_tier`, and afterwards the project has no new job and no new hold.
4. A submission that omits the tier behaves exactly as today: `best`, Opus, same resolved model, same charge. Existing rows with an empty tier read the same way.
5. `GET /models` returns one entry per tier with its model and per-Mtok rates from the platform card; a tier whose model the card does not name is still returned, priced at the dearest rates, and flagged as unnamed.
6. A fast-tier run is settled at the card's Haiku rates and a best-tier run at its Opus rates, from the same snapshot, through the existing settlement path.
7. The start reserve is identical for all three tiers — asserted explicitly, so nobody later reads this change as having tightened the gate.
8. A browser test drives the control: the menu opens beside Start, lists three tiers with prices fetched from `GET /models`, and choosing one posts that tier. It fails without the change.

## Not in scope

**Effort.** The obvious second knob — the agent runs at the API's default `high` because `EnsureWorker` sets only the model id — and deliberately deferred: the rate card is per model, so `Covers` can price a tier but cannot describe an effort choice, and folding both into one control would put a number on screen that is not what the run costs.

**A session budget cap**, the platform's hard dollar ceiling per session; adjacent, and a different lever (this one lowers the rate, that one bounds the total).

**A project-level default tier** and **a platform policy over which tiers are allowed** — options B and C of the design discussion, both reasonable follow-ups, both widening the scope from "chosen before a run" to "configured somewhere else".

**The experiment path.** `KindExperiment` is declared (`job.go:49`) and never dispatched from HTTP — `handlers.go:325` is the only enqueue site and hardcodes `KindAudit` — so there is nothing to half-wire. `internal/experiment/runner.go:26` keeps its own `defaultRunnerModel`; the 023 chain owns that surface when it puts a person in front of it.

**A concrete model id as user input**, for the reason the first decision gives.

**A second locale.** `Lang` is `'en'` only (`i18n.ts:8`) and `i18n-coverage.test.ts` keeps it honest; new keys go in the English block.
