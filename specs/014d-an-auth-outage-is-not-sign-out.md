---
title: "An auth outage is not sign-out"
status: built
---

# 014d — An auth outage is not sign-out

**Status:** built.

**Scope:** distinguish failure to load `/me` from a successful unauthenticated response at app startup. This is the authentication boundary only; the broader read-surface empty/error states remain separate follow-up work.

**Depends on:** [006c](006c-identity-and-access.md), [006e](006e-frontend.md), [013](013-the-address-bar-is-the-state.md).

## Problem

The app maps every `/me` rejection (network failure, 5xx, invalid JSON) to `{authenticated:false}`. During an outage it therefore replaces the application with a Sign in button. Following it restarts an authentication flow that cannot repair the unavailable service and can trap a signed-in person in a loop.

## Decisions

**Authentication has three load states.** Startup is loading, a successful `/me` response is authoritative, and a rejection is an unavailable state with retry. Only a successful `{authenticated:false}` renders Sign in.

**Retry stays on the current address.** Retrying re-reads `/me` without navigation, preserving the deep link in the address bar. Background wallet refresh retains the last known identity if it fails; it does not evict an active session from the UI.

## Acceptance criteria

1. `/me` 5xx, network failure, or invalid JSON renders an unavailable message and Retry, never Sign in.
2. Retry on the same URL recovers the app when `/me` succeeds.
3. A successful `{authenticated:false}` still renders Sign in with the current path in `next`.

## Tests

- Browser test starts with `/me` returning 503, asserts no login link, restores the endpoint, clicks Retry, and sees the requested app screen without navigation.
- Existing auth and routing tests continue to cover successful authenticated and unauthenticated responses.

## Outcome

Startup now has explicit loading, unavailable, and authoritative `/me` states. An outage renders service copy and retries in place without a login link or URL change; successful unauthenticated responses remain the only path to Sign in. The browser regression exercises 503 → Retry → recovered deep-linked project end to end.
