---
title: "Sign-in is open, budget is not"
status: built
---

# 006q — Sign-in is open, budget is not

**Status:** built. Migration 3 adds `revoked_at`/`revoked_by` to `access_grants`; `Revoke` records instead of deleting (idempotent, and a second revocation does not rewrite the first), `Resolve` answers not-granted for a revoked row exactly as for a missing one, `Grant` clears the revocation, and `List` carries it so the console can show a ban and undo it. `Access.SelfGrant` admits a principal at `TierUser` on the OIDC callback — never promoting, never demoting, never re-admitting a revoked address — beside the 006f membership bind, and a failure costs the admission rather than the sign-in. A new arrival started with nothing to spend, and still does. Covered by a grant contract both stores answer to (in-memory and Postgres, the latter run against a real database), four callback tests (a stranger is admitted, a seeded admin is not demoted, a ban survives a re-login, a broken store still signs you in), and a per-route assertion of what an unfunded account still permits. The admin console, invite hint and revoked-access screen say what is now true.

**Half superseded by [011](011-what-a-session-costs.md).** The open sign-in stands entirely — it is the whole point of this spec and 011 does not touch it. What 011 replaces is the *thing* that limits: a session counter becomes a credit balance, so `DefaultSessionQuota` and the zero it was set to are gone, along with the migration that lowered it and the tests that pinned it. The sentences below that name the counter are read as history; the sentences about who may sign in are read as current. Where the two are entangled, this file says which is which inline.
**Scope:** anyone may sign in and look around; nobody may spend without budget. The admission decision moves off the grant table and onto the quota, and the grant table becomes a registry of who has signed in rather than a list of who may. A rework of [006c](006c-identity-and-access.md)'s gate.
**Depends on:** [006c](006c-identity-and-access.md) (the fail-closed gate and the grant store), [006l](006l-the-session-quota.md) (the number that did the limiting, itself superseded by [011](011-what-a-session-costs.md)), [006k](006k-the-platform-admin-console.md) (where an admin sets it).

## Problem

006c fused two questions into one boolean. `Tier.Granted()` is `tier >= TierUser`, and `webauth.Gate` refuses everyone below it with `not_invited`. So *may this person sign in* and *may this person spend our money* have exactly one answer, decided by an admin, in advance, per person.

That was right for a closed beta with a shared API credential and no meter. It is wrong now, for two reasons. First, it makes every evaluation of the product a support ticket: someone who wants to see what a reproduction looks like must find an admin, wait, and come back. Second, 006l already built the thing that actually protects the credential — a session quota enforced inside `Enqueue`, in the inserting transaction. With a real meter in place, refusing sign-in is protecting a resource that is already protected, at the cost of the entire top of the funnel.

The invitation flow makes the contradiction visible. A project owner invites a collaborator, the collaborator receives mail ([006p](006p-the-invitation-that-arrives.md)), follows the link — and is told to ask an admin. The owner had the authority to share their work and no authority to let anyone see it.

## Decisions

**Signing in grants `TierUser`.** `HandleCallback` — the one moment a verified identity exists — seeds a grant for the session's email if none exists, at `TierUser`. The gate is untouched: it still resolves per request, still fails closed, still answers `not_authenticated` and `not_granted` (renamed below), and `RequireTier(TierAdmin)` still guards every admin route. What changes is only that a first sign-in creates the row the gate then finds.

The alternative — letting `TierNone` past the gate and pushing tier checks down per route — is semantically purer (`TierNone` is already documented as "signed in, not granted") and much larger: every route currently behind one fail-closed choke point would need auditing for what an ungranted person may do, and a route added later would default open. One choke point that everyone passes is safer than thirty that each decide.

This is the same hook 006f's membership bind uses, and for the same reason: sign-in is the only moment that always happens.

**The default quota is zero, and nobody is grandfathered — which takes a migration, not a constant.** *(The counter is gone as of [011](011-what-a-session-costs.md); the argument below is why it went to zero, and 011 inherits its conclusion — a new arrival still starts with nothing to spend, now in credits.)* `platform.DefaultSessionQuota` becomes `0`, both the seeded value and the fail-closed fallback, which stay the same constant so they cannot drift.

Changing the constant is not enough, and this is the part that is easy to get wrong. Migration 1 interpolated that constant **at migration time**, so a deployment that already ran it holds whatever the value was then, and `Settings()` returns the stored row rather than the fallback. Without a second migration, "nobody is grandfathered" would be true of a fresh database and false of the only one that matters. Migration 2 therefore rewrites the row, unconditionally and exactly once. Sparing a row an admin had already changed was the first instinct and is the wrong one: it would make "everyone starts at zero" true of some deployments and false of others, answerable only by querying each database. One rule for everyone means the cutover applies to everyone. An admin who wants a different number sets it afterwards and it sticks — the migration is version-gated and never runs again.

Existing grantees are not topped up: one rule for everyone, no invisible two-class distinction between accounts created before and after this spec. Anyone who needs budget gets it the same way from now on — an admin top-up, or credits somebody else allocated to a project they work in. (011 applied this rule to itself: the counter was dropped rather than converted, because a count and a balance are different quantities and a carried-over number would be a balance nobody could explain.)

