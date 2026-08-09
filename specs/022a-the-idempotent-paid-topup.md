# 022a — The idempotent paid top-up

**Status:** built. Leaf of [022](022-money-comes-in-without-an-admin.md). Migration **v3** (`external_ref` + the partial unique index, mirroring `credit_entries_one_charge`), `Entry.ExternalRef` threaded through the single insert and the statement scan, `ErrNoRef`, and `TopupPaid`/`RefundPaid`/`EntryByExternalRef` on the `Store` interface and both stores — idempotent via `insertPaidTx`'s `ON CONFLICT (external_ref) WHERE external_ref IS NOT NULL DO NOTHING` on Postgres and a ref scan under the lock in memory. `internal/payment` holds the `Provider` port, its `Event`/`CheckoutParams` shapes, `ErrUnconfigured`, and a `MemProvider` fake. The contract test gained `TestPGStoreAndMemStorePostPaidEntriesIdempotently` (credit once on a retried ref, negative-allowed refunds, independent dedupe, `EntryByExternalRef`), green against the in-memory store **and real Postgres 16**; the whole module builds, vets, and tests clean. Commits `e70666b` (ledger) and `605cc8b` (port).

**One deviation from the sketch:** `RefundPaid` takes a **positive magnitude** and posts `-amount`, so it reuses `ErrNotPositive` with the package's one meaning of "an amount must be positive," rather than the draft's negative-amount signature. 022c calls it with the original purchase's (positive) credited amount.

**Scope:** the money-safety core, offline. One ledger migration (an `external_ref` column and its partial unique index), two store methods a webhook will later call — a `topup` posted by a payment rather than an admin, and a negative `adjust` for a refund — each **idempotent on `external_ref`**, and a minimal payment **port** with a memory adapter so the HTTP leaf can depend on an interface, not the Stripe client. No Stripe SDK, no HTTP, no frontend.

**Depends on:** [011](011-what-a-session-costs.md) (built — the ledger, `Entry`, `KindTopup`, `KindAdjust`, `insertTx`, the shared store contract).

## Goal

Give the ledger a place to record a payment provider's reference and to refuse a doubled credit, plus the two methods that post a purchase and a refund — all provable against the mem + Postgres store contract with no network.

## What to do

1. **Migration v3** — append to the `migrations` slice in `internal/credit/migrate.go` (v2 is already `project_id`/`project_name`, so this is `{3, …}`):
   ```sql
   ALTER TABLE credit_entries ADD COLUMN external_ref TEXT;
   CREATE UNIQUE INDEX credit_entries_external_ref
     ON credit_entries (external_ref) WHERE external_ref IS NOT NULL;
   ```
   This mirrors the existing `credit_entries_one_charge` partial-unique template; additive and nullable over live rows.

2. **`Entry.ExternalRef`** — add `ExternalRef string \`json:"external_ref,omitempty"\`` to `Entry` (`internal/credit/credit.go`). Thread it through the single INSERT in `PGStore.insertTx` (`pgstore.go:149`, 9→10 columns), the `Entries` scan (`pgstore.go:56`/`:67`, `COALESCE(external_ref,'')`), and `MemStore.append` (`memstore.go:143`).

3. **`ErrNoRef`** — a paid entry must be idempotent, so an empty ref is a programming error: add `ErrNoRef = errors.New("a paid entry needs an external reference")` to `credit.go`.

4. **`TopupPaid`** on the `Store` interface and both impls:
   ```go
   // TopupPaid posts a purchased credit into a wallet, idempotent on ref.
   // No principal: authority came from a verified webhook signature, upstream.
   TopupPaid(ctx context.Context, email string, credited int64, ref, actor string) error
   ```
   Posts a `KindTopup` entry, `Actor=actor` (e.g. `"stripe:pi_…"`), `ExternalRef=ref`. No `requireAdmin`. Validates: non-empty email (`ErrNoEmail`), `credited > 0` (`ErrNotPositive`), non-empty ref (`ErrNoRef`). **Idempotent:** on PG, `INSERT … ON CONFLICT (external_ref) DO NOTHING`; on mem, scan under the lock for an existing `ExternalRef==ref` and return nil if found. A duplicate returns nil (at-most-once insert, exactly-once credit).

5. **`RefundPaid`** on the `Store` interface and both impls:
   ```go
   // RefundPaid reverses a purchase: a negative adjust, idempotent on ref.
   RefundPaid(ctx context.Context, email string, amount int64, ref, actor string) error
   ```
   `amount` is a **positive magnitude**; the entry posted is `-amount`, a `KindAdjust` (per 011 — **not** a new kind), `ExternalRef=ref`. Validates `amount > 0` (`ErrNotPositive` otherwise) and a non-empty ref, via the shared `checkPaid`. Same idempotency. The wallet **may go negative** — that is the point; no floor here.

6. **`EntryByExternalRef`** on the `Store` interface and both impls:
   ```go
   // EntryByExternalRef finds the entry that carries ref, if any. 022c uses it
   // to reverse a purchase by the exact micro-USD it credited (FX drift aside).
   EntryByExternalRef(ctx context.Context, ref string) (Entry, bool, error)
   ```

7. **The idempotent insert** — add `insertPaidTx` (PG) beside `insertTx` that appends `ON CONFLICT (external_ref) DO NOTHING` and reports whether a row landed, or extend `insertTx` with a conflict mode. Keep one write point per store.

8. **The payment port** — new package `internal/payment`, interface only plus a memory adapter (the Stripe adapter is 022b):
   ```go
   type Currency string // "usd" | "eur"
   type Provider interface {
       // CreateCheckout returns a hosted URL for a top-up of amountMinor in
       // currency, carrying meta (email, snapshotted spread, currency) so the
       // webhook can credit correctly. Returns ErrUnconfigured if no keys.
       CreateCheckout(ctx context.Context, p CheckoutParams) (url string, err error)
       // ParseWebhook verifies the signature and returns a typed event.
       ParseWebhook(payload []byte, sigHeader string) (Event, error)
   }
   ```
   Keep `CheckoutParams`/`Event` minimal; a `MemProvider` records calls and lets a test synthesize the events 022b/022c will handle. Define `ErrUnconfigured`.

## Tests (`internal/credit`, `internal/payment`)

Extend the shared `runStoreContract` (`store_contract_test.go:53`) so both mem and Postgres cover:
- `TopupPaid` credits the wallet once; a **second call with the same ref is a no-op** and `Balance` moved once.
- `RefundPaid` posts a negative adjust; `Balance` goes below zero; a duplicate ref is a no-op.
- `TopupPaid` empty ref → `ErrNoRef`; non-positive → `ErrNotPositive`. `RefundPaid` positive amount → `ErrNotPositive`.
- A `TopupPaid` and a `RefundPaid` with **different** refs both apply (independent dedupe).
- `EntryByExternalRef` returns the posted entry and `false` for an unknown ref.
- Postgres cases run only under `TEST_DATABASE_URL` (skipped otherwise), per the existing harness.
- `internal/payment`: the `MemProvider` round-trips a checkout call and yields a parseable event; `ErrUnconfigured` when unconfigured.

## Boundaries

- No Stripe SDK, no HTTP routes, no frontend, no settings changes — those are 022b.
- A refund is `KindAdjust` (011), **not** a new `KindRefund`.
- Do not touch `Allocate`, `Reclaim`, admin `Topup`, holds, or settlement.
- No freeze, no override, no solvency read (022c/022d).
