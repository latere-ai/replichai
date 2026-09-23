---
title: "The reproduction registry"
status: implemented
---

# 003 — The reproduction registry

**Status:** implemented (`413a635`)
**Scope:** detecting that a paper has been reproduced before, and relating the reproductions.
**Depends on:** 001, 002

## Problem

A paper may be reproduced more than once: with a sharpened method, for a second opinion, or in a later attempt once data became available. The system needs to notice, and to relate the attempts rather than silently duplicating them.

## What "conflict" means here

Not a collision to prevent, but a **prior reproduction to link to**.

Repeat reproductions are legitimate and often the point: re-running an audit after the method changes is how you tell whether the method improved. So the rule is:

- **Same paper identity → same canonical slug**, always. The first slug ever assigned to an identity wins, even if the paper's title later changes.
- **Every repository still gets its own random suffix**, so reproductions of one paper are siblings (`pleasqlarify-visual-pragmatic-repair-a3f9`, `…-7c21`), grouped by a shared prefix and by identity in their manifests.
- **The operator is told** before a repeat is created: what exists, when, under which method digest, with what verdict.

## Where the registry lives

**Derived, not stored.** Enumerate the organization's repositories and read each `replication.json`. There is no second source of truth, so nothing can drift out of sync with the organization; a repository deleted by hand disappears from the registry.

Cached locally with a short TTL for speed. The cache is disposable and never authoritative.

Rejected: a registry file inside `replichai`. It would need a commit per publication, would race between concurrent runs, and would silently disagree with the organization as soon as anyone acted outside the tool.

## Suffix collisions

Four base-32 characters is about 1M values. With a few thousand reproductions the birthday-collision probability is small but not zero, and the failure is user-visible. So: generate, check against the registry, regenerate on collision, up to five attempts, then widen to six characters. Deterministic, tested, and not dependent on luck.

## Interface

Package `internal/registry`. The registry reads through a narrow `Source` seam
(below): enumerate the organization, read one repository's manifest. Because
the registry is derived, `Registry` holds no state of its own beyond the source
and its cache; every method takes a context and is safe to call concurrently.

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

**Read seam.** The registry reads through its own narrow `Source`, not 002's
write-side `Forge`: 006f's real GitHub access went through `internal/github`
(list, read a file), not `publish.Forge`, so the registry wraps that. A GitHub
source enumerates the `open-hai` organization and reads each manifest via
`internal/github`; a **filesystem source (a directory of `replication.json`
files) is the local implementation, so the full audit→publish→registry loop
runs offline** with no organization and no network (acceptance criterion 7).

```go
// Source enumerates published reproductions and reads their manifests.
type Source interface {
	List(ctx context.Context) ([]string, error)                  // repository names
	Manifest(ctx context.Context, repo string) (publish.Manifest, error)
}
```

The manifest type is `publish.Manifest` (002), so the registry never redefines
the record it reads.

## Acceptance criteria

1. `Lookup` matches on identity key, never on slug or title, asserted with the two-title PleaSQLarify case.
2. A paper whose title changed between reproductions still resolves to its canonical slug, taken from the earliest reproduction.
3. `AllocateName` returns `<canonical-slug>-<suffix>` and never returns a name that already exists.
4. An injected collision is retried and resolved; exhausting the retries widens the suffix rather than failing.
5. A repository with a missing or malformed `replication.json` is skipped with a warning, not a crash, so the registry survives hand-edited repositories.
6. `Lookup` is read-only and safe to call concurrently.
7. The registry runs against the filesystem `Forge` with no network access, the same seam that makes the local loop possible.

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

A repeat under an *unchanged* method digest is the suspicious case, because it means re-running the same method on the same paper, and the prompt says so.

## Out of scope

Comparing two reproductions of one paper (diffing rates, verdicts, hidden-decision lists). That is worth building and is a later spec.

## Outcome

Implemented in `internal/registry` across five commits (`064a24b` the Source reconciliation, `a0b0038` `github.ListRepos`, `3deab5f` the registry core, `ef56dc5` the GitHub source, `413a635` the `audit --publish` wiring). All seven acceptance criteria covered by tests that run offline against a fake source and the filesystem source; 87% coverage, race-clean.

Built as specified, with one reconciliation folded back into the Interface: the registry reads through its own narrow `Source` (list, and read a manifest, returning `publish.Manifest`), **not** 002's write-side `Forge`. 006f's real GitHub access went through `internal/github`, so the registry wraps that (`GitHubSource`) rather than a `GitHubForge` that never existed; a `FSSource` reads FSForge's directory layout for the offline loop.

The operator flow landed in `audit --publish`: before a sibling is created, it warns about every prior reproduction (date, method digest, verdict, flagging a same-method re-run) and allocates the name through `AllocateName`, which combines the canonical slug from the earliest reproduction with a collision-checked suffix that widens from 4 to 6 characters, so repeats of one paper share a slug and never collide. The non-interactive `--detach` flow prints the warning rather than prompting, because a blocking `[y/N]` prompt would not fit the async model.
