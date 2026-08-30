---
title: "A verdict belongs to a session"
status: built
---

# 015 — A verdict belongs to a session

**Status:** built. Every acceptance criterion holds. `frontend/src/papers.ts` owns the join and the gradeable rule with `papers.test.ts` against it; `/p/{id}/s/{jobId}/{verdict,artifacts}` exist and `/p/{id}/artifacts` is gone; the project's verdict tab reads across papers; sessions nest under their paper. `routing.test.mjs` drives the reported defect in Chrome — two papers, each with a published session, each asserted to list *and render* its own files, which fails on the line this spec removes. Frontend unit tests went 74 → 83; the browser suite is green.

**Scope:** move the verdict and the artifact browser off the project and onto the session that produced them, and give the project an outcome view that reads across every paper it holds. Deletes `/p/{id}/artifacts`, repurposes `/p/{id}/verdict`, adds `/p/{id}/s/{jobId}/verdict` and `/p/{id}/s/{jobId}/artifacts`, and nests a project's sessions under their paper. No new backend routes. Revises [013](013-the-address-bar-is-the-state.md) (the path set and its `ProjectTab` union) and completes [012](012-a-project-holds-papers.md) (two screens that never followed the paper into being a first-class thing).
**Depends on:** [012](012-a-project-holds-papers.md) (`project_papers`, `jobs.paper_key`, the papers routes), [013](013-the-address-bar-is-the-state.md) (`pathOf`/`viewOf` over the `View` union), [006f](006f-taking-a-reproduction-out.md) (the repository a session publishes), [006s](006s-reading-a-session-in-time.md) (the session screen this adds tabs beside).

## Problem

A project holds papers; a paper has sessions; a session publishes a repository and is graded into a manifest. The Artifacts and Verdict tabs know none of that. They read one line:

```ts
// frontend/src/App.tsx:1505
const published = (sessions ?? []).find((s) => s.repo_name) ?? null
```

Sessions arrive newest-first (`internal/job/pgstore.go:300`, `ORDER BY created_at DESC, id DESC`), so both tabs render *the most recent session in the whole project that has a repository* — and the same one, whichever paper the reader had in mind. A project holding papers A and B, run in that order, shows B's files under a tab that names the project. A's reproduction is in the product, reachable by no address. That is not a display bug; it is the pre-012 assumption that a project is one paper's runs, left standing in the two screens that report results.

The same line is wrong a second way. `RepoName` is *"set once publishing begins"* (`internal/job/job.go:163`), and the state machine permits `publishing → failed` (`job.go:201-208`). So a session that reached publishing and then failed satisfies the predicate and outranks an older session that finished. The tab can show a half-written repository and call it the project's result.

This is the third instance of one root cause. 012's own Outcome records the first: the worker enriched a project's placeholder title only when it matched the *running job's* ref — correct exactly while every paper started at create. Each time, code that derived one session from a project survived the split that made a project hold many papers. The fix is not to teach that derivation about papers. It is to delete it: nothing in this product owns a verdict or a repository except the session that produced it.

## Decisions

**A verdict and a repository are a session's, and live at the session's address.** The backend already says so — `/jobs/{id}/reproduction`, `/jobs/{id}/artifacts`, `/jobs/{id}/artifact` all take a job id, and there is no project-level equivalent to build. The frontend follows: `/p/{id}/s/{jobId}` gains two siblings, `/verdict` and `/artifacts`, beside the transcript it already shows. `published` is deleted, and with it the idea that a project has *a* session.

**`/p/{id}/artifacts` is removed, not redirected.** A file browser over "the project's repository" has no correct form, because there is no such repository — there are as many as there are published sessions. A tab that has to pick one is the defect, so it goes rather than being taught to pick better. Nothing outside `frontend/test` links either project tab; both are app paths, neither has been mailed or published.

**`/p/{id}/verdict` becomes the project's outcome view**, and is the one place that reads across papers: every paper the project holds, every session that produced a manifest, each with its verdict, its grading date, its redistribution clearance and its repository — each row linking inward to that session's own verdict page. This is what a visitor to a published project came for, and it is the question the deleted tab was pretending to answer.

It does not duplicate the sessions tab. The sessions tab is the working view: papers you hold, Start buttons, what is running, what it cost. The verdict tab is the result view: only sessions that finished and were graded, ordered by paper. One is what to do next; the other is what was found.

