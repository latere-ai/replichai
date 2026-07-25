package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestListReposPaginates(t *testing.T) {
	// A full first page forces a second request; a short second page ends it.
	full := make([]map[string]string, 100)
	for i := range full {
		full[i] = map[string]string{"name": "repo-" + string(rune('a'+i%26))}
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "1" {
			_ = json.NewEncoder(w).Encode(full)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "last-one"}})
	})
	names, err := c.ListRepos(context.Background(), "open-hai")
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(names) != 101 || names[len(names)-1] != "last-one" {
		t.Errorf("got %d names, last %q", len(names), names[len(names)-1])
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

func TestTreePathsReturnsBlobsOnly(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "recursive=1") {
			t.Errorf("tree should be requested recursively, got %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"truncated":false,"tree":[
			{"path":"README.md","type":"blob"},
			{"path":"src","type":"tree"},
			{"path":"src/main.py","type":"blob"}]}`))
	})
	paths, err := c.TreePaths(context.Background(), "open-hai", "x", "main")
	if err != nil {
		t.Fatalf("TreePaths: %v", err)
	}
	want := []string{"README.md", "src/main.py"}
	if !slices.Equal(paths, want) {
		t.Errorf("paths = %v, want %v (blobs only)", paths, want)
	}
}

func TestPutFileNewAndUpdate(t *testing.T) {
	// New file: the GET stat 404s, so no sha is sent.
	var body map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
	})
	if err := c.PutFile(context.Background(), "open-hai", "x", "replication.json", "add manifest", []byte(`{"schema":1}`)); err != nil {
		t.Fatalf("PutFile new: %v", err)
	}
	if _, hasSHA := body["sha"]; hasSHA {
		t.Error("a new file must not send a sha")
	}
	if body["content"] != base64.StdEncoding.EncodeToString([]byte(`{"schema":1}`)) {
		t.Error("content must be base64-encoded")
	}

	// Existing file: the GET stat returns a sha, which the PUT must echo.
	c2 := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"sha":"blobsha"}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	})
	if err := c2.PutFile(context.Background(), "open-hai", "x", "replication.json", "update", []byte(`{}`)); err != nil {
		t.Fatalf("PutFile update: %v", err)
	}
	if body["sha"] != "blobsha" {
		t.Errorf("updating an existing file must echo its sha, got %v", body["sha"])
	}
}

func TestGetFileContentDecodesBase64(t *testing.T) {
	want := []byte(`{"schema":1}`)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/open-hai/x/contents/replication.json" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		// GitHub wraps base64 at 60 cols; include a newline to prove we strip it.
		enc := base64.StdEncoding.EncodeToString(want)
		_ = json.NewEncoder(w).Encode(map[string]any{"content": enc[:4] + "\n" + enc[4:], "encoding": "base64"})
	})
	got, ok, err := c.GetFileContent(context.Background(), "open-hai", "x", "replication.json")
	if err != nil || !ok {
		t.Fatalf("GetFileContent: ok=%v err=%v", ok, err)
	}
	if string(got) != string(want) {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestGetFileContentAbsent(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if _, ok, err := c.GetFileContent(context.Background(), "open-hai", "x", "replication.json"); ok || err != nil {
		t.Errorf("absent file should be ok=false, no error; got ok=%v err=%v", ok, err)
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
