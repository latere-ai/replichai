---
title: "The address bar is the state"
status: built
---

# 013 — The address bar is the state

**Status:** built. `frontend/src/routes.ts` holds the `View` union with `pathOf`/`viewOf` and the `useRouter` hook; `App.tsx` reads its view from the address instead of from `useState`. `projectFromPath`, `publicHandleFromPath` and the `replaceState('/')` that discarded the path after startup are all gone, and with them `deeplink.test.ts` and `profile-page.test.ts`, whose coverage moved into `routes.test.ts`. No server change.

**Verified in Chrome** (`frontend/test/routing.test.mjs`, driven over a mocked API): a click into a project puts `/p/{id}` in the address bar and a click into its session `/p/{id}/s/{jobId}`; the verdict tab reaches `/p/{id}/verdict` and survives a reload on that tab; a cold `/p/{id}/s/{jobId}` renders the transcript and fills its breadcrumb (project title and paper reference) from what the screen loaded; Back walks session → project → dashboard and Forward walks back down; the rail's destinations land on `/registry`, `/console`, `/settings` and `/new`; a mistyped tab keeps its project; a stray path is corrected to `/`. Every assertion was verified to fail with the change stashed.

Two refinements came out of the build, both in the spec below: a correction passes `replace` explicitly rather than relying on the path-changed rule, and `navigate` ignores an update that returns the view it was given, because otherwise a screen offering the same title on every render would write history on every render.

**Scope:** give every screen a URL. Navigation inside the app becomes a path the browser owns, so a reload stays where it was, Back and Forward work, and a screen can be linked to. One new module (`frontend/src/routes.ts`) holds the path↔state mapping; `App.tsx` reads its view from it instead of from `useState`. No server change: `internal/spa` already serves `index.html` for any unmatched path, which is the half of routing that was already in place.

**Depends on:** [006e](006e-frontend.md) (the screens themselves), [006p](006p-the-invitation-that-arrives.md) (`/p/{id}` is in mailed invitations), [006r](006r-the-public-profile.md) (`/u/{handle}` is a published address), [006c](006c-identity-and-access.md) (`/login?next=` carries where to come back to).

## Problem

`App.tsx` states it directly: *"Navigation inside the app is state, not a router."* One `useState<View>` in `Shell` decides what is on screen, and the address bar never moves. Three things follow.

**A reload starts over.** Fifteen minutes into reading a 771-event transcript, a refresh, a browser restart, or a crash lands on the dashboard. The session is still there; the way back to it is four clicks and remembering which project it was under.

**Nothing can be shared.** "Look at this verdict" is not a link. It is a screenshot, or an instruction to click through. For a product whose central claim is *published, inspectable evidence*, a reproduction that cannot be addressed is a gap: the published GitHub repository has a URL, and the session that produced it does not.

**Back leaves the app.** The browser's Back button has no in-app history to walk, so it exits to whatever was open before, which reads as the app crashing. There is no Forward either.

Two deep links already exist and prove the shape works: `/p/{id}` from an invitation mail and `/u/{handle}` for a researcher's page. Both are read once at startup and then discarded: `Shell` calls `history.replaceState(null, '', '/')` immediately, precisely because a path that outlives its consumption would take a reload back to a project the reader had left. That line is the current design acknowledging its cost.

## Decisions

**The path and the view are one bijection, not two parsers.** `routes.ts` exports the `View` union together with `pathOf(view)` and `viewOf(path)`. `View` is already a discriminated union carrying exactly the fields a path needs, so the mapping is total in both directions and the compiler catches a new screen that has no URL. Both functions are pure and are tested as a round trip, the way `projectFromPath` is tested today; they replace `projectFromPath` and `publicHandleFromPath`, which were the same job done twice for two paths.

**Push only when the path changes.** `navigate(view)` writes the path with `pushState` when `pathOf` differs from the current location, and `replaceState` when it does not. That default gets three behaviours right without a flag at any call site: opening a screen adds a history entry, a tab change adds one (its path differs), and filling in a title the app had not fetched yet adds none.

A correction (a tab the reader may not open, an address being fixed) passes `replace` explicitly, because a corrected address must not be a place Back can return to: it would be corrected again, and Back would never get past it. And `navigate` ignores an update that returns the view it was handed, so a screen that offers the same title on every render writes history on none of them.

**The API owns its roots, so two screens are renamed.** `/projects`, `/public` and `/admin` are API paths (the release smoke asserts that `GET /projects` answers with a JSON error), and Go's mux matches them before the SPA fallback, so a browser sent there gets JSON. The registry and the console take names of their own; nothing else moves.

