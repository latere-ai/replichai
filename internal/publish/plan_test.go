// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"errors"
	"slices"
	"testing"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
)

func pleasQLarify() paper.Identity {
	return paper.Identity{
		Key:    "10.1145/3772318.3791265",
		Scheme: paper.SchemeDOI,
		Title:  "PleaSQLarify: Visual Pragmatic Repair for Natural Language Database Querying",
		Slug:   "pleasqlarify-visual-pragmatic-repair",
	}
}

func passingResult(paths ...string) audit.Result {
	arts := make([]provider.Artifact, len(paths))
	for i, p := range paths {
		arts[i] = provider.Artifact{Path: p}
	}
	return audit.Result{
		ArtifactDir:  "/nonexistent/artifacts", // proves PlanPublication reads nothing
		MethodDigest: "sha256:abc",
		Status:       provider.RunStatus{Verdict: &provider.Verdict{Grade: provider.GradePassed}},
		Artifacts:    arts,
	}
}

// TestPlanClassifiesTheFullArtifactSet uses the motivating fixture: worker
// reports and src/, a gated benchmark and the paper PDF as third-party files,
// plus a compiled file and a secret.
func TestPlanClassifiesTheFullArtifactSet(t *testing.T) {
	result := passingResult(
		"README.md", "REPRODUCIBILITY.md", "SOURCES.md", "UNVERIFIED.md",
		"src/main.py", "src/lib/util.py",
		"data/benchmark.csv",    // third-party: the gated benchmark case
		"paper.pdf",             // third-party
		"src/__pycache__/x.pyc", // compiled, so dropped
		".env",                  // secret, so dropped
	)
	plan, err := PlanPublication(result, pleasQLarify(), "a3f9")
	if err != nil {
		t.Fatalf("PlanPublication: %v", err)
	}

	// AC6: the include set is exactly the worker-written files.
	wantInclude := []string{"README.md", "REPRODUCIBILITY.md", "SOURCES.md",
		"UNVERIFIED.md", "src/main.py", "src/lib/util.py"}
	got := slices.Clone(plan.Include)
	slices.Sort(got)
	slices.Sort(wantInclude)
	if !slices.Equal(got, wantInclude) {
		t.Errorf("Include = %v, want exactly the worker files %v", plan.Include, wantInclude)
	}

	// AC5: third-party files are quarantined, with a reason.
	quarantined := map[string]string{}
	for _, q := range plan.Quarantine {
		quarantined[q.Path] = q.Reason
	}
	for _, p := range []string{"data/benchmark.csv", "paper.pdf"} {
		if quarantined[p] == "" {
			t.Errorf("%s should be quarantined with a reason", p)
		}
	}

	// AC4: compiled files and secrets appear in neither set.
	for _, p := range []string{"src/__pycache__/x.pyc", ".env"} {
		if slices.Contains(plan.Include, p) || quarantined[p] != "" {
			t.Errorf("%s must never appear in a plan", p)
		}
	}
}

func TestPlanManifestContent(t *testing.T) {
	result := passingResult("README.md", "src/main.py", "data/x.csv")
	plan, err := PlanPublication(result, pleasQLarify(), "a3f9")
	if err != nil {
		t.Fatalf("PlanPublication: %v", err)
	}
	m := plan.Manifest
	if plan.RepoName != "pleasqlarify-visual-pragmatic-repair-a3f9" {
		t.Errorf("RepoName = %q", plan.RepoName)
	}
	if m.Reproduction.Repo != "open-hai/pleasqlarify-visual-pragmatic-repair-a3f9" {
		t.Errorf("Repo = %q", m.Reproduction.Repo)
	}
	if m.Reproduction.Verdict != provider.GradePassed {
		t.Errorf("Verdict = %v, want passed", m.Reproduction.Verdict)
	}
	if m.Reproduction.MethodDigest != "sha256:abc" {
		t.Errorf("MethodDigest = %q", m.Reproduction.MethodDigest)
	}
	if m.Reproduction.Rate != nil {
		t.Errorf("Rate should be nil (open mapping), got %v", m.Reproduction.Rate)
	}
	if !m.Reproduction.Created.IsZero() {
		t.Error("Created must be zero until publish stamps it")
	}
	// A quarantined file was present, so the review reflects it.
	if !m.Redistribution.ThirdPartyData || m.Redistribution.Review != ReviewExcluded {
		t.Errorf("redistribution = %+v, want excluded", m.Redistribution)
	}
}

func TestPlanClearWhenNothingWithheld(t *testing.T) {
	result := passingResult("README.md", "src/main.py")
	plan, _ := PlanPublication(result, pleasQLarify(), "a3f9")
	if plan.Manifest.Redistribution.ThirdPartyData || plan.Manifest.Redistribution.Review != ReviewClear {
		t.Errorf("redistribution = %+v, want clear", plan.Manifest.Redistribution)
	}
	if len(plan.Quarantine) != 0 {
		t.Errorf("nothing should be quarantined, got %v", plan.Quarantine)
	}
}

func TestPlanRejectsAnEmptySuffix(t *testing.T) {
	if _, err := PlanPublication(passingResult("README.md"), pleasQLarify(), ""); !errors.Is(err, ErrNoName) {
		t.Errorf("err = %v, want ErrNoName", err)
	}
}

// TestPlanIncludesEveryMethodDeliverable checks that every deliverable the
// method asks the worker to write survives the offline publish. verdict.json
// was once withheld because the gate's allowlist was a second, hand-maintained
// copy of the method's list. The plan now derives it from method.Deliverables,
// and this test checks that the two cannot drift.
func TestPlanIncludesEveryMethodDeliverable(t *testing.T) {
	deliverables := method.Deliverables()
	result := passingResult(append(slices.Clone(deliverables), "src/main.py", "paper.pdf")...)
	plan, err := PlanPublication(result, pleasQLarify(), "a3f9")
	if err != nil {
		t.Fatalf("PlanPublication: %v", err)
	}
	for _, name := range deliverables {
		if !slices.Contains(plan.Include, name) {
			t.Errorf("%s is a method deliverable but the plan withheld it; quarantine = %v", name, plan.Quarantine)
		}
	}
	if !slices.Contains(plan.Include, "verdict.json") {
		t.Error("verdict.json must be published: it is the finding as data")
	}
	for _, q := range plan.Quarantine {
		if q.Path != "paper.pdf" {
			t.Errorf("quarantined %s, which is worker-written", q.Path)
		}
	}
}
