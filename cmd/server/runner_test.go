package main

import (
	"testing"

	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/publish"
)

// toRepro must render the verdict as its name ("passed"), not the uint8 code
// point string(Grade) produces — the bug that showed a control character in the
// API before.
func TestToReproVerdictIsAName(t *testing.T) {
	m := publish.Manifest{
		Paper:          publish.ManifestPaper{Title: "A Paper"},
		Reproduction:   publish.Reproduction{Repo: "open-hai/x", Verdict: provider.GradePassed},
		Redistribution: publish.Redistribution{Review: publish.ReviewClear},
	}
	got := toRepro("x", m)
	if got.Verdict != "passed" {
		t.Errorf("verdict = %q, want \"passed\"", got.Verdict)
	}
	if !got.CanPublish || got.Review != "clear" || got.Title != "A Paper" {
		t.Errorf("repro = %+v", got)
	}
}
