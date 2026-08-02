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

// A manifest barred because its transcript leaked a secret must not be publishable
// — the new review reason (009 commit 1) reads as not-clear at the CanPublish
// seam, so the public flip refuses it just as a tree leak does.
func TestToReproSecretInTranscriptCannotPublish(t *testing.T) {
	m := publish.Manifest{
		Reproduction:   publish.Reproduction{Repo: "open-hai/x", Verdict: provider.GradePassed},
		Redistribution: publish.Redistribution{Review: publish.ReviewSecretInTranscript},
	}
	if got := toRepro("x", m); got.CanPublish {
		t.Errorf("a secret_in_transcript manifest must not be publishable; repro = %+v", got)
	}
}
