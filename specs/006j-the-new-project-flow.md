# 006j — The New-project flow

**Superseded by [012](012-a-project-holds-papers.md) and [036](036-a-project-can-start-empty.md).** `POST /resolve` and the rename stand. Creating a project stopped enqueueing anything, so the best-effort-per-paper enqueue, the 422 partial success, `fundNewProject`/`undoProject`, the frontend's partial-success apparatus and the never-leave-an-empty-project rule are all gone — and a project may now be created with a title and no papers at all.

**Status:** built. `POST /resolve` previews a reference through the server fetcher; `POST /projects` creates a named project with one session per paper (best-effort per paper, never leaving an empty project) and seeds collaborator emails as members; `PATCH /projects/{id}` renames. The frontend "New project" screen (renamed from "New audit") drives all of it: a name field, repeatable paper rows each with an inline Resolve preview, and a collaborators field. Handlers are covered against fakes (resolve, create all/partial/none, collaborators, rename owner vs non-owner); the resolver helpers back the preview through the same fetcher the worker uses.

Before this, a project was born implicitly from a single paper submit (`POST /papers`), its title defaulting to the reference, with no way to name it, seed collaborators, or start more than one paper at once.
**Scope:** a create-with-papers endpoint and a resolve-preview endpoint, plus the frontend "New project" screen that drives them. It adds no new store and no new lifecycle; it composes the existing project store, job enqueue (with its double-trigger guard), and membership store (006c) behind one create. A child of [006g](006g-projects-and-replication-sessions.md).
**Depends on:** [006g](006g-projects-and-replication-sessions.md) (projects own sessions; the enqueue guard), [006c](006c-identity-and-access.md) (the membership store the collaborators are seeded into), [001](001-paper-identity-and-slugs.md) (`paper.Resolve` / `paper.Key`).

## Problem

`POST /papers {url, project}` does two jobs: with no project it creates one and enqueues the first session; with a project it appends. That is right for "add a paper," but it makes "start a project" a side effect. There is no way to: name the project up front, check a link resolves *before* committing, submit several papers together, or invite collaborators at creation. A bad link is only discovered when the run's Resolve phase fails, minutes later.

## Decisions

**A dedicated `POST /projects`, leaving `POST /papers` as the append path.** Create and append are different acts; overloading one endpoint would tangle the double-trigger guard with creation. `POST /papers` is unchanged.

```
POST /projects  { title?, papers: [url, …], collaborators: [email, …] }
  → 201 { project_id, title, sessions: [ {url, job_id} | {url, error} ], members: [email…] }
```

**Best-effort per paper; never leave an empty project.** Each paper is enqueued independently and reported in `sessions[]` as either a `job_id` or an `error` (a bad reference, or the double-trigger guard catching a duplicate). The project is kept if **≥1** session started; if **every** paper failed, the just-created project is deleted and the call is a 422 carrying the per-paper errors — the same compensating rule `submitToNewProject` already applies, generalised to N papers. The Resolve preview catches most bad links before this point.

**A `POST /resolve` preview over the server's HTTP fetcher.** `POST /resolve {url}` runs `paper.Resolve` with the same `newHTTPFetcher` the worker uses, returning `{key, title, scheme, slug}` — real metadata (an arXiv title, a DOI), so the operator sees what they are about to reproduce. It is behind a `PaperResolver` seam in `Deps`, so the handlers stay off `internal/paper`. A reference that is not a DOI/arXiv/URL is a 400 (`unresolvable_paper`); a source that cannot be reached is a 502 (`resolve_failed`), distinct so the UI can say "couldn't reach the source" rather than "bad link." The call is bounded by the fetcher's timeout.

**Collaborators are seeded through the existing members API.** After the project is created, the owner is seeded as its lead (idempotent) and each collaborator email is added as a `RoleCollaborator` member — the exact path `addMember` uses, so there is one authority model. A single collaborator failing does not fail the create; it is reported.

**Rename with `PATCH /projects/{id} {title}` (owner-only).** `project.Store.SetTitle` already exists (the worker uses it); this exposes it so a project can be renamed after creation. The httpapi `ProjectStore` interface gains `SetTitle`.

**The title.** `title` when given names the project; blank falls back to the first paper's reference (today's `defaultTitle`), which the worker's Resolve phase then enriches with the resolved paper title. A named project is never clobbered by resolution (the worker only enriches a title still equal to the placeholder).

## Acceptance criteria

1. `POST /projects` with a title and several valid papers creates one project (that title) with one session per paper, each with a `job_id`, and returns 201.
2. Among mixed papers, a bad or duplicate one is reported as `{url, error}` while the valid ones start; the project survives with the started sessions.
3. When every paper fails, no project is left behind (it is deleted) and the response is 422 with the per-paper errors.
4. Collaborator emails are seeded as project members (visible through `GET /projects/{id}/members`); the owner is the lead. A collaborator seed failure is reported, not fatal.
5. `POST /resolve` returns the resolved `{key, title, scheme, slug}` for a real arXiv/DOI reference; an unresolvable reference is 400 and an unreachable source is 502 — kept distinct.
6. `PATCH /projects/{id} {title}` renames an owned project and is refused (403) for a non-owner.
7. The frontend "New project" screen (renamed from "New audit") drives all of the above: a name field, a repeatable paper row with a Resolve button that previews the title inline, and a collaborators field — submitting one project.
8. Every new handler is covered against fakes (no network): the resolver seam is faked; create is covered for all-succeed, partial, and all-fail; rename for owner and non-owner.

## Out of scope

- **PDF upload** at create time (multipart) — 006d; the URL/DOI/arXiv path is this spec's.
- **Editing papers/collaborators after create** beyond what the existing members API and `POST /papers` already allow (add a paper, add/remove a member). No bulk edit screen.
- **A resolve cache.** Each preview hits the source through the fetcher's timeout; caching identical lookups is a later optimisation, not needed for correctness.
