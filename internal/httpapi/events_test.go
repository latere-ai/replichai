// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
)

// publicSession enqueues a session in a fresh public project, so the SSE tests'
// unauthenticated client may view it (the stream is scoped like every session
// read). It returns the project store to wire into Deps and the session.
func publicSession(t *testing.T, store *job.MemStore) (*project.MemStore, job.Job) {
	t.Helper()
	ctx := context.Background()
	projects := project.NewMemStore()
	p, err := projects.Create(ctx, "sub", "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := projects.SetPublic(ctx, p.ID, project.SignOff{By: "sub", AttestationVersion: "v1"}); err != nil {
		t.Fatal(err)
	}
	j, err := store.Enqueue(ctx, job.Submission{Principal: "sub", Ref: "ref", Kind: job.KindAudit, Project: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	return projects, j
}

// AC2: the SSE stream snapshots current state first (a late viewer sees where
// the run is, not a blank screen), flushes each event, and streams subsequent
// transitions until the run finishes.
func TestSSESnapshotThenStream(t *testing.T) {
	store := job.NewMemStore()
	ctx := context.Background()
	projects, j := publicSession(t, store)
	// The run is already mid-flight before anyone connects.
	_ = store.Advance(ctx, j.ID, job.PhaseResolving, nil)
	_ = store.Advance(ctx, j.ID, job.PhaseRunning, nil)

	rt := NewRouter(Deps{Jobs: store, Projects: projects, Reproductions: &fakeRepro{}, Gate: allowAll, SSEInterval: 5 * time.Millisecond})
	srv := httptest.NewServer(rt.Handler())
	defer srv.Close()

	// Drive the run to completion shortly after the client connects.
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = store.Advance(ctx, j.ID, job.PhasePublishing, nil)
		_ = store.Advance(ctx, j.ID, job.PhaseDone, nil)
	}()

	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, "GET", srv.URL+"/jobs/"+j.ID+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content-type = %q, want event-stream", resp.Header.Get("Content-Type"))
	}

	var datas []string
	var ids []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "id: "):
			ids = append(ids, strings.TrimPrefix(line, "id: "))
		case strings.HasPrefix(line, "data: "):
			d := strings.TrimPrefix(line, "data: ")
			datas = append(datas, d)
			if strings.Contains(d, `"phase":"done"`) {
				resp.Body.Close() // got the terminal event; stop reading
			}
		}
	}

	if len(datas) == 0 {
		t.Fatal("no events received")
	}
	// The snapshot shows the phase the run was already in — running, not submitted.
	if !strings.Contains(datas[0], `"phase":"running"`) {
		t.Errorf("first event should snapshot the current phase (running), got %s", datas[0])
	}
	// The stream reached the terminal phase.
	last := datas[len(datas)-1]
	if !strings.Contains(last, `"phase":"done"`) {
		t.Errorf("stream should end at done, last event %s", last)
	}
	// Event ids are present and increase.
	if len(ids) < 2 || ids[0] == ids[len(ids)-1] {
		t.Errorf("event ids should increment, got %v", ids)
	}
}

// progressRunner drives a job through the phases and reports usage several times
// during the running phase (an Observe-only update, no phase change).
type progressRunner struct{ step time.Duration }

func (progressRunner) Resolve(context.Context, *job.Job) error { return nil }
func (r progressRunner) Execute(_ context.Context, _ *job.Job, observe func(provider.RunStatus)) error {
	for i := 1; i <= 3; i++ {
		observe(provider.RunStatus{State: provider.StateRunning, Usage: provider.Usage{InputTokens: int64(i * 100)}})
		time.Sleep(r.step)
	}
	return nil
}
func (progressRunner) Publish(context.Context, *job.Job) error         { return nil }
func (progressRunner) Close(context.Context, managed.SessionRef) error { return nil }
func (progressRunner) Resume(context.Context, *job.Job, func(provider.RunStatus)) error {
	return nil
}

// AC2+AC5 through the HTTP surface: a real worker's Observe-only progress (usage
// with no phase change) reaches the SSE stream — not just phase transitions.
func TestSSEStreamsWorkerProgress(t *testing.T) {
	store := job.NewMemStore()
	ctx := t.Context()
	projects, j := publicSession(t, store)

	rt := NewRouter(Deps{Jobs: store, Projects: projects, Reproductions: &fakeRepro{}, Gate: allowAll, SSEInterval: 5 * time.Millisecond})
	srv := httptest.NewServer(rt.Handler())
	defer srv.Close()

	// Connect first (snapshot: submitted), then let the worker drive the job.
	reqCtx, reqCancel := context.WithTimeout(ctx, 3*time.Second)
	defer reqCancel()
	req, _ := http.NewRequestWithContext(reqCtx, "GET", srv.URL+"/jobs/"+j.ID+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	go job.NewWorker(store, progressRunner{step: 15 * time.Millisecond}).Process(ctx)

	var sawProgress, sawDone bool
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		d := strings.TrimPrefix(line, "data: ")
		if strings.Contains(d, `"input_tokens":100`) || strings.Contains(d, `"input_tokens":300`) || strings.Contains(d, `"InputTokens":100`) {
			sawProgress = true
		}
		if strings.Contains(d, `"phase":"done"`) {
			sawDone = true
			resp.Body.Close()
		}
	}
	if !sawProgress {
		t.Error("the stream never carried the run's observed usage — Observe-only updates are not reaching SSE")
	}
	if !sawDone {
		t.Error("the stream did not reach the terminal phase")
	}
}

// A reconnect with Last-Event-ID resumes the id sequence rather than restarting.
func TestSSEResumesFromLastEventID(t *testing.T) {
	store := job.NewMemStore()
	ctx := context.Background()
	projects, j := publicSession(t, store)
	// A terminal job: the handler sends one snapshot and returns.
	_ = store.Advance(ctx, j.ID, job.PhaseResolving, nil)
	_ = store.Advance(ctx, j.ID, job.PhaseRunning, nil)
	_ = store.Advance(ctx, j.ID, job.PhasePublishing, nil)
	_ = store.Advance(ctx, j.ID, job.PhaseDone, nil)

	rt := NewRouter(Deps{Jobs: store, Projects: projects, Reproductions: &fakeRepro{}, Gate: allowAll, SSEInterval: 5 * time.Millisecond})
	srv := httptest.NewServer(rt.Handler())
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/jobs/"+j.ID+"/events", nil)
	req.Header.Set("Last-Event-ID", "41")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	body := readAllString(resp.Body)
	if !strings.Contains(body, "id: 42") {
		t.Errorf("reconnect should resume ids after 41, got:\n%s", body)
	}
}

// A stream for a missing job is a 404, not an empty stream.
func TestSSEMissingJob(t *testing.T) {
	rt := NewRouter(Deps{Jobs: job.NewMemStore(), Reproductions: &fakeRepro{}, Gate: allowAll})
	srv := httptest.NewServer(rt.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/jobs/nope/events")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// readAllString drains r and returns what arrived. A stream cut off mid-flight
// is the thing under test, so a read error is not a failure here: the partial
// body is the observation. io.ReadAll returns exactly that — what it read, plus
// the error we are choosing to ignore.
func readAllString(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return string(b)
}
