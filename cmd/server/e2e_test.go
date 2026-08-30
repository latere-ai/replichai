// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
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

	ctx := t.Context()

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
	// Wired as the composition root wires them, so the move at the end of this
	// test carries the session across (035).
	projects := project.NewMemStore(project.WithSessions(store))
	router := httpapi.NewRouter(httpapi.Deps{
		Jobs:          store,
		Projects:      projects,
		Reproductions: &registryReader{source: registry.NewFSSource(t.TempDir())},
		Gate:          webauth.Gate(acc),
		SSEInterval:   10 * time.Millisecond,
	})
	srv := httptest.NewServer(webauth.DevMiddleware(devSub, devEmail)(router.Handler()))
	defer srv.Close()

	// Open a project holding one paper (a DOI resolves offline, so the run stays
	// free). Since 012 this records and starts nothing — the create costs no
	// credits, which is why it works on a deployment with no ledger wired at all.
	body := strings.NewReader(`{"papers":["10.1145/3544548.3580778"]}`)
	resp, err := http.Post(srv.URL+"/projects", "application/json", body)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	var created struct {
		ProjectID string `json:"project_id"`
		Papers    []struct {
			Key string `json:"key"`
		} `json:"papers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	resp.Body.Close()
	if created.ProjectID == "" || len(created.Papers) != 1 || created.Papers[0].Key == "" {
		t.Fatalf("create returned %+v", created)
	}

	// Starting it is the second act, and the only one that runs anything.
	body = strings.NewReader(`{"paper_key":"` + created.Papers[0].Key + `"}`)
	resp, err = http.Post(srv.URL+"/projects/"+created.ProjectID+"/sessions", "application/json", body)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("start status = %d, want 201", resp.StatusCode)
	}
	var out struct {
		JobID     string `json:"job_id"`
		ProjectID string `json:"project_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode start: %v", err)
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
	var finished bool
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
			finished = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !finished {
		t.Fatalf("job did not finish in time; last phase %q", phase)
	}

	// 035: the paper was filed under the wrong project, and moving it takes the
	// run with it. This is the whole point of the move — a reproduction that is
	// already finished is exactly when somebody notices where it belongs.
	resp, err = http.Post(srv.URL+"/projects", "application/json", strings.NewReader(`{"title":"Where it belongs","papers":["10.1145/3411764.3445649"]}`))
	if err != nil {
		t.Fatalf("create the destination: %v", err)
	}
	var second struct {
		ProjectID string `json:"project_id"`
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create the destination: status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&second); err != nil {
		t.Fatalf("decode the destination: %v", err)
	}
	resp.Body.Close()

	move := `{"paper_key":"` + created.Papers[0].Key + `","to_project":"` + second.ProjectID + `"}`
	resp, err = http.Post(srv.URL+"/projects/"+created.ProjectID+"/papers/move", "application/json", strings.NewReader(move))
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("move status = %d, want 204", resp.StatusCode)
	}
	resp.Body.Close()

	// Read it back the way the project screen does, on both projects. The
	// destination was opened with a paper of its own, so the moved one arrives
	// beside it — and arrives carrying its session, which is the whole point.
	arrived := readPapers(t, srv.URL, second.ProjectID)
	if len(arrived) != 2 {
		t.Fatalf("destination papers = %+v; want two", arrived)
	}
	var moved *paperView
	for i, p := range arrived {
		if p.Key == created.Papers[0].Key {
			moved = &arrived[i]
		}
	}
	if moved == nil || moved.Sessions != 1 {
		t.Fatalf("the moved paper did not arrive with its session: %+v", arrived)
	}
	if papers := readPapers(t, srv.URL, created.ProjectID); len(papers) != 0 {
		t.Fatalf("source papers = %+v; want none", papers)
	}
	if j, err := store.Get(ctx, out.JobID); err != nil || j.ProjectID != second.ProjectID {
		t.Fatalf("the session stayed behind: %+v, err %v", j, err)
	}
}

// paperView is the project screen's read of one paper: the record, plus how
// many sessions have run against it.
type paperView struct {
	Key      string `json:"key"`
	Sessions int    `json:"sessions"`
}

func readPapers(t *testing.T, base, projectID string) []paperView {
	t.Helper()
	resp, err := http.Get(base + "/projects/" + projectID + "/papers")
	if err != nil {
		t.Fatalf("list papers: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list papers status = %d, want 200", resp.StatusCode)
	}
	var out []paperView
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode papers: %v", err)
	}
	return out
}
