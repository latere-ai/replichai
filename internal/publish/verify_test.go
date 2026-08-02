package publish

import (
	"slices"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
)

func TestVerifyTreeAcceptsARealReproductionRepo(t *testing.T) {
	// A normal software repo: reports, source, tests, config, deps, generated
	// results — none of it third-party. This is the case that must not
	// false-positive (an agent-authored repo is not four report files).
	clean := []string{
		"README.md", "REPRODUCIBILITY.md", "SOURCES.md", "UNVERIFIED.md",
		".gitignore", "LICENSE", "requirements.txt", "pyproject.toml", "Makefile",
		"src/main.py", "src/lib/util.py", "tests/test_main.py",
		"configs/default.yaml", "results/accuracy.csv", "figures/fig1.png",
	}
	if flagged := VerifyTree(clean); len(flagged) != 0 {
		t.Errorf("a real reproduction repo must verify clean, flagged %v", flagged)
	}
}

func TestVerifyTreeFlagsThirdPartyDataAndSecrets(t *testing.T) {
	tree := []string{
		"README.md", "src/main.py", // the reproduction
		"paper.pdf",            // the paper
		"data/imagenet.tar.gz", // a dataset archive
		"models/weights.pt",    // model weights
		"cache/store.sqlite",   // a database
		".env",                 // a secret
	}
	flagged := VerifyTree(tree)
	slices.Sort(flagged)
	want := []string{".env", "cache/store.sqlite", "data/imagenet.tar.gz", "models/weights.pt", "paper.pdf"}
	if !slices.Equal(flagged, want) {
		t.Errorf("flagged = %v, want %v", flagged, want)
	}
}

func TestManifestForReflectsCleanliness(t *testing.T) {
	id := paper.Identity{Key: "arxiv:2302.03714", Scheme: paper.SchemeArXiv, Title: "A Paper", Slug: "a-paper"}
	when := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)

	clean := ManifestFor(id, "open-hai/a-paper-a3f9", "sha256:x", provider.GradePassed, when, RedistributionFrom(true, true))
	if clean.Redistribution.ThirdPartyData || clean.Redistribution.Review != ReviewClear {
		t.Errorf("clean manifest = %+v", clean.Redistribution)
	}
	if clean.Reproduction.Repo != "open-hai/a-paper-a3f9" || !clean.Reproduction.Created.Equal(when) {
		t.Errorf("reproduction = %+v", clean.Reproduction)
	}
	if clean.Paper.Scheme != paper.SchemeArXiv {
		t.Errorf("paper scheme = %v", clean.Paper.Scheme)
	}

	dirty := ManifestFor(id, "open-hai/a-paper-a3f9", "sha256:x", provider.GradePassed, when, RedistributionFrom(false, true))
	if !dirty.Redistribution.ThirdPartyData || dirty.Redistribution.Review != ReviewExcluded {
		t.Errorf("dirty manifest = %+v", dirty.Redistribution)
	}
}

// A secret in the transcript, on an otherwise-clean tree, is barred under its own
// review reason — never misreported as third-party data — and is not eligible
// for the public flip. A tree leak still takes precedence over a transcript one.
func TestRedistributionFromClassifiesEachBar(t *testing.T) {
	cases := []struct {
		treeClean, transcriptClean bool
		wantData                   bool
		wantReview                 string
	}{
		{true, true, false, ReviewClear},
		{false, true, true, ReviewExcluded},
		{true, false, false, ReviewSecretInTranscript},
		{false, false, true, ReviewExcluded}, // tree leak wins
	}
	for _, c := range cases {
		got := RedistributionFrom(c.treeClean, c.transcriptClean)
		if got.ThirdPartyData != c.wantData || got.Review != c.wantReview {
			t.Errorf("RedistributionFrom(%v,%v) = %+v, want data=%v review=%q",
				c.treeClean, c.transcriptClean, got, c.wantData, c.wantReview)
		}
		id := paper.Identity{Key: "k", Scheme: paper.SchemeArXiv, Slug: "s"}
		m := ManifestFor(id, "open-hai/s-a3f9", "sha256:x", provider.GradePassed, time.Now(), got)
		eligible, _ := EligibleForPublic(m)
		if want := c.wantReview == ReviewClear; eligible != want {
			t.Errorf("EligibleForPublic for review %q = %v, want %v", c.wantReview, eligible, want)
		}
	}
}

func TestSameManifestIgnoresCreated(t *testing.T) {
	id := paper.Identity{Key: "arxiv:2302.03714", Scheme: paper.SchemeArXiv, Slug: "s"}
	a := ManifestFor(id, "open-hai/s-a3f9", "sha256:x", provider.GradePassed,
		time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC), RedistributionFrom(true, true))
	b := ManifestFor(id, "open-hai/s-a3f9", "sha256:x", provider.GradePassed,
		time.Date(2026, 7, 25, 11, 30, 0, 0, time.UTC), RedistributionFrom(true, true)) // different time only
	if !SameManifest(a, b) {
		t.Error("manifests differing only in created should be the same")
	}

	c := ManifestFor(id, "open-hai/s-a3f9", "sha256:x", provider.GradePassed, a.Reproduction.Created, RedistributionFrom(false, true))
	if SameManifest(a, c) {
		t.Error("a different redistribution review must not be the same")
	}
}

func TestEligibleForPublic(t *testing.T) {
	clear := Manifest{Redistribution: Redistribution{ThirdPartyData: false, Review: ReviewClear}}
	if ok, reason := EligibleForPublic(clear); !ok {
		t.Errorf("a clear manifest should be eligible, got %q", reason)
	}

	excluded := Manifest{Redistribution: Redistribution{ThirdPartyData: true, Review: ReviewExcluded}}
	if ok, reason := EligibleForPublic(excluded); ok || reason == "" {
		t.Errorf("a manifest that withheld data must be refused with a reason, got ok=%v reason=%q", ok, reason)
	}
}
