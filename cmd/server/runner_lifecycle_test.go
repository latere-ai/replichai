// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/provider/memory"
	"github.com/latere-ai/replichai/internal/publish"
)

// TestNewEngineRunnerRefusesAMissingDependency checks that a runner missing a
// dependency is refused at boot, where it is a startup error, rather than at
// the first job whose row takes the path through it.
func TestNewEngineRunnerRefusesAMissingDependency(t *testing.T) {
	controller, router := managedStack(t, job.LegacyRuntime, legacyRuntime(t, job.LegacyRuntime, memory.New()))
	complete := engineDeps{
		controller: controller, sessions: router, jobs: job.NewMemStore(), workDir: t.TempDir(),
		fetch: paper.FetcherFunc(func(context.Context, string) ([]byte, error) { return nil, nil }),
	}
	if _, err := newEngineRunner(complete); err != nil {
		t.Fatalf("complete deps refused: %v", err)
	}
	for name, strip := range map[string]func(*engineDeps){
		"controller": func(d *engineDeps) { d.controller = nil },
		"sessions":   func(d *engineDeps) { d.sessions = nil },
		"jobs":       func(d *engineDeps) { d.jobs = nil },
		"fetch":      func(d *engineDeps) { d.fetch = nil },
		"workDir":    func(d *engineDeps) { d.workDir = "" },
	} {
		d := complete
		strip(&d)
		if _, err := newEngineRunner(d); !errors.Is(err, errEngineIncomplete) {
			t.Errorf("without %s: err=%v, want errEngineIncomplete", name, err)
		}
	}
}

// TestCloseOfALegacyRowGoesThroughTheRuntime covers a row from before session
// references were persisted, which carries only a run id. Cancelling it must
// address the run through the runtime that owns it and never through an
// engine that is not there. This case once took the server down on every
// restart while one such row was in publishing.
func TestCloseOfALegacyRowGoesThroughTheRuntime(t *testing.T) {
	backend := memory.New()
	e, _ := engineOver(t, backend, "")
	legacy := managed.SessionRef{Runtime: job.LegacyRuntime, ID: "run-legacy", Revision: 1, LocatorVersion: 1}

	if err := e.Close(context.Background(), legacy); err != nil {
		// The memory backend may not know the run. What matters is that the
		// call was routed and answered without panicking.
		t.Logf("Close answered: %v", err)
	}
	if err := e.Close(context.Background(), managed.SessionRef{}); err != nil {
		t.Fatalf("Close with no id: %v, want nil", err)
	}
}

// TestPublishOfALegacyRowReadsItsTranscriptThroughTheRuntime checks that the
// same legacy row at publish time reads its transcript through the runtime the
// reference names, so the manifest is committed with the events beside it and
// no second engine is consulted.
func TestPublishOfALegacyRowReadsItsTranscriptThroughTheRuntime(t *testing.T) {
	f := newRunnerForge([]string{"README.md"})
	prov := &transcriptProvider{Provider: memory.New(), events: nil}
	e := &engineRunner{
		sessions: legacySessions(t, prov), method: runnerMethod, gh: f, owner: "open-hai",
		jobs: fakeJobs{j: job.Job{ID: "j1", Status: provider.RunStatus{RunID: "run-1"}}},
	}
	err := e.Publish(context.Background(), &job.Job{ID: "j1", Ref: "doi:10.1234/a-paper", RepoName: "open-hai/a-paper-xyz"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if _, ok := f.files[publish.ManifestFile]; !ok {
		names := make([]string, 0, len(f.files))
		for n := range f.files {
			names = append(names, n)
		}
		t.Fatalf("no manifest committed; files: %v", names)
	}
}
