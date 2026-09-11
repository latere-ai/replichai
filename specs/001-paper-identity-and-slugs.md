---
title: "Paper identity and slugs"
status: implemented
---

# 001 — Paper identity and slugs

**Status:** implemented (`8da41d8`)
**Scope:** deriving a stable identity and a human-readable slug for one paper.

## Problem

Every downstream feature needs to answer two different questions, and using one value for both breaks both:

1. **Is this the same paper we already audited?** This needs an identity that does not change.
2. **What should the repository be called?** This needs a name a person can read.

A title answers the second question and fails the first.

## Evidence this is a real risk

The first paper audited by this system, *PleaSQLarify*, carries **two different titles**:

| Source | Title |
|---|---|
| arXiv, publisher | …Visual Pragmatic Repair for Natural Language Database **Querying** |
| Authors' own site | …Visual Pragmatic Repair for Natural Language Database **Interfaces** |

A title-keyed system would have created two unrelated reproductions of one paper and never noticed. Titles also change between preprint and camera-ready, are re-cased by different indexes, and collide across venues for short titles.

## Decisions

**Identity is a resolved external identifier, in priority order:**

| Priority | Scheme | Bare key | Canonical string |
|---|---|---|---|
| 1 | `doi` | lowercased, `doi:` and URL prefixes stripped → `10.1145/3772318.3791265` | `doi:10.1145/3772318.3791265` |
| 2 | `arxiv` | versionless, lowercased → `2603.01795` | `arxiv:2603.01795` |
| 3 | `sha256` | SHA-256 of the extracted PDF text, as a last resort → `1f2e…` | `sha256:1f2e…` |

The **key** is the bare identifier; the **scheme** is a separate typed field. The `scheme:key` rendering is presentation only, produced by `Identity.String()`, and is the form a person reads. Storing the scheme separately lets the manifest in 002 carry `key` and `scheme` as two fields, and lets `sha256` sit alongside `doi` without one being mistaken for the other.

A paper reference is resolved to an identity **before** anything is created. arXiv entries that carry a DOI resolve to the DOI, so the preprint and the published version are one paper, not two.

**An arXiv reference is therefore upgraded, not accepted as final.** A DOI is terminal: it is the highest-priority identifier and nothing can enrich it, so a reference that already carries a DOI resolves offline with no network. An arXiv ID is not terminal: the same paper's published DOI is discovered by looking the preprint up, and only that lookup can distinguish a preprint with a DOI from a preprint without one. Resolving an arXiv reference therefore consults its metadata:

- lookup completes and a DOI is present: `doi:` (preprint and published version collapse to one identity);
- lookup completes and no DOI is present: `arxiv:` (a preprint-only paper);
- **lookup cannot complete** (metadata unreachable, no fetcher): **error, do not guess**. Falling back to `arxiv:` here would let the same paper resolve to `arxiv:X` on a bad network and `doi:Y` on a good one, which is the identity drift this spec exists to prevent. A paper's DOI is a stable fact; failing to reach it is a transient condition, not a different identity. The short-TTL cache in [003](003-reproduction-registry.md) absorbs the repeat cost.

Resolving an arXiv reference therefore requires the network; a `Fetcher` must be supplied (injected in tests, a real arXiv-metadata client in production).

**Slug is derived from the title, and is presentation only.** Lowercase, ASCII-fold, strip punctuation, collapse whitespace to hyphens, drop leading articles, then greedily fill whole words up to a word boundary at ≤ 48 characters. `PleaSQLarify: Visual Pragmatic Repair for Natural Language Database Querying` → `pleasqlarify-visual-pragmatic-repair-for-natural` (48 characters; the title is longer than the budget, so it stops on the last word that fits). Prettier truncation, such as trimming a dangling preposition, is a later refinement and not part of this rule.

**The slug never determines identity.** Two papers may share a slug; they will not share an identity. One paper may change its title and therefore its slug; its identity is unchanged, and the first slug ever assigned to that identity stays canonical (see 003).

**A URL that carries no identifier is read from its structured declaration, not scraped.** A DOI or an arXiv ID is taken straight from the reference. A bare publisher or authors' URL is fetched and its Highwire / Google Scholar `citation_*` meta tags (`citation_doi`, `citation_arxiv_id`, `citation_title`) are read. These tags are the per-page declaration of the paper's identity that academic publishers emit, so an arbitrary site is handled without guessing from its freeform body. A declared DOI wins; a declared arXiv ID re-enters the deterministic arXiv lookup; a page that declares neither falls back to the content digest. Resolution is therefore deterministic end to end: the same page yields the same identity every run. Reading a page with an LLM, for the long tail of pages that omit the tags, is deliberately left to a later spec, because a model in the identity path would risk the drift this spec exists to prevent.

## Interface

Package `internal/paper`. The scheme is a typed enum in the shape the port
already uses for `provider.Grade` and `provider.RunState`: a `uint8` whose zero
value is `SchemeUnknown`, so a struct field that was never set cannot read as a
real scheme, and whose `MarshalJSON` emits the stable name (`"doi"`) the
manifest stores.

