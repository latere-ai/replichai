// Package publish turns one completed audit's artifacts into a published
// reproduction repository.
//
// It runs in two steps with a hard line between them. PlanPublication is pure —
// it classifies artifacts through a fail-closed redistribution gate and builds
// the manifest, touching no network, no repository, and no clock. Publish then
// carries a plan out against a Forge, the swappable seam over repository
// hosting, and is the only part that reads files or creates anything.
package publish

import (
	"reflect"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
)

// SchemaVersion is the manifest schema this package writes and 003 reads.
const SchemaVersion = 1

// Manifest is replication.json — the record every published repository carries
// at its root, and the only thing 003's registry reads. It serialises to the
// exact shape 003 expects, so the two never drift.
type Manifest struct {
	Schema         int            `json:"schema"`
	Paper          ManifestPaper  `json:"paper"`
	Reproduction   Reproduction   `json:"reproduction"`
	Redistribution Redistribution `json:"redistribution"`
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
// is a truthful "not recorded", never a fabricated zero.
type Rate struct {
	Value       float64 `json:"value"`
	Numerator   float64 `json:"numerator"`
	Denominator int     `json:"denominator"`
}

// Redistribution records what the gate did: whether anything was withheld, and
// the review state. Under the fail-closed structural gate, ThirdPartyData is
// true whenever any file was quarantined.
type Redistribution struct {
	ThirdPartyData bool   `json:"third_party_data"`
	Review         string `json:"review"`
}

// Review states for Redistribution.Review.
const (
	ReviewClear              = "clear"                // nothing was withheld
	ReviewExcluded           = "excluded"             // third-party files were quarantined from the tree
	ReviewSecretInTranscript = "secret_in_transcript" // a secret in the transcript bars the flip (005/009)
)

// SameManifest reports whether two manifests are materially equal, ignoring the
// created timestamp. Re-finalizing a reproduction whose facts have not changed
// then rewrites nothing, so it adds no redundant commit.
func SameManifest(a, b Manifest) bool {
	a.Reproduction.Created = time.Time{}
	b.Reproduction.Created = time.Time{}
	return reflect.DeepEqual(a, b)
}
