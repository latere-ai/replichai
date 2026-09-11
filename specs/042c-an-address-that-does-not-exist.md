---
title: "An address that does not exist"
status: built
---

# 042c — An address that does not exist

**Status:** built. All eight criteria hold.

**Scope:** a route-shape list in Go, a `notfound` view in the client, and `noindex` on the screens only an operator can render.

**Depends on:** [042a](042a-the-head-the-server-writes.md), [013](013-the-address-bar-is-the-state.md).

The drift guard found the first disagreement immediately: `/p/{id}/s` is an
address the client does have. routes.ts resolves a truncated session link back
to its project rather than to the dashboard, so the server's shape list had to
admit it. That is exactly the class of divergence the test exists for, and it
surfaced before the code shipped rather than as a 404 on a truncated link
somebody pasted.

## Problem

```
$ curl -so /dev/null -w '%{http_code}\n' https://replichai.latere.ai/this-page-does-not-exist-xyz
200
```

`viewOf` resolves anything it does not recognise to the dashboard, `useRouter` rewrites the address to `/`, and the server answered 200 with the full shell before either ran. That is an unbounded set of URLs returning success with identical content, which a search engine calls a soft 404. It also misleads a person, who asked for a page and was silently given a different one.

Separately, the operator screens are indexable and empty: `/wallet` and `/console` render nothing for a signed-out reader, which is exactly what a crawler is.

## Decisions

**Go learns the route shapes.** A small table beside the SEO code: the fixed screens (`/`, `/registry`, `/trash`, `/new`, `/wallet`, `/settings`, `/settings/profile`, `/console`) and the four patterns already registered for injection. Anything else is not an address this app has.

**The duplication is deliberate and guarded.** `routes.ts` stays the client's single mapping; Go holds a second copy for the same reason `index.html` holds a second copy of the theme resolution: nothing else can run this early in the request. A Go test reads `frontend/src/routes.ts`, extracts its screen paths, and fails when the two disagree, so the copy cannot drift unnoticed.

**A miss is 404 with the shell.** The status is what a crawler needs; the body is the app, so a person who mistyped gets a page that can navigate. `X-Robots-Tag: noindex` goes with it.

**The client renders a real not-found screen.** `View` gains `{ name: 'notfound', path }`, carrying the path so `pathOf` stays total and the address is not corrected. A typo the app silently rewrites to `/` is how the soft 404 reached a person in the first place. The screen says what was not found and offers a way back.

**The operator screens are `noindex`, not `Disallow`-only.** robots.txt asks a crawler not to fetch them; the header governs one that already has the URL. Both are needed, because they answer different questions.

**A well-formed id that resolves to nothing stays 200.** `/p/{id}` for a private or unknown project is a route this app has, and its own member reaches it by signing in. 042a already gives it `noindex`, which is the part that matters here.

## Criteria

1. `GET /this-page-does-not-exist-xyz` returns 404, the app shell, and `noindex`.
2. Every path the client's `SCREENS` names returns 200.
3. `/p/{id}`, `/p/{id}/{tab}`, `/p/{id}/s/{job}`, `/p/{id}/s/{job}/{tab}` and `/u/{handle}` return 200 for any well-formed id.
4. A Go test derives the fixed screen paths from `frontend/src/routes.ts` and fails if the Go table omits or invents one.
5. `/wallet`, `/settings`, `/settings/profile`, `/console`, `/new` and `/trash` carry `X-Robots-Tag: noindex`.
6. Static assets are unaffected: a real file under `assets/` or `fonts/` is still served with its long cache lifetime.
7. `viewOf` returns `notfound` for an unrecognised path, `pathOf` round-trips it, and the address bar keeps what was typed.
8. The app renders a not-found screen with a way back; `frontend/test/routing.test.mjs` asserts it in a real browser.

## Not in scope

A 410 for a withdrawn publication (006o's retraction is recorded, and the page still reads as not public), and any redirect rules.
