# 016 — A paper that is not published yet

**Status:** deferred (ideation). Names the forks; resolves none. No code, no schema, no terraform follows from this file as written.
**Scope:** submitting a paper the world cannot fetch — a draft, a manuscript under submission — and running sessions against it, including the folder of materials that may arrive with it. Three seams are involved and they are not the same seam: where the bytes live, what the *identity* of a mutable document is, and whether supplied materials change what a session measures.
**Depends on:** [001](001-paper-identity-and-slugs.md) (`paper.Key`, `SchemeSHA256`), [006d](006d-pdf-ingestion.md) (the PDF reader — this spec **depends on it and does not absorb it**; see below), [012](012-a-project-holds-papers.md) (`project_papers`, and the paper-with-sessions rule this collides with), [007b](007b-experiments-over-instruments.md) (`experiment.ArtifactStore` — the blob seam already in the tree — and the uploaded-dataset precedent), [008](008-drive-data-plane.md) (the storage fork), [006i](006i-server-side-mount-and-publish.md) / [014c](014c-publication-requires-evidence.md) (what publishing does, and must not do here).

## Why deferred

Nothing about this is hard to *store*. It is hard to *identify*, and the identity question reaches into the two indexes the whole product keys on. Building the upload button first would mean discovering the identity model by migration. It is also not on the critical path: the published-paper loop is what the MVP proves, and the golden set (`evals/`) is entirely published papers. So this is recorded at ideation level, in [008](008-drive-data-plane.md)'s shape, to be picked up as its own investigation.

## Problem

Every paper the platform can hold is one the world can already fetch. `paper.Key` is deliberately offline and derived from an immutable public reference — a DOI, an arXiv id, a URL — which is what lets a key be computed without a network call and lets two people reproducing the same paper collide in the registry ([003](003-reproduction-registry.md)).

A draft has none of those. And the draft is where a reproduction is worth the *most*: an inner loop that does not reproduce is still fixable before it becomes a published record. A manuscript under submission is the case with the highest value and the least support.

Three asks hide inside "support uploaded papers", and conflating them is how this becomes one spec that implements as three:

| Ask | Seam it lands on | Difficulty |
|---|---|---|
| The bytes have to live somewhere, project-scoped, managed in the UI | `experiment.ArtifactStore` + infra | mechanical, but forked (below) |
| A document that changes has to have an identity | `paper.Key`, `project_papers`, `jobs_active_paper_project`, the registry | **the actual spec** |
| The replication may need a folder of materials, not just a paper | the mount ([006i](006i-server-side-mount-and-publish.md)), the method, the verdict | changes what a session *means* |

## The identity problem is the spec

`SchemeSHA256` already exists in `internal/paper/scheme.go`, and 006d already says what to do with an identifier-less PDF: digest the *extracted text*. Applied to a draft, that is a correct rule with a consequence nobody has costed.

A draft is mutable. v1, v2, v3 of one manuscript digest to three different keys, and every index in the product treats them as three unrelated papers:

- `project_papers` is keyed `(project_id, paper_key)`, so the project screen fills with three near-identical rows.
- Sessions carry `jobs.paper_key`; a session run against v1 is not reachable from v2, and the "replicate again" path lands on whichever row the user happens to click.
- The double-trigger guard `jobs_active_paper_project` is a unique index on `(project_id, paper_key)` over live phases — so v1 and v2 of one manuscript can run **simultaneously**, spending twice, and the guard is silent because it is doing exactly what it was told.
- 012's rule is that a paper with sessions is permanently undeletable. Every stale draft version is therefore permanent.
- The registry relates reproductions *by identity*, so sibling versions of one manuscript never relate to each other.

This is the arXiv-preprint-versus-published-DOI divergence 012's Outcome section already recorded as unsolved, promoted from a cosmetic duplicate row to the normal case. Three candidate answers, none chosen here:

**A. Every version is its own paper.** Zero new machinery and completely honest — a session did run against those exact bytes. It is also unreadable at the project level, and it makes the guard useless for the case it exists to catch.

