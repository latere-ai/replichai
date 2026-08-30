---
title: "A paper carries its bibliography"
status: built
---

# 037 — A paper carries its bibliography

**Status:** built. All nine criteria hold.

Resolution turned out to be mostly subtraction, as the problem statement predicted: Crossref, arXiv and the `citation_*` tags were already being fetched, and the parsers kept the title alone. Widening them surfaced a latent bug in the shared meta-tag matcher — it read a `content="…"` attribute up to the first quote *of either kind*, so a conference called `CHI '24` came back as `CHI`, and the same matcher reads the title and the DOI. It now matches by the attribute's own delimiter (`internal/paper/record.go`).

Two decisions were made in the building. The admin sweep is an HTTP route (`POST /admin/papers/refresh`) rather than a command: the production image is distroless and has no shell to run one in. And enrichment reads-then-writes inside a single transaction, because the rule it applies depends on what is already stored — a read outside the write's transaction would let a correction land between the two and be overwritten by exactly the enrichment this spec says must never overwrite one.

`make db-contract` did not run `internal/project`'s Postgres contract at all; it does now, which is what caught an untyped parameter in the enrichment UPDATE that the in-memory store could not see.

**Amended after use.** Reading a project somebody else assembled, a citation line turned out not to be enough: it says where a paper appeared, not what it is about. The record therefore also carries the **abstract** its source publishes — Crossref's deposited JATS with its markup stripped, arXiv's `<summary>`, a page's `citation_abstract` — under the same rule as every other field: never written by a model, correctable by hand, and absent rather than invented where no source publishes one.

The layout changed with it. A paper was a table row, and the title was the item that shrank: at the card's width it read as "There is more to context than l…" beside a reference printed in full. It is now a block — title on its own line, citation under it, abstract clamped to three lines with a control that opens it, and the controls on their own line beneath. The reference is printed as the identifier it is (`10.1145/…`, `arXiv:…`) and links to the paper.

**Scope:** a paper recorded in a project keeps the bibliographic record its identifier already returns — authors, venue, year, volume, issue, pages, publisher — instead of a title and nothing else. Resolution stops discarding what it fetched, the record is stored beside the paper, the project screen shows it, an owner can correct it, and there is a way to fill in every paper that predates this. Backend and frontend. One migration.

**Depends on:** [001](001-paper-identity-and-slugs.md) (resolution and the identity it produces), [012](012-a-project-holds-papers.md) (the paper row this widens), [035](035-a-paper-can-move-to-another-project.md) (a paper moves between projects, and its record goes with it).

## Problem

A paper in a project is `Key, Ref, Title, AddedBy, AddedAt` and nothing more (`internal/project/project.go:150`). The screen can therefore say what a paper is *called* and what was typed to name it, and nothing about what it is: who wrote it, where it appeared, or when. Anybody reading a project — its owner six months later, a collaborator arriving, a stranger following a public link — is looking at a title and a `doi:10.1145/…` string.

The data is not missing. It is fetched and thrown away:

- `titleFromCrossref` (`internal/paper/paper.go:318`) requests the whole Crossref record and parses `message.title[0]` out of a response that also carries `author`, `container-title`, `issued`, `page`, `volume`, `publisher`.
- `arxivFeed` (`:247`) parses `<title>` and `<doi>` from an Atom entry that also carries every author, the published date, and `journal_ref`.
- The publisher-page scraper (`:274`) reads `citation_doi`, `citation_arxiv_id` and `citation_title` from HTML that, by the same Highwire convention, usually also carries `citation_author`, `citation_journal_title`, `citation_publication_date`, `citation_firstpage` and `citation_lastpage`.

So the first half of this spec is subtraction: stop dropping fields on the floor. The second half is what to do when the sources disagree with reality, which they do — a Crossref record with an author's name mangled, an arXiv preprint whose `journal_ref` is empty because it was never updated after acceptance.

## Decisions

**No model ever writes a bibliographic field.** These fields are a citation. A citation that is plausible rather than correct is worse than one that is absent, because nothing about it looks wrong. Every stored field comes from an identifier's own metadata API or from the publisher's own meta tags, or from a person who corrected it. A reference with neither — a bare `url:` to a page with no `citation_*` tags — stays sparse, and says so.

**A correction is permanent.** A field a person edited is never overwritten by a later resolve. This is why the record cannot be a cache: refreshing is something the app does on its own initiative (on the button, on the sweep), and a refresh that quietly replaced a name somebody had fixed would make the fix pointless. The row records which fields were edited; enrichment writes only the others.

**The record lives on the paper row, not on the identity.** `paper.Identity` grows the fields so resolution can return them, but the durable copy is `project_papers`, for the reason 012 already put the title there: the paper in a project is what the project shows, and it must render with no network. A paper that moves (035) carries its record with it, because it moves as a row.

**Authors are an ordered list, stored as one.** Author order is part of a citation, and "et al." is a rendering decision rather than a storage one. A `text[]` column keeps the order the source gave and lets the screen decide how many to show.

**Filling in is an action, not a background habit.** An owner's project gets a control that resolves every paper whose record is incomplete; an admin command does the same across every project, for the ones nobody opens. Both are idempotent, both skip edited fields, and both go through the same code as a fresh add — one enrichment path, not three. Crossref and arXiv are courteous services with published etiquette: the sweep serialises its requests and identifies itself, rather than opening a connection per paper.

**Correcting is a member's act, and it is scoped to the project.** `PATCH /projects/{id}/papers/{key}` is the first route that writes a paper field at all — today the only writer is a background goroutine (`enrichTitles`). It follows 012's rule for who may act on a project's contents rather than inventing one.

## Acceptance criteria

1. Resolving a DOI returns authors (in order), container title, year, volume, issue, pages and publisher when Crossref carries them, and a resolve for a record missing any of them succeeds with those fields empty.
2. Resolving an arXiv id returns the authors and the published year, and the journal reference when the preprint carries one.
3. Resolving a plain URL reads `citation_author`, `citation_journal_title`, `citation_publication_date` and the page range when the page publishes them, and yields an otherwise sparse record when it does not.
4. Adding a paper to a project stores the record; the project screen shows authors, venue and year on the paper row, and shows a bare reference as needing details rather than as complete.
5. `PATCH /projects/{id}/papers/{key}` corrects any field for a member, refuses a stranger, and marks exactly the fields it changed as edited.
6. A subsequent enrichment — the button, the sweep, or a re-add — leaves every edited field untouched and fills only the empty ones.
7. An owner's "fill in details" action completes every incomplete paper in the project and reports how many it changed; running it twice changes nothing the second time.
8. The admin sweep walks every project, is safe to re-run, and serialises its upstream requests.
9. A paper moved to another project (035) arrives with its record and its edit marks intact.

## Out of scope

The paper's own text. Nothing here reads a PDF, and no field is inferred from one. A reference that resolves to nothing stays a reference — [016](016-a-paper-that-is-not-published-yet.md) is where an unpublished manuscript is argued about, and it is deferred.

Citation export (BibTeX, RIS). The record is what an export would need, and it is worth having first; the format question can wait until somebody asks for a specific one.
