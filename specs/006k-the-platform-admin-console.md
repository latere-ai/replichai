# 006k — The platform admin console

**Status:** built, and its shape outlived its contents. `internal/platform` owns the single-row `platform_settings` (pinned by a check constraint, fail-closed to a compiled-in default), with mem and Postgres stores against one contract test. `webauth.MountAdmin` registers every admin route behind `RequireTier(admin)` and returns the set, so a route added without the guard fails the test. The frontend Admin tab (rail group "Platform", admin-only) drives grant, promote, revoke, the policy row, and the per-person grant. Verified against a real Postgres and by hand through the running app.

**What [011](011-what-a-session-costs.md) changed, and what it did not.** Everything structural here stands: one pinned policy row, fail-closed to a compiled-in constant, admin-only and re-checked in the store; every route enumerated and guarded; one screen with two cards. What changed is what the row *holds* and what a top-up *is*. The row's `default_session_quota` became a start reserve, an overdraft floor and a rate card, all in micro-USD; the email-keyed `quota_topups` table is gone, replaced by a wallet balance in the ledger, which records who granted it and when — the audit trail a table of integers never had. The routes below are read with that substitution. The `Store` interface shrank with the table: `SetTopup`, `Topups` and `Allowance` are gone, leaving `Settings` and `SetSettings`, so acceptance criteria 5 and 6 are answered by 011's ledger tests rather than met here. The row has kept growing since — [022b](022b-checkout-and-the-webhook.md)'s purchase spread and [039](039-a-description-can-be-drafted.md)'s draft model sit beside the reserve, the floor and the rate card, and `MountAdmin` enumerates their routes alongside the six below.
**Scope:** the admin's own screen. A `platform_settings` row this deployment owns, per-person grants, and the frontend Admin tab that drives them together with the grant endpoints [006c](006c-identity-and-access.md) already built. It adds no authority model: admin tier is 006c's, and every route here is the same `RequireTier(admin)` shape. A child of [006](006-web-application.md).
**Depends on:** [006c](006c-identity-and-access.md) (the tier, the grant endpoints, the store-level `requireAdmin` re-check), [006b](006b-job-model-and-persistence.md) (Postgres and the per-package migration idiom).
**Consumed by:** [011](011-what-a-session-costs.md), which enforces the numbers this spec stores (and [006l](006l-the-session-quota.md), which did before it).

## Problem

Running the seed phase means answering three questions daily — who may sign in, who is an admin, and how much may a newcomer try — and today two of them are answerable only by `curl` and the third not at all.

`POST /admin/access` exists and no frontend calls it (`frontend/src/api.ts` has no `/admin` client), so admitting a researcher means hand-signing a request. The bootstrap lists `ADMIN_EMAILS`/`SEED_EMAILS` live in `deploy/base/deployment.yaml`, so the *first* admin costs a redeploy — correct for a bootstrap, wrong for everyday admission. And there is no platform policy anywhere: no table, no column, no env var holds "how many sessions may an invited person run". 006c deliberately scoped policy out; this spec is where it lands.

## Decisions

**Platform policy is a typed row in the database, not an environment variable.** 006c rejected an env-var grant list because every change costs a redeploy and leaves no record of who made it; the same reasoning binds a policy number an admin is expected to tune weekly. `platform_settings` is a single row, pinned by `CHECK (id = 1)` so a second row is a constraint violation rather than an ambiguity, with typed columns and not a JSON bag: adding a setting is a migration, which is exactly the review step a platform-wide policy change deserves.

```sql
CREATE TABLE platform_settings (
  id                    SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  default_session_quota INT NOT NULL,
  updated_by            TEXT NOT NULL,
  updated_at            TIMESTAMPTZ NOT NULL
);
```

**The read is fail-closed.** A missing row, an unreachable database, or a decode failure resolves to the *compiled-in conservative default*, never to a permissive one. A limit that silently disappears when Postgres hiccups is not a limit. The seeded row and the fallback constant are the same value, so the two cannot drift. (011 keeps this rule and works out its direction per number: a start reserve fails high so nothing starts, an overdraft floor fails tight so it kills no later than it would have.)

**Top-ups are their own relation, keyed by email.** Admins hand budget to individuals, so it lives beside the policy rather than on it. That column does *not* go on `access_grants`: 006c states plainly that the table is an ACL and not a user table, and one spending-policy column is how that erodes. It is keyed by **email**, not principal, for the same reason a grant is — an admin funds someone who has not signed in yet, and it applies on their first login with no second step. (011 keeps the relation and the email key, and moves it into the ledger, where a top-up is an entry naming its actor rather than an integer nobody can trace.)

```sql
CREATE TABLE quota_topups (
  email      TEXT PRIMARY KEY,
  extra      INT NOT NULL,
  granted_by TEXT NOT NULL,
  granted_at TIMESTAMPTZ NOT NULL
);
```