**A session is gradeable when `phase === 'done'` and it carries a repository — never on the repository alone.** That predicate is written down once, in code, because the line this spec removes got it wrong: `RepoName` marks that publishing *started*, and `publishing → failed` is a legal transition, so a repository name says a directory exists, not that anything finished. The verdict view must not lean on `getReproduction` answering `404` to keep a half-written repository out; a correctness rule enforced by a forge miss is the same rule waiting to come back. `failed` is terminal, so "terminal" is not the test either.

**Verdicts are read per session, from the client, as the rows render.** `getReproduction(jobId)` exists and each call is one manifest fetch through the forge. A batch route would be one round trip wrapping N of the same forge reads, plus a route to build and gate, and `clearedForPublish` already shows the server-side shape of that loop if it is ever wanted. Only gradeable sessions are requested, and each renders its verdict when it lands, so a slow forge delays one row rather than the page. A project with many graded sessions is the case that would argue for storing the verdict on the job at finalize; that is a migration, and it is out of scope until the read cost is felt rather than predicted.

**Sessions nest under their paper.** The project's flat Sessions card is what let the association go unnoticed: a list of runs with no indication of what each was a run *of*. Each paper row in `PapersCard` carries its own sessions, joined on `paper_key` — the same join `PaperView` already does server-side (`internal/httpapi/handlers.go:159-174`). A session whose `paper_key` matches no paper the project holds is listed under an "other sessions" group rather than dropped; the seed migration makes that rare, but a paper is removable before it has run and a job's key is a string, so the view must not silently lose a run.

**The frontend `Job` type gains `paper_key`.** The backend has emitted it since 006g (`internal/job/job.go:160`); `frontend/src/api.ts:36` simply never declared it. That one line is the whole of the data work — the grouping needs no backend change.

**A tab is a path segment; a paper key is still not.** Job ids match `[A-Za-z0-9_-]+` and nest safely, so `/p/{id}/s/{jobId}/verdict` parses under 013's existing session pattern. 012's rule is untouched: no address here carries a `paper.Key`, because `doi:10.1145/…` spans four segments. A paper is addressed by the sessions grouped under it, never by a path.

## Addresses

```
/p/{id}                      the project: papers, each with its sessions
/p/{id}/verdict              the outcome view: paper × session → verdict
/p/{id}/people               members            (unchanged)
/p/{id}/s/{jobId}            that session's transcript   (unchanged)
/p/{id}/s/{jobId}/verdict    that session's manifest
/p/{id}/s/{jobId}/artifacts  that session's repository files
```

Removed: `/p/{id}/artifacts`.

`ProjectTab` loses `'artifacts'`. `View`'s session variant gains an optional `tab?: SessionTab` where `SessionTab = 'transcript' | 'verdict' | 'artifacts'`, with `transcript` the bare path — the same rule `sessions` follows on a project, so the link already shared of a session keeps its meaning.

## API surface

Unchanged. `GET /jobs/{id}/reproduction`, `GET /jobs/{id}/artifacts`, `GET /jobs/{id}/artifact?path=` are already per-session and already gated by `viewableSession`. `GET /projects/{id}` and `GET /projects/{id}/papers` supply the grouping.

## Frontend

`VerdictTab` and `ArtifactsTab` stop taking a `Job | null` and take a job id, moving to the session screen. The session screen's `tx-head` becomes a tab strip; the transcript reader — scrubber, rail, stream — is what the transcript tab renders, unchanged.

**The join and the gradeable predicate are a module, not inline JSX.** `frontend/src/papers.ts` holds `groupByPaper(papers, sessions)` and `isGradeable(job)`, beside `papers.test.ts` — the shape `transcript.ts`, `sidebar.ts` and `session-stop.ts` already follow, and the reason the rule this spec exists to fix is testable at all. `App.tsx` renders what the module returns.

`ProjectVerdicts` is new: papers from `listPapers`, sessions from the project detail, grouped by key, one `getReproduction` per gradeable session, each row a link to that session's verdict. A project with nothing graded shows the existing `noVerdict` empty state.

`PapersCard` renders each paper's sessions beneath it, reusing `SessionRow` as-is. The separate Sessions card is deleted; the "other sessions" group takes its place only when a session's paper is not among the project's.

**A paper's session count comes from the grouped sessions, not from `ProjectPaper.sessions`.** The server-computed count and the array the row renders would be two answers to one question, fetched by two components on two clocks — a paper that has just been started reads "1 session" over an empty list until whichever refresh lands second. The count the row shows is the length of what it lists, and the server's field stays what it already is: the input to the never-remove-a-run-that-happened rule (012), not a number the screen prints.

