// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"bufio"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
)

// readUntilEnd reads a stream to its close and returns the data of the frame
// named streamEndEvent, or "" when the stream closed without one.
func readUntilEnd(t *testing.T, url string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()
	var event, endData string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: ") && event == streamEndEvent:
			endData = strings.TrimPrefix(line, "data: ")
		}
	}
	if ctx.Err() != nil {
		t.Fatal("the stream was still open after 3s")
	}
	return endData
}

// TestStreamEndsWhenTheViewerLosesAccess checks that an open stream re-reads
// access: a project withdrawn after the stream opened stops being readable by a
// viewer with no stake, and the stream says so before closing.
func TestStreamEndsWhenTheViewerLosesAccess(t *testing.T) {
	store := job.NewMemStore()
	projects, j := publicSession(t, store)
	rt := NewRouter(Deps{Jobs: store, Projects: projects, Reproductions: &fakeRepro{}, Gate: allowAll,
		SSEInterval: 5 * time.Millisecond, SSERecheck: 10 * time.Millisecond})
	srv := httptest.NewServer(rt.Handler())
	defer srv.Close()

	go func() {
		time.Sleep(60 * time.Millisecond)
		if err := projects.SetPrivate(context.Background(), j.ProjectID, project.Withdrawal{By: "sub", Reason: "test"}); err != nil {
			t.Errorf("withdraw: %v", err)
		}
	}()
	if got := readUntilEnd(t, srv.URL+"/jobs/"+j.ID+"/events"); got != `{"reason":"not_found"}` {
		t.Fatalf("end frame = %q, want reason not_found", got)
	}
}

// TestStreamEndsWhenTheSessionIsDeleted checks the final frame for a session
// that disappears under an open stream.
func TestStreamEndsWhenTheSessionIsDeleted(t *testing.T) {
	store := job.NewMemStore()
	projects, j := publicSession(t, store)
	rt := NewRouter(Deps{Jobs: store, Projects: projects, Reproductions: &fakeRepro{}, Gate: allowAll,
		SSEInterval: 5 * time.Millisecond})
	srv := httptest.NewServer(rt.Handler())
	defer srv.Close()

	go func() {
		time.Sleep(60 * time.Millisecond)
		if err := store.Delete(context.Background(), j.ID); err != nil {
			t.Errorf("delete: %v", err)
		}
	}()
	if got := readUntilEnd(t, srv.URL+"/jobs/"+j.ID+"/events"); got != `{"reason":"not_found"}` {
		t.Fatalf("end frame = %q, want reason not_found", got)
	}
}

// flakyJobs fails Get after the first calls, as a database that stops answering.
type flakyJobs struct {
	job.Store
	gets  atomic.Int32
	after int32
}

func (f *flakyJobs) Get(ctx context.Context, id string) (job.Job, error) {
	if f.gets.Add(1) > f.after {
		return job.Job{}, errors.New("database unavailable")
	}
	return f.Store.Get(ctx, id)
}

// TestStreamSaysSoWhenTheReadFails checks that a read error ends the stream
// with a final frame instead of silently, so the browser can reconnect.
func TestStreamSaysSoWhenTheReadFails(t *testing.T) {
	store := job.NewMemStore()
	projects, j := publicSession(t, store)
	jobs := &flakyJobs{Store: store, after: 2}
	rt := NewRouter(Deps{Jobs: jobs, Projects: projects, Reproductions: &fakeRepro{}, Gate: allowAll,
		SSEInterval: 5 * time.Millisecond})
	srv := httptest.NewServer(rt.Handler())
	defer srv.Close()

	if got := readUntilEnd(t, srv.URL+"/jobs/"+j.ID+"/events"); got != `{"reason":"unavailable"}` {
		t.Fatalf("end frame = %q, want reason unavailable", got)
	}
}
