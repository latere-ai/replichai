package publish

import (
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
)

// This file is the post-run half of the redistribution gate (006f). When the
// worker commits directly into a mounted repository, the publisher can no
// longer filter before the push — so Classify, the same gate 002 uses, runs
// afterwards as a verifier of what was actually committed, and as the guard on
// the one irreversible action: making a reproduction public.

// VerifyTree checks that every committed path is worker-written, and returns
// the paths that are not. An empty result means the bounded workspace held —
// only the reproduction's own files were committed. A non-empty result means
// third-party content leaked into the repository, which bars it from ever being
// made public.
//
// It reuses Classify, so the rule verified here is exactly the rule 002
// enforced: the gate's logic is one place, only its moment moves.
func VerifyTree(paths []string) []string {
	var leaked []string
	for _, p := range paths {
		if include, _ := Classify(Provenance{Path: p, Origin: originOf(p)}); !include {
			leaked = append(leaked, p)
		}
	}
	return leaked
}

// ManifestFor builds the manifest for a reproduction the worker committed into
// a mounted repository. Unlike PlanPublication, there is no file set to filter —
// the agent already committed — so the redistribution status comes from the
// verifier's result over the committed tree: clean iff nothing leaked.
func ManifestFor(id paper.Identity, repo, methodDigest string, verdict provider.Grade, created time.Time, clean bool) Manifest {
	review := ReviewClear
	if !clean {
		review = ReviewExcluded
	}
	return Manifest{
		Schema: SchemaVersion,
		Paper: ManifestPaper{
			Key:    id.Key,
			Scheme: id.Scheme,
			Title:  id.Title,
			Slug:   id.Slug,
		},
		Reproduction: Reproduction{
			Repo:         repo,
			Created:      created.UTC(),
			MethodDigest: methodDigest,
			Verdict:      verdict,
		},
		Redistribution: Redistribution{ThirdPartyData: !clean, Review: review},
	}
}

// EligibleForPublic reports whether a reproduction may be made public: its
// manifest must show the redistribution gate passed clean, with nothing
// withheld. It answers only the redistribution question — trust and identity
// (who may flip the switch) are the caller's concern (006c), never this
// package's.
func EligibleForPublic(m Manifest) (bool, string) {
	if m.Redistribution.ThirdPartyData {
		return false, "third-party data was withheld from this reproduction"
	}
	if m.Redistribution.Review != ReviewClear {
		return false, "the redistribution review is not clear (" + m.Redistribution.Review + ")"
	}
	return true, ""
}
