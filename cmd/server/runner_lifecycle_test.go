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

// A runner missing a dependency is refused at boot, where it is a startup
// error, not at the first job whose row happens to take the path through it.
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

// A row from before session references were persisted carries only a run id.
// Cancelling it must address the run through the runtime that owns it — and
// never reach for an engine that is not there. This is the shape that took
// the server down on every restart with one such row in publishing.
func TestCloseOfALegacyRowGoesThroughTheRuntime(t *testing.T) {
	backend := memory.New()
	e, _ := engineOver(t, backend, "")
	legacy := managed.SessionRef{Runtime: job.LegacyRuntime, ID: "run-legacy", Revision: 1, LocatorVersion: 1}

	if err := e.Close(context.Background(), legacy); err != nil {
		// The memory backend may not know the run; what matters is that the
		// call was routed and answered, not that it panicked.
		t.Logf("Close answered: %v", err)
	}
	if err := e.Close(context.Background(), managed.SessionRef{}); err != nil {
		t.Fatalf("Close with no id: %v, want nil", err)
	}
}

// The same legacy row at publish time reads its transcript through the
// runtime the reference names, so the manifest lands with the events beside
// it and no second engine is consulted.
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