```go
// Scheme is the kind of external identifier an identity resolves to.
type Scheme uint8

const (
	SchemeUnknown Scheme = iota
	SchemeDOI
	SchemeArXiv
	SchemeSHA256
)
// String/MarshalJSON/UnmarshalJSON serialise as "doi" | "arxiv" | "sha256",
// mirroring provider.Grade.

// Identity is a paper's stable identity plus its presentation slug.
type Identity struct {
	Key     string   // bare: "10.1145/3772318.3791265"
	Scheme  Scheme   // SchemeDOI
	Title   string
	Slug    string
	Sources []string
}

// String renders the canonical "scheme:key" form.
func (id Identity) String() string // "doi:10.1145/3772318.3791265"

// Resolve turns a reference into an Identity. It never mutates state.
func Resolve(ctx context.Context, reference string, opts ...Option) (Identity, error)

// Option configures resolution. WithFetcher injects the HTTP seam so a test —
// or a run over a reference that already contains a DOI or arXiv ID — resolves
// without a network, the same injection pattern audit.Auditor uses for its
// clock and provider.
type Option func(*resolver)

func WithFetcher(f Fetcher) Option
```

`Resolve` accepts a DOI, an arXiv URL or bare ID, a publisher URL, or a direct PDF URL.

## Acceptance criteria

1. A DOI, an arXiv abstract URL, an arXiv PDF URL, and a bare arXiv ID for the same paper all resolve to one identity.
2. An arXiv entry carrying a DOI resolves to the DOI scheme, not the arXiv scheme.
3. Both PleaSQLarify title variants produce the same identity, asserted explicitly on key and scheme, citing this as the motivating case. They also produce the *same* slug, because the titles differ only in a final word past the 48-character window: the title drift is absorbed. That a slug can also *track* a title is covered separately, with a synthetic pair that differs within the window.
4. Slugs are ASCII, lowercase, hyphen-separated, ≤ 48 characters, with no leading or trailing hyphen.
5. Unicode titles (accents, CJK, mathematical symbols) fold to a non-empty ASCII slug; a title that folds to nothing falls back to the identity key.
6. A publisher page that blocks automated fetching degrades to the identifier parsed from the URL rather than failing, and to the *byte-identical* identity a no-fetcher resolution yields (same key, same key-derived slug, empty title), so a blocked fetch is indistinguishable from no fetch.
7. The **identity key** never depends on the network: a DOI's key is parsed offline (`Key()` never fetches, and a resolution with no fetcher yields the key and a key-based slug). With a fetcher, a DOI's human title is enriched from **Crossref**, the canonical, deterministic DOI metadata service, which is reliable where publisher pages block bots. That title drives both the slug (a readable repository name) and the project title; the publisher page's `citation_title` is a fallback for a URL reference, and a failed lookup degrades to the key-based slug. Because Crossref is deterministic (a DOI yields one title), the title-derived slug is stable. The offline guarantee is kept where it matters (the key and the dedup guard never use the network) while the slug and title become human-readable. (An arXiv reference requires the network: its DOI must be looked up. A lookup that cannot complete errors rather than falling back to `arxiv:`, so identity never drifts with connectivity.)

## Out of scope

Repository creation, conflict handling, and the registry. This spec ends at an `Identity` value.

## Outcome

Implemented in `internal/paper` (`8da41d8`), all seven acceptance criteria covered by tests that run without a real network; 92% statement coverage.

Built as specified, with three decisions taken during implementation and folded back above:

- **The AC1/AC2-vs-AC7 contradiction was resolved before coding** (`5f29366`). AC1 and AC2 both require looking an arXiv preprint up to discover its DOI, which AC7's original "no network for a DOI *or arXiv ID*" forbade. AC7 was narrowed to DOI-only. The fetch-failure policy the contradiction exposed (error rather than fall back to `arxiv:`) is now a stated decision, so identity cannot drift with connectivity.
- **The AC3/AC4 slug inconsistency was resolved before coding** (`7ab3c33`). The two PleaSQLarify titles differ only in a final word past the 48-character window, so they yield the *same* slug, not different ones; AC3 was corrected, and the Decisions example fixed to the greedy-fill algorithm's real output.
- **Generic-URL resolution reads the `citation_*` meta standard**, deterministically: a declared DOI wins, a declared arXiv ID re-enters the arXiv lookup, and a page declaring neither falls back to a content digest. An LLM-backed resolver for pages that omit the tags was prototyped and then removed: a model in the identity path risks the drift this spec exists to prevent, and it belongs in its own spec.

Deferred, as designed: the real HTTP `Fetcher` is wired at the CLI edge (`replichai resolve`), not in this package; PDF text extraction for the content-digest path digests the fetched bytes for now; and an exploring, LLM-backed resolver for tag-less pages is left to a later spec.

**AC6/AC7 refined twice: title and slug enrichment for DOIs via Crossref.** The original reading ("never touch the network when a DOI is present") left `paper.title` empty for a submitted publisher URL, since the DOI short-circuited before any fetch. A first pass fetched the publisher page's `citation_title` to fill only `Identity.Title`, keeping the slug key-derived out of caution: a flaky scrape could otherwise allocate two repository names for one paper. But publishers like ACM block the scrape, so the title stayed empty for exactly the common case. The second, current refinement resolves a DOI's title from **Crossref**, the canonical DOI metadata API, which is unblocked and deterministic, as the primary source, with the publisher page as the fallback. Because Crossref is deterministic, the earlier flakiness objection is gone, so the title now drives the **slug** too: reproduction repositories get readable, title-based names (`walk-this-beam-…-<suffix>`), and the project's title is set from the same resolved title. The offline guarantee is preserved where it matters: the identity **key** and the dedup guard (`Key()`) never fetch, and a no-fetcher or blocked resolution degrades to the key-based slug with an empty title (AC6). Only the *presentation* (title, slug) now depends on Crossref, never the identity key.
