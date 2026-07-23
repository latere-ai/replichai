# 003 — The reproduction registry

**Status:** draft
**Scope:** detecting that a paper has been reproduced before, and relating the reproductions.
**Depends on:** 001, 002

## Problem

A paper may be reproduced more than once — a sharpened method, a second opinion, a later attempt once data became available. The system needs to notice, and to relate the attempts rather than silently duplicating them.

## What "conflict" means here

Not a collision to prevent. A **prior reproduction to link to**.

Repeat reproductions are legitimate and often the point: re-running an audit after the method changes is how you tell whether the method improved. So the rule is:

- **Same paper identity → same canonical slug**, always. The first slug ever assigned to an identity wins, even if the paper's title later changes.
- **Every repository still gets its own random suffix**, so reproductions of one paper are siblings — `pleasqlarify-visual-pragmatic-repair-a3f9`, `…-7c21` — grouped by a shared prefix and by identity in their manifests.
- **The operator is told** before a repeat is created: what exists, when, under which method digest, with what verdict.

## Where the registry lives

**Derived, not stored.** Enumerate the organisation's repositories and read each `replication.json`. No second source of truth, so nothing can drift out of sync with reality; a repository deleted by hand simply disappears from the registry.

Cached locally with a short TTL for speed. The cache is disposable and never authoritative.

Rejected: a registry file inside `replichai`. It would need a commit per publication, would race between concurrent runs, and would silently disagree with the organisation the moment anyone acted outside the tool.

## Suffix collisions

Four base-32 characters is ~1M values. With a few thousand reproductions the birthday-collision probability is small but not zero, and the failure is user-visible. So: generate, check against the registry, regenerate on collision, up to five attempts, then widen to six characters. Deterministic, tested, no reliance on luck.

## Interface

Package `internal/registry`. The registry reads through the same `Forge` seam
002 defines, extended here with the read side — enumerate the organisation, read
one repository's manifest. Because the registry is derived, `Registry` holds no
state of its own beyond the forge and its cache; every method takes a context
and is safe to call concurrently.

```go
// PriorReproduction is one existing reproduction of a paper, read from a
// repository's manifest.
type PriorReproduction struct {
	Repo         string
	Created      time.Time
	MethodDigest string
	Verdict      provider.Grade
}

// Registry is derived from the forge, never stored.
type Registry struct { /* forge + disposable cache */ }

func New(forge Forge) *Registry

// Lookup returns every prior reproduction of an identity, matched on identity
// key alone — never on slug or title. Empty, not an error, when none exist.
func (r *Registry) Lookup(ctx context.Context, id paper.Identity) ([]PriorReproduction, error)

// CanonicalSlug returns the slug from the earliest reproduction of an identity.
// The bool is false when the identity has never been reproduced — absence is a
// value, the (T, bool) shape provider.Get uses, not a nil string.
func (r *Registry) CanonicalSlug(ctx context.Context, id paper.Identity) (string, bool, error)

// AllocateName returns "<canonical-slug>-<suffix>" with a suffix checked
// against the registry, retried on collision, widened to six characters after
// five attempts. Never returns a name that already exists.
func (r *Registry) AllocateName(ctx context.Context, id paper.Identity) (string, error)
```

**Forge, read side.** 002's `Forge` gains the two methods the registry needs.
The GitHub adapter enumerates the `open-hai` organisation; a **filesystem
adapter — a directory of `replication.json` files — is the local
implementation, so the full audit→publish→registry loop runs offline** with no
organisation and no network. This is the same adapter that acceptance criterion
7 already requires for testing; it is not new scope, it is the seam named.

```go
type Forge interface {
	// ... the write side from 002 ...
	List(ctx context.Context) ([]string, error)             // repository names
	Manifest(ctx context.Context, repo string) (Manifest, error)
}
```

## Acceptance criteria

1. `Lookup` matches on identity key, never on slug or title — asserted with the two-title PleaSQLarify case.
2. A paper whose title changed between reproductions still resolves to its canonical slug, taken from the earliest reproduction.
3. `AllocateName` returns `<canonical-slug>-<suffix>` and never returns a name that already exists.
4. An injected collision is retried and resolved; exhausting the retries widens the suffix rather than failing.
5. A repository with a missing or malformed `replication.json` is skipped with a warning, not a crash — the registry survives hand-edited repositories.
6. `Lookup` is read-only and safe to call concurrently.
7. The registry runs against the filesystem `Forge` with no network access — the same seam that makes the local loop possible.

## Operator flow

```
$ replichai audit https://arxiv.org/abs/2603.01795 --publish

  identity  doi:10.1145/3772318.3791265
  ! reproduced before:
      open-hai/pleasqlarify-visual-pragmatic-repair-a3f9
      2026-07-22 · method sha256:1f2e… · passed
    this run uses method sha256:9a7c… (changed)

  continue and create a sibling reproduction? [y/N]
```

A repeat under an *unchanged* method digest is the suspicious case — it means re-running the same method on the same paper — and the prompt says so.

## Out of scope

Comparing two reproductions of one paper (diffing rates, verdicts, hidden-decision lists). That is worth building and is a later spec.
