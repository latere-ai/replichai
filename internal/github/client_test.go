// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"time"

	managed "latere.ai/x/managed-agents"
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
	if got["private"] != true {
		t.Errorf("request body = %v, want a private repository", got)
	}
	// A repository the host will push a workspace into must be created with
	// no commit. GitHub's auto_init writes one, and a workspace that was
	// never cloned from anywhere cannot descend from it: every local
	// --publish run was refused as a non-fast-forward after doing the work.
	if got["auto_init"] != false {
		t.Errorf("a new repository was created with a commit already in it: %v", got)
	}
	if repo.Owner != "open-hai" || repo.Name != "x-a3f9" || !repo.Private {
		t.Errorf("repo = %+v", repo)
	}
}

func TestCreateRepoExistsIsTyped(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Repository creation failed.","errors":[{"resource":"Repository","code":"custom","field":"name","message":"name already exists on this account"}]}`))
	})
	if _, err := c.CreateRepo(context.Background(), "open-hai", "x", true); !errors.Is(err, ErrRepoExists) {
		t.Errorf("err = %v, want ErrRepoExists", err)
	}
}

// TestCreateRepoOtherValidationFailuresAreNotExists checks that only an
// "already exists" 422 is reported as ErrRepoExists. GitHub answers 422 to
// every validation failure, and treating an invalid name or a plan limit as an
// existing repository once sent the caller to fetch a repository that was
// never created.
func TestCreateRepoOtherValidationFailuresAreNotExists(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Repository creation failed.","errors":[{"resource":"Repository","code":"custom","field":"name","message":"name is too long (maximum is 100 characters)"}]}`))
	})
	_, err := c.CreateRepo(context.Background(), "open-hai", strings.Repeat("x", 101), true)
	if errors.Is(err, ErrRepoExists) {
		t.Fatalf("an invalid name was reported as an existing repository: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "name is too long") {
		t.Fatalf("err = %v, want GitHub's reason", err)
	}
}

// TestAFailureCarriesGitHubsMessage checks that a refusal carries GitHub's
// message. "unexpected status 403" told an operator nothing, while "Resource
// not accessible by integration" says which permission the token lacks.
func TestAFailureCarriesGitHubsMessage(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration","documentation_url":"https://docs.github.com/rest"}`))
	})
	err := c.Invite(context.Background(), "open-hai", "x", "alice")
	var refused *APIError
	if !errors.As(err, &refused) {
		t.Fatalf("err = %T %v, want *APIError", err, err)
	}
	if refused.Status != http.StatusForbidden || refused.Message != "Resource not accessible by integration" {
		t.Fatalf("APIError = %+v", refused)
	}
	for _, want := range []string{"PUT", "/repos/open-hai/x/collaborators/alice", "403", "Resource not accessible by integration"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
	if refused.Temporary() {
		t.Error("a permission denial is not temporary; retrying it cannot help")
	}
	// A body that is not GitHub's JSON shape still reaches the message.
	plain := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gateway is on fire", http.StatusBadGateway)
	})
	if err := plain.SetPublic(context.Background(), "open-hai", "x"); err == nil || !strings.Contains(err.Error(), "gateway is on fire") {
		t.Errorf("a plain-text body was dropped: %v", err)
	}
}