**B. An upload is a paper *record* that holds versions.** A stable key (`upload:<id>`) is minted at first upload; versions live underneath it and each session records the version digest it actually ran. The content digest becomes *evidence* — recorded on the session, provable — rather than identity. Costs a new scheme in `paper.Scheme`, a versions table, and a decision on what the guard now means: one active session per paper, or per version? (Per paper reads right: two versions of a manuscript running at once is the mistake the guard is for.)

**C. The draft collapses into its published identity later.** The good outcome for an uploaded manuscript is that it gets published, at which point it acquires the DOI it should always have had, and the sessions already run against it should follow. That is a *merge* of two paper keys — the operation 012 explicitly did not solve for arXiv-versus-DOI. Here it stops being optional: the happy path demands it.

B and C are likely the pair, and C is the part that must be designed before A ships, because a system that cannot merge will accumulate orphans from day one.

**On 006d:** this spec **depends on** it, and must not silently claim its surface. 006d answers *"these bytes are a paper that already has an identity — find it."* That stays true and stays useful, because a draft PDF often already carries an arXiv id. This spec is what happens when there is no identifier at all and the bytes will change next week. If 016 is ever taken, 006d ships first or with it, unchanged.

## Storage: the fork

The tree already has the right seam. `experiment.ArtifactStore` (`internal/experiment/store.go`) is `Put`/`Get`/`Delete`/`DeletePrefix` over string keys, with memory and filesystem implementations and prefix conventions per owning entity (`datasets/<id>`, `experiments/<id>/`). Uploaded papers are one more prefix behind it. Nothing above the seam cares which branch below is taken, which is what makes deferring this cheap.

**Branch 1 — drive is the store.** `drive.latere.ai` is already the thing being asked for: S3/MinIO-backed user files, a Drive-style file-tree UI, ownership and sharing, quotas, provenance zones, presigned browser multipart upload (the `dr-15` CORS rule in `terraform/storage.tf` exists for exactly that). [008](008-drive-data-plane.md) already names drive as replichai's artifact data plane. Building a second project-scoped file manager inside replichai duplicates a shipped product and contradicts a spec on the map. The cost is that it makes drive a hard runtime dependency of replichai ahead of 008's own sequencing, and it inherits 008's unresolved question: does project membership ([006c](006c-identity-and-access.md)) delegate to drive's sharing model, or stay the source of truth with drive as dumb storage?

**Branch 2 — replichai's own prefix in the shared bucket.** An S3 `ArtifactStore` against `latere-storage` under a `replichai/` prefix, the same product-namespaced pattern `cella/` and `lux/` already use. If taken, the infra changes are known and small, and they are named here so nobody rediscovers them:

- a `replichai-*` lifecycle rule, which must slot **after** `lux-` in `storage.tf` — the block order is load-bearing and alphabetical, per the 2026-06 drift incident recorded in that file's comment;
- a second entry in `allowed_origins` on `digitalocean_spaces_bucket_cors_configuration.storage`, which today permits the drive origin only — needed **only** if the SPA PUTs presigned parts directly rather than proxying through the API;
- a scoped `digitalocean_spaces_key` for the service, per the `images_catalog` precedent;
- **not** the CDN. `cdn.latere.ai` serves public objects; see Embargo.

Terraform is a separate repository. This spec names what would change there and commits nothing to it.

**The UI is the asymmetry between the branches, not the bucket.** What is being asked for is project-scoped file management: the project screen ([012](012-a-project-holds-papers.md)) grows a Files card beside its Papers card, or a paper row that lists its versions and lets one be replaced — plus upload progress, size and type refusals, a per-project quota, and a delete that actually removes bytes. Branch 1 inherits all of that from drive, which ships a file tree, quotas, and browser multipart upload today. Branch 2 gets a bucket in an afternoon and then builds every one of those screens. Costed on the bucket alone Branch 2 looks cheaper than it is, and that is probably the tie-breaker.

