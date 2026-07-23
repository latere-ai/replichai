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

| Priority | Identifier | Normalised form |
|---|---|---|
| 1 | DOI | lowercased, `doi:` and URL prefixes stripped → `10.1145/3772318.3791265` |
| 2 | arXiv ID | versionless, lowercased → `arxiv:2603.01795` |
| 3 | Content digest | SHA-256 of the extracted PDF text, as a last resort → `sha256:…` |

A paper reference is resolved to an identity **before** anything is created. arXiv entries that carry a DOI resolve to the DOI, so the preprint and the published version are one paper, not two.

**Slug is derived from the title, and is presentation only.** Lowercase, ASCII-fold, strip punctuation, collapse whitespace to hyphens, drop leading articles, truncate on a word boundary to ≤ 48 characters. `PleaSQLarify: Visual Pragmatic Repair for Natural Language Database Querying` → `pleasqlarify-visual-pragmatic-repair`.

**The slug never determines identity.** Two papers may share a slug; they will not share an identity. One paper may change its title and therefore its slug; its identity is unchanged, and the first slug ever assigned to that identity stays canonical (see 003).

## Interface

```python
@dataclass(frozen=True)
class PaperIdentity:
    key: str          # "10.1145/3772318.3791265"
    scheme: str       # "doi" | "arxiv" | "sha256"
    title: str
    slug: str
    sources: list[str]

def resolve_paper(reference: str, *, fetch=...) -> PaperIdentity: ...
```

`resolve_paper` accepts a DOI, an arXiv URL or bare ID, a publisher URL, or a direct PDF URL.

## Acceptance criteria

1. A DOI, an arXiv abstract URL, an arXiv PDF URL, and a bare arXiv ID for the same paper all resolve to one identity.
2. An arXiv entry carrying a DOI resolves to the DOI scheme, not the arXiv scheme.
3. Both PleaSQLarify title variants produce the same identity and different slugs — and the test asserts the identity match explicitly, citing this as the motivating case.
4. Slugs are ASCII, lowercase, hyphen-separated, ≤ 48 characters, with no leading or trailing hyphen.
5. Unicode titles (accents, CJK, mathematical symbols) fold to a non-empty ASCII slug; a title that folds to nothing falls back to the identity key.
6. A publisher page that blocks automated fetching degrades to the identifier parsed from the URL rather than failing.
7. Resolution never mutates state and never touches the network when the reference already contains a DOI or arXiv ID.

## Out of scope

Repository creation, conflict handling, and the registry. This spec ends at a `PaperIdentity` value.
