---
title: "Download the whole project"
status: built
---

# 020 — Download the whole project

**Status:** built. Every acceptance criterion holds. `internal/httpapi/archive.go` streams `GET /projects/{id}/archive` through `archive/zip` straight to the `ResponseWriter`; `archive_test.go` opens the returned bytes with `archive/zip` and asserts the entry names, the manifest, the 409, the view-rights gate, and the recorded failure. The projects list carries the download.

Three things came out of the build. **The download is an anchor, not a fetch.** A `fetch` would buffer the whole archive in the tab before saving it, which is the memory cost the streaming server side exists to avoid. So `archiveUrl` returns a URL, the row renders an `<a>`, and the browser test asserts the tag rather than only the href.

**The filename needed its own rule.** `Content-Disposition` is a quoted header and a project title is free text, so a title is reduced to `[A-Za-z0-9._-]`, capped at 80 characters, and falls back to `project-{id}` when nothing survives, so an untitled project still downloads.

**Sessions are archived oldest first**, which is the reverse of `ListByProject`. A list of sessions is most useful newest first; an archive reads in the order the work happened.

Two corrections came from review. The download button was gated on `session_count > 0` while the handler refuses when *no session has a repository*. These are different conditions, and since the download is a top-level navigation, a project whose sessions all failed before publishing would have replaced the app with a JSON error body. `ProjectStatus` gained a `Published` count (`COUNT(*) FILTER (WHERE repo_name <> '')`), surfaced as `published_sessions`, so the button's condition and the handler's are the same one. And a stranger on a private project now gets 404 rather than `projectForRead`'s 403, the rule 019 adopted for the same reason: a stranger should not learn whether a project exists.

The stream depends on there being no `WriteTimeout`: `cmd/server` sets only `ReadHeaderTimeout` and the ingress allows 3600s, which SSE already needs. A `WriteTimeout` added later would cut a large archive mid-body and hand the reader a truncated zip with a 200. That is the one failure the `ERRORS` section cannot report, because it happens after the manifest is written.

**Scope:** one action that gives a reader everything a project produced, as a single archive. Adds `GET /projects/{id}/archive`, streamed as a zip built from each session's committed tree, and the button that calls it.

**Depends on:** [006f](006f-taking-a-reproduction-out.md) (the repository a session publishes), [005](005-transcript.md) (the transcript inside it), [015](015-a-verdict-belongs-to-a-session.md) (a verdict belongs to a session, so the archive is organized by session), [018](018-one-list-of-projects.md) (the row the button sits on).

## Problem

A project's output is a set of repositories, one per published session, and the app already proxies them file by file: `GET /jobs/{id}/artifacts` lists a tree, `GET /jobs/{id}/artifact?path=` returns one file (`handlers.go:1705`, `:1729`). That pass-through exists because the repositories are usually private, so a reader with legitimate access cannot clone them.

So there is no way to get the work out in bulk. A project with three sessions and forty files each is a hundred and twenty clicks, and the reader ends up with a hundred and twenty loose files and no record of which session each came from. For a product whose argument is that a verdict should be checkable, taking the evidence away to check it should not be hard.

## Decisions

**One archive per project, organized by session.** 015 established that a verdict and a repository belong to the session that produced them, and a project holds several papers. So the archive's top level is sessions, not files:

```
<project-title>/
  MANIFEST.txt          what this is, which project, when, which sessions
  sessions/
    <job-id>/
      <the session's committed tree, verbatim>
```

A flat merge would collide two sessions' `REPRODUCIBILITY.md` and lose the distinction 015 restored.

**It is streamed, not built.** The handler writes into `archive/zip` over the `ResponseWriter` as it walks. A project's repositories can be large, and buffering one in memory to learn its size before sending is a memory cost with no benefit; nobody needs a `Content-Length` here.

**The status code is therefore committed before the work is done, so failures go inside the archive.** Once the first byte is written the response is a 200 whatever happens next. A session whose forge read fails is recorded as a line in `MANIFEST.txt` under `ERRORS`, and the archive still contains everything that did read. The alternative, aborting mid-stream and leaving the reader a truncated zip with a 200, is the outcome to engineer against, because a truncated archive is silently incomplete and a noted one is not.

**View rights, not ownership.** The gate is `mayViewProject`, the same predicate every other project-scoped read consults. A collaborator can download the project they work in; a reader of a public project can download what was published. Ownership is not the question anywhere else in this app and is not the question here.

**Sessions with no repository are skipped, and the manifest says so.** A session that never reached publishing has nothing committed. It gets a `MANIFEST.txt` line rather than an empty directory, so the archive's contents and the project's session list can be reconciled.

**Committed trees only.** The transcript and `replication.json` are already committed into the repository by the finalize step, so they come along without a second code path. Nothing is fetched from outside the forge to build the archive: it contains exactly the evidence the project published, which is what makes it worth downloading.

**One project at a time.** No bulk export across projects. The unit of work is the project, and an all-projects archive is a different feature with a different failure mode (one bad project spoiling a large download).

## API

```
GET /projects/{id}/archive
→ 200 application/zip
  Content-Disposition: attachment; filename="<slug>.zip"
→ 403/404 through mayViewProject
→ 409 no_artifacts   when no session in the project has a repository
```

The 409 is the one refusal that can be decided before the first byte: it is read off the session list, and it deserves a real status because "this project has produced nothing yet" is different from "here is an empty zip".

## Acceptance criteria

1. `GET /projects/{id}/archive` returns a zip whose entries are `sessions/{job-id}/{path}` for every file in every session's committed tree.
2. The archive contains a `MANIFEST.txt` naming the project, the time, and one line per session, including the sessions that were skipped and the reads that failed.
3. A project where no session has a repository answers 409 `no_artifacts` with no body.
4. A collaborator and a public reader both succeed; a stranger on a private project gets 404.
5. A forge read that fails for one session still produces a zip containing the other sessions, with the failure recorded under `ERRORS`.
6. The Projects row's download button fetches the archive and the browser saves it.
7. A test opens the returned bytes with `archive/zip` and asserts the entry names; a zip that only *looks* like one is the failure mode a byte-count assertion misses.

## Not in scope

Bulk export across projects; a background job with an email when it is ready; resumable downloads; including a git history (the archive is a tree, not a clone); tar.gz; per-session download buttons.