// TestARateLimitIsTemporaryAndSaysHowLong checks that a rate limit is a
// temporary failure with a wait attached, in the shape managed.IsTemporary
// already matches, so a caller backs off rather than failing a publication.
func TestARateLimitIsTemporaryAndSaysHowLong(t *testing.T) {
	t.Run("secondary limit with Retry-After", func(t *testing.T) {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`))
		})
		_, err := c.GetRepo(context.Background(), "open-hai", "x")
		var limited *APIError
		if !errors.As(err, &limited) || !limited.Temporary() || limited.RetryAfter() != 30*time.Second {
			t.Fatalf("err = %v; want a temporary failure asking for 30s", err)
		}
		if !managed.IsTemporary(err) {
			t.Error("managed.IsTemporary does not recognize the rate limit")
		}
	})

	t.Run("primary limit with a reset epoch", func(t *testing.T) {
		now := time.Unix(1_700_000_000, 0)
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", "1700000090")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded for installation ID 1."}`))
		})
		c.now = func() time.Time { return now }
		_, err := c.HeadCommit(context.Background(), "open-hai", "x")
		var limited *APIError
		if !errors.As(err, &limited) || !limited.Temporary() || limited.RetryAfter() != 90*time.Second {
			t.Fatalf("err = %v; want a temporary failure asking for 90s", err)
		}
		if !strings.Contains(err.Error(), "retry after 1m30s") {
			t.Errorf("the error does not say how long to wait: %v", err)
		}
	})

	t.Run("429 with no figure is still temporary", func(t *testing.T) {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		})
		_, err := c.TreePaths(context.Background(), "open-hai", "x", "main")
		var limited *APIError
		if !errors.As(err, &limited) || !limited.Temporary() || limited.RetryAfter() != 0 {
			t.Fatalf("err = %v; want temporary with no figure", err)
		}
	})

	t.Run("a plain 403 is a denial, not a limit", func(t *testing.T) {
		c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-RateLimit-Remaining", "4999")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Must have admin rights to Repository."}`))
		})
		err := c.SetPublic(context.Background(), "open-hai", "x")
		if managed.IsTemporary(err) {
			t.Fatalf("a permission denial was reported as temporary: %v", err)
		}
	})
}

// TestGetFileContentReadsALargeFileThroughTheRawPath checks the case of a file
// GitHub will not inline, which is one over about a megabyte: the JSON answer
// says encoding "none" and carries no content. The file is present, so it is
// read through the raw media type. Answering absent once made a large
// verdict.json read as never written.
func TestGetFileContentReadsALargeFileThroughTheRawPath(t *testing.T) {
	want := []byte(strings.Repeat(`{"boundary":[]}`, 10))
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/open-hai/x/contents/verdict.json" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Accept") == "application/vnd.github.raw" {
			_, _ = w.Write(want)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"content": "", "encoding": "none", "sha": "big", "size": 2_000_000})
	})
	got, ok, err := c.GetFileContent(context.Background(), "open-hai", "x", "verdict.json")
	if err != nil || !ok {
		t.Fatalf("GetFileContent: ok=%v err=%v", ok, err)
	}
	if string(got) != string(want) {
		t.Fatalf("content = %q, want the raw bytes", got)
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

// A repository with no commit is a state finalize has to be able to write
// down, not an error that stops it. Repositories are created empty so the
// host's push can be their first commit, so this is the shape of every run
// that pushed nothing — the case 044d exists for. GitHub reports it three
// ways, and all three answer the same.
func TestHeadCommitEmptyRepo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"empty list", http.StatusOK, `[]`},
		{"not found", http.StatusNotFound, `{"message":"Not Found"}`},
		{"conflict", http.StatusConflict, `{"message":"Git Repository is empty."}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			sha, err := c.HeadCommit(context.Background(), "open-hai", "x")
			if err != nil {
				t.Fatalf("an empty repository is not a failure: %v", err)
			}
			if sha != "" {
				t.Errorf("sha = %q, want empty", sha)
			}
		})
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

func TestSetPrivateSendsPrivateTrue(t *testing.T) {
	var got map[string]any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/open-hai/x" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	})
	if err := c.SetPrivate(context.Background(), "open-hai", "x"); err != nil {
		t.Fatalf("SetPrivate: %v", err)
	}
	if got["private"] != true {
		t.Errorf("SetPrivate must send private:true, got %v", got)
	}
}

// TestSetPrivateRefusedSurfaces checks that a refused visibility change is
// returned as an error. GitHub refuses the change on some repositories, such
// as one in a fork network or one under an organization policy, and the client
// must report that rather than a withdrawal it did not perform. The caller
// decides that a forge failure is non-fatal, and it can only decide that if it
// is told.
func TestSetPrivateRefusedSurfaces(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "cannot change visibility", http.StatusUnprocessableEntity)
	})
	if err := c.SetPrivate(context.Background(), "open-hai", "x"); err == nil {
		t.Error("a refused visibility change should error")
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
		// GitHub wraps base64 at 60 columns; the newline checks that it is
		// stripped.
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

// TestGetFileRawStreamsRawMedia checks that GetFileRaw streams content through
// the raw media type, which is the path a large file such as a full transcript
// over the inline limit must use.
func TestGetFileRawStreamsRawMedia(t *testing.T) {
	want := []byte(`{"role":"agent","kind":"message"}` + "\n")
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/open-hai/x/contents/transcript.jsonl" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github.raw" {
			t.Errorf("Accept = %q, want the raw media type", got)
		}
		_, _ = w.Write(want) // raw bytes, not JSON-wrapped
	})
	got, ok, err := c.GetFileRaw(context.Background(), "open-hai", "x", "transcript.jsonl")
	if err != nil || !ok || string(got) != string(want) {
		t.Fatalf("GetFileRaw = %q ok=%v err=%v", got, ok, err)
	}
}

func TestGetFileRawAbsent(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if _, ok, err := c.GetFileRaw(context.Background(), "open-hai", "x", "transcript.jsonl"); ok || err != nil {
		t.Errorf("absent file: ok=%v err=%v; want false, nil", ok, err)
	}
}

func TestFileUnchangedComparesBlobSHA(t *testing.T) {
	content := []byte("a large transcript...\n")
	// The git blob sha of the content, so the server can echo it.
	sha := gitBlobSHA(content)

	same := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": sha, "encoding": "none"})
	})
	if ok, err := same.FileUnchanged(context.Background(), "open-hai", "x", "transcript.jsonl", content); err != nil || !ok {
		t.Errorf("matching blob sha should be unchanged: ok=%v err=%v", ok, err)
	}

	diff := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "deadbeef", "encoding": "none"})
	})
	if ok, err := diff.FileUnchanged(context.Background(), "open-hai", "x", "transcript.jsonl", content); err != nil || ok {
		t.Errorf("a different sha should be changed: ok=%v err=%v", ok, err)
	}

	absent := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	if ok, _ := absent.FileUnchanged(context.Background(), "open-hai", "x", "new.txt", content); ok {
		t.Error("an absent file is not unchanged")
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
