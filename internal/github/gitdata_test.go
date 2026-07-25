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
			w.WriteHeader(http.StatusNotFound) // absent → files are changed
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
