# 006e — Frontend

**Superseded by [013](013-the-address-bar-is-the-state.md), [018](018-one-list-of-projects.md) and [034](034-the-console-rebuilt-from-the-design.md).** The embed, the fail-closed shell and the English-only i18n scaffold still describe the app. The screens do not: navigation is the address bar rather than one `useState`, the dashboard reads `GET /projects` rather than `GET /jobs`, a project holds many papers rather than one, and the console was rebuilt from a design. Submitting posts to `POST /projects/{id}/papers`, not `/papers`.

**Status:** implemented (submit, watch, inspect; the dropped-PDF path is [006d](006d-pdf-ingestion.md))
**Scope:** the researcher-facing single-page app — sign in, submit a paper, watch it run, and read the result — served from the Go binary. A child of [006](006-web-application.md).
**Depends on:** [006a](006a-http-api-and-streaming.md) (the API and SSE it consumes), [006c](006c-identity-and-access.md) (`/me`, the login redirect, the not-invited state).

## Problem

The engine and the API exist; a researcher has no way to reach them but `curl`. This spec is the browser surface that makes the app usable end to end: a page to sign in, drop in a paper URL, watch the run's phases stream by, and see the published reproductions — nothing more than a thin client over 006a.

## Decisions

**bun + React, embedded in the binary.** The SPA is built with bun (Vite + React + TypeScript) and embedded via `go:embed` into `internal/spa`, which serves it: real assets as themselves, `index.html` as the fallback for any unmatched path so a deep link loads the app and lets it route. There is no separate static host — one binary serves the API and the app. The built `dist/` is gitignored behind a committed `PLACEHOLDER`, so `go build`/`go test` work without a frontend build and the handler serves a "not built" notice; the Dockerfile builds the frontend with bun and the Go stage embeds it.

**The shell is public; the data is gated.** The bundle holds no secrets, so `internal/spa` is registered as an open `GET /` fallback. Every data call it makes goes through the gated API, so an ungranted or unauthenticated visitor gets the shell and then a not-invited or sign-in screen — never operator data. This is the same fail-closed posture the gate enforces, viewed from the client.

**`/me` drives the view.** On load the app fetches `/me` and renders one of three states: unauthenticated → a sign-in button linking to `/login`; authenticated but tier `none` → the not-invited screen (naming the email, telling them to ask an admin); granted → the operator dashboard. This is exactly the state 006c's `/me` was built to expose.

**"My projects" is server truth.** The dashboard lists the caller's own jobs from the gated `GET /jobs` (006b's `ListByPrincipal`), newest-first, streaming each still-running one's phase over SSE — one project per job for the beta. Each finished project shows its title, verdict, and forge link by joining to the published reproduction (003), which the same dashboard already fetches. This replaced an earlier `localStorage` list of submitted ids: ownership is durable and follows the principal across browsers, not a per-device cache.

**All copy is English and Chinese.** Every user-facing string lives in one `i18n` table with `en` and `zh`, a language toggle, and a missing-key fallback to English, so the two never drift and a half-translated string never renders blank.

## Acceptance criteria

1. The Go binary serves the app at `/` and falls back to `index.html` for deep links; a missing frontend build serves a notice rather than failing — asserted in `internal/spa`.
2. The app renders sign-in, not-invited, and operator states off `/me` alone, so an ungranted principal never sees operator data.
3. Submitting a URL posts to `/papers` and the run's phases stream live over SSE to `done`/`failed`; verified end to end through the compose stack.
4. Every string is available in `en` and `zh`, switchable at runtime.
5. The bundle is public (open `GET /`); all data calls are gated, so the fail-closed posture holds at the client.

## Out of scope

Dropping a PDF to submit ([006d](006d-pdf-ingestion.md)). The take-it-out actions — claim the repository, flip it public ([006f](006f-taking-a-reproduction-out.md)). An admin console beyond the `/admin/access` API. Per-run detail pages beyond the live phase and the forge link. A component library or design system: a small hand-written stylesheet is enough for a beta.
