// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	managed "latere.ai/x/managed-agents"
	managedmemory "latere.ai/x/managed-agents/memory"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/publish"
	"github.com/latere-ai/replichai/internal/verdict"
)

// validVerdict is a verdict.json that passes 021a's schema, so a harvest that
// recovers it produces a manifest recording findings present.
func validVerdict(t *testing.T) []byte {
	t.Helper()
	v := verdict.Verdict{
		Schema:     verdict.Schema,
		Verdict:    verdict.Partial,
		Summary:    "The classifier retrains and matches within 1.8 pp.",
		GradedAt:   time.Date(2026, 8, 22, 7, 59, 0, 0, time.UTC),
		Components: verdict.Components{Verified: 1, Blocked: 1, Total: 2},
		Boundary: []verdict.Claim{
			{Claim: "Accuracy 91.4%", Loop: verdict.Inner, Outcome: verdict.Verified, Citation: "§6.1", Evidence: "89.6% retrained"},
			{Claim: "Latency under 40 ms", Loop: verdict.Inner, Outcome: verdict.Blocked, Citation: "§6.3", Evidence: "needs the authors' tablet"},
			{Claim: "Preferred by participants", Loop: verdict.Outer, Outcome: verdict.NotScored, Citation: "§7", Evidence: "human study"},
		},
		Decisions: []verdict.Decision{{Question: "Window length?", Where: "§4", Assumed: "250 ms", Sensitivity: "±0.4 pp"}},
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal findings: %v", err)
	}
	return raw
}

// harvestSetup reproduces the incident: a repository holding nothing but what
// the system wrote before the run, and a session whose files exist only in the
// runtime's output directory because the agent never pushed.
func harvestSetup(t *testing.T, files map[string][]byte) (*engineRunner, job.Job, *job.MemStore) {
	t.Helper()
	forge := newRunnerForge([]string{"README.md", publish.IdentityFile})
	id := paper.Identity{Key: "arxiv:2603.01795", Title: "A Paper", Slug: "a-paper", Scheme: paper.SchemeArXiv}
	raw, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	forge.files[publish.IdentityFile] = append(raw, '\n')

	runtime := managedmemory.NewWithStore(nil, managedmemory.Config{Grade: managed.GradePassed, Files: files})
	store := job.NewMemStore()
	ctx := context.Background()
	j, _ := store.Enqueue(ctx, job.Submission{Principal: "p", Project: "project", Ref: "paper", Kind: job.KindAudit})
	_, _, _ = store.Claim(ctx, "worker")
	_ = store.Advance(ctx, j.ID, job.PhaseRunning, nil)
	j, _ = store.Get(ctx, j.ID)
	j.RepoName = "open-hai/a-paper-xyz"

	engine := &engineRunner{
		controller: managedController(t, "memory", runtime),
		method:     runnerMethod, jobs: store, workDir: t.TempDir(),
		gh: forge, owner: "open-hai", token: "mount-token",
	}
	return engine, j, store
}

func forgeOf(e *engineRunner) *fakeForge { return e.gh.(*fakeForge) }

// TestRetainHarvestsARunThatNeverPushed checks the incident end to end: the
// agent committed into the mount and never pushed, so the repository is empty,
// and its whole reproduction, including verdict.json, exists only as session
// files. The run must end with the reproduction in the repository and a
// manifest that records findings present.
func TestRetainHarvestsARunThatNeverPushed(t *testing.T) {
	engine, j, _ := harvestSetup(t, map[string][]byte{
		"REPRODUCIBILITY.md": []byte("# The verdict\n"),
		"verdict.json":       validVerdict(t),
		"src/run.py":         []byte("print('ran')\n"),
	})
	if err := engine.Execute(context.Background(), &j, func(provider.RunStatus) {}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	forge := forgeOf(engine)
	for _, p := range []string{"REPRODUCIBILITY.md", "verdict.json", "src/run.py"} {
		if _, ok := forge.files[p]; !ok {
			t.Errorf("%s was not recovered into the repository", p)
		}
	}
	// Harvest happens before finalize, so the manifest sees the recovered
	// findings and no UNVERIFIED.md note is written.
	var m publish.Manifest
	if err := json.Unmarshal(forge.files[publish.ManifestFile], &m); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.Findings == nil || !m.Findings.Present {
		t.Errorf("manifest findings = %+v, want present", m.Findings)
	}
	// The ordering 044b and 044d share: finalize reads a tree the harvest has
	// already contributed to, so a run recovered through the output directory is
	// not then reported as having committed nothing.
	if m.Findings == nil || !m.Findings.Reproduction {
		t.Errorf("a recovered run was recorded as an empty repository: %+v", m.Findings)
	}
	if _, ok := forge.files[publish.UnverifiedFile]; ok {
		t.Errorf("a recovered run wrote an UNVERIFIED note:\n%s", forge.files[publish.UnverifiedFile])
	}
}

// TestRetainHarvestRefusesThirdPartyData checks that the redistribution
// boundary holds on the harvest path too: a paper PDF in the output directory
// never reaches the repository.
func TestRetainHarvestRefusesThirdPartyData(t *testing.T) {
	engine, j, _ := harvestSetup(t, map[string][]byte{
		"paper.pdf":    []byte("%PDF-1.4"),
		"verdict.json": validVerdict(t),
	})
	if err := engine.Execute(context.Background(), &j, func(provider.RunStatus) {}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	forge := forgeOf(engine)
	if _, ok := forge.files["paper.pdf"]; ok {
		t.Error("paper.pdf was committed and must not have been")
	}
	if _, ok := forge.files["verdict.json"]; !ok {
		t.Error("verdict.json should still have been recovered")
	}
}
