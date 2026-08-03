# 006q — Sign-in is open, budget is not

**Status:** designed.
**Scope:** anyone may sign in and look around; nobody may spend without budget. The admission decision moves off the grant table and onto the quota, and the grant table becomes a registry of who has signed in rather than a list of who may. A rework of [006c](006c-identity-and-access.md)'s gate.
**Depends on:** [006c](006c-identity-and-access.md) (the fail-closed gate and the grant store), [006l](006l-the-session-quota.md) (the number that now does the limiting), [006k](006k-the-platform-admin-console.md) (where an admin sets it).

## Problem

006c fused two questions into one boolean. `Tier.Granted()` is `tier >= TierUser`, and `webauth.Gate` refuses everyone below it with `not_invited`. So *may this person sign in* and *may this person spend our money* have exactly one answer, decided by an admin, in advance, per person.

That was right for a closed beta with a shared API credential and no meter. It is wrong now, for two reasons. First, it makes every evaluation of the product a support ticket: someone who wants to see what a reproduction looks like must find an admin, wait, and come back. Second, 006l already built the thing that actually protects the credential — a session quota enforced inside `Enqueue`, in the inserting transaction. With a real meter in place, refusing sign-in is protecting a resource that is already protected, at the cost of the entire top of the funnel.

The invitation flow makes the contradiction visible. A project owner invites a collaborator, the collaborator receives mail ([006p](006p-the-invitation-that-arrives.md)), follows the link — and is told to ask an admin. The owner had the authority to share their work and no authority to let anyone see it.

## Decisions

**Signing in grants `TierUser`.** `HandleCallback` — the one moment a verified identity exists — seeds a grant for the session's email if none exists, at `TierUser`. The gate is untouched: it still resolves per request, still fails closed, still answers `not_authenticated` and `not_invited`, and `RequireTier(TierAdmin)` still guards every admin route. What changes is only that a first sign-in creates the row the gate then finds.

The alternative — letting `TierNone` past the gate and pushing tier checks down per route — is semantically purer (`TierNone` is already documented as "signed in, not granted") and much larger: every route currently behind one fail-closed choke point would need auditing for what an ungranted person may do, and a route added later would default open. One choke point that everyone passes is safer than thirty that each decide.

This is the same hook 006f's membership bind uses, and for the same reason: sign-in is the only moment that always happens.

**The default quota is zero, and nobody is grandfathered.** `platform.DefaultSessionQuota` becomes `0` — both the seeded value and the fail-closed fallback, which stay the same constant so they cannot drift. Existing grantees are not topped up: one rule for everyone, no invisible two-class distinction between accounts created before and after this spec. Anyone who needs budget gets it the same way from now on — an admin top-up, or a project owner's sponsorship.

This is a deliberate, visible cost: people who could run three sessions this morning cannot this afternoon. It is chosen over a backfill because a backfill leaves the platform permanently unable to answer "why does this account have 3?" without consulting a migration.

**What changes meaning, stated plainly.** The grant table stops being an admission list:

| before | after |
|---|---|
| a row means *an admin let this person in* | a row means *this person has signed in* |
| `Revoke` un-invites | `Revoke` bans — it locks out someone who is already here |
| `SEED_EMAILS` pre-admits colleagues | vestigial; a seeded row now only pre-sets a tier |
| the admin People list is the guest list | it is the user list, and it grows on its own |

`ADMIN_EMAILS` keeps working exactly as it does: seeding `TierAdmin` before first login, which the self-grant must never downgrade. `SEED_EMAILS` stays supported and is documented as no longer necessary.

**Zero budget is a state with copy, not an error code.** A new arrival's first submit would otherwise be a bare `402 quota_exceeded` — technically correct and hostile. The frontend distinguishes *no budget yet* (`limit === 0`, nothing used) from *budget spent* (`used >= limit > 0`) and says something different for each: the first explains how to get budget (ask an admin; or work in a project whose owner sponsors sessions), the second says what was used. `/me` already returns `used`, `limit`, and `unlimited`, so no new endpoint is needed.

Everything a person with no budget *can* do stays available: sign in, create a project, be invited into one, read a public project, read a project shared with them, edit their profile. The wall is at spending, and only there.

**Revocation must actually lock out, and that is where the implementation starts.** `access.Access` documents `Revoke` as *"removes a grant by email"*, idempotent — a delete. With sign-in self-granting, a deleted row is re-created by the revoked person's next sign-in, turning a ban into a logout. So revocation stops being a delete and becomes a recorded state, and this is a migration and a contract change before it is a feature: begin here, not with the self-grant.

| what changes | to what |
|---|---|
| `access_grants` | gains `revoked_at` (`NULL` while never revoked) and `revoked_by` |
| `Revoke` | sets those columns and drops the tier to `TierNone`, keeping the row; still idempotent, and its doc comment says so |
| `Resolve` | answers not-granted for a revoked row, exactly as it does for a missing one |
| the self-grant | refuses an email whose row is revoked — the whole point |
| `Grant` | on a revoked row clears the revocation; an admin re-admitting someone is a first-class act, not a reason to hand-edit a column |
| the admin People list | shows a revoked person as revoked rather than dropping them, so a ban is visible and reversible |

The alternative — a separate deny list — keeps `Revoke` a delete at the cost of two tables answering one question about one email, which is the shape 006c rejected for grants in the first place.

## Acceptance criteria

1. A principal with no grant who completes the OIDC callback holds `TierUser` afterwards, and reaches the operator surface on the next request.
2. A principal seeded `TierAdmin` (via `ADMIN_EMAILS`) who signs in stays `TierAdmin` — the self-grant never downgrades an existing grant.
3. `Revoke` records `revoked_at`/`revoked_by` and keeps the row; `Resolve` answers not-granted for it; a revoked email that signs in again is *not* re-granted and the gate still answers `not_invited`; `Grant` on that row re-admits and clears the revocation. Covered by the access store contract for both implementations.
4. `platform.DefaultSessionQuota` is `0`; a brand-new principal's `/me` reports `limit: 0`, `used: 0`, and `POST` of a session is refused `402 quota_exceeded` — from inside `Enqueue`, in the inserting transaction, unchanged from 006l.
5. An admin top-up of *n* lets that person run exactly *n* sessions; sponsorship by a project owner still charges the owner (006l), unchanged.
6. A person with zero budget can sign in, create a project, accept being invited to one, read what they may read, and edit their profile — asserted per route, so a future route cannot quietly join the wrong side of the line.
7. The frontend renders the no-budget-yet state distinctly from the budget-spent state, and the "not invited" screen is reachable only for a revoked or admin-refused account.
8. `deploy/README.md` and `example.env` describe `SEED_EMAILS` as optional, the admin console's copy describes the People list as who has signed in, and the project invite hint no longer tells the owner their invitee needs a platform invitation ([006p](006p-the-invitation-that-arrives.md) AC9).
9. The dev bypass is unchanged: it authenticates and does not grant, so a local run still needs `ADMIN_EMAILS` to operate.

## Out of scope

- **USD credits and payment.** The user's stated direction is a credit balance in currency rather than a session count, so that billing integration has somewhere to land. That is a larger model change — pricing per session, refunds, top-up purchase, invoicing — and is recorded in [`BACKLOG.md`](BACKLOG.md). This spec keeps the session counter and only changes its default.
- **Self-serve budget.** No trial grant, no free first session, no credit card. An admin or a project owner is how budget arrives.
- **Rate limiting sign-ups.** Sign-in is bounded by the identity provider's own account creation, which is where abuse should be answered.
- **Deleting the tier model.** Two tiers stay. `TierAdmin` is what the admin console checks.