One new i18n key (`otherSessions`), added to every locale including `zh` in the same change.

## Acceptance criteria

1. A project holding papers A and B, each with a published session, shows A's files under A's session and B's under B's — and no screen shows one paper's artifacts under an address that names the project.
2. `/p/{id}/s/{jobId}/artifacts` and `/p/{id}/s/{jobId}/verdict` render that session's repository and manifest, survive a reload, and are reachable by Back and Forward from the transcript.
3. `/p/{id}/artifacts` no longer resolves to an artifacts tab: 013's rule for an unrecognised segment applies and the reader lands on the project with the address corrected.
4. `/p/{id}/verdict` lists every graded session grouped by paper, and each row opens that session's verdict.
5. A session that entered `publishing` and then failed is never presented as a result: it appears in its paper's session list with its failure, and is absent from the verdict view.
6. A project's sessions each appear under the paper they targeted; a session whose `paper_key` matches no held paper appears under "other sessions" rather than vanishing.
7. `pathOf` and `viewOf` round-trip every `View` variant including the two new session tabs, and no app path collides with an API prefix.
8. A public visitor to a published project can reach any session's verdict and artifacts without being a member — the gate is `viewableSession`'s, unchanged.

## Testing strategy

- `routes.test.ts` (bun): the round trip over the new `View` shape, `/p/{id}/artifacts` falling back to the project, session tab parsing, and the existing no-collision invariant.
- `papers.test.ts` (bun): sessions joined to papers by key, a session with an unheld key landing in "other", a paper with no sessions keeping its row, and `isGradeable` rejecting a `publishing → failed` session that carries a repository while accepting a `done` one. That last case is the test that fails on today's `find((s) => s.repo_name)`.
- `routing.test.mjs` (Chrome, over the mocked API): a two-paper project where each paper's session publishes a different repository — click into each and assert the file list differs, which is the reported bug reproduced end to end. Plus reload and Back across the session tabs.
- The existing `routing.test.mjs` verdict-tab assertions move to the new addresses.

## Out of scope

- **Storing the verdict on the job** so the outcome view needs no per-session forge read. Worth doing when the read is felt; it is a migration and a worker change, and it does not change a single address.
- **Comparing sessions of one paper** — two reproductions of the same paper side by side is [003](003-reproduction-registry.md)'s question, not a tab's.
- **Artifacts for a session still running.** The endpoint answers `409 not_ready` until publishing begins, and the tab shows that rather than polling.
- **A project-wide file browser.** Deleted here on the grounds that it cannot be correct; if a project-level artifact ever exists it will be a published thing of the project's own, not a union of its repositories.

## Outcome

Every acceptance criterion holds. Three things are worth recording.

**The predicate as first drafted contradicted AC5.** The Decisions section said rows were requested for sessions that were *terminal* and carried a repository — but `failed` is terminal, and a `publishing → failed` session carries `repo_name`, so that predicate admitted exactly the run AC5 says must never be presented as a result. It would have worked in practice, because `getReproduction` would 404 on a half-written repository and the row would render "unavailable" — which is the same defect this spec indicts, enforced by a forge miss instead of by a rule. `isGradeable` is `phase === 'done' && !!repo_name`, written once in `papers.ts`, and it is the case `papers.test.ts` asserts most directly.

**The two sources of truth were real and were removed.** `ProjectPaper.sessions` is a server-computed count and `PapersCard` fetched papers independently of the screen's session list — two clocks, so a paper just started would read "1 session" over an empty list until whichever request landed second. Papers are now read by `ProjectScreen` alongside the sessions, and a paper row's count is the length of what it lists. The server's field keeps its one job: the input to 012's never-remove-a-run-that-happened rule.

**A grade was leaking the record's identifier.** The manifest stores the grader's own names (`needs_revision`, `internal/provider/enum.go`) and both verdict surfaces capitalised the first letter and printed the rest, so a real run read "Needs_revision" on screen. `verdictLabel` reads it the way a person would. Unspecified, found only by rendering the screens against the real vocabulary rather than against invented verdict words.

**Deliberate divergence:** the session screen's back button and paper reference moved into a header the wrapper owns (`.session-head`), and the transcript kept its own toolbar for the live dot and token counts. The transcript reader is otherwise untouched — it became a fragment inside the session's flex column rather than owning it.
