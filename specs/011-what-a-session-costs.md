# 011 — What a session costs

**Status:** built, with one bridge still standing after [041d](041d-models-and-meters-name-their-source.md): `provider.Usage` is what settlement still prices, through `Job.cost()` calling `Rates.Price`, while the normalized `managed.Usage` and `Rates.PriceManaged` wait to take over. Two things below have moved — the card is keyed by `managed.ModelRef` (provider, id, billing variant) rather than a bare model string, so "an unknown model fails expensive" reads against a *ref* and legacy string rows backfill to `anthropic`; and the five-class table names neither the unreported-lifetime cache write, charged at the dearer rate, nor the per-meter `Extra` rates a non-Anthropic runtime bills through. `provider.Usage` carries cache writes split by lifetime; `internal/credit` is the ledger (both stores against one contract, the advisory lock confirmed necessary by removing it); the gate lives inside `Enqueue` and the session counter, sponsorship and `jobs.charged_to` are gone. Settlement charges a session for the tokens it burned, priced from a card snapshotted at enqueue; the hidden floor is projected project-wide and published nowhere. The counter is retired from the specs, and the surfaces are in: a project shows what it holds and what it may still commit, credits move both ways under the ledger's own role rules, a wallet shows the statement that explains it, and a session says what it cost. Deliberately *not* here: payment — an admin is still how credits come into existence.

**Verified by hand, against a real Postgres and the running app.** A new account reports zero and is refused `402 no_credits`; an admin top-up of $50 appears in the wallet statement naming the admin who granted it; a submit funds a fresh project with one reserve and leaves $40; the ledger records `allocate → hold → charge → release` in that order for each session. A run that never reached the provider (arXiv rate-limited the resolve) charged nothing and returned its hold whole, which is the rule for a session with no usage. A run that finished charged 5 µUSD for one input token — priced at the *dearest* rates, because the in-process provider reports no model and `Card.For` fails expensive, which is the intended behaviour seen working rather than assumed. Reclaiming $4 moved both balances in one response; reclaiming more than the pot holds was `402`. A project below the start reserve refused the next submit and admitted it again once topped up.

One caveat worth stating, and a command for it: against the real backend, `ResolveModel` returns whatever the vendor's model list holds for the tier preference, which may not be one of the three ids the seeded card names. That path charges the dearest rates rather than the model's own — correct by design, and not free, but not that model's price either.

Reading the vendor's model list by eye answers the wrong question, because the engine asks for a *preference* and the adapter resolves it by substring against whatever is listed today. `Card.Covers` drives that same resolution and compares its answer to the card, and **the server runs it at every boot** — it is the one process holding both the deployment's card and the provider credential, so the check happens whether anybody remembers to ask. It logs and never blocks: a card a release behind is a billing imprecision, not a reason to refuse to boot. `replichai rates` prints the same comparison from a laptop:

```
PREFERENCE  RESOLVES TO             PRICED AS         INPUT $/MTok  OUTPUT $/MTok
best        claude-opus-5-20260901  DEAREST ON CARD   $5.00         $25.00
balanced    claude-sonnet-5         claude-sonnet-5   $3.00         $15.00
```