## A paper and its materials are two different things

"A zipped folder of files to use for the replication" is not a paper. The paper is the document making the claim; the materials are code, data, and protocol. Today materials reach a session one way — the agent finds and downloads them, into the sandbox but deliberately *outside* the mounted repository ([006i](006i-server-side-mount-and-publish.md), [002](002-publishing-reproductions.md)'s redistribution gate). A supplied bundle would be a third materials source, alongside that and [007b](007b-experiments-over-instruments.md)'s uploaded dataset.

Two things are already decided by precedent and should not be re-litigated when this is taken. The bundle lands **outside the mount**, so it physically cannot be committed into a reproduction repository — 007b's rule for uploaded datasets, for the same reason: it is not ours to redistribute, and here it is an author's unpublished material. And its bytes go to the `ArtifactStore`, not to git.

What is genuinely open is what it does to the verdict. **An audit handed its materials is not measuring what an audit that had to find them measured.** "Could an independent agent reproduce this from the paper?" and "does this code run?" are different questions with different `rate`s, and the manifest currently reports one number as if there were one question. If a supplied bundle is accepted, the verdict has to say so indelibly — the same treatment [007c](007c-the-synthetic-pilot.md) gives a synthetic cohort — or reproductions stop being comparable across sessions. This is the sharpest reason the feature is not free.

## Embargo

A manuscript under submission is confidential, and this product's whole shape points the other way: it publishes verdicts to a public zone, serves public objects over a CDN, and gives researchers public pages ([006r](006r-the-public-profile.md)).

- An uploaded paper is private, and its session is **unpublishable** — barred from the owner's public flip ([006f](006f-taking-a-reproduction-out.md)/[006n](006n-who-signed-off.md)), not merely private by default. A flip is one button and one signature away, and the material behind it is unpublished work.
- The transcript ([005](005-transcript.md)) quotes the paper. Committed transcript evidence ([014c](014c-publication-requires-evidence.md)) would therefore contain the manuscript, which is a leak through the evidence layer rather than the artifact layer.
- Bytes never land under a public-read prefix, and never behind `cdn.latere.ai`.
- **Deletion collides with 012.** "Delete my manuscript" is a request this system must be able to honour, and 012 makes a paper with sessions permanently undeletable *on purpose* — the record of what was reproduced is not editable. Both rules are right and they cannot both hold for uploaded content. Resolving that is part of taking this spec, not a detail of it.

This is a different concern from [BACKLOG](BACKLOG.md) item 4. There the risk is *standing* — a verdict naming authors who never agreed to it. Here the owner **is** the author, and the risk is *leakage*.

## Open questions

1. **Identity of a mutable document** — A, B, or C above, and whether merge (C) is designed before anything ships.
2. **What the guard guards** once versions exist: one active session per paper, or per version?
3. **Storage branch**, and with it whether 008 is pulled forward or replichai grows its own prefix and swaps later behind `ArtifactStore` — weighed on the file-management UI, not on the bucket.
4. **Quota and limits.** Per-project storage ceiling, per-file size, accepted types. Drive has an answer; a `replichai/` prefix has none, and an unbounded upload path against a shared bucket is a cost surface with no meter, in a product where [011](011-what-a-session-costs.md) meters everything else.
5. **Does a supplied bundle change the verdict**, and if so how is that recorded so rates stay comparable?
6. **Deletion versus the permanent record** — which rule yields, and what the tombstone-free removal looks like (the project's clean-removal preference argues against a "withdrawn" marker).
7. **Extraction of a zip** — where it is unpacked, file-count limits, and whether an archive is even the right transport versus a file tree.
8. **Does the draft ever enter the registry?** Relating reproductions by identity is 003's job; an unpublished paper arguably has no business in a derived public index at all.

## Out of scope

Everything, until the published-paper loop's remaining depth ships and [008](008-drive-data-plane.md) is decided. This file exists so the model is shaped correctly when the time comes, and so the identity consequence is not discovered by an upload button.
