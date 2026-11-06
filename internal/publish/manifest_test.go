// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package publish

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
)

// TestManifestSerializesToTheSpecShape asserts the manifest marshals to exactly
// the JSON shape 002 documents, for every field except created, which is set at
// publish time.
func TestManifestSerializesToTheSpecShape(t *testing.T) {
	m := Manifest{
		Schema: SchemaVersion,
		Paper: ManifestPaper{
			Key:    "10.1145/3772318.3791265",
			Scheme: paper.SchemeDOI,
			Title:  "PleaSQLarify: Visual Pragmatic Repair for Natural Language Database Querying",
			Slug:   "pleasqlarify-visual-pragmatic-repair",
		},
		Reproduction: Reproduction{
			Repo:         "open-hai/pleasqlarify-visual-pragmatic-repair-a3f9",
			MethodDigest: "sha256:abc",
			Verdict:      provider.GradePassed,
			Rate:         &Rate{Value: 0.4, Numerator: 2, Denominator: 5},
		},
		Redistribution: Redistribution{ThirdPartyData: false, Review: ReviewClear},
	}

	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got["schema"] != float64(1) {
		t.Errorf("schema = %v, want 1", got["schema"])
	}
	paperObj := got["paper"].(map[string]any)
	if paperObj["scheme"] != "doi" {
		t.Errorf("paper.scheme = %v, want doi", paperObj["scheme"])
	}
	if paperObj["slug"] != "pleasqlarify-visual-pragmatic-repair" {
		t.Errorf("paper.slug = %v", paperObj["slug"])
	}
	repro := got["reproduction"].(map[string]any)
	if repro["verdict"] != "passed" {
		t.Errorf("reproduction.verdict = %v, want passed", repro["verdict"])
	}
	rate := repro["rate"].(map[string]any)
	if rate["value"] != 0.4 || rate["denominator"] != float64(5) {
		t.Errorf("reproduction.rate = %v", rate)
	}
	redist := got["redistribution"].(map[string]any)
	if redist["third_party_data"] != false || redist["review"] != "clear" {
		t.Errorf("redistribution = %v", redist)
	}
}

// TestRateOmittedWhenNil checks that a missing rate is omitted rather than
// written as a zero object.
func TestRateOmittedWhenNil(t *testing.T) {
	raw, err := json.Marshal(Reproduction{Repo: "open-hai/x", Verdict: provider.GradePassed})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(raw); strings.Contains(got, "rate") {
		t.Errorf("a nil rate should be omitted, got %s", got)
	}
}
