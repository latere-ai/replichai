package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New("test-token", WithBaseURL(server.URL))
}

func TestCreateRepoPrivate(t *testing.T) {
	var got map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/orgs/open-hai/repos" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing bearer token")
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(Repo{Name: "x-a3f9", HTMLURL: "https://github.com/open-hai/x-a3f9", Private: true})
	})

	repo, err := c.CreateRepo(context.Background(), "open-hai", "x-a3f9", true)
	if err != nil {
		t.Fatalf("CreateRepo: %v", err)
	}
	if got["private"] != true || got["auto_init"] != true {
		t.Errorf("request body = %v, want private+auto_init", got)
	}
	if repo.Owner != "open-hai" || repo.Name != "x-a3f9" || !repo.Private {
		t.Errorf("repo = %+v", repo)
	}
}

func TestCreateRepoExistsIsTyped(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	})
	if _, err := c.CreateRepo(context.Background(), "open-hai", "x", true); !errors.Is(err, ErrRepoExists) {
		t.Errorf("err = %v, want ErrRepoExists", err)
	}
}

func TestGetRepoRecoversAfterExists(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/open-hai/x-a3f9" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Repo{Name: "x-a3f9", Private: true})
	})
	repo, err := c.GetRepo(context.Background(), "open-hai", "x-a3f9")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if repo.Owner != "open-hai" || repo.Name != "x-a3f9" {
		t.Errorf("repo = %+v", repo)
	}
}

func TestCreateRepoUnexpectedStatus(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	if _, err := c.CreateRepo(context.Background(), "open-hai", "x", true); err == nil {
		t.Error("a 403 should surface as an error")
	}
}

func TestInviteCreatesInvitation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/repos/open-hai/x/collaborators/alice" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated) // invitation created
	})
	if err := c.Invite(context.Background(), "open-hai", "x", "alice"); err != nil {
		t.Errorf("Invite: %v", err)
	}
}

func TestInviteAlreadyCollaborator(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent) // already a collaborator
	})
	if err := c.Invite(context.Background(), "open-hai", "x", "alice"); err != nil {
		t.Errorf("an existing collaborator should be a no-op, got %v", err)
	}
}

func TestHeadCommit(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/repos/open-hai/x/commits") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"sha":"1e76be1ade"}]`))
	})
	sha, err := c.HeadCommit(context.Background(), "open-hai", "x")
	if err != nil {
		t.Fatalf("HeadCommit: %v", err)
	}
	if sha != "1e76be1ade" {
		t.Errorf("sha = %q", sha)
	}
}

func TestHeadCommitEmptyRepo(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	if _, err := c.HeadCommit(context.Background(), "open-hai", "x"); err == nil {
		t.Error("a repo with no commits should error")
	}
}

func TestInviteForbidden(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	if err := c.Invite(context.Background(), "open-hai", "x", "alice"); err == nil {
		t.Error("a forbidden invite should error")
	}
}

func TestSetPublicSendsPrivateFalse(t *testing.T) {
	var got map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/open-hai/x" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	})
	if err := c.SetPublic(context.Background(), "open-hai", "x"); err != nil {
		t.Fatalf("SetPublic: %v", err)
	}
	if got["private"] != false {
		t.Errorf("SetPublic must send private:false, got %v", got)
	}
}

func TestErrorStatusSurfaces(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	if err := c.SetPublic(context.Background(), "open-hai", "x"); err == nil {
		t.Error("a 500 should surface as an error")
	}
}
