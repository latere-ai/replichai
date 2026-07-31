# 006n — Who signed off

**Status:** built. `internal/profile` owns the researcher profile (its own package, its own migration) behind `GET`/`PATCH /me/profile`; `project.SetPublic` takes a `SignOff` struct and writes the signer snapshot, with the signer's redacted email derived from the session rather than the body; the publish route validates the snapshot with the profile's own rules and requires a contact. The frontend has the settings form, the three-step publish wizard (prefilled from the profile), the signed-off card naming the signer, and a public zone that lists a project by its signer instead of an opaque OIDC subject. Attestation v2. Covered by the profile store contract (both implementations), the project store contract's snapshot round-trip, unit tests for redaction and validation, and handler tests for the profile API, the publish validation, the session-derived signer, the snapshot-not-a-view property, and a v1 sign-off reading back empty. Verified live against a running server (profile round-trip, `missing_contact`/`invalid_*`/`not_clear` ordering) and visually in a headless browser.
**Scope:** giving the public sign-off a *person* — a researcher profile a user maintains once in settings, a step-by-step publish wizard that confirms responsibility and collects the contact the public may write to, and a signed-off card that names who stands behind the reproduction. A child of [006f](006f-taking-a-reproduction-out.md).
**Depends on:** [006f](006f-taking-a-reproduction-out.md) (the one-way public flip and its recorded attestation), [006g](006g-projects-and-replication-sessions.md) (the project that owns visibility), [006c](006c-identity-and-access.md) (the principal a profile hangs off).

## Problem

006f made publishing a responsibility act: the owner signs an attestation, and the deciding principal, the attestation version, and the time are written to Postgres. That record exists — and nobody can read it.

The published project shows *“Public — signed off 7/30/2026.”* A date. Not a name, not an institution, and no way to ask a question about the result. The public zone is no better: it renders `Published by: {p.owner}`, and `owner` is the OIDC subject — an opaque identifier a stranger cannot resolve to a person.

This is not a missing UI field. `principal()` returns `id.Sub`, and the access model deliberately stores no display data (“read from the IDP per request, never stored here”) — which means a per-request IdP read only ever describes *the person making the request*. Viewing someone else's public project, there is nothing on the server that can say who they are. An attestation nobody can attribute endorses nothing.

The publish control is also thinner than the act it performs: one checkbox in a sidebar card, for a decision that is one-way, opens a transcript and its artifacts to everyone, and puts the signer's name behind a scientific claim.

## Decisions

**A researcher profile, self-asserted, in its own package.** `internal/profile` owns one row per principal: display name, institution, public contact email, ORCID, and a Google Scholar profile. It is deliberately *not* in `internal/access` — a grant is an authorisation record and its “no display data” rule stays true. Nothing here is authenticated: the platform does not verify that someone works where they say they work, exactly as a preprint server does not. What the platform *does* guarantee is the binding between the claim and the principal who made it.

```
GET   /me/profile → { display_name, institution, contact_email, orcid, scholar_url }
PATCH /me/profile { … }  → the saved profile
```

