package publish

import (
	"slices"
	"testing"
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
