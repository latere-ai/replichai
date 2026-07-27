package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider/memory"
	"github.com/latere-ai/replichai/internal/registry"
	"github.com/latere-ai/replichai/internal/webauth"
)

// TestServerEndToEnd is the lightweight full-stack check: it wires the real
// composition root — HTTP router, job worker, engine runner — over the
// in-process memory provider and an in-memory store, submits a paper, and
// asserts the job walks all the way to done. No Docker, no network, no API key,
// no cost. It exercises every seam the compose stack does except Postgres
// itself (which the job store contract covers against a real database).
func TestServerEndToEnd(t *testing.T) {
	m, err := method.Load("") // discovers ../../method from this package
	if err != nil {
		t.Fatalf("load method: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := job.NewMemStore()
	runner := &engineRunner{
		provider: memory.New(), // the free, in-process engine — no real audit runs
		method:   m,
		fetch:    newHTTPFetcher(5 * time.Second),
		workDir:  t.TempDir(),
	}
	go job.NewWorker(store, runner, job.WithPoll(5*time.Millisecond)).Start(ctx, 1)

	// The dev principal is seeded as an admin (as ADMIN_EMAILS does in compose),
	// so it clears the real fail-closed gate.
	acc := access.NewMemStore()
	if err := acc.SeedAdmin(ctx, devEmail); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	projects := project.NewMemStore()
	router := httpapi.NewRouter(httpapi.Deps{
		Jobs:          store,
		Projects:      projects,
		Reproductions: &registryReader{source: registry.NewFSSource(t.TempDir())},
		Gate:          webauth.Gate(acc),
		SSEInterval:   10 * time.Millisecond,
	})
	srv := httptest.NewServer(webauth.DevMiddleware(devSub, devEmail)(router.Handler()))
	defer srv.Close()

	// Submit a paper with no project (a DOI resolves offline, so the run stays
	// free): the composition root opens a fresh project and its first session.
	body := strings.NewReader(`{"url":"10.1145/3544548.3580778"}`)
	resp, err := http.Post(srv.URL+"/papers", "application/json", body)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status = %d, want 202", resp.StatusCode)
	}
	var out struct {
		JobID     string `json:"job_id"`
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	resp.Body.Close()
	if out.JobID == "" || out.ProjectID == "" {
		t.Fatalf("submit returned %+v, want both ids", out)
	}
	// The project was really opened and owns the session.
	if p, err := projects.Get(ctx, out.ProjectID); err != nil || p.Owner != devSub {
		t.Fatalf("project = %+v, err %v", p, err)
	}

	// Poll until the job reaches a terminal phase.
	deadline := time.Now().Add(10 * time.Second)
	var phase string
	for time.Now().Before(deadline) {
		j, err := store.Get(ctx, out.JobID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		phase = string(j.Phase)
		if j.Phase.Terminal() {
			if j.Phase != job.PhaseDone {
				t.Fatalf("job ended in %s (error: %q), want done", j.Phase, j.Err)
			}
			// Every phase timestamp was recorded on the way.
			for _, p := range []job.Phase{job.PhaseSubmitted, job.PhaseResolving, job.PhaseRunning, job.PhasePublishing, job.PhaseDone} {
				if j.PhaseAt[p].IsZero() {
					t.Errorf("phase %s has no timestamp", p)
				}
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job did not finish in time; last phase %q", phase)
}
