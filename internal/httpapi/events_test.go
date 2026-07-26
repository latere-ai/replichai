package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/job"
)

// AC2: the SSE stream snapshots current state first (a late viewer sees where
// the run is, not a blank screen), flushes each event, and streams subsequent
// transitions until the run finishes.
func TestSSESnapshotThenStream(t *testing.T) {
	store := job.NewMemStore()
	ctx := context.Background()
	j, _ := store.Enqueue(ctx, "sub", "ref", job.KindAudit, "")
	// The run is already mid-flight before anyone connects.
	_ = store.Advance(ctx, j.ID, job.PhaseResolving, nil)
	_ = store.Advance(ctx, j.ID, job.PhaseRunning, nil)

	rt := NewRouter(Deps{Jobs: store, Reproductions: &fakeRepro{}, Gate: allowAll, SSEInterval: 5 * time.Millisecond})
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

// A reconnect with Last-Event-ID resumes the id sequence rather than restarting.
func TestSSEResumesFromLastEventID(t *testing.T) {
	store := job.NewMemStore()
	ctx := context.Background()
	j, _ := store.Enqueue(ctx, "sub", "ref", job.KindAudit, "")
	// A terminal job: the handler sends one snapshot and returns.
	_ = store.Advance(ctx, j.ID, job.PhaseResolving, nil)
	_ = store.Advance(ctx, j.ID, job.PhaseRunning, nil)
	_ = store.Advance(ctx, j.ID, job.PhasePublishing, nil)
	_ = store.Advance(ctx, j.ID, job.PhaseDone, nil)

	rt := NewRouter(Deps{Jobs: store, Reproductions: &fakeRepro{}, Gate: allowAll, SSEInterval: 5 * time.Millisecond})
	srv := httptest.NewServer(rt.Handler())
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/jobs/"+j.ID+"/events", nil)
	req.Header.Set("Last-Event-ID", "41")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()

	body, _ := readAllString(resp.Body)
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

func readAllString(r interface{ Read([]byte) (int, error) }) (string, error) {
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.String(), nil
		}
	}
}