Both are gated (an invited user's own record) and neither takes a principal in the body — the profile written is always the caller's.

**The profile is a default; the sign-off is a snapshot.** This is the load-bearing decision. The wizard *prefills* from the profile, but what the owner confirms is copied onto the project row at sign-off and never read from the profile again. Changing your institution next year does not silently rewrite what an already-published attestation claimed — the record keeps saying what you signed. This is the same rule the manifest already follows: a verdict is an artifact, not a view.

New columns on `projects`, all written by `SetPublic`, all empty for the v1 sign-offs that predate this spec:

| column | source |
|---|---|
| `signed_off_name`, `signed_off_institution`, `signed_off_orcid`, `signed_off_scholar` | the wizard, prefilled from the profile |
| `signed_off_contact` | the wizard — the address published for inquiries |
| `signed_off_email` | **derived server-side**, never from the body: the session's verified email, stored already redacted |

`SetPublic` takes a `SignOff` struct rather than growing to seven positional arguments.

**Two email fields, because they answer different questions.** *Who signed* and *where to write* are not the same address. The contact email is deliberately public — it is the point of “public inquiries”, and it is rendered in full as a `mailto:`. The signer's login email identifies the person and is **stored redacted** (`h***n@changkun.de`): redacting at render would still put the full address on the wire, where any signed-in viewer could read it. The redaction happens once, server-side, at sign-off, against the email the identity provider verified — so the identity on a published attestation cannot be typed in by hand.

**Contact is required; institution is optional.** Publishing a result with no way to reach anyone about it defeats the purpose of publishing it. An institution is not required — an independent researcher is still a researcher — but when given it is what makes the sign-off read as an endorsement rather than a username. ORCID and Scholar are optional and format-checked (`0000-0002-1825-0097`; a `scholar.google.*` URL), because a malformed identifier on a public record is worse than an absent one.

**Attestation v2.** The affirmation text changes (it now covers the contact being published), so the version does. The wizard's *affirmation* step and its *details* step stay separate on purpose: the attestation stays a clean versioned artifact, and the identity data is a payload beside it, not mixed into the text whose version is being recorded.

**Publishing is a wizard, not a checkbox.** Three steps in a modal, each with its own commitment:

1. **What becomes public** — the sessions, the full transcript, the artifacts; and that it is one-way.
2. **Who stands behind it** — name, institution, contact (required), ORCID, Scholar; prefilled from the profile, with a “save these to my profile” option so the next publish is one click.
3. **Responsibility** — the v2 attestation text, an explicit affirmation, and the optional forge-repository flip. Only here does the button say *Sign off & make public*.

**The card, and the public zone, name the person.** The signed-off card renders the snapshot: name, institution, redacted signer, contact as a `mailto:`, ORCID and Scholar as links. The public zone's `Published by:` renders `signed_off_name` when it has one — replacing an opaque sub with a person. A v1 sign-off has no snapshot and falls back to today's date-only line, rendered cleanly rather than as a row of empty fields.

Rejected: rendering the signer's *current* profile on the public card (a settings edit would retroactively change a published claim). Rejected: collecting affiliation only at publish time with no profile (correct record, but the owner retypes their institution on every project). Rejected: verifying the institution — no email-domain challenge, no ORCID OAuth; this spec records a claim and binds it to a principal, and says so.

## Acceptance criteria

1. `GET /me/profile` returns the caller's profile (an empty one before it is ever saved, not a 404); `PATCH /me/profile` saves and returns it, always against the caller's principal and never one named in the body.
2. A malformed ORCID, Scholar URL, or contact email is refused (400) with a distinct code, on both the profile save and the publish call.
3. `POST /projects/{id}/public` refuses (400) a body with no contact email, and refuses an attestation version that is absent — the existing gate. Institution, ORCID, and Scholar absent are accepted.
4. A successful flip records the snapshot on the project: name, institution, contact, ORCID, Scholar from the body, and `signed_off_email` **redacted, derived from the session**, not from the body — a body that supplies its own `signed_off_email` cannot influence what is stored.
5. Changing the profile after publishing does not change what a published project reports.
6. `GET /projects/{id}` and the public list carry the snapshot fields; a project published before this spec (v1, no snapshot) returns them empty and renders as today's date-only line.
7. The publish wizard walks the three steps, prefills step 2 from the profile, blocks Continue without a contact, and only offers *Sign off & make public* after the affirmation is checked; the settings screen edits and saves the profile.
8. The signed-off card names the signer, their institution, the redacted signer email, and a `mailto:` contact, with ORCID and Scholar linked when present; the public zone lists a public project by its signer's name.
9. Every new handler and store path is covered against fakes; the project store contract covers the snapshot round-trip for both implementations; the redaction and validation helpers are unit-tested. English and Chinese strings land together.

## Out of scope

- **Verifying affiliation** — no institutional email challenge, no ORCID OAuth handshake. The claim is self-asserted and bound to a principal; a later spec can add proof.
- **Co-signers.** One project, one signing owner. Collaborators are listed as members, not as endorsers of the result.
- **Editing a published sign-off.** It is a record. A correction would be a new attestation, and there is no un-publish to attach one to (006f's one-way rule). *(Since [006o](006o-withdrawing-a-publication.md) there is: withdraw and publish again writes a fresh attestation. Editing one in place is still out of scope — a corrected record is a new sign-off, not an edited one.)*
- **Profile display anywhere else** — no author page, no per-user public index. The profile surfaces through a sign-off, nowhere else.
