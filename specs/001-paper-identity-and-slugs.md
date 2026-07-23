# 001 — Paper identity and slugs

**Status:** draft
**Scope:** deriving a stable identity and a human-readable slug for one paper.

## Problem

Every downstream feature needs to answer two different questions, and conflating them breaks both:

1. **Is this the same paper we already audited?** — needs an identity that does not drift.
2. **What should the repository be called?** — needs something a human can read.

A title answers the second and fails the first.

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

The **key** is the bare identifier; the **scheme** is a separate typed field. The `scheme:key` rendering is presentation only, produced by `Identity.String()`, and is what a human reads. Storing the scheme separately is what lets the manifest in 002 carry `key` and `scheme` as two fields, and what lets `sha256` sit alongside `doi` without one masquerading as the other.

A paper reference is resolved to an identity **before** anything is created. arXiv entries that carry a DOI resolve to the DOI, so the preprint and the published version are one paper, not two.

**An arXiv reference is therefore upgraded, not trusted as final.** A DOI is terminal — it is the highest-priority identifier and nothing can enrich it, so a reference that already carries a DOI resolves offline with no network. An arXiv ID is *not* terminal: the same paper's published DOI is discovered by looking the preprint up, and only that lookup can tell preprint-with-a-DOI from preprint-with-none. So resolving an arXiv reference consults its metadata:

- lookup completes, a DOI is present → `doi:` (preprint and published version collapse to one identity);
- lookup completes, no DOI is present → `arxiv:` (a genuine preprint-only paper);
- **lookup cannot complete** (metadata unreachable, no fetcher) → **error, do not guess**. Falling back to `arxiv:` here would let the same paper resolve to `arxiv:X` on a bad network and `doi:Y` on a good one — the exact identity drift this spec exists to prevent. A paper's DOI is a stable fact; failing to reach it is a transient condition, not a different identity. The short-TTL cache in [003](003-reproduction-registry.md) absorbs the repeat cost.

Resolving an arXiv reference is thus network-required; a `Fetcher` must be supplied (injected in tests, a real arXiv-metadata client in production).

**Slug is derived from the title, and is presentation only.** Lowercase, ASCII-fold, strip punctuation, collapse whitespace to hyphens, drop leading articles, truncate on a word boundary to ≤ 48 characters. `PleaSQLarify: Visual Pragmatic Repair for Natural Language Database Querying` → `pleasqlarify-visual-pragmatic-repair`.

**The slug never determines identity.** Two papers may share a slug; they will not share an identity. One paper may change its title and therefore its slug; its identity is unchanged, and the first slug ever assigned to that identity stays canonical (see 003).

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
3. Both PleaSQLarify title variants produce the same identity and different slugs — and the test asserts the identity match explicitly, citing this as the motivating case.
4. Slugs are ASCII, lowercase, hyphen-separated, ≤ 48 characters, with no leading or trailing hyphen.
5. Unicode titles (accents, CJK, mathematical symbols) fold to a non-empty ASCII slug; a title that folds to nothing falls back to the identity key.
6. A publisher page that blocks automated fetching degrades to the identifier parsed from the URL rather than failing.
7. Resolution never mutates state, and never touches the network when the reference already contains a **DOI** — the terminal identifier. (An arXiv reference is network-required: its DOI must be looked up. A lookup that cannot complete errors rather than falling back to `arxiv:`, so identity never drifts with connectivity.)

## Out of scope

Repository creation, conflict handling, and the registry. This spec ends at an `Identity` value.
