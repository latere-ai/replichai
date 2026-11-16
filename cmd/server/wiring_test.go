// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/github"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/project"
)

// checkTrashSeesARunningSession checks that the project store opened by
// openStores knows which sessions are in flight, so trashing a project with a
// running session is refused and one without is not.
func checkTrashSeesARunningSession(t *testing.T, st stores) {
	t.Helper()
	ctx := context.Background()
	p, err := st.projects.Create(ctx, "owner", "t")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	key := fmt.Sprintf("doi:10.1/%d", time.Now().UnixNano())
	if _, err := st.jobs.Enqueue(ctx, job.Submission{Principal: "owner", Kind: job.KindAudit, Ref: key,
		PaperKey: key, Project: p.ID, Payer: job.PayerPerson}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := st.projects.Trash(ctx, p.ID); !errors.Is(err, project.ErrSessionsActive) {
		t.Fatalf("trash with a session in flight = %v, want ErrSessionsActive", err)
	}
	idle, err := st.projects.Create(ctx, "owner", "idle")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := st.projects.Trash(ctx, idle.ID); err != nil {
		t.Fatalf("trash with no session: %v", err)
	}
}

func TestMemoryStoresTrashSeesARunningSession(t *testing.T) {
	st, err := openStores(context.Background(), "", "")
	if err != nil {
		t.Fatalf("openStores: %v", err)
	}
	defer st.close()
	checkTrashSeesARunningSession(t, st)
}

func TestPostgresStoresTrashSeesARunningSession(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the Postgres wiring test")
	}
	st, err := openStores(context.Background(), url, url)
	if err != nil {
		t.Fatalf("openStores: %v", err)
	}
	defer st.close()
	checkTrashSeesARunningSession(t, st)
}

func TestForgeOwners(t *testing.T) {
	if got := (&forgePublisher{owner: "open-hai"}).Owner(); got != "open-hai" {
		t.Errorf("forgePublisher.Owner() = %q", got)
	}
	if got := (forgeProvisioner{owner: "open-hai"}).Owner(); got != "open-hai" {
		t.Errorf("forgeProvisioner.Owner() = %q", got)
	}
}

// TestPrepareCreatesAndRetriesATakenName checks that creating is the check: a
// name GitHub reports taken is not reused, a new one is drawn and created.
func TestPrepareCreatesAndRetriesATakenName(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/orgs/open-hai/repos"):
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/orgs/open-hai/repos":
			if posts.Add(1) == 1 {
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(`{"message":"Repository creation failed.","errors":[{"resource":"Repository","code":"custom","field":"name","message":"name already exists on this account"}]}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"name":"made"}`))
		default:
			t.Errorf("unexpected request %s %s (a taken name must not be fetched)", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	f := githubForge{github.New("tok", github.WithBaseURL(srv.URL))}
	name, err := f.Prepare(context.Background(), "open-hai", paper.Identity{Slug: "a-paper"})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if posts.Load() != 2 {
		t.Fatalf("create called %d times, want 2 (the taken name, then a new one)", posts.Load())
	}
	if !strings.HasPrefix(name, "a-paper-") {
		t.Fatalf("name %q", name)
	}
}
