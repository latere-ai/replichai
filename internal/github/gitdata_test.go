// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package github

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCommitFilesAtomic(t *testing.T) {
	var treeBody map[string]any
	blobs := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/open-hai/x/contents/"):
			w.WriteHeader(http.StatusNotFound) // absent, so the files are changed
		case r.URL.Path == "/repos/open-hai/x":
			_ = json.NewEncoder(w).Encode(map[string]string{"default_branch": "main"})
		case r.URL.Path == "/repos/open-hai/x/git/ref/heads/main":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": "basecommit"}})
		case strings.HasPrefix(r.URL.Path, "/repos/open-hai/x/git/commits/") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": map[string]string{"sha": "basetree"}})
		case r.URL.Path == "/repos/open-hai/x/git/blobs":
			blobs++
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "blob"})
		case r.URL.Path == "/repos/open-hai/x/git/trees":
			_ = json.NewDecoder(r.Body).Decode(&treeBody)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "newtree"})
		case r.URL.Path == "/repos/open-hai/x/git/commits" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "newcommit"})
		case r.URL.Path == "/repos/open-hai/x/git/refs/heads/main":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})

	committed, err := c.CommitFiles(context.Background(), "open-hai", "x", "chore: transcript",
		map[string][]byte{"TRANSCRIPT.md": []byte("index"), "transcript/001.md": []byte("part")})
	if err != nil || !committed {
		t.Fatalf("CommitFiles: committed=%v err=%v", committed, err)
	}
	if blobs != 2 {
		t.Errorf("want a blob per file, got %d", blobs)
	}
	if treeBody["base_tree"] != "basetree" {
		t.Errorf("tree should build on the base tree, got %v", treeBody["base_tree"])
	}
	if tree, _ := treeBody["tree"].([]any); len(tree) != 2 {
		t.Errorf("tree should have both files, got %d entries", len(tree))
	}
}

func TestCommitFilesIdempotent(t *testing.T) {
	content := []byte("already there")
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/open-hai/x/contents/") {
			// The remote blob sha matches, so nothing changed.
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": gitBlobSHA(content), "encoding": "none"})
			return
		}
		t.Errorf("no git-data write should happen when unchanged: %s %s", r.Method, r.URL.Path)
	})

	committed, err := c.CommitFiles(context.Background(), "open-hai", "x", "msg",
		map[string][]byte{"TRANSCRIPT.md": content})
	if err != nil {
		t.Fatalf("CommitFiles: %v", err)
	}
	if committed {
		t.Error("an unchanged file set must not create a commit")
	}
}

// TestCommitFilesRetriesAStaleParent covers a branch that moved between
// reading its head and updating it, which GitHub answers with 422. This
// happens when the second commit of a finalize lands on a head its own first
// commit just moved, and when GitHub's ref reads lag a Contents API write. One
// retry on the new head is enough; two failures are a real error.
func TestCommitFilesRetriesAStaleParent(t *testing.T) {
	head, patches, blobs := "basecommit", 0, 0
	var parents []any
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/open-hai/x/contents/"):
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/repos/open-hai/x":
			_ = json.NewEncoder(w).Encode(map[string]string{"default_branch": "main"})
		case r.URL.Path == "/repos/open-hai/x/git/ref/heads/main":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": head}})
		case strings.HasPrefix(r.URL.Path, "/repos/open-hai/x/git/commits/") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": map[string]string{"sha": "tree-of-" + head}})
		case r.URL.Path == "/repos/open-hai/x/git/blobs":
			blobs++
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "blob"})
		case r.URL.Path == "/repos/open-hai/x/git/trees":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "newtree"})
		case r.URL.Path == "/repos/open-hai/x/git/commits" && r.Method == http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			parents = append(parents, body["parents"])
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "newcommit"})
		case r.URL.Path == "/repos/open-hai/x/git/refs/heads/main":
			patches++
			if patches == 1 {
				// Somebody else moved the branch; this parent is stale.
				head = "movedcommit"
				w.WriteHeader(http.StatusUnprocessableEntity)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})

	committed, err := c.CommitFiles(context.Background(), "open-hai", "x", "chore: transcript",
		map[string][]byte{"TRANSCRIPT.md": []byte("index")})
	if err != nil || !committed {
		t.Fatalf("CommitFiles: committed=%v err=%v", committed, err)
	}
	if patches != 2 {
		t.Errorf("ref updates = %d, want a retry", patches)
	}
	// The retry re-reads the branch and builds on the head it actually has.
	if len(parents) != 2 {
		t.Fatalf("commits created = %d, want 2", len(parents))
	}
	if got, _ := parents[1].([]any); len(got) != 1 || got[0] != "movedcommit" {
		t.Errorf("the retry built on %v, want the moved head", parents[1])
	}
	// Blobs are content-addressed; a retry must not re-upload them.
	if blobs != 1 {
		t.Errorf("blobs uploaded = %d, want 1", blobs)
	}
}

// TestCommitFilesGivesUpOnAPersistentlyMovingBranch checks that there is one
// retry and not a loop. A branch that keeps moving is a real failure, and the
// caller gets the error rather than an unbounded retry.
func TestCommitFilesGivesUpOnAPersistentlyMovingBranch(t *testing.T) {
	patches := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/open-hai/x/contents/"):
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/repos/open-hai/x":
			_ = json.NewEncoder(w).Encode(map[string]string{"default_branch": "main"})
		case r.URL.Path == "/repos/open-hai/x/git/ref/heads/main":
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": "basecommit"}})
		case strings.HasPrefix(r.URL.Path, "/repos/open-hai/x/git/commits/") && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"tree": map[string]string{"sha": "basetree"}})
		case r.URL.Path == "/repos/open-hai/x/git/blobs":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "blob"})
		case r.URL.Path == "/repos/open-hai/x/git/trees":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "newtree"})
		case r.URL.Path == "/repos/open-hai/x/git/commits" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"sha": "newcommit"})
		case r.URL.Path == "/repos/open-hai/x/git/refs/heads/main":
			patches++
			w.WriteHeader(http.StatusUnprocessableEntity)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})

	if _, err := c.CommitFiles(context.Background(), "open-hai", "x", "msg",
		map[string][]byte{"TRANSCRIPT.md": []byte("index")}); err == nil {
		t.Fatal("a branch that keeps moving must surface as an error")
	}
	if patches != 2 {
		t.Errorf("ref updates = %d, want exactly two attempts", patches)
	}
}
