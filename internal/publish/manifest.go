// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package publish turns one completed audit's artifacts into a published
// reproduction repository.
//
// It runs in two separate steps. PlanPublication is pure: it classifies
// artifacts through a fail-closed redistribution gate and builds the manifest,
// and it touches no network, no repository, and no clock. Publish then carries
// a plan out against a Forge, the replaceable interface over repository
// hosting, and is the only part that reads files or creates anything.
package publish

import (
	"reflect"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
)

// SchemaVersion is the manifest schema this package writes and 003 reads.
//
// It stayed at 1 when Findings was added (021a). The field is optional and
// additive, no reader checks the number, and 002 fixes this shape. Bumping it
// would have rewritten every published manifest on its next finalize to signal
// a change no reader could act on.
const SchemaVersion = 1

// Manifest is replication.json, the record every published repository carries
// at its root and the only file 003's registry reads. It serialises to the
// exact shape 003 expects, so the two do not drift.
type Manifest struct {
	Schema         int            `json:"schema"`
	Paper          ManifestPaper  `json:"paper"`
	Reproduction   Reproduction   `json:"reproduction"`
	Redistribution Redistribution `json:"redistribution"`
	Findings       *Findings      `json:"findings,omitempty"`
}

// Findings records what the system made of the agent's verdict.json (021a). It
// is a record and not a gate: the findings are the agent's claim, the
// transcript is the evidence, and the manifest is the system's statement.
// Making the system's statement depend on whether the agent's JSON parses
// would invert all three, so a missing or invalid file is recorded here and
// the run still publishes.
//
// A nil Findings means the manifest predates 021a. That is not the same as a
// run that had no findings, which carries Present false and a reason.
type Findings struct {
	Present  bool     `json:"present"`
	Problems []string `json:"problems,omitempty"` // why it could not be read, when it could not
	// Reproduction reports whether the repository holds a reproduction at all,
	// meaning any path the run committed itself. A run that pushed nothing
	// still gets a manifest, because the run happened and cost money; the
	// manifest must not imply that a reproduction is there to read (044d).
	Reproduction bool `json:"reproduction"`
}

// ManifestPaper is the paper's identity, straight from paper.Identity (001).
type ManifestPaper struct {
	Key    string       `json:"key"`
	Scheme paper.Scheme `json:"scheme"`
	Title  string       `json:"title"`
	Slug   string       `json:"slug"`
}

// Reproduction is the record of this reproduction. Created is stamped at
// publish time, so a plan's manifest carries a zero time until then.
type Reproduction struct {
	Repo         string         `json:"repo"`
	Created      time.Time      `json:"created"`
	MethodDigest string         `json:"method_digest"`
	Verdict      provider.Grade `json:"verdict"`
	Rate         *Rate          `json:"rate,omitempty"`
}

// Rate is the decomposed reproducibility rate, present only when it could be
// parsed out of the worker's report. It is run-dependent (004), so a nil Rate
// means not recorded rather than a fabricated zero.
type Rate struct {
	Value       float64 `json:"value"`
	Numerator   float64 `json:"numerator"`
	Denominator int     `json:"denominator"`
}

// Redistribution records what the gate did: whether anything was withheld, and
// the review state. Under the fail-closed structural gate, ThirdPartyData is
// true whenever any file was quarantined.
type Redistribution struct {
	ThirdPartyData bool        `json:"third_party_data"`
	Review         ReviewState `json:"review"`
}

// ReviewState is the outcome of the redistribution review.
type ReviewState string

// Review states for Redistribution.Review.
const (
	ReviewClear              ReviewState = "clear"                // nothing was withheld
	ReviewExcluded           ReviewState = "excluded"             // third-party files were quarantined from the tree
	ReviewSecretInTranscript ReviewState = "secret_in_transcript" // a secret in the transcript bars the flip (005/009)
)

// SameManifest reports whether two manifests are materially equal, ignoring the
// created timestamp. Re-finalizing a reproduction whose facts have not changed
// then rewrites nothing, so it adds no redundant commit.
func SameManifest(a, b Manifest) bool {
	a.Reproduction.Created = time.Time{}
	b.Reproduction.Created = time.Time{}
	return reflect.DeepEqual(a, b)
}
