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

The method writes a fixed set of outputs — `README.md`, `REPRODUCIBILITY.md`, `src/`, `UNVERIFIED.md`, `SOURCES.md`. Everything in that set is worker-written; anything *else* in the output tree is third-party data that arrived by download, which is exactly how the second audit's mistake happened. And `SOURCES.md` records the paper's identity and the searches performed — not a per-file redistribution clearance — so there is no structured provenance to read.

The gate is therefore **structural and fails closed**:

1. Classify every collected file by origin: **worker-written** — the fixed report files and everything under `src/` — or **third-party** — everything else.
2. Only worker-written files are included. Every third-party file is quarantined, *always*: there is no auto-clearance path, because the structured provenance that would justify one does not exist yet.
3. Compiled residue and files with known-secret names never enter the plan at all — neither included nor quarantined. A content sweep at publish time is the backstop for a secret that slips through under an innocent name.
4. Quarantined files are listed in `EXCLUDED.md` in the published repository, with the reason, so the gap is documented rather than silent.

Failing open means publishing someone else's gated data under your organisation's name. The gate fails closed: the gated benchmark of the motivating case is third-party, so it is quarantined **by origin**, with no term-reading required.

**Deferred to a later spec:** clearing a specific third-party file for redistribution, and catching a no-redistribution term on a file that *is* worker-written, both need per-artifact provenance the method does not emit. Until that exists, third-party is never published — the safe default.

## Interface

Package `internal/publish`. The manifest serialises to exactly the JSON above;
`Rate` is a pointer because it may be absent (see the open mapping), and
`Created` is stamped at publish time, so a plan's manifest carries a zero time
until then. `PlanPublication` is pure and does no I/O — classification is by
path, never by content — so `Publish` alone reads files and touches the forge.

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
	Created      time.Time      `json:"created"`        // stamped at publish
	MethodDigest string         `json:"method_digest"`
	Verdict      provider.Grade `json:"verdict"`        // marshals to "passed"
	Rate         *Rate          `json:"rate,omitempty"` // nil when undecomposable
}

type Redistribution struct {
	ThirdPartyData bool   `json:"third_party_data"` // true if anything was quarantined
	Review         string `json:"review"`           // "clear" when nothing was
}

// Origin says who produced an artifact. The zero value is deliberately unknown
// so an unclassified file is never mistaken for worker-written.
type Origin uint8

const (
	OriginUnknown Origin = iota
	OriginWorker      // a fixed report file, or anything under src/
	OriginThirdParty  // everything else — downloaded, not written here
)

// Classify is the redistribution decision, pure and reusable: an artifact is
// published only when it is worker-written. 005's transcript gate reuses it.
func Classify(p Provenance) (include bool, reason string)

// Provenance is what the gate knows about one artifact: its path and origin.
type Provenance struct {
	Path   string
	Origin Origin
}

// PublishPlan is what will be published, computed before anything is created.
type PublishPlan struct {
	RepoName   string            // "<slug>-<suffix>", no org prefix
	SourceDir  string            // where Include paths are read from at publish
	Include    []string          // worker-written paths cleared to publish
	Quarantine []QuarantinedFile // path + why it was withheld
	Manifest   Manifest
}

type QuarantinedFile struct {
	Path   string
	Reason string
}

// PlanPublication is pure: no network, no repository, no clock. The suffix is
// supplied — from NewSuffix, or from 003's collision-checked allocation — so
// naming is deterministic and every naming/quarantine/manifest test runs here.
func PlanPublication(result audit.Result, id paper.Identity, suffix string) (PublishPlan, error)

// NewSuffix returns four lowercase base-32 characters. 003 wraps this with a
// collision check against the registry; standalone, one draw is enough.
func NewSuffix() (string, error)

// Published is the composable result later steps act on — 006c's collaborator
// invitation takes Published.Repo.
type Published struct {
	Repo    Repo // the forge handle: owner, name, URL
	Preview bool // Live() was not passed; nothing was created
	Existed bool // the repository already existed (idempotent re-publish)
}

// Publish previews by default and creates nothing; Live() makes it real. When
// live it creates the private repository if absent, then pushes — pushing even
// when the repository already existed, so a run that failed mid-push completes
// on retry rather than leaving a permanent gap.
func Publish(ctx context.Context, plan PublishPlan, forge Forge, opts ...PublishOption) (Published, error)

func Live() PublishOption               // publish for real
func WithClock(now func() time.Time) PublishOption // the created stamp; default time.Now

// Forge is the seam over repository hosting, mirroring provider.Provider: one
// adapter per host, swappable and testable without a network. 003 extends it
// with the read side its registry needs; a filesystem Forge runs the whole
// audit→publish→registry loop offline.
type Forge interface {
	Exists(ctx context.Context, name string) (bool, error)
	Create(ctx context.Context, name string, private bool) (Repo, error)
	Push(ctx context.Context, repo Repo, files []File) error
}
```

## Acceptance criteria

1. `PlanPublication` is pure: no network, no repository, no clock. Every test of naming, quarantine, and manifest content runs against it — asserting all manifest fields except the publish-stamped `created`.
2. `Publish` previews by default and creates nothing; publishing for real requires passing `Live()`.
3. Created repositories are private, and a test asserts visibility is never `public`.
4. Compiled residue and known-secret filenames never appear in a plan — asserted directly, and named honestly as a filename check, not a proof of secret-freedom. A publish-time content sweep is the backstop.
5. Every file not written by the worker is quarantined and named, with a reason, in the `EXCLUDED.md` of the published repository — asserted with the gated-benchmark case, which is quarantined *by origin*.
6. The include set is exactly the worker-written files (the fixed reports and `src/`); the classifier is fail-closed, and a test asserts nothing outside that set is ever included. Clearing a third-party file, or catching a term on a worker-written one, needs structured provenance and is out of scope here.
7. Publication is idempotent *for the same plan*: re-publishing it does not create a second repository, and completes a push that failed partway. The name carries a random suffix, so re-*planning* yields a new name — cross-run "same paper, one repository" is 003's job, not this.
8. A failure partway through leaves nothing public: repositories are private, and a re-publish of the same plan converges rather than forking.

## Out of scope

Conflict detection across reproductions — that is 003. Making anything public.
