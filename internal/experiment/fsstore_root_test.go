package experiment

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A row id names a file, so an id that climbs is an id that names someone
// else's file. The store resolves every read and write inside its own directory
// and lets the operating system refuse the climb, rather than trusting whoever
// minted the id.
func TestFSStoreConfinesRowIDs(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "store")
	store, err := NewFSStore(dir)
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	planted := filepath.Join(parent, "secret.json")
	if err := os.WriteFile(planted, []byte(`{"id":"secret","project_id":"someone-else"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// "experiments/../../secret.json" resolves to the parent of the store.
	if e, err := store.GetExperiment(ctx, "../../secret"); err == nil {
		t.Errorf("a climbing id must not read outside the store, got %+v", e)
	}
	if err := store.CreateExperiment(ctx, &Experiment{ID: "../../planted"}); err == nil {
		t.Error("a climbing id must not write outside the store")
	}
	if _, err := os.Stat(filepath.Join(parent, "planted.json")); err == nil {
		t.Error("a row was written outside the store")
	}
	if err := store.DeleteDataset(ctx, "../../secret"); err == nil {
		t.Error("a climbing id must not delete outside the store")
	}
	if _, err := os.Stat(planted); err != nil {
		t.Errorf("the file outside the store was deleted: %v", err)
	}
}

// The artifact store's keys are checked for traversal before use, but a check
// on the string cannot see a symlink standing in the middle of the path. The
// store resolves keys against its own open directory, so a link planted inside
// it cannot redirect a write, a read, or a delete to somewhere else.
func TestFSArtifactStoreRefusesSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "blobs")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	blobs, err := NewFSArtifactStore(dir)
	if err != nil {
		t.Fatalf("NewFSArtifactStore: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("this filesystem does not do symlinks: %v", err)
	}
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := blobs.Put(ctx, "link/planted.txt", []byte("x")); err == nil {
		t.Error("Put through a symlink must be refused")
	}
	if _, err := os.Stat(filepath.Join(outside, "planted.txt")); err == nil {
		t.Error("Put wrote outside the store through a symlink")
	}
	if raw, ok, err := blobs.Get(ctx, "link/victim.txt"); err == nil && ok {
		t.Errorf("Get through a symlink must be refused, got %q", raw)
	}
	if err := blobs.Delete(ctx, "link/victim.txt"); err == nil {
		t.Error("Delete through a symlink must be refused")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("the file outside the store was deleted: %v", err)
	}
}
