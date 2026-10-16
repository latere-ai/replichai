// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
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
// unauthenticated client may view it. The stream is scoped like every other
// session read. It returns the project store to wire into Deps and the
// session.
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
	j, err := store.Enqueue(ctx, job.Submission{Principal: "sub", Ref: "ref", PaperKey: "doi:10.0/ref", Kind: job.KindAudit, Project: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	return projects, j
}

// TestSSESnapshotThenStream covers AC2: the SSE stream sends a snapshot of the
// current state first, so a late viewer sees where the run is rather than a
// blank screen, flushes each event, and streams subsequent transitions until
// the run finishes.
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

	// The run is driven to completion once the client has its snapshot, on the
	// first frame read below rather than after a fixed pause, so the snapshot
	// is always the running phase and the stream always has later phases to
	// show.
	advanced := false
	finish := func() {
		if advanced {
			return
		}
		advanced = true
		_ = store.Advance(ctx, j.ID, job.PhasePublishing, nil)
		_ = store.Advance(ctx, j.ID, job.PhaseDone, nil)
	}

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
			finish() // The snapshot has arrived, so the run moves on.
			if strings.Contains(d, `"phase":"done"`) {
				resp.Body.Close() // The terminal event arrived, so stop reading.
			}
		}
	}

	if len(datas) == 0 {
		t.Fatal("no events received")
	}
	// The snapshot shows the phase the run was already in, which is running,
	// not submitted.
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

// progressRunner drives a job through the phases and reports usage during the
// running phase, as Observe-only updates with no phase change. It waits for
// the stream reader between steps, so each state stays on the row until the
// stream has shown it: running with no usage, then running with the first
// usage. The stream re-reads the row on a timer and sends only the latest
// state, so without the waits the steps could land in one read and the
// stream would rightly show only the last of them.
type progressRunner struct {
	// running and observed are closed by the reader once the stream has shown
	// the running phase and the first usage.
	running, observed <-chan struct{}
}

func (progressRunner) Resolve(context.Context, *job.Job) error { return nil }
func (r progressRunner) Execute(ctx context.Context, _ *job.Job, observe func(provider.RunStatus)) error {
	if err := waitFor(ctx, r.running); err != nil {
		return err
	}
	observe(provider.RunStatus{State: provider.StateRunning, Usage: provider.Usage{InputTokens: 100}})
	if err := waitFor(ctx, r.observed); err != nil {
		return err
	}
	observe(provider.RunStatus{State: provider.StateRunning, Usage: provider.Usage{InputTokens: 300}})
	return nil
}

// waitFor blocks until ch is closed or ctx ends, so a runner stuck on a test
// that already failed ends with the test's context instead of leaking.
func waitFor(ctx context.Context, ch <-chan struct{}) error {
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (progressRunner) Publish(context.Context, *job.Job) error         { return nil }
func (progressRunner) Close(context.Context, managed.SessionRef) error { return nil }
func (progressRunner) Resume(context.Context, *job.Job, func(provider.RunStatus)) error {
	return nil
}

// TestSSEStreamsWorkerProgress covers AC2 and AC5 through HTTP: a real
// worker's Observe-only progress, meaning usage with no phase change, reaches
// the SSE stream, not only phase transitions.
func TestSSEStreamsWorkerProgress(t *testing.T) {
	store := job.NewMemStore()
	ctx := t.Context()
	projects, j := publicSession(t, store)

	rt := NewRouter(Deps{Jobs: store, Projects: projects, Reproductions: &fakeRepro{}, Gate: allowAll, SSEInterval: 5 * time.Millisecond})
	srv := httptest.NewServer(rt.Handler())
	defer srv.Close()

	// Connect first, so the snapshot is the submitted phase, then let the
	// worker drive the job.
	reqCtx, reqCancel := context.WithTimeout(ctx, 3*time.Second)
	defer reqCancel()
	req, _ := http.NewRequestWithContext(reqCtx, "GET", srv.URL+"/jobs/"+j.ID+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	running, observed := make(chan struct{}), make(chan struct{})
	go job.NewWorker(store, progressRunner{running: running, observed: observed}).Process(ctx)

	// The runner holds each state until the stream has shown it, so a usage
	// event can only follow a running event that carried none, with no phase
	// change between them: the update that put it there was Observe alone.
	var sawRunning, sawProgress, sawDone bool
	var doneUsage int64
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev struct {
			Phase  job.Phase          `json:"phase"`
			Status provider.RunStatus `json:"status"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("decode event %q: %v", line, err)
		}
		switch {
		case ev.Phase == job.PhaseRunning && ev.Status.Usage.InputTokens == 0 && !sawRunning:
			sawRunning = true
			close(running)
		case ev.Phase == job.PhaseRunning && ev.Status.Usage.InputTokens == 100 && sawRunning && !sawProgress:
			sawProgress = true
			close(observed)
		case ev.Phase == job.PhaseDone:
			sawDone = true
			doneUsage = ev.Status.Usage.InputTokens
			resp.Body.Close()
		}
	}
	if !sawRunning {
		t.Error("the stream never showed the running phase")
	}
	if !sawProgress {
		t.Error("the stream never carried the run's observed usage while running, so Observe-only updates are not reaching SSE")
	}
	if !sawDone {
		t.Error("the stream did not reach the terminal phase")
	}
	// A transition keeps the latest observation rather than copying back an
	// older one, so the terminal event carries the last usage reported.
	if sawDone && doneUsage != 300 {
		t.Errorf("the terminal event carried input tokens %d, want the last observed 300", doneUsage)
	}
}

// TestSSEResumesFromLastEventID checks that a reconnect with Last-Event-ID
// resumes the id sequence rather than restarting it.
func TestSSEResumesFromLastEventID(t *testing.T) {
	store := job.NewMemStore()
	ctx := context.Background()
	projects, j := publicSession(t, store)
	// For a terminal job the handler sends one snapshot and returns.
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

// TestSSEMissingJob checks that a stream for a missing job answers 404, not
// an empty stream.
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

// readAllString drains r and returns what arrived. A stream cut off before it
// ends is what is under test, so a read error is not a failure here; the
// partial body is the observation. io.ReadAll returns what it read together
// with the error, and the error is ignored.
func readAllString(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return string(b)
}
