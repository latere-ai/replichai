// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// signedIn is a session holding a credential, over a scripted auth server.
func signedIn(t *testing.T) *Session {
	t.Helper()
	s := session(t, &provider{approved: true})
	if _, err := s.Login(context.Background(), nil); err != nil {
		t.Fatalf("Login: %v", err)
	}
	return s
}

// Every call presents a bearer, and the bearer is the short-lived actor token
// rather than the long-lived stored credential.
func TestEveryCallPresentsAnActorToken(t *testing.T) {
	var presented []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented = append(presented, r.Header.Get("Authorization"))
		switch {
		case r.URL.Path == "/projects" && r.Method == http.MethodPost:
			writeJSON(w, map[string]string{"project_id": "p-1", "title": "Local work"})
		case r.URL.Path == "/projects" && r.Method == http.MethodGet:
			writeJSON(w, []map[string]string{{"id": "p-1", "title": "Local work"}})
		case strings.HasSuffix(r.URL.Path, "/sessions/import"):
			writeJSON(w, map[string]string{"job_id": "j-1"})
		default:
			writeJSON(w, map[string]string{})
		}
	}))
	defer api.Close()

	client := NewClient(signedIn(t), api.URL)
	client.HTTP = api.Client()

	created, err := client.CreateProject(context.Background(), "Local work")
	if err != nil || created.ID != "p-1" {
		t.Fatalf("CreateProject = %+v, %v", created, err)
	}
	projects, err := client.ListProjects(context.Background())
	if err != nil || len(projects) != 1 {
		t.Fatalf("ListProjects = %+v, %v", projects, err)
	}
	id, err := client.ImportSession(context.Background(), "p-1", ImportRecord{
		PaperKey: "doi:10.1145/3654777.3676417", Runtime: "local", SessionID: "7f3a91c2", Phase: "done",
	})
	if err != nil || id != "j-1" {
		t.Fatalf("ImportSession = %q, %v", id, err)
	}
	if len(presented) != 3 {
		t.Fatalf("%d calls made", len(presented))
	}
	for _, header := range presented {
		if header != "Bearer actor-1" {
			t.Fatalf("a call presented %q rather than the minted actor token", header)
		}
	}
}

// A refusal carries the server's own machine-readable reason, not only a
// status line: an operator whose push is refused needs to know why.
func TestARefusalCarriesTheServersReason(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]string{"error": "paper_not_found"})
	}))
	defer api.Close()
	client := NewClient(signedIn(t), api.URL)
	client.HTTP = api.Client()

	_, err := client.ImportSession(context.Background(), "p-1", ImportRecord{})
	if err == nil || !strings.Contains(err.Error(), "paper_not_found") {
		t.Fatalf("ImportSession = %v", err)
	}
}

// Filing a paper that is already there is not an error: a batch pushed twice
// must not fail on the second attempt, which is the whole point of a push
// being resumable. The reason is the app's own — a stub that invents one tests
// nothing, which is how this client shipped speaking an API nobody serves.
func TestFilingAPaperTwiceIsNotAFailure(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		writeJSON(w, map[string]string{"error": "paper_exists"})
	}))
	defer api.Close()
	client := NewClient(signedIn(t), api.URL)
	client.HTTP = api.Client()

	if err := client.AddPaper(context.Background(), "p-1", "10.1145/3654777.3676417"); err != nil {
		t.Fatalf("AddPaper = %v", err)
	}
}

// The record carries no locator: it holds a host path and a process handle,
// which mean nothing on the server and leak the operator's home directory.
func TestTheRecordCarriesNoHostPath(t *testing.T) {
	encoded, err := json.Marshal(ImportRecord{
		PaperKey: "doi:10.1145/3654777.3676417", Runtime: "local", SessionID: "7f3a91c2",
		Phase: "done", Grade: "passed", Repo: "open-hai/a-paper-xyz",
		Model:     ModelRef{Provider: "anthropic", ID: "claude-opus-5"},
		Usage:     UsageRecord{InputFresh: 1000, Output: 200},
		StartedAt: time.Now(), FinishedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"locator", "directory", "/Users/", "/home/", "workspace"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("the record carries %q:\n%s", forbidden, encoded)
		}
	}
}
