package registry

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/latere-ai/replichai/internal/publish"
)

func writeRepo(t *testing.T, root, name string, body []byte) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, publish.ManifestFile), body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestFSSourceReadsAndParses(t *testing.T) {
	root := t.TempDir()
	good, _ := json.Marshal(manifestOf("repo-a", "10.1145/x", "a-paper", t1))
	writeRepo(t, root, "repo-a", good)
	writeRepo(t, root, "repo-bad", []byte("{not json"))

	src := NewFSSource(root)
	names, err := src.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("want 2 repos, got %v", names)
	}

	m, err := src.Manifest(context.Background(), "repo-a")
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if m.Paper.Key != "10.1145/x" {
		t.Errorf("parsed manifest = %+v", m.Paper)
	}
	if _, err := src.Manifest(context.Background(), "repo-bad"); err == nil {
		t.Error("a malformed manifest should error (the registry then skips it)")
	}

	// End to end through the registry: the good repo is found, the bad skipped.
	r := New(src, WithWarner(func(string) {}))
	priors, err := r.Lookup(context.Background(), doiID("10.1145/x"))
	if err != nil || len(priors) != 1 {
		t.Errorf("Lookup over FSSource: %d priors, err=%v", len(priors), err)
	}
}

func TestFSSourceMissingRootIsEmpty(t *testing.T) {
	src := NewFSSource(filepath.Join(t.TempDir(), "does-not-exist"))
	names, err := src.List(context.Background())
	if err != nil || len(names) != 0 {
		t.Errorf("a missing root should list nothing without error, got %v err=%v", names, err)
	}
}
