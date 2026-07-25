# 006e — Frontend

**Status:** draft
**Scope:** the browser application — submit a paper, watch it run, inspect the result, and take it out — over 006a's API. A child of [006](006-web-application.md).
**Depends on:** [006a](006a-http-api-and-streaming.md) (the only thing it talks to).

## Problem

Everything the engine and the web layer built is reachable only over HTTP. This spec is the surface a researcher actually uses: paste a link or drop a PDF, watch the audit reason and run, read the verdict and the transcript, and — if they want — receive the repository or make it public. It renders state the API already owns; it invents none.

## Decisions

**One client, driven by `/me`.** The app fetches `/me` first and branches on it: an ungranted principal sees the **not-invited screen** and nothing else; a seed user sees the operator UI; a trusted user additionally sees the visibility control. The frontend never decides access — it renders what `/me` reports and lets the server refuse anything forged past the UI. `/me` is the single source of what a user may see.

**Submit is a link *or* a drop.** One field takes a URL; the same surface accepts a dragged PDF (006d). Submitting returns a job and moves straight to watching it — the user never waits on a request.

**Watching is the live stream, read from the server's memory of the run.** The job page subscribes to `GET /jobs/{id}/events` (SSE) and narrates the phases — resolving, running, publishing — with the agent's progress and, once 005 lands, its reasoning and tool calls. Reconnecting resumes from `Last-Event-ID`; a user who opens the page an hour later still sees where it is, because the server streams from the job, not from their connection.

**Inspection is hindsight, not a live-only view.** A finished reproduction shows its verdict, its decomposed rate, the transcript, and token usage — the primary-source trail, read through the API from the manifest, 003, and 005. Nothing here is a copy the frontend keeps.

**The two exits live on one panel, gated by tier.** A finished reproduction's *take it out* panel offers, per `/me`: **claim** (invite my linked forge account; if unlinked, prompt the identity-only link first) and, for a trusted user, **make public** (a deliberate, confirmed, one-way action). A seed user sees the public control disabled with why. GitHub appears on this panel and nowhere else — not at sign-in, not while submitting or watching.

**Bilingual from the start.** All copy ships in English and Chinese (`zh`), including the not-invited screen, the phase labels, error reasons (`not_invited`, `not_trusted`), and the take-it-out confirmations. Locale is a first-class concern, not a retrofit — every string added lands in both.

## Interface (sketch)

```
/                 submit (URL or PDF drop) — or the not-invited screen
/jobs/{id}        live progress via SSE
/reproductions    the list (003)
/reproductions/{id}   verdict, rate, transcript, usage; the take-it-out panel
```

The client consumes 006a's routes only; it holds no credential and no forge client.

## Acceptance criteria

1. An ungranted principal is shown the not-invited screen and no operator control; the branch is driven by `/me`, so a forged UI cannot reveal more than the server allows.
2. Submitting a URL and dropping a PDF both start a job and land on its live view.
3. The job view streams phases and progress over SSE and resumes on reconnect; opening it after the run started still shows current state.
4. A finished reproduction renders verdict, rate, transcript, and usage from the API alone, with no client-side copy of reproduction data.
5. The take-it-out panel offers claim and (trusted only) make-public per `/me`; the public control is disabled with a reason for a seed user, and GitHub appears on no other screen.
6. Every user-facing string is present in both `en` and `zh`; a missing translation fails a completeness check.

## Out of scope

The API and its guarantees (006a), the access decision (006c), the invitation and visibility mechanics (006f). The choice of frontend framework — this spec fixes behaviour and copy, not the stack. Any direct talk to GitHub or the IDP: the client speaks only to 006a.