**A new `internal/platform` package, its own migrations, its own version table.** Every store package here (`job`, `access`, `project`) owns its migration list and version table and is applied in a fixed order by `openStores`; platform is the fourth and depends on none of them. It ships with a `MemStore` twin against one contract, so the whole admin surface tests with no database.

**Admin authority is re-checked in the store, as 006c's is.** `access.MemStore/PGStore.Grant` call `requireAdmin` internally so the tier holds even if a route were mounted outside the guard. The platform store does the same through a one-method seam, so it depends on the *tier* and not on the access package's internals:

```go
// Authority answers what a caller may do; satisfied by access.Access.
type Authority interface {
    Resolve(ctx context.Context, p access.Principal) (access.Tier, bool, error)
}

type Settings struct {
    DefaultSessionQuota int
    UpdatedBy           string
    UpdatedAt           time.Time
}

type Store interface {
    Settings(ctx context.Context) (Settings, error)              // fail-closed; no caller tier needed
    SetSettings(ctx context.Context, by access.Principal, s Settings) error
    SetTopup(ctx context.Context, by access.Principal, email string, extra int) error
    Topups(ctx context.Context, by access.Principal) ([]Topup, error)
    // Allowance is the effective cap for one person: default + topup, and the
    // one place the two compose. It takes a Principal, not a bare email,
    // because the tier is half the answer — an admin is unlimited.
    Allowance(ctx context.Context, p access.Principal) (n int, unlimited bool, err error)
}
```

**The admin routes are mounted from one place and enumerated.** Today they are three loose `mux.Handle` calls in `cmd/server/main.go`, outside the router's `gated`/`open` enumeration that makes 006c AC2 testable. This spec extracts `webauth.MountAdmin(mux, guard, …) []httpapi.Route`, which registers every admin route and returns the set, so a test can drive each one with a `TierUser` principal and assert a refusal — and a route added without the guard fails that test instead of shipping open.

| Route | Does |
|---|---|
| `GET /admin/access` | list grants (006c, unchanged) |
| `POST /admin/access` | seed or promote by email (006c, unchanged) |
| `DELETE /admin/access/{email}` | revoke (006c, unchanged) |
| `GET /admin/settings` | read the platform row |
| `PUT /admin/settings` | write it, recording `updated_by` |
| `POST /admin/access/{email}/credits` | add credits to that person's wallet (`PUT …/quota` before 011) |

**The tab is admin-only in the rail and refused server-side regardless.** `me.tier === 'admin'` decides whether the Platform group renders; it decides nothing else. Every write is refused by the tier guard *and* by the store, so a forged request from a signed-in user with the tab hidden gets a `403`, which is the property AC1 tests.

**The console is one screen with two cards.** *People* — the grant table (email, tier, whether it is bound to a principal, top-up, revoke) plus an invite-by-email form that seeds `user` or `admin`. *Platform* — the policy numbers, with each value's meaning stated in the UI rather than implied. It is a fourth `View` variant in `App.tsx` reached from a rail group, the same shape every other screen takes; there is no router to add to.

## Acceptance criteria

1. Every admin route is refused for a signed-in non-admin, driven from the enumerated set `MountAdmin` returns, so a route mounted without the guard fails the test. The store refuses too: `SetSettings`/`SetTopup` called directly with a `TierUser` principal return `ErrNotAdmin`.
2. `PUT /admin/settings` persists the policy row with `updated_by` set to the calling admin, and `GET /admin/settings` reads it back — asserted against both the mem and Postgres stores through one contract test.
3. A missing `platform_settings` row and a failing store both resolve to the compiled-in conservative default, never to unlimited, and the seeded value equals that constant.
4. A second row cannot exist: an insert with `id = 2` violates the check constraint.
5. A top-up set for an email that has never signed in applies to that person once they do — `Allowance` returns `default + extra` for the email, and `SetTopup` is refused for a non-admin.
6. `Allowance` reports `unlimited` for a platform admin and `default + topup` for everyone else, in one place both stores share.
7. The Admin tab renders only for `tier === 'admin'` (asserted on the pure predicate), and drives grant, promote, revoke, top-up, and the settings write against the real endpoints.

## Out of scope

- **Enforcing the number.** Holding a session against a balance, settling it, and displaying what is left are [011](011-what-a-session-costs.md) (and were [006l](006l-the-session-quota.md) before it). This spec stores the policy; that one spends against it.
- **The used/limit column in the grant table.** It needs 006l's counter, and landed with it: the column is composed here from the settings row and the top-up rather than read through `platform.Allowance`, because resolving an unbound grant by email would *bind* it — a read must never write.
- **Retiring `ADMIN_EMAILS`.** The bootstrap stays: an empty database must still yield a first admin without a console to admit them. (`SEED_EMAILS` sat beside it and was removed by [006q](006q-sign-in-is-open-budget-is-not.md), which granted what it pre-granted.)
- **An audit log of admin actions.** `updated_by`/`granted_by` record the last writer, not a history. A real log is its own spec.
- **Any second platform setting** (concurrency, model choice, feature flags). The table is shaped to take them; this spec adds one column.
