package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/provider/memory"
	"github.com/latere-ai/replichai/internal/registry"
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

	router := httpapi.NewRouter(httpapi.Deps{
		Jobs:          store,
		Reproductions: &registryReader{source: registry.NewFSSource(t.TempDir())},
		Gate:          devGate,
		SSEInterval:   10 * time.Millisecond,
	})
	srv := httptest.NewServer(withDevPrincipal(router.Handler()))
	defer srv.Close()

	// Submit a paper (a DOI resolves offline, so the whole run stays free).
	body := strings.NewReader(`{"url":"10.1145/3544548.3580778","project":"e2e"}`)
	resp, err := http.Post(srv.URL+"/papers", "application/json", body)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status = %d, want 202", resp.StatusCode)
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode submit: %v", err)
	}
	resp.Body.Close()
	if out.JobID == "" {
		t.Fatal("no job id returned")
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
