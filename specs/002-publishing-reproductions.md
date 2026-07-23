# 002 — Publishing reproductions

**Status:** draft
**Scope:** taking one completed audit's artifacts and publishing them as their own repository.
**Depends on:** 001

## Problem

An audit currently ends with files in a local directory. They should end up as a repository under the `open-hai` organisation, one per reproduction, so each is independently citable, forkable, and versioned.

## Decisions

**One reproduction, one repository.** Named `<slug>-<suffix>`, where `slug` comes from 001 and `suffix` is four lowercase base-32 characters. Every repository carries a suffix — including the first — so no reproduction is privileged and adding a second never requires renaming the first.

**Visibility: private.** All published repositories are created private. Making them public is a deliberate, separate decision per repository, taken by a human, and is gated on the licence check below.

**Every repository carries a manifest.** `replication.json` at the root:

```json
{
  "schema": 1,
  "paper": {
    "key": "10.1145/3772318.3791265",
    "scheme": "doi",
    "title": "PleaSQLarify: Visual Pragmatic Repair for Natural Language Database Querying",
    "slug": "pleasqlarify-visual-pragmatic-repair"
  },
  "reproduction": {
    "repo": "open-hai/pleasqlarify-visual-pragmatic-repair-a3f9",
    "created": "2026-07-23T09:14:00Z",
    "method_digest": "sha256:…",
    "verdict": "passed",
    "rate": {"value": 0.4, "numerator": 2.0, "denominator": 5}
  },
  "redistribution": {"third_party_data": false, "review": "clear"}
}
```

The manifest is what makes 003 possible without a separate database: the registry is derived by reading manifests.

**`method_digest`** pins which version of the method produced the reproduction. Two reproductions of one paper are only comparable if they were produced by the same method, and the digest is how you tell.

## The redistribution gate — mandatory, blocking

Publishing artifacts can republish data the reproduction was not licensed to redistribute. This is not hypothetical. Two findings from the first two audits:

- One paper's benchmark is password-gated and its terms say, verbatim, *"do not upload to GitHub."* The audit correctly recorded this; an automatic publisher would have violated it.
- The second audit copied third-party research data into its own output tree. That data happened to be openly licensed. The next one may not be.

Therefore **no artifact set is published until it passes a redistribution check**:

1. Classify every collected file: reproduction source (written by the worker), third-party data, or third-party document.
2. Anything not written by the worker is quarantined by default and excluded from the initial commit.
3. `SOURCES.md` already records each artifact's provenance and licence; the check reads it and requires an explicit clear-to-redistribute per third-party item.
4. Unresolved items are listed in `EXCLUDED.md` inside the published repository, with the reason, so the gap is documented rather than silent.

Failing open here means publishing someone else's gated data under your organisation's name. The gate fails closed.

## Interface

```python
@dataclass(frozen=True)
class PublishPlan:
    repo_name: str
    include: list[Path]
    quarantine: list[tuple[Path, str]]   # path, reason
    manifest: dict

def plan_publication(result: AuditResult, identity: PaperIdentity) -> PublishPlan: ...
def publish(plan: PublishPlan, *, forge: Forge, dry_run: bool = True) -> str: ...
```

`Forge` is a narrow interface over repository hosting (create, push, set visibility), so hosting is swappable and testable without network access — the same seam pattern the provider layer uses.

## Acceptance criteria

1. `plan_publication` is pure: it touches no network and creates no repository. Every test of naming, quarantine, and manifest content runs against it.
2. `publish` defaults to `dry_run=True`; publishing for real requires an explicit opt-in.
3. Created repositories are private, and a test asserts visibility is never `public`.
4. Compiled residue and secrets never appear in a plan, asserted directly.
5. Any file not attributable to the worker lands in `quarantine`, and the resulting repository contains an `EXCLUDED.md` naming it.
6. An artifact whose provenance records a no-redistribution term is quarantined even if it is otherwise attributable — asserted with the gated-benchmark case as the fixture.
7. Publication is idempotent: re-publishing the same plan does not create a second repository.
8. A failure partway through leaves no half-populated public repository.

## Out of scope

Conflict detection across reproductions — that is 003. Making anything public.
