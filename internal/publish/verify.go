package publish

import (
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
)

// This file is the post-run half of the redistribution gate (006f). When the
// worker commits directly into a mounted repository, the publisher can no
// longer filter before the push — so Classify, the same gate 002 uses, runs
// afterwards as a verifier of what was actually committed, and as the guard on
// the one irreversible action: making a reproduction public.

// VerifyTree checks a mounted repository's committed tree for third-party
// content that should not have been committed, and returns the offending paths.
// An empty result means the bounded workspace held — the agent committed its
// reproduction and kept downloads out. A non-empty result bars the repository
// from ever being made public.
//
// Unlike 002's PlanPublication, this cannot allow-list: an agent-authored repo
// is a real software project — .gitignore, LICENSE, requirements.txt, tests,
// configs, generated results — not a fixed set of report files. So the method
// is the primary control (keep downloads out of the repo), and this is the
// backstop: it flags only what is almost certainly third-party — the paper, a
// dataset, model weights, an archive, or a leaked secret. Everything else is
// presumed the reproduction's own work.
func VerifyTree(paths []string) []string {
	var flagged []string
	for _, p := range paths {
		if isThirdPartyData(p) || isSecretName(p) {
			flagged = append(flagged, p)
		}
	}
	return flagged
}

// thirdPartyExts are file types that, committed into a reproduction repository,
// are almost always redistributed third-party content rather than the
// reproduction's own source or results: a paper or document, a bulk dataset,
// model weights, or an archive.
var thirdPartyExts = map[string]bool{
	".pdf": true, ".ps": true, ".doc": true, ".docx": true,
	".zip": true, ".tar": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true,
	".h5": true, ".hdf5": true, ".parquet": true, ".arrow": true, ".feather": true,
	".pt": true, ".pth": true, ".ckpt": true, ".safetensors": true, ".onnx": true, ".pb": true, ".weights": true,
	".sqlite": true, ".sqlite3": true, ".db": true, ".mdb": true,
}

func isThirdPartyData(p string) bool {
	dot := strings.LastIndex(p, ".")
	if dot < 0 {
		return false
	}
	return thirdPartyExts[strings.ToLower(p[dot:])]
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
