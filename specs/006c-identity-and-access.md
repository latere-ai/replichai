# 006c — Identity, tiers, and the access gate

**The forge-account link below is gone, and the tier table has one wrong row.** The link was deferred to [006f](006f-taking-a-reproduction-out.md) as its only consumer; 006f invites the login the request names instead, and ownership is granted by the organisation's invitation. Nothing ever wrote or read it, so its two `Access` methods, both store implementations, the `forge_links` table (dropped in migration 5) and `/me`'s always-empty `linked_accounts` are all removed — with them go the `/{forge}` routes and acceptance criterion 5. Separately, [006q](006q-sign-in-is-open-budget-is-not.md) made a first sign-in self-grant `TierUser`, so the table below is wrong to say an admin sets it; migration 3 also added `revoked_at`/`revoked_by`, because a self-granting sign-in turns a deleted grant into a logout. The fail-closed gate, the two portable claims, seed-and-bind, and the one-way binding all hold.

**Status:** implemented; the forge-account link is removed (it never gained the consumer it was deferred to)
**Scope:** who may sign in, who may operate, and how a reproduction gets granted — the OIDC login, the fail-closed `access_grants` gate, the two tiers, and the proven forge-account link. A child of [006](006-web-application.md).

**As built.** `internal/access` is the model (grants, tiers, project membership, seed-by-email with one-way binding; in-memory and Postgres against one contract). `internal/webauth` is the HTTP edge: the fail-closed gate that re-resolves a grant per request, the OIDC login via `latere.ai/x/pkg/oidclogin` (portable standard OIDC), a session cookie carrying only `{sub, email}` — sealed with a hand-rolled AES-256-GCM codec, because `oidclogin` owns the token flow but not session storage and `pkg/oidc`'s codec is welded to its own login `Client` — `/me`, and the `/admin/access` endpoints. Because latere auth keeps `email` (PII) out of the ID token, the callback fetches it from `/userinfo`. The whitelist is bootstrapped from `ADMIN_EMAILS` (`SeedGrant`, `granted_by=bootstrap`); runtime changes go through an admin. (`SEED_EMAILS` sat beside it until [006q](006q-sign-in-is-open-budget-is-not.md) opened sign-in, which granted what it pre-granted and so removed it.) A loopback-only dev bypass authenticates a synthetic principal for local runs and grants nothing.

**Depends on:** [006b](006b-job-model-and-persistence.md) (Postgres, and the routes the gate mounts on). Revised by [007](007-from-reproduction-to-replication.md): project membership sits beside the tiers, sharing their seed-and-bind rule and none of their authority.

Binding is on the **IdP-authenticated email**: latere auth vouches for the email of an authenticated session and enforces its uniqueness, so the session's email is trusted the same way its `sub` is, and the operator whitelist is the actual guard. There is no `email_verified` check.

## Problem

One shared credential pays for every run, so access starts closed. Signing in must prove who someone is without welding this product to the identity provider's own authorisation model, and operating must be gated on a decision this deployment owns, not one read out of someone else's token. And the one place a reproduction leaves the organisation — a collaborator invitation — needs the user's forge account to be *proven*, never typed.

## Decisions

**Authentication is portable; authorisation is ours.** The identity provider is an OIDC provider and nothing more. The login is a standard public PKCE flow; from the ID token we keep exactly two things and discard the rest:

| Claim | Use |
|---|---|
| `sub` | the stable principal every row keys on |
| `email` | seeding a grant for someone not yet signed in — nothing else |

No IDP-issued role, scope, org membership, or admin flag reaches an authorisation decision. Reading them would weld the beta to one issuer's schema; swapping issuers must be a config change. The library is a conformant OIDC client, not a vendor SDK whose value is the claims we just refused. Rejected: gating on an IDP `replichai:operate` scope or seed-org membership — it puts our access policy in another service's admin UI.

**The gate is a single structural check, fail-closed.** The `access_grants` table is the whole model: an empty table admits nobody. The operator routes (006a) are mounted behind one gate they cannot be served without — not a condition sprinkled through handlers, the same shape as 002's redistribution gate. `GET /me` sits *outside* it, so the frontend can render the not-invited screen; everything else answers `403` with a reason (`not_granted` for an ungranted principal — `not_invited` until [006q](006q-sign-in-is-open-budget-is-not.md) renamed a state that no longer exists; `not_owner` for a caller reaching another owner's project actions), read surfaces included, because a private project's repositories are private.

**Grants seed by email, bind to a principal, one-way.**

- A row is `(email PK, principal, tier, granted_by, granted_at)`. An admin seeds by email someone who has never signed in.
- On that person's first login, the row binds to their `sub` — matched on the IdP-authenticated email, and only until bound. **Binding is one-way:** once `principal` is set, email is never consulted for that row again, and a second principal presenting the same email is refused, not admitted onto someone else's grant.
- The table is an ACL, not a user table: principal, a seed email, a tier. No name, no avatar — display data is read from `/userinfo` at the callback (the only moment an access token exists) and carried in the client-held sealed session cookie, never in a server table; it goes stale until the next sign-in and confers nothing ([006m](006m-the-signed-in-person.md)). Rejected: an env-var email list — every grant would cost a redeploy and leave no record of who admitted whom.