| Path | Screen |
| --- | --- |
| `/` | the dashboard (projects) |
| `/registry` | the public registry |
| `/new` | new project |
| `/settings` | settings |
| `/console` | the admin console |
| `/p/{id}` | a project, on its sessions |
| `/p/{id}/{verdict\|artifacts\|people}` | a project, on that tab |
| `/p/{id}/s/{jobId}` | a session's transcript |
| `/u/{handle}` | a researcher's page |

`/p/{id}` and `/u/{handle}` keep their exact current meaning: both are already published addresses, one in mail that has been sent and one on pages that name a person. A test asserts no app path collides with a prefix the dev server proxies to the backend, which is the reverse of the check `api-proxy.test.ts` already makes in the other direction.

**A session's URL names its project.** `/p/{id}/s/{jobId}` rather than `/s/{jobId}`: the session screen's Back goes to the project, and a bare job id would make that a fetch before the breadcrumb could render. The project id is in the path because the breadcrumb needs it, not because the API does.

**A path carries identity; the screen fetches the rest.** `View` keeps its optional display hints (a project's title, a session's paper reference) and the path carries none of them: a title in a URL is a copy of a record that can change. On a cold load the screens already fetch what they render, so they report what they loaded upward and the hint is filled in silently, under the push-only-when-the-path-changes rule. The breadcrumb's existing `…` covers the moment before that arrives.

**An unknown path is not an error screen.** `viewOf` returns the dashboard for anything it does not recognise, and the app rewrites the address to `/` without a history entry. A 404 screen would be the right answer for a public site; here every path is either one of ours or a typo in a hand-edited URL, and the useful response is the app rather than a dead end. A path that names a project and then something unrecognisable (a mistyped tab, a session link cut short at `/p/{id}/s`) resolves to *that project* rather than to the dashboard: the reader addressed a real thing, and the unreadable part of the address is the only part worth discarding.

**A tab the reader may not open resolves to sessions.** `people` is the owner's tab. A non-owner following `/p/{id}/people` gets the sessions tab and the address corrected, rather than an empty panel, the same rule the screen already applies when it hides the tab.

**Sign-in returns to where you were.** `/login?next=` currently carries only a project deep link, because that was the only path that existed. It now carries whatever path the reader arrived on; `safeNext` already refuses anything that is not a local path, so this is a client change alone.

**`history.replaceState(null, '', '/')` is deleted**, along with the `openAtProject` prop it was there to clean up after. The path is now the state, so there is nothing to consume and discard.

## Out of scope

**The transcript's reading position.** Search text, kind filters, the scrubber and the selected event stay in component state. They are where a reader has got to, not a destination, and a query string rewritten on every keystroke fills the back stack with keystrokes. A link to *the transcript* is what this spec delivers; a link to a filtered reading of one is a separate question.

**Route-level code splitting.** The bundle is one chunk and stays one chunk here; splitting it is a load-time change that needs its own measurement.

**A router library.** The mapping is 100 lines of pure function over a union that already exists. Dissolving that union into route params would cost the exhaustiveness check that makes the mapping total.

## Acceptance criteria

1. Every screen has a URL, and opening it directly renders that screen: `/`, `/registry`, `/new`, `/settings`, `/console`, `/p/{id}`, `/p/{id}/verdict`, `/p/{id}/artifacts`, `/p/{id}/people`, `/p/{id}/s/{jobId}`, `/u/{handle}`.
2. Navigating inside the app changes the address bar, and reloading any of those addresses lands on the same screen with the same data.
3. Back and Forward walk in-app history: dashboard → project → session → Back shows the project, Forward the session again.
4. `pathOf` and `viewOf` round-trip every `View` variant, and `viewOf` maps an unrecognised path to the dashboard.
5. No app path is shadowed by an API prefix, asserted against `vite.config.ts` rather than by hand.
6. An unauthenticated visit to any app path signs in and comes back to that path.
7. A non-owner opening `/p/{id}/people` gets the sessions tab and a corrected address.
8. `/p/{id}` and `/u/{handle}` behave exactly as they do today, and no `replaceState` erases the path after startup.

## Tests

- `routes.test.ts` (bun): the round trip over every variant, unknown-path fallback, trailing slashes, handle normalisation (`/u/Ada` → `ada`, percent-decoded), tab parsing, and the no-collision invariant read out of `vite.config.ts`. Replaces `deeplink.test.ts` and `profile-page.test.ts`, whose two functions this spec removes.
- `routing.test.mjs` (Chrome, beside the tooltip and transcript browser tests): drive the real app over a mocked API: click into a project and a session and assert the address; reload and assert the screen survives; Back and Forward; an unknown path corrected to `/`; a project tab addressed directly.
