# 006r — The public profile

**Status:** built. Migration 2 adds `handle` (NULL when unset, so the unique index constrains only the people who chose one) and `public` (default false) to `profiles`; the store gains `ByHandle` and `PrincipalHandle`, with `ErrHandleTaken` raised from the unique index rather than a read-then-write, and one `ErrNotFound` for an unknown handle, a private profile, and a profile with no handle alike. `GET /profiles/{handle}` is open and lists the signer's public projects by filtering the public zone, so a withdrawal removes it from the page the moment it leaves the zone. `POST /projects/{id}/public` refuses `409 profile_not_public` before recording anything. The signer's name links to `/u/{handle}` through a `signer_handle` view field resolved per read and never snapshotted. `/u/{handle}` renders before every auth gate. `PATCH /me/profile` became a real patch on the way — it replaced, which would have let a settings save silently take somebody's page down. Covered by the profile store contract (both implementations, the Postgres half against a real database), six handler tests, and two frontend tests for the path parsing.
**Scope:** a researcher gets a page. A handle they choose, a profile they decide to make public, and the published work that names them — linked from every sign-off they signed. A child of [006n](006n-who-signed-off.md).
**Depends on:** [006n](006n-who-signed-off.md) (the profile and the signer snapshot), [006f](006f-taking-a-reproduction-out.md) (the public zone the page draws from), [006q](006q-sign-in-is-open-budget-is-not.md) (everyone signed in has a profile to fill in).

## Problem

006n gave a published reproduction a person: a name, an institution, a contact, an ORCID. It is a snapshot, deliberately, and it is a dead end. A reader who finds a reproduction persuasive and wants to know who ran it can read five fields and go no further — there is no page for the person, and no way to see the other three reproductions they signed.

That is the wrong shape for a platform whose output is an attestation. An attestation is only as good as the standing of the person making it, and standing accrues across work. Today it evaporates at the edge of each project.

The obstacle is structural: `profile.Profile` is keyed by `Principal`, an opaque OIDC subject. It cannot go in a URL — it is ugly, it is the identity provider's internal identifier, and putting it on a public page leaks it. There is no public name for a person on this platform.

## Decisions

**A handle, chosen by its owner.** `profiles` gains `handle`: unique, lowercase, `[a-z0-9-]{3,39}`, chosen in settings, editable. The page is `/u/{handle}`. Rejected: a slug generated from the display name (renaming changes it, collisions produce `-2` suffixes that read badly under a scholarly attestation) and an opaque public id (unique for free, unreadable and uncitable — which defeats the purpose). A handle is the one identifier a person can put in a CV.

A taken handle is a `409` with its own error code, surfaced next to the field like every other profile validation error (006n's pattern). Handles are reserved against a small deny list (`admin`, `api`, `public`, `settings`, `projects`, `u`, `me`, `login`, `logout`, `callback`) so a profile can never shadow a route.

**Public is a decision, and it is off.** `profiles` gains `public bool`, default false. A private profile is what exists today: readable by its owner, snapshotted at sign-off, invisible otherwise. Making it public publishes the same fields the sign-off already publishes — display name, institution, contact, ORCID, Scholar — plus the list of public projects the person signed. Nothing new about them becomes visible; what becomes visible is the *aggregation*, which is the point.

**A public profile is required to publish a project.** The public flip refuses (`409 profile_not_public`) when the signer's profile is not public with a handle set. Publishing is asserting a result under your name; the platform's position is that the name must resolve to something. The publish wizard makes this a step rather than a surprise — it already prefills from the profile, and it now checks and offers to flip it in place.

**The snapshot stays authoritative; the link is a convenience.** 006n's invariant is not negotiable: what a published sign-off reports is what was copied onto the project when it was signed, and editing a profile never rewrites a published attestation. So the signed-off card keeps rendering the snapshot exactly as it does — name, institution, "signed in as", contact, ORCID, Scholar — and the *name* additionally becomes a link to `/u/{handle}` when the signer has a public profile, and plain text when they do not.

This means a link that can come and go, and that is correct. A person who makes their profile private after publishing keeps their published attestation intact and loses the link; nothing on the record changes, because the record was never the profile. Rejected: locking the profile public while any project is public (a cross-package precondition, and a refusal the user must decode as "withdraw the project first"), and serving a snapshot-derived profile page (a historical record and a live profile are two different pages, and they would share a URL).

`signed_off_handle` is therefore *not* snapshotted. The link resolves through the current profile of the principal who signed, which is what makes it a live link rather than a second, staler copy of the same person.

**The page is public, unauthenticated, and thin.** `GET /u/{handle}` serves the SPA; `GET /api/profiles/{handle}` returns the public fields plus the public projects that principal signed, and `404`s for a handle that is unknown or whose profile is private — no distinction between the two, so the endpoint cannot be used to enumerate handles. The contact is rendered in full, exactly as the sign-off card renders it: it was published for inquiries. The account email never appears — that is what the redacted `signed_off_email` is for, and it stays on the attestation where it identifies a signature.

Reading a public profile requires no sign-in, matching the public zone: a reproduction anyone can read, signed by someone anyone can look up.

## Acceptance criteria

1. `PATCH /me/profile` accepts `handle` and `public`; the handle is validated for shape, uniqueness (`409 handle_taken`), and the reserved list; changing a handle frees the old one.
2. `GET /api/profiles/{handle}` returns the public fields and the signer's public projects for a public profile, and `404` for an unknown handle, a private profile, and a profile with no handle — indistinguishably.
3. The projects listed are exactly those that are `visibility: public` and were signed by that principal; a withdrawn project leaves the list.
4. `POST /projects/{id}/public` refuses `409 profile_not_public` when the owner's profile is not public or has no handle, before any sign-off is recorded.
5. The publish wizard shows the requirement and can set the handle and flip the profile public in place, then continue — never dropping the user into settings and back.
6. The signed-off card renders the snapshot unchanged, and links the signer's name to `/u/{handle}` only when that principal's profile is currently public with a handle. Making a profile private afterwards removes the link and changes nothing else on the card — asserted directly.
7. The public zone lists a project by its snapshot's `signed_off_name` as it does today, linking to the profile under the same condition.
8. `/u/{handle}` renders for a signed-out visitor.
9. The profile store contract covers handle uniqueness, freeing, and the public flag for both implementations; the migration adds `handle` (unique, nullable) and `public` (default false).

## Out of scope

- **Verification.** Nothing here is checked, exactly as 006n states: the platform does not confirm that someone works where they say they work. An ORCID is a link, not a proof. Verified affiliation is a real feature and a different one.
- **Following, activity feeds, profile-to-profile anything.** This is a page, not a network.
- **Avatars and free-text bios.** The IdP's picture already renders in the app shell (006m); a public avatar and a bio are more surface to moderate for little gain at this stage.
- **Reserving a handle before it is used**, or handle transfer between accounts.
- **Institutional pages.** An institution is a string on a profile, not an entity.
