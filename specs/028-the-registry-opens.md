# 028 — The registry opens

**Status:** designed, not built.

**Scope:** a reader with no account can browse the public zone and read everything a public project already permits — the registry list, the project, its sessions, and each session's transcript, verdict, and artifacts. Server: the gate moves off those reads, and a public reader's copy of each record drops the fields they have no stake in. Frontend: the app shell renders signed out rather than redirecting every address to the sign-in screen. No schema change, no new endpoint.

**Depends on:** [006f](006f-taking-a-reproduction-out.md) (the public zone and the sign-off that fills it), [006r](006r-the-public-profile.md) (the precedent — a researcher's page is already open), [013](013-the-address-bar-is-the-state.md) (every screen is an address somebody can arrive at cold), [017](017-the-first-screen.md) (which refused to link here *because* it was closed), [018](018-one-list-of-projects.md), [021](021-what-the-run-actually-found.md) (the session screens a public reader reads), [011](011-what-a-session-costs.md) (the money that must not travel with them).

## Problem

A reproduction is published so that somebody can check it. Today nobody can, unless they first get an account: `GET /public` and every read under a project are registered with `gatedHandle` (`internal/httpapi/handlers.go:98-118`), so the gate refuses the request before the handler is reached.

The refusal is not a policy the reads themselves hold. `mayViewProject` already returns true for a project whose visibility is public, with no identity check at all (`handlers.go:1841`), and `viewableSession` delegates to it (`:1862`) — so the authorization model has said "anyone may read this" since 006f, and the gate has been overruling it. 006r noticed the same thing one level down and opened `GET /profiles/{handle}`: a signed attestation names a person, and a name nobody can look up is not an attestation. The work that name is attached to has exactly the same problem.

The consequence is concrete. A published project's URL, pasted into a paper, a mail, or a review, lands a reader on a sign-in screen. 017 saw this coming and decided that the landing would link nowhere a signed-out visitor could go — which left the first screen with one action on it. Opening the registry is what unblocks that, and [029](029-the-landing-is-a-place.md) is what spends it.

## Decisions

**The gate moves off the reads a public project already permits, and off nothing else.** Nine routes change from `gatedHandle` to `openHandle`:

| Opens | Why |
|---|---|
| `GET /public` | the zone itself; lists public projects only |
| `GET /projects/{id}` | `mayViewProject` already admits anyone for a public project |
| `GET /projects/{id}/papers` | what the project is about |
| `GET /jobs/{id}` | scoped by `viewableSession` to the project's own rule |
| `GET /jobs/{id}/transcript` | the evidence half of the attestation (005) |
| `GET /jobs/{id}/reproduction` | what the authors released (021c) |
| `GET /jobs/{id}/verdict` | the verdict itself (021a) |
| `GET /jobs/{id}/artifacts` | the file list |
| `GET /jobs/{id}/artifact` | one file's contents |

These stay gated, each for its own reason:

- `GET /jobs` is caller-scoped by construction (`ListByPrincipal`, `handlers.go:1907`). Opening it would answer an anonymous caller with an empty list, which is not a read anybody wants and is one refactor away from being wrong.
- `GET /jobs/{id}/events` is the live SSE stream. A public project is one that finished and was signed off; a reader arriving at it needs terminal state, not a subscription. Open, it is an unbounded connection surface held for nobody's benefit.
- `GET /projects/{id}/members` names people who did not publish themselves.
- `GET /projects/{id}/archive` streams a whole repository. The published repository is the mount 006f already made, and the artifact list is open above; a public reader has the evidence without the platform serving a tarball to an unauthenticated caller.

**An anonymous caller must not resolve to somebody.** `principal(r)` is `""` when unauthenticated, and every read compares it against an owner and against membership rows. Two properties are asserted rather than assumed: a project whose `Owner` is empty is not readable by an anonymous caller, and `members.Role` with an empty sub *and* an empty email matches no invitation row. Both would pass unnoticed in dev and be a disclosure in prod.

**Redaction keys on stake, not on sign-in.** A public reader — signed out or signed in and browsing somebody else's work — sees the work, not the money and not the principals. The predicate is `mayWorkInProject`, the same one that already withholds `ProjectCredits` (`handlers.go:1790`), so there is one answer to "is this reader inside the project" and every field that depends on it reads from it.

| Field | Today | For a reader with no stake |
|---|---|---|
| `project.owner` | the raw OIDC sub | dropped |
| `spend_30d` | micro-USD spent | dropped |
| `job.principal` | the submitter's sub | dropped |
| `job.cost` | micro-USD | dropped |
| `credits` | already withheld | unchanged |
| sign-off block, `signer_handle`, `owner_name`/`owner_handle` | published on purpose (006n, 006r) | kept |
| phases, timings, verdict, transcript, artifacts | the reproduction | kept |

This changes what a *signed-in* stranger sees on the registry today — spend and an owner sub travel out of `projectViews` (`handlers.go:997-1008`) to anyone signed in. That is a leak being closed, not a feature being removed; no screen reads either field for somebody else's project.

The projection is one function applied where these three handlers write out, not a second set of DTOs: two structs that must agree about a reproduction is how they come to disagree.

**The shell renders signed out.** `App.tsx:189` sends every unauthenticated view to `SignIn`, and only `/u/{handle}` escapes it (`:182`). Instead, `Shell` tolerates `me.authenticated === false`: same frame, same rail, with the items that need an account — Projects, Trash, New, Wallet, Settings, Console — absent, and the header carrying a **Sign in** button pointing at `/login?next={current path}`. A signed-out reader browsing the registry is reading the app, not a preview of it.

The public views are `registry`, `project`, `session`, and `profile`. Every other view signed out is still the landing: there is nothing at `/new` or `/wallet` to show somebody without an account, and rendering an empty frame around that is worse than asking them in.

**A project a public reader may not see reads as not-found, and offers the way in.** `getProject` already answers 404 rather than 403 so a private project does not confirm it exists (`handlers.go:1881`). Signed out, that 404 renders as a screen saying this is not public, with a sign-in link carrying `next` — so the invitation-mail deep link that 017 criterion 5 protects still round-trips, and a stranger guessing ids learns nothing.

**The live rail stays behind sign-in.** With `/jobs/{id}/events` gated, a session screen opened by a public reader shows the recorded transcript and the settled verdict. A public project is finished; there is nothing live to watch.

## Acceptance criteria

1. Signed out, `GET /public`, `GET /projects/{id}`, `GET /projects/{id}/papers` and the six `GET /jobs/{id}...` reads answer 200 for a public project, and 404 for a private one — with no session cookie at all.
2. Signed out, `GET /jobs`, `GET /jobs/{id}/events`, `GET /projects/{id}/members` and `GET /projects/{id}/archive` are still refused by the gate.
3. An anonymous caller does not read a project whose owner is the empty string, and does not match a membership row through an empty sub or empty email.
4. For a reader who may not work in the project, the JSON of the registry list, the project, and each session carries no `owner`, `spend_30d`, `principal`, or `cost` — asserted on the serialised bytes, not on the struct. The owner and a member still see all four.
5. The sign-off block, signer handle, session phases, verdict, transcript, and artifact list are unchanged for a public reader.
6. `OpenRoutes` enumerates exactly the routes above; the router test asserts the set, so opening a tenth route is a decision somebody makes in this list rather than a line that slips in.
7. In Chrome, signed out: `/registry` renders the app shell with the public rail and a Sign in button; a public `/p/{id}` and its session tabs render; `/wallet` renders the landing; a private `/p/{id}` renders the not-public screen whose sign-in link carries `next=/p/{id}`.
8. Every string added is in `i18n.ts`.
