package publish

import (
	"slices"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
)

func TestVerifyTreeCleanWhenOnlyWorkerFiles(t *testing.T) {
	clean := []string{"README.md", "REPRODUCIBILITY.md", "SOURCES.md", "UNVERIFIED.md",
		"src/main.py", "src/lib/util.py"}
	if leaked := VerifyTree(clean); len(leaked) != 0 {
		t.Errorf("a bounded workspace should verify clean, leaked %v", leaked)
	}
}

func TestVerifyTreeFlagsCommittedThirdParty(t *testing.T) {
	tree := []string{"README.md", "src/main.py", "data/benchmark.csv", "paper.pdf"}
	leaked := VerifyTree(tree)
	slices.Sort(leaked)
	want := []string{"data/benchmark.csv", "paper.pdf"}
	if !slices.Equal(leaked, want) {
		t.Errorf("leaked = %v, want %v", leaked, want)
	}
}

func TestManifestForReflectsCleanliness(t *testing.T) {
	id := paper.Identity{Key: "arxiv:2302.03714", Scheme: paper.SchemeArXiv, Title: "A Paper", Slug: "a-paper"}
	when := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC)

	clean := ManifestFor(id, "open-hai/a-paper-a3f9", "sha256:x", provider.GradePassed, when, true)
	if clean.Redistribution.ThirdPartyData || clean.Redistribution.Review != ReviewClear {
		t.Errorf("clean manifest = %+v", clean.Redistribution)
	}
	if clean.Reproduction.Repo != "open-hai/a-paper-a3f9" || !clean.Reproduction.Created.Equal(when) {
		t.Errorf("reproduction = %+v", clean.Reproduction)
	}
	if clean.Paper.Scheme != paper.SchemeArXiv {
		t.Errorf("paper scheme = %v", clean.Paper.Scheme)
	}

	dirty := ManifestFor(id, "open-hai/a-paper-a3f9", "sha256:x", provider.GradePassed, when, false)
	if !dirty.Redistribution.ThirdPartyData || dirty.Redistribution.Review != ReviewExcluded {
		t.Errorf("dirty manifest = %+v", dirty.Redistribution)
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