The CLI reads the deployment's own card when `DATABASE_URL` is set — that row is what sessions are actually priced from — and the compiled-in seed otherwise; `-json` makes it a pipeline check. The production image ships only the server, so the CLI is a laptop tool and the boot log is what an operator actually reads.
**Scope:** replace the session counter with money. An append-only credit ledger at two levels — a person's wallet and a project's pot — priced in micro-USD, metered against the tokens a session actually burned, held at start and settled at the end. The counter, its top-ups, its sponsorship and its payer picker are removed rather than deprecated. A rework of [006l](006l-the-session-quota.md), and the promotion of `BACKLOG.md` item 5 onto the roadmap.
**Depends on:** [006b](006b-job-model-and-persistence.md) (the job store, the enqueue transaction, the worker's poll loop), [006k](006k-the-platform-admin-console.md) (where an admin sets policy and grants), [006g](006g-projects-and-replication-sessions.md) (projects own sessions), [006c](006c-identity-and-access.md) (project membership and `RoleLead`), [006h](006h-session-transcript-viewer.md) / [006s](006s-reading-a-session-in-time.md) (the usage a session records).
**Supersedes:** 006l entirely; 006q's zero-quota default (its open sign-in stands).

## Problem

006l meters a session **count**, and 006q made that count the only thing between an open sign-in and the platform's API credential. A count is not a unit of cost. Reproducing a small notebook and reproducing a multi-day training run differ by orders of magnitude and both decrement the same integer, so the number an admin tunes cannot be set to a value that is simultaneously generous to the first and safe against the second. There is no budget to absorb the difference.

The two-level shape the counter never had is the second half of the problem. A session belongs to a project, and a project is worked on by several people; the counter is keyed on a *person*, so 006l had to invent sponsorship (`projects.sponsored`, `charge_to`, a payer picker, `not_sponsored`) to let a collaborator spend in someone else's project. All of that machinery exists to answer "whose count does this session draw down" — a question that disappears the moment the project itself holds a balance.

## Decisions

### Money is an append-only ledger, never a balance column

One table, `credit_entries`, whose rows are signed amounts against a holder. A balance is `SUM(amount)`; there is no stored balance anywhere.

```
holder      user:<email> | project:<id>
amount      BIGINT, signed, micro-USD
kind        topup | allocate | reclaim | hold | release | charge | adjust
job_id      set on hold/release/charge/adjust; NULL otherwise
actor       who caused it
created_at
```

This is 006l's own rule about the session count — *"a derived count over `jobs`, never a stored counter: a materialised balance is a second source of truth that drifts the first time a session is deleted or a phase is corrected"* — applied to the thing the count became. Credits break the derivation (an allocation is not a session), so the ledger *is* the derivation, and every balance in the product is a fold over it.

It also answers, structurally, the question 006q could only complain about: *why does this account hold this amount?* Because these rows say so, each naming its actor and its cause.

**Rejected: `users.credits` / `projects.credits` columns with a history table.** One statement debits, and `UPDATE … WHERE credits >= $1 RETURNING` is atomic without an advisory lock — genuinely simpler and faster. It is two sources of truth for one number, and only one of them is enforced: the first correction, missed event, or hand-edit makes the history a story about a balance rather than the balance itself. A ledger cannot disagree with itself.

### Micro-USD, integer, always

`int64` millionths of a dollar. Model prices are quoted per million tokens, so a rate multiplies without rounding at the step where rounding would compound. Floats are never used for money anywhere in this system; formatting to "$1.23" is a display concern and lives in the frontend.

The hidden project cap is a policy number in the same unit (200 EUR ≈ 200_000_000 µUSD at whatever rate the admin decides when typing it — the platform does no currency conversion, and holds no notion of EUR).

### Two levels: a wallet funds a pot, and only the pot is ever spent

A person's wallet holds credits they were given. A project's pot holds credits allocated to it. **A session always charges the project it belongs to.** A wallet is never debited by a run.

| move | who may | direction |
|---|---|---|
| top-up | platform admin only | → a person's wallet |
| allocate | any project member | their wallet → the project pot |
| reclaim | project **leads** only | the project pot → their wallet |
| charge / refund | the system, at settlement | the project pot |

The pot is **fungible and uncontributed**: it does not remember who put what in. A lead may reclaim into their own wallet credits another member allocated. That is the model as asked for, and it is stated here so it is a decision rather than a discovered surprise: allocating to a project is entrusting money to that project's leads. Reclaim is lead-only rather than any-member so that an invitation to collaborate is not also the ability to empty the funding.

Because the project pays, **sponsorship is deleted, not ported**: `projects.sponsored`, `SetSponsored`, `POST /projects/{id}/sponsor`, the `charge_to` request field, the payer picker, `not_sponsored`, and `jobs.charged_to` all go. Sponsorship was standing consent to spend the owner's *personal* count; a project balance is that consent, expressed once as money rather than per-submit as a flag.

### A session is priced from its own tokens, against a rate card snapshotted at enqueue

Cost is `usage × rate`, per token class. The rate card lives in platform settings, keyed by model, and an admin changes it rather than a release doing so:

| class | why it is its own rate |
|---|---|
| input | base |
| output | billed above input |
| cache write, 5m | billed above fresh input |
| cache write, 1h | billed above the 5m write |
| cache read | billed far below input |

**`provider.Usage` must be widened first.** It carries `InputTokens`, `OutputTokens`, `CachedInputTokens` today and drops cache *creation* entirely, which the SDK reports broken down by cache lifetime. Charging without it under-bills every session by the most expensive input class there is. This widening is the first commit of this spec, before any ledger exists.

**Editing it is an API write today, and the console only shows it.** `PUT /admin/settings` takes the whole card, and the Platform screen renders it beside the two numbers it bounds — an admin setting a floor should be able to see what output tokens cost. Building a per-cell editor for a list that changes when a vendor changes prices is deferred rather than done: the numbers are typed, the write is one request, and a wrong cell in a rate card mis-bills every session until somebody notices. Stated here so "admin-editable" is not read as "editable in the console".

**The card is snapshotted onto the job row at enqueue, and everything prices from the snapshot.** Two consumers read the card at different times — the watchdog, every poll, and settlement, once — and an admin editing rates mid-run would otherwise let a session be killed against one ceiling and charged against another. A snapshot also makes pricing `func(usage, snapshot) int64`: pure, no I/O, therefore composable into the transaction that settles. And it makes "why was this session charged this much?" answerable from the ledger row plus the job row, with no archaeology through an audit log of rate edits.

**An unknown model fails expensive.** A span naming a model the card does not price is charged at the highest rate the card holds, and logged loudly. Refusing to start on an unknown model turns a vendor's new model id into an outage; charging zero turns it into free compute.

### The start gate is the real containment, and it lives inside `Enqueue`

A session may start only if its project's **available** balance is at least the admin-configured start reserve:

```
balance   = SUM(amount)                       -- what the pot holds
available = SUM(amount) − SUM(open holds)     -- what it may still commit
```

The check and the hold are one transaction with the job insert, serialised per **project** by `pg_advisory_xact_lock` — the same structure 006l built for the counter, for the same two reasons, of which the second is again the stronger:

- A handler check is bypassable; `Enqueue` is the one narrow point every session passes through, so a submit path added later inherits the gate instead of escaping it.
- A read-then-insert check races. With a ledger the race is worse than a miscount: the balance and the debit are *the same table*, so the hold must be written in the transaction that reads the balance, not by a second connection.

The hold is what makes concurrency safe in the ordinary case: ten simultaneous submits into one project each take a reserve out of `available`, so the tenth is refused rather than admitted against a balance the other nine had already committed.

An over-gate enqueue returns `job.ErrNoCredits` → `402 no_credits`, and the client shows the top-up modal. The start reserve is public — the frontend must be able to say *this project needs at least $X to start a session* before the submit is attempted.

**Both policy numbers fail closed, in opposite directions.** `platform.go` already argues this for the quota — *"a quota that disappears when Postgres hiccups is not a quota"* — and 006q made the seeded value and the fallback one constant so they cannot drift. The same rule, twice, with the directions worked out rather than assumed:

| number | seeded and fallback | a failed settings read resolves | so the failure mode is |
|---|---|---|---|
| start reserve | one constant, deliberately **high** | high | nothing starts |
| overdraft floor | one constant, deliberately **tight** | tight | a run is killed sooner |

A reserve defaulting to zero would let any project start a session on an empty pot, which is precisely the unbounded case this spec exists to close, so zero is not an available default for it. A per-project cap override that cannot be read falls back to the global default, and the global default that cannot be read falls back to the constant.

### A running session never dies for money, and a project may go negative

Once started, the meter accrues but does not kill. A session killed mid-run has burned real money and produced nothing, which is the worst of both outcomes; letting it finish converts spend into a result. Settlement therefore charges what was actually burned, taking the pot negative if the run overran its reserve.

Containment is the *next* session's start gate: a negative project has no available balance, so it cannot begin anything until someone tops it up. That is honest, visible, and needs no mid-run machinery.

**Settlement happens inside the store's terminal write.** The same argument as the gate: `Advance(…, PhaseDone|PhaseFailed)` and `Cancel` are where a session becomes terminal, and settling anywhere else — in the worker, in a handler — is settling somewhere a future path can miss. The hold is released and the charge posted in the transaction that writes the terminal phase, so a session is never terminal-but-unsettled.

**`charge` is unique per job; `adjust` is not.** A `UNIQUE (job_id) WHERE kind = 'charge'` partial index makes settlement exactly-once under a retried or raced terminal transition. Correction still has to be possible: a session that goes terminal with no worker driving it (a restart, a `Cancel` while the backend session is still live) settles from last-observed usage while the backend may keep burning, and reconciliation afterwards needs to post the difference. That difference is an `adjust`, which carries no uniqueness constraint.

**The worker polls once more before going terminal.** Cumulative usage reaches the row through `Observe`, which is a separate write from `Advance`; the last turn's spend can land after the final observation, systematically under-charging by up to one poll interval. A final `Poll` + `Observe` immediately before the terminal transition closes it. In `stop` the per-job run context is already cancelled, so that last poll uses the pool context, not the run's.

**"Failed is refunded" is gone.** `Phase.Spends()` existed only to serve the counter's refund rule and is deleted with it. A session that burned tokens is charged for them whatever phase it ends in, including one the user cancelled. Only a session that never reached the provider — no usage recorded — releases its hold with no charge. This is a visible behaviour change and it is the point: a refund for a run that cost real money is the platform paying for it.

### The hard cap is a backstop, and it is not a published number

A single admin-configured **project overdraft floor**, global by default and overridable per project: when a project's projected balance would fall below `−cap`, the run that crossed it is killed and settled. It bounds one runaway replication (a session starts at ≥ reserve, so it can only fall to the floor) and total project exposure, with one number.

**The projection is project-wide, not per-session.** The watchdog rides the observe path, which sees one session's cumulative usage — but the cap is a property of the *project*, and accrued spend does not enter the ledger until settlement. So each of N concurrent sessions in one project computing `balance − own accrued` would see none of the others, every one of them would stay above the floor, and together they would take the pot to roughly `−N × cap`. This is the same race 006l argued at the gate, and the hold does not cover it: a hold is the reserve, not the overrun. The condition is therefore

```
balance − Σ accrued(every in-flight session in this project)  ≤  −cap
```

and the session that dies is the one whose observation crossed it. Killing the largest spender would be defensible and needs a second read to decide; killing the one in hand is deterministic from the observation already at hand, and the next observation kills the next one if the projection is still under water.

It is **not surfaced to users**, because a published overdraft is an allowance to be spent: not on `/me`, not on any project payload, not an error code, not an i18n string, not an SSE cause. A killed run records a **generic failure cause** on `jobs.error` — which *is* owner-facing, serialised as `"error"` on the job payload — while the true cause is logged and shown in the admin console only.

Stated plainly rather than buried: the platform tells the owner their session failed and does not tell them why. That is withholding a reason, not asserting a false one, and it is chosen deliberately over an exploitable number. Behavioural inference from a run that dies at a repeatable balance is not worth engineering against; the code paths that would leak the number outright are, and they are enumerated above.

### Creating a project funds it in the same transaction

Since 006j a project is opened *by submitting a paper*, so a project born with an empty pot fails its own first enqueue and 006j's compensating delete fires on every create. `POST /projects` therefore carries an `allocate` amount, defaulting to the start reserve, moved from the creator's wallet to the new pot in the transaction that creates it. A creator with an empty wallet is refused with the same `no_credits` code as any other unfunded start, so they get the top-up modal rather than a generic create failure.

This **changes the shape of that refusal**, and 006j chose the current shape deliberately, so it is a decision and not an implementation detail. Today an unfunded create reaches `422 no_sessions_started` carrying a per-paper `quota_exceeded` (006j's keep-the-project-if-one-started rule, restated in 006q AC6), because the budget was checked per enqueue. An unfunded *wallet* fails before any paper is enqueued — there is nothing per-paper to report — so the refusal becomes a top-level `402 no_credits` and no project is created at all. 006j's rule is untouched where it still applies: a create whose allocation succeeds and whose papers then fail individually still keeps the project and reports per paper.

### There is no cutover

No backfill, no conversion, no compatibility shim. There are no users to protect, so the quota tables are dropped rather than migrated, everyone starts at zero credits, and an admin top-up is how money arrives. This follows 006q's precedent and its reasoning — one rule for everyone, and no balance whose origin is a migration nobody can read.

Payment is not in this spec. The top-up surface is admin-only and shaped so that a purchase (Stripe, in minor units, which is why the ledger is integer) can later post the same `topup` entry from a webhook instead of from a console.

### What is removed

Clean removal, no deprecation phase, per the standing rule on this codebase.

**Go** — `platform.DefaultSessionQuota`, `Settings.DefaultSessionQuota` and its column, `quota_topups` with `Topup`/`SetTopup`/`Topups`, `platform.Store.Allowance`, `job.Allowance`, `job.ErrQuotaExceeded`, `job.Store.Consumed`, `checkQuotaTx`, `WithPGAllowance`, `Phase.Spends()`, `Submission.ChargedTo`/`Payer()`, `jobs.charged_to`, `projects.sponsored`, `SetSponsored`, `POST /projects/{id}/sponsor`, `webauth.Quota`/`QuotaFunc`/`ConsumedFunc`, wire codes `quota_exceeded` and `not_sponsored`, and `access.EmailOf` — which existed only to price a payer known by their subject against email-keyed top-ups, and has nothing left to answer now that a wallet is read by the address `/me` already holds.

**Frontend** — the `Quota` type, `budget.ts` and its tests, the quota pill, the payer picker, the sponsorship toggle, the admin used/limit column, and every `quota*`/`charge*` i18n string (the frontend is single-locale today; if a `zh` catalogue exists when this lands, it changes in the same commit). The payer picker was the `Radio`/`RadioGroup` primitive's only consumer, so it goes too, with its CSS — a control nothing renders is dead code, not a design system.

**Tests to replace** — `internal/job/quota_test.go`, `internal/httpapi/quota_test.go`, `internal/httpapi/no_budget_test.go`, `internal/platform/platform_test.go`, `internal/webauth/adminmount_test.go`, `internal/webauth/coverage_test.go`.

**Specs and docs** — `BACKLOG.md` item 5 is deleted, including its sentence in the closing "Why these are backlog" paragraph, which names it. 006q keeps its open sign-in and is edited where it describes the zero quota as live. `specs/README.md` gains this row and its build-sequence prose is updated.

006l is fully superseded but **stays where it is**, with its status rewritten to say so and to point here. Retiring it to `.archive/` would break three inbound links (006k, 006q, `specs/README.md`) for a spec whose value is now historical — it is the record of why the counter was built the way it was, which is half the argument this spec inherits. A superseded spec that is still linked and still readable costs nothing; a moved one costs four edits and a reader's trust in the cross-references.

`internal/experiment.Caps` (007c) stays. It bounds a pilot's *size* — participants, trials, model calls — before it starts, on a different axis from money, and the two coexist deliberately.

### Package shape

A new `internal/credit` owns the ledger: the entry model, its migrations, balances, top-up, allocate, reclaim, and the rate card's pricing function. It imports neither `job` nor `platform`.

It brings its own migrator, on the pattern the other five follow: a `credit_migrations` version-namespace table so it composes on one database without owning anyone else's versions, and **`migrationLock = 6`** — 1 through 5 are taken (`job`, `access`, `platform`, `profile`, `project`), and a collision would silently serialise unrelated migrators, visible only under a rolling deploy.

`internal/job` imports `internal/credit`. 006l's rule was that `job` must not import `platform` — a *policy* package whose numbers an admin tunes — and that rule stands: the rate card and the reserve reach `Enqueue` through the job snapshot and an injected seam, not through an import. A ledger primitive that must execute in the job store's own transaction is a different kind of dependency, and inverting it would mean smuggling a `pgx.Tx` through an interface that also has to be satisfiable by the in-memory store.

The seam differs per store — the Postgres one takes ledger operations that accept a `pgx.Tx`, the in-memory one takes their lock-held equivalents — and the store **contract test** asserts behaviour, not seam shape, so both implementations answer the same questions. A nil seam means unmetered, which is what an offline run and every test that is not about money want; this is the direct replacement for 006l's *"a nil `Allowance` means unlimited"*.

## Build sequence

Each step is one small diff, in order, and the system is coherent after each.

1. **Widen `provider.Usage`** with cache-creation by lifetime; thread it through the anthropic adapter, the transcript derivation (006s), and the viewer.
2. **`internal/credit`**: entries, migrations, balances, the rate card and its pure pricing function, top-up / allocate / reclaim with the role rules. Both stores against one contract test.
3. **The gate**, which is where the old meter dies whole: the start reserve in platform settings, the check-and-hold inside `Enqueue` under the project advisory lock, `ErrNoCredits` → `402 no_credits`, the project-create allocation, and the removal of the counter **and of sponsorship** in the same commit.

   The quota check cannot wait: `DefaultSessionQuota` is `0`, so it refuses every non-admin enqueue that has no top-up row, and landing the credit gate beside it would leave a fully funded project still refused `402 quota_exceeded` by a permanently closed second gate. Two gates in one transaction, one of them shut, is not a coherent intermediate state.

   Sponsorship is folded in here rather than left to step 6 for a different reason: it exists to answer *whose count does a collaborator's session draw down*, and the moment the project pays, that question has no referent. Keeping the toggle and the payer picker alive for another commit would mean writing tests for a control that no longer decides anything. So `projects.sponsored`, `charge_to`, `not_sponsored` and `jobs.charged_to` go with the counter.
4. **Settlement**: the rate snapshot on the job row, the final poll before terminal, release-and-charge inside the terminal write, the `charge` uniqueness constraint.
5. **The backstop**: the overdraft floor in settings (`DefaultOverdraftCap`, fail-tight) with its per-project override (`projects.credit_cap`, `Store.SetCreditCap`, `PUT /admin/projects/{id}/cap` — admin policy, never the owner's), `Store.Accrued` for the project-wide projection, `job.Budget`/`WithBudget` and the watchdog on the observe path, `GenericFailure` as the recorded cause, and the true reason in the log and the admin console only.
6. **Removal**: what is left after step 3 absorbed the rest — `BACKLOG.md` item 5 and the sentence naming it, 006l's status, and 006q where it describes the zero quota as live.
7. **Surfaces**: `/me` wallet balance, project balance and available (members only), `GET /me/credits` and its statement, allocate and reclaim, a session's own cost, and the admin console's reserve, floor and (read-only) rate card.

## Acceptance criteria

1. A balance is a fold over `credit_entries` and appears nowhere as a stored column; `topup`, `allocate`, `reclaim`, `hold`, `release`, `charge`, `adjust` each round-trip through both stores against one contract test.
2. `allocate` moves credits wallet → pot as one transaction that cannot leave money in neither place; `reclaim` moves pot → wallet and is refused for a non-lead member with `403`, asserted for a member, a lead, and a stranger.
3. A project whose `available` is below the start reserve is refused at enqueue with `ErrNoCredits` → `402 no_credits`, from **inside** the inserting transaction — asserted through every session-creating path (`POST /papers`, `POST /projects`), so a new route cannot escape it.
4. Concurrency is exact: parallel submits into one project at the boundary let through exactly the number the available balance supports, because each holds its reserve in the transaction that reads the balance. Confirmed to fail without the advisory lock.
5. Cost equals `usage × snapshotted rates`, per token class including both cache-write lifetimes; a session priced after an admin edits the rate card mid-run is charged at the rates it started with, not the current ones.
6. A session that overruns its reserve **runs to completion** and settles negative; the project's balance is negative afterwards and its next enqueue is refused `402 no_credits`.
7. Settlement is exactly-once, and the assertion sits where the guarantee actually bites: settling one session twice, and racing several settlements in separate transactions, each produce one charge and one release. Not through the job store — its phase machine already serialises terminal writes, so a test driving cancels concurrently would pass with the ledger's guard removed, and says so. An `adjust` posted afterwards is still accepted.
8. A session ending `failed` or cancelled with recorded usage is **charged**, not refunded; one that never reached the provider releases its hold with no charge.
9. The final poll before the terminal transition is asserted: usage arriving after the last ordinary observation is included in the charge.
10. A run crossing the project overdraft floor is killed and settled, and the cap leaks nowhere — asserted by a test that greps the `/me`, project, job, and SSE payloads and the i18n catalogue for the number and for any cap-named code, and that the job's `error` is the generic cause while the admin console reports the true one.
11. The floor holds against **concurrent** sessions: N sessions running in one project, each individually under the cap but jointly over it, produce a kill — the projection subtracts every in-flight session's accrued spend, not just the observing session's. Confirmed to fail against a per-session projection.
12. Both policy numbers fail closed in their own direction: with the settings read failing, the reserve resolves high (an enqueue is refused) and the floor resolves tight (a run is killed no later than it would have been), and the seeded value and the fallback are one constant each. A backstop that cannot read its inputs at all stops **nothing** — a run killed by a database hiccup turns a transient fault into destroyed work, which is worse than the overspend it was guarding.
13. A deployment that meters money but cannot price a session refuses it at enqueue (`ErrUnpriceable`), holding nothing. An unpriceable session would otherwise settle at zero *and* be invisible to the floor's accrual — free compute counting against no bound. A rate card that cannot be *read* is a different case and resolves to the compiled-in list, because falling back to nothing would make every session free for as long as the fault lasted.
14. The floor's two reads are ordered accrual-then-balance, so a session settling between them is counted twice rather than not at all: the projection may fire the floor early, never late.
15. `POST /projects` allocates from the creator's wallet in the create transaction, one start reserve per paper unless the request names an amount; a creator with an empty wallet is refused `no_credits` and no half-made project survives (006j's compensating delete).
16. A create that opens **no** session returns its credits: the compensating delete unwinds the allocation as well as the project, so a create refused by the double-trigger guard leaves the wallet where it started. Asserted on both create paths, and confirmed to fail without the unwind — the ledger has no foreign key on a project, and a reclaim needs a role in one that no longer exists. Membership is seeded only once a session has started, for the same reason: nothing cascades a membership row away.
17. Every item in "What is removed" is gone from the build, the schema, the wire, and the frontend — asserted by the compiler, by a schema assertion that the dropped columns and tables do not exist, and by a grep test over the removed wire codes.
18. A platform admin tops up a wallet, and that person can then allocate and run exactly what the credits pay for; the ledger explains each step by actor and cause, and `/me/credits` shows them that statement.
19. A project reports its balance and its available balance to a member and to nobody else — a stranger reading a public project is looking at the research, not at whose money paid for it. Both numbers are reported, because they differ by the sessions running now, and that difference is why a project with credits can still refuse a submit.
20. Any member may allocate to a project; only a lead may reclaim; neither may take credits a running session is holding. Each refusal is its own code and its own sentence, because each has a different remedy.
21. A session reports what it cost, derived from its snapshot rather than stored, and reports nothing about the rates it was priced at — the price list is the platform's, not the owner's.

## Out of scope

- **Purchase.** No Stripe, no checkout, no invoice, no tax, no refund-to-card. An admin is how credits are created. The ledger's `topup` entry is the seam a webhook will post to.
- **A rate-card editor in the console.** The card is written through `PUT /admin/settings` and shown read-only. A per-cell editor is a small feature over a list that changes rarely and mis-bills everything when it is wrong; it waits for someone to actually need it.
- **Per-contributor accounting inside a pot.** The pot is fungible by decision; who allocated what is recorded on the entries for audit, and confers no claim on reclaim.
- **Currency other than micro-USD.** No conversion, no EUR-denominated balances, no FX. An admin typing a cap in euros converts it themselves.
- **Pausing a session for approval.** The right end state for a run that hits its ceiling is to pause and ask a human, and it needs a new non-terminal phase, a resume path through the provider, a notification, and a timeout reaper. Recorded as the successor to the backstop, not built here.
- **Per-model spend attribution below the session.** Cost is charged per session from cumulative usage. Attributing a session's spend across the models it used is a reporting feature over the same transcript.
- **Rolling budgets, monthly resets, plans, and prepaid tiers.** A balance with no expiry is what this builds; a time-bucketed allowance is a different policy over the same ledger.
- **Metering the CLI.** `replichai audit --publish` runs outside the HTTP surface with the operator's own credentials, exactly as 006l stated. Still a decision, still not a bug.