**Two tiers gate the platform; publishing is not one of them.**

| Tier | Set by | May |
|---|---|---|
| **Platform admin** | bootstrap admin-email list in the env, seeded at startup; admins promote others | grant, revoke, promote — and all below |
| **User** (invited) | a platform admin | sign in, own projects, submit, watch, read results and transcripts, invite collaborators to their own projects, claim ownership if a forge account is linked, and make *their own* project public with a responsibility sign-off ([006f](006f-taking-a-reproduction-out.md)) |

There is no self-serve path up the tiers. The public flip is authorised by *project ownership* plus a sign-off, not by a platform tier — so it lives with the project (006f), not here.

**Project membership reuses the seed-and-bind rule, and admits nobody to the platform.** [007](007-from-reproduction-to-replication.md) needs replication projects led by researchers named by email. That is this table's *shape* applied to a second relation — `(principal, project, role)` with roles *lead* and *collaborator*, seeded by email and bound one-way on first login — not a second membership system, and not an extension of the tier ladder.

The two are orthogonal, and that is the point. Tier decides what someone may do on the platform; project role decides which project's experiments and data they may touch. A project lead may add a colleague by email, and that colleague still reaches no operator route until a platform admin grants them a tier — the UI says so plainly when a lead adds someone ungranted. Rejected: having a project invitation seed a platform grant. It is the obvious convenience, and it is exactly the self-serve escalation [006](006-web-application.md) rejected — one lead could then admit the world onto a shared credential.

**The forge link is proven, stored against the principal, identity-scoped.** Linking a forge account is a separate OAuth handshake requesting **identity only** — `repo` scope is never requested, for anyone, ever. The proven login is written by the link callback alone, never a request body: one row per principal and forge, so a claim months later still knows where to send the invitation. This is what 006f's collaborator invitation consumes. `/me` reports *linked accounts*, and the routes are `/{forge}`-parameterised — GitHub is the only forge today, not the only shape.

**No development shortcut confers a grant.** A stub issuer for local work authenticates a synthetic principal but seeds no row, and a deployment reachable off loopback refuses to start with the dev bypass configured. The bypass is for local work, not a back door into the beta.

## Interface (sketch)

```go
type Tier int // User | PlatformAdmin

type Principal struct { Sub, Email string }

type Access interface {
    Resolve(ctx, Principal) (tier Tier, granted bool, err error) // binds on first login
    Grant(ctx, byAdmin Principal, email string, tier Tier) error
    Revoke(ctx, byAdmin Principal, email string) error
    LinkedAccount(ctx, principal, forge string) (login string, ok bool, err error)
}

type Role int // Collaborator | Lead   (007)

// Membership is the second relation, sharing Access's seed-by-email and
// bind-on-first-login rules and none of its tier semantics.
type Membership interface {
    Add(ctx, byLead Principal, project, email string, r Role) error
    Remove(ctx, byLead Principal, project, email string) error
    Role(ctx, principal, project string) (Role, bool, error) // binds on first login
}

// Gate is the middleware 006a mounts; RequireTier guards the admin-only routes.
func Gate(a Access) func(http.Handler) http.Handler
```

## Acceptance criteria

1. Only `sub` and the IdP-authenticated email are consumed; a test drives login against a stub issuer whose token also carries roles/scopes/org claims and asserts none reach an authorisation decision.
2. With an empty `access_grants` table, an authenticated session reaches no operator route — the gate enumerates 006a's gated routes and each answers `not_granted`; a route added without the gate fails the test.
3. A grant seeded by email admits on first sign-in and binds to the principal; a second principal presenting that email afterwards is refused; revoking locks the holder out on the next request with no session left standing.
4. Tier is enforced server-side: only a platform admin may grant, revoke, or promote, refused even when the request is forged past the UI. (The public flip's owner check lives with the project, 006f.)
5. The forge link requests identity scope only and stores the proven login against the principal; a client-supplied login is never trusted — asserted by driving `claim` for an unlinked user (`409`) and a linked one (invitation sent).
6. The dev bypass authenticates but never grants, and a non-loopback deployment refuses to start with it enabled.
7. Project membership admits nobody to the platform: a test adds an ungranted email to a project, signs that principal in, and asserts they still answer `not_granted` on every gated route. Membership and tier are resolved independently, and neither can be satisfied by the other.
8. Project membership binds one-way on the same terms as a grant — seeded by email, bound to the first principal, a second principal presenting that email refused.

## Out of scope

The routes themselves and their mounting (006a). The collaborator invitation and the owner's public flip that consume the link and project ownership (006f). Any use of the IDP beyond the two claims — its roles, orgs, billing are another product's model. Multi-tenant authorisation, org RBAC, and per-user credentials — deferred with the rest of multi-tenancy. The frontend's not-invited screen (006e); this spec makes the state, that spec renders it.