This is a deliberate, visible cost: people who could run three sessions this morning cannot this afternoon. It is chosen over a backfill because a backfill leaves the platform permanently unable to answer "why does this account have 3?" without consulting a migration.

**What changes meaning, stated plainly.** The grant table stops being an admission list:

| before | after |
|---|---|
| a row means *an admin let this person in* | a row means *this person has signed in* |
| `Revoke` un-invites | `Revoke` bans — it locks out someone who is already here |
| `SEED_EMAILS` pre-admits colleagues | **removed** — it granted what signing in now grants |
| the admin People list is the guest list | it is the user list, and it grows on its own |

`ADMIN_EMAILS` keeps working exactly as it does: it is how a platform admin exists before there is anyone to promote them, seeding `TierAdmin` before their first login, which the self-grant must never downgrade.

`SEED_EMAILS` is deleted rather than deprecated — from `main.go`, the manifest, and the docs. It pre-granted `TierUser` to an address that had not signed in, which is precisely what signing in now does, so keeping it would leave a knob that does nothing and a sentence explaining why it does nothing. The standing rule on this codebase is clean removal, not tombstones.

For the same reason the gate's refusal reason becomes `not_granted`. `not_invited` named a state that no longer exists — nobody is waiting for an invitation — and it is a wire code with one client.

**Zero budget is a state with copy, not an error code.** A new arrival's first submit would otherwise be a bare `402` — technically correct and hostile. The frontend distinguishes *nothing yet* from *this ran out* and says something different for each: the first explains how to get some, the second says what happened. The distinction outlived the counter; 011 keeps it and only changes the two states it draws (an empty wallet, versus a project that has spent down).

Everything a person with no budget *can* do stays available: sign in, be invited into a project, read a public project, read a project shared with them, rename and manage a project they own, invite collaborators to it, and edit their profile. The wall is at spending, and only there.

**Creating a project is on the spending side, and that is not a bug.** An earlier draft of this spec listed "create a project" among what a broke account can still do. It cannot, and should not: since 006j a project is opened *by submitting a paper*, and every path that creates one enqueues a session in the same request. There is no empty project to make. So `POST /projects` with no budget is refused, the half-made project is undone by the compensating delete that already existed, and the client is told enough to say *nothing to spend yet* rather than *could not start*. (011 narrows the refusal's shape — an unfunded wallet fails before any paper is enqueued, so it is a top-level `402` rather than a per-paper report — and says so there.) Introducing an empty project to make this sentence true would undo a deliberate 006j decision for the sake of a nicer-sounding acceptance criterion.

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
3. `Revoke` records `revoked_at`/`revoked_by` and keeps the row; `Resolve` answers not-granted for it; a revoked email that signs in again is *not* re-granted and the gate still answers `not_granted`; `Grant` on that row re-admits and clears the revocation. Covered by the access store contract for both implementations.
4. A brand-new principal arrives with nothing to spend, and a `POST` of a session is refused `402` — from inside `Enqueue`, in the inserting transaction. *(Superseded in its specifics by [011](011-what-a-session-costs.md): the constant, the migration that lowered it and the tests that rewound a database to assert the cutover are all gone with the counter. What survives is the rule — a new account starts at nothing — and 011 asserts it in credits.)*
5. An admin's grant lets that person run exactly what it pays for. *(011: the grant is credits, they are allocated to a project, and the project is what a session charges.)*
6. A person with zero budget can sign in, read what they may read, edit their profile, and rename, invite to, and read the members of a project they own; being invited into somebody else's project makes it visible to them. `POST /projects` and `POST /papers` are refused `402`/`422` — creating a project starts a session, so it is spending. Asserted per route, so a future route cannot quietly join the wrong side of the line.
7. The frontend renders the no-budget-yet state distinctly from the budget-spent state. The no-access screen does **not** claim a revocation: a session minted before this spec, an address the identity provider did not return, and an address already bound to another principal all reach it too, and naming a ban would accuse somebody of something that did not happen. It says what is certain, and offers signing out — the remedy for every cause but an actual revocation.
8. `SEED_EMAILS` is gone from `main.go`, `deploy/base/deployment.yaml`, and the docs; the gate answers `not_granted`; the admin console's copy describes the People list as who has signed in; and the project invite hint no longer tells the owner their invitee needs a platform invitation ([006p](006p-the-invitation-that-arrives.md) AC9).
9. The dev bypass is unchanged: it authenticates and does not grant, so a local run still needs `ADMIN_EMAILS` to operate.

## Out of scope

- **USD credits and payment.** The user's stated direction is a credit balance in currency rather than a session count, so that billing integration has somewhere to land. That is a larger model change — pricing per session, refunds, top-up purchase, invoicing — and is recorded in [`BACKLOG.md`](BACKLOG.md). This spec keeps the session counter and only changes its default.
- **Self-serve budget.** No trial grant, no free first session, no credit card. An admin or a project owner is how budget arrives.
- **Rate limiting sign-ups.** Sign-in is bounded by the identity provider's own account creation, which is where abuse should be answered.
- **Deleting the tier model.** Two tiers stay. `TierAdmin` is what the admin console checks.
