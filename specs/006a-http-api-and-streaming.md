# 006a — HTTP API and progress streaming

**Status:** draft
**Scope:** the HTTP server that wraps the audit engine — submit a paper, watch a job, read a result — and the seam the access gate mounts on. A child of [006](006-web-application.md).
**Depends on:** [006b](006b-job-model-and-persistence.md) (the job store and worker it enqueues onto). Consumes [003](003-reproduction-registry.md) and [005](spec 005) for its read surfaces.

## Problem

The engine already returns values and streams progress through an observer, "so an HTTP handler can wrap it unchanged." This spec is that handler and nothing more: it translates HTTP to the job model and back, and adds no engine logic. The two things that make it more than CRUD are that an audit runs for up to an hour — so submission cannot block — and that its progress must reach the browser as it happens.

## Decisions

**A submission returns a job, not a result.** `POST /papers` validates the reference (or accepts a multipart PDF, whose extraction is 006d's), enqueues a job through 006b, and returns `{ job_id }` immediately. No request handler ever waits on a run. Status is polled at `GET /jobs/{id}`; progress is streamed.

**Progress is Server-Sent Events.** `GET /jobs/{id}/events` is an SSE stream carrying the observer's `RunStatus` transitions and, once 005 lands, transcript events. One-way, so SSE rather than WebSocket: it proxies cleanly, reconnects with `Last-Event-ID`, and needs no upgrade. Each write is flushed; a slow reader is dropped, never allowed to stall the run, which streams from the job's state regardless of who is watching.

**Read surfaces present, never copy.** `GET /reproductions`, `/reproductions/{id}`, and `/reproductions/{id}/transcript` read straight through 003 (derived from the forge) and 005. No handler caches a reproduction into its own shape — the forge and the manifest stay the source of truth. Every one answers `403 not_invited` for an ungranted principal, because 002 keeps repositories private and a listing is not public.

**The gate is a seam this spec exposes, not a policy it owns.** The operator routes are mounted behind one middleware that 006c supplies; 006a decides *which* routes are operator routes and makes that set enumerable. A route added outside the gate is a bug 006c's test must be able to catch, so the router surfaces its gated set rather than hiding it in `mux` registration. The decision of who passes is 006c's; the structural guarantee that nothing bypasses the check is 006a's.

**The router is the source of truth for the route set.** `/me`, `/login`, `/callback`, `/logout`, and the link routes sit *outside* the gate (that is how the frontend learns to render the not-invited screen); everything else sits inside it. Both sets come from one registration so the two cannot drift.

## Interface (sketch)

```
Router exposes:
  Handler() http.Handler              // fully wired, gate mounted
  GatedRoutes() []Route               // method+path of every operator route, for 006c's test

open:     GET /login /callback /logout /me ; GET /links/{forge} /links/{forge}/callback
gated:    POST /papers ; GET /jobs/{id} ; GET /jobs/{id}/events (SSE) ;
          POST /jobs/{id}/claim ; GET /reproductions[/{id}[/transcript]]
trusted:  POST /reproductions/{id}/visibility
admin:    GET/POST/DELETE /admin/access
```

Handlers depend on interfaces (the job store, the registry reader, the gate), never concrete Postgres or GitHub — so every route tests against fakes, no database and no network.

## Acceptance criteria

1. `POST /papers` returns a job id without blocking on the run — a test asserts the handler returns while the fake worker is still "running."
2. The SSE endpoint flushes each event and survives a slow or vanishing client without stalling the job; reconnection with `Last-Event-ID` resumes.
3. Read surfaces return exactly what 003 and 005 hold, through their interfaces, with no separate persistence.
4. `GatedRoutes()` enumerates every operator route from the router itself; a route registered without going through the gated group does not appear as open — it appears in the gated set or the test that compares the two fails.
5. Every handler wraps `internal/audit` / the job store; none contains engine logic, asserted by the dependency graph (no import of `internal/method` or a provider adapter).

## Out of scope

The job store and worker themselves (006b). Who is allowed through the gate and how grants work (006c). PDF text extraction (006d). The browser UI (006e). The take-it-out actions behind `/claim` and `/visibility` (006f). Field-level request/response shapes beyond the route surface — those settle in implementation.
