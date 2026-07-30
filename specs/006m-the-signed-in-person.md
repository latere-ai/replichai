# 006m — The signed-in person

**Status:** built. `HTTPUserInfo` decodes `name`/`preferred_username`/`picture` beside the email; the callback seals them into the session and `safePicture` drops anything but an absolute `https` URL; `/me` reports them (omitted when absent, present for an ungranted principal); the rail footer and Settings render an `Avatar` that falls back to initials on an absent, non-https, or failed-to-load picture. 006c's text is amended to match. Covered by `internal/webauth/profile_test.go` and `frontend/src/profile.test.ts`.
**Scope:** showing a researcher their own name and avatar instead of an email and two letters. It amends one sentence of [006c](006c-identity-and-access.md) and touches four files. A child of [006e](006e-frontend.md).
**Depends on:** [006c](006c-identity-and-access.md) (the OIDC callback, the sealed session cookie, `/me`).

## Problem

The rail's footer and the Settings screen identify a person by raw email and `initials(me.email)`. The identity provider has a name and a picture; nothing in the app has ever asked for them.

006c says display data "comes from `/userinfo` per request and is never stored" — and that is **not implementable as written**. `HandleCallback` discards the access token once the email is fetched (`internal/webauth/login.go:120-134`), so there is nothing left to re-fetch with, and `HTTPUserInfo` decodes exactly one claim and drops the rest (`internal/webauth/userinfo.go:62`). The intent behind that sentence is sound; its mechanism does not exist.

## Decisions

**Fetch the display claims once, at the callback, and carry them in the sealed session cookie.** The cookie is client-held, AES-256-GCM sealed, and expires with the session; nothing lands in a server table. That preserves what 006c actually cares about — `access_grants` is an ACL and not a user table — while dropping the "per request" clause it cannot honour. **006c's text is amended in this change**, not left to contradict the code.

`UserInfoFunc` returns a small struct rather than a bare email:

```go
type Profile struct {
    Email   string // as today
    Name    string // "name", falling back to "preferred_username"
    Picture string // absolute URL, or empty
}
```

`Session` gains `Name` and `Picture`. This stays clear of the structural test that forbids caching authority in the session (`TestSessionCarriesNoTier`): a display name confers nothing, and the tier is still re-resolved per request.

**No display claim ever reaches an authorisation decision.** 006c AC1's rule is unchanged and worth restating, because this is the first time a claim beyond `sub`/`email` is kept at all: `name` and `picture` are rendered and nothing else. AC1's stub-issuer test keeps asserting that roles, scopes, and org claims change no outcome.

**A `picture` is a URL from the issuer's host, and is treated as untrusted.** It is rendered only when it parses as an `https` URL, with `referrerPolicy="no-referrer"` so browsing the app does not report page visits to the image host, and `onError` falling back to initials — an avatar that 404s must degrade to what is there today, not to a broken-image glyph. There is no CSP in this deployment today; if one is added, `img-src` must admit the issuer's host or this feature goes dark silently.

**Stale is acceptable and bounded.** A name changed at the IdP updates on next sign-in, within the 12-hour session TTL. Re-fetching per request would need a stored access token, which is a materially worse trade for a label.

**Email stays visible.** The name is added above it, not swapped for it: in a tool where an admin admits people *by email* and grants bind to email, the email is the identifier, and hiding it behind a display name would make the one string that matters unreadable.

## Acceptance criteria

1. The callback decodes `name`, `preferred_username`, and `picture` from `/userinfo`, preferring `name` over `preferred_username`, and seals them into the session; a userinfo response carrying none of them still signs in with email alone.
2. `GET /me` reports `name` and `picture` when present and omits them when absent, and reports them for an ungranted (`tier: none`) principal too, so the not-invited screen can greet a person.
3. `Session` still carries no tier, role, or grant — the existing structural test passes unchanged, and no display claim reaches an authorisation decision (006c AC1's stub-issuer test is unchanged).
4. The rail footer and the Settings screen render the avatar and name, and fall back to `initials(email)` when `picture` is absent, when it is not an `https` URL, or when the image fails to load.
5. 006c's "display data comes from `/userinfo` per request and is never stored" is amended in the same change to describe what the code does.

## Out of scope

- **Editing a profile in-app.** The IdP owns the name and the picture; this app renders them.
- **Caching or proxying the avatar** through the server. A hotlink with `no-referrer` is the whole feature.
- **Names for anyone but the signed-in person.** Members lists, grant tables, and collaborator invitations stay email-keyed — the app never fetches another user's profile, which would need a directory read 006c does not grant.
