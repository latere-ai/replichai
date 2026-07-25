# 006c — Identity, tiers, and the access gate

**Status:** draft
**Scope:** who may sign in, who may operate, and how a reproduction gets granted — the OIDC login, the fail-closed `access_grants` gate, the three tiers, and the proven forge-account link. A child of [006](006-web-application.md).
**Depends on:** [006b](006b-job-model-and-persistence.md) (Postgres, and the routes the gate mounts on).

## Problem

One shared credential pays for every run, so access starts closed. Signing in must prove who someone is without welding this product to the identity provider's own authorisation model, and operating must be gated on a decision this deployment owns, not one read out of someone else's token. And the one place a reproduction leaves the organisation — a collaborator invitation — needs the user's forge account to be *proven*, never typed.

## Decisions

**Authentication is portable; authorisation is ours.** The identity provider is an OIDC provider and nothing more. The login is a standard public PKCE flow; from the ID token we keep exactly two things and discard the rest:

| Claim | Use |
|---|---|
| `sub` | the stable principal every row keys on |
| `email` + `email_verified` | seeding a grant for someone not yet signed in — nothing else |

No IDP-issued role, scope, org membership, or admin flag reaches an authorisation decision. Reading them would weld the beta to one issuer's schema; swapping issuers must be a config change. The library is a conformant OIDC client, not a vendor SDK whose value is the claims we just refused. Rejected: gating on an IDP `replichai:operate` scope or seed-org membership — it puts our access policy in another service's admin UI.

**The gate is a single structural check, fail-closed.** The `access_grants` table is the whole model: an empty table admits nobody. The operator routes (006a) are mounted behind one gate they cannot be served without — not a condition sprinkled through handlers, the same shape as 002's redistribution gate. `GET /me` sits *outside* it, so the frontend can render the not-invited screen; everything else answers `403` with a reason (`not_invited`, or `not_trusted` for a seed user reaching the visibility flip), read surfaces included, because 002 keeps repositories private.

**Grants seed by email, bind to a principal, one-way.**

- A row is `(email PK, principal, tier, granted_by, granted_at)`. An admin seeds by email someone who has never signed in.
- On that person's first login, the row binds to their `sub` — matched on a *verified* email, never a self-asserted one, and only until bound. **Binding is one-way:** once `principal` is set, email is never consulted for that row again, and a second principal presenting the same email is refused, not admitted onto someone else's grant.
- The table is an ACL, not a user table: principal, a seed email, a tier. No name, no avatar — display data comes from `/userinfo` per request and is never stored. Rejected: an env-var email list — every grant would cost a redeploy and leave no record of who admitted whom.

**Three tiers, and only one may publish publicly.**

| Tier | Set by | May |
|---|---|---|
| **Platform admin** | bootstrap admin-email list in the env, seeded at startup; admins promote others | grant, revoke, promote — and all below |
| **Trusted user** | a platform admin, explicitly | all below, **and** flip a reproduction public |
| **Seed user** | a platform admin | submit, watch, read results and transcripts, claim ownership if a forge account is linked |

There is no self-serve path up the tiers.

**The forge link is proven, stored against the principal, identity-scoped.** Linking a forge account is a separate OAuth handshake requesting **identity only** — `repo` scope is never requested, for anyone, ever. The proven login is written by the link callback alone, never a request body: one row per principal and forge, so a claim months later still knows where to send the invitation. This is what 006f's collaborator invitation consumes. `/me` reports *linked accounts*, and the routes are `/{forge}`-parameterised — GitHub is the only forge today, not the only shape.

**No development shortcut confers a grant.** A stub issuer for local work authenticates a synthetic principal but seeds no row, and a deployment reachable off loopback refuses to start with the dev bypass configured. The bypass is for local work, not a back door into the beta.

## Interface (sketch)

```go
type Tier int // SeedUser | TrustedUser | PlatformAdmin

type Principal struct { Sub, Email string; EmailVerified bool }

type Access interface {
    Resolve(ctx, Principal) (tier Tier, granted bool, err error) // binds on first login
    Grant(ctx, byAdmin Principal, email string, tier Tier) error
    Revoke(ctx, byAdmin Principal, email string) error
    LinkedAccount(ctx, principal, forge string) (login string, ok bool, err error)
}

// Gate is the middleware 006a mounts; RequireTier guards the visibility flip.
func Gate(a Access) func(http.Handler) http.Handler
```

## Acceptance criteria

1. Only `sub` and a verified email are consumed; a test drives login against a stub issuer whose token also carries roles/scopes/org claims and asserts none reach an authorisation decision.
2. With an empty `access_grants` table, an authenticated session reaches no operator route — the gate enumerates 006a's gated routes and each answers `not_invited`; a route added without the gate fails the test.
3. A grant seeded by verified email admits on first sign-in and binds to the principal; a second principal presenting that email afterwards is refused; revoking locks the holder out on the next request with no session left standing.
4. Tier is enforced server-side: a seed user's visibility flip is refused even when the request is forged past the UI; only a platform admin may grant, revoke, or promote.
5. The forge link requests identity scope only and stores the proven login against the principal; a client-supplied login is never trusted — asserted by driving `claim` for an unlinked user (`409`) and a linked one (invitation sent).
6. The dev bypass authenticates but never grants, and a non-loopback deployment refuses to start with it enabled.

## Out of scope

The routes themselves and their mounting (006a). The collaborator invitation and visibility flip that consume the link and tier (006f). Any use of the IDP beyond the two claims — its roles, orgs, billing are another product's model. Multi-tenant authorisation, org RBAC, and per-user credentials — deferred with the rest of multi-tenancy. The frontend's not-invited screen (006e); this spec makes the state, that spec renders it.
