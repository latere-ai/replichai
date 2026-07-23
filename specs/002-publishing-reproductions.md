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

**How the manifest maps onto the port.** `paper.{key, scheme, title, slug}` comes straight from the `paper.Identity` of 001. `verdict` is `provider.Grade` — it already marshals to `"passed"`, so `audit.Result.Grade()` fills it directly, with no translation. `rate` is the one field the port does not produce as a value: the reproducibility rate is prose the worker writes into `REPRODUCIBILITY.md`, and it is deliberately run-dependent (a re-decomposed inner loop scored 40% over 5 components where the reference scored 79% over 7 — [004](004-go-port.md), commit `8aedf19`). The port checks conformance on the verdict, not the rate. So `rate` in the manifest is an **open mapping**: the publisher either parses the decomposed rate out of `REPRODUCIBILITY.md` and records it, or omits the field when it cannot. It is therefore modelled as a nil-able `*Rate`, never a fabricated zero. Deciding the parse is left to implementation; the manifest schema tolerates its absence.

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

Package `internal/publish`. The manifest is a typed struct so it serialises to
exactly the JSON above; `Rate` is a pointer because it may be absent (see the
open mapping). `PlanPublication` is pure; `Publish` acts through the `Forge`
seam.

```go
// Manifest is replication.json — the record every published repository carries
// and the only thing 003's registry reads.
type Manifest struct {
	Schema         int            `json:"schema"`
	Paper          ManifestPaper  `json:"paper"`
	Reproduction   Reproduction   `json:"reproduction"`
	Redistribution Redistribution `json:"redistribution"`
}

type Reproduction struct {
	Repo         string         `json:"repo"`
	Created      time.Time      `json:"created"`
	MethodDigest string         `json:"method_digest"`
	Verdict      provider.Grade `json:"verdict"`        // marshals to "passed"
	Rate         *Rate          `json:"rate,omitempty"` // nil when undecomposable
}

// PublishPlan is what will be published, computed before anything is created.
type PublishPlan struct {
	RepoName   string
	Include    []string          // worker-written paths cleared to publish
	Quarantine []QuarantinedFile // path + why it was withheld
	Manifest   Manifest
}

type QuarantinedFile struct {
	Path   string
	Reason string
}

// PlanPublication is pure: no network, no repository. It classifies artifacts,
// runs the redistribution gate, and builds the manifest.
func PlanPublication(result audit.Result, id paper.Identity) (PublishPlan, error)

// Publish carries a plan out against a forge. It is a preview (creates nothing)
// unless Live() is passed — publishing for real is the explicit act, the
// inverse of the old dry_run=True default. It returns the repository name.
func Publish(ctx context.Context, plan PublishPlan, forge Forge, opts ...PublishOption) (string, error)

func Live() PublishOption

// Forge is the seam over repository hosting, mirroring provider.Provider: one
// adapter per host, swappable and testable without a network. 003 extends it
// with the read side its registry needs, and adds a filesystem Forge so the
// whole audit→publish→registry loop runs offline.
type Forge interface {
	Create(ctx context.Context, name string, private bool) (Repo, error)
	Push(ctx context.Context, repo Repo, files []File) error
}
```

## Acceptance criteria

1. `PlanPublication` is pure: it touches no network and creates no repository. Every test of naming, quarantine, and manifest content runs against it.
2. `Publish` previews by default and creates nothing; publishing for real requires passing `Live()`.
3. Created repositories are private, and a test asserts visibility is never `public`.
4. Compiled residue and secrets never appear in a plan, asserted directly.
5. Any file not attributable to the worker lands in `quarantine`, and the resulting repository contains an `EXCLUDED.md` naming it.
6. An artifact whose provenance records a no-redistribution term is quarantined even if it is otherwise attributable — asserted with the gated-benchmark case as the fixture.
7. Publication is idempotent: re-publishing the same plan does not create a second repository.
8. A failure partway through leaves no half-populated public repository.

## Out of scope

Conflict detection across reproductions — that is 003. Making anything public.
