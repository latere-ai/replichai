package registry

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A repository name and an artifact path both arrive from the read surface: the
// name from a session record, the path from a query string. Neither may be used
// to read anything the caller was not authorised to see, so the source resolves
// them inside nested roots and the operating system refuses the climb.
//
// Joining and cleaning the two into one path is not enough, which is what these
// cases pin down: "repo-a" + "../repo-b/secret.txt" collapses to a path that is
// still inside the registry directory, so a check that only asked "does this
// stay under the root?" would have served another session's repository.
func TestFSSourceConfinesRepoAndPath(t *testing.T) {
	registryRoot := t.TempDir()
	writeRepo(t, registryRoot, "repo-a", []byte("{}"))
	writeRepo(t, registryRoot, "repo-b", []byte("{}"))
	if err := os.WriteFile(filepath.Join(registryRoot, "repo-b", "secret.txt"), []byte("other session"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file next to the registry, to prove the registry root itself holds.
	outside := filepath.Join(filepath.Dir(registryRoot), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	src := NewFSSource(registryRoot)
	ctx := context.Background()

	raw, ok, err := src.File(ctx, "repo-a", "../repo-b/secret.txt")
	if err == nil || ok || raw != nil {
		t.Errorf("a path climbing into a sibling repository must be refused, got %q ok=%v err=%v", raw, ok, err)
	}
	if raw, ok, err := src.File(ctx, "repo-a", "../../outside.txt"); err == nil || ok || raw != nil {
		t.Errorf("a path climbing out of the registry must be refused, got %q ok=%v err=%v", raw, ok, err)
	}
	if raw, ok, err := src.File(ctx, "..", "outside.txt"); err == nil || ok || raw != nil {
		t.Errorf("a repository name climbing out of the registry must be refused, got %q ok=%v err=%v", raw, ok, err)
	}
	if _, err := src.Manifest(ctx, "../"+filepath.Base(registryRoot)+"/repo-b"); err == nil {
		t.Error("a repository name climbing out of the registry must be refused by Manifest")
	}
	if paths, err := src.Tree(ctx, ".."); err == nil {
		t.Errorf("a repository name climbing out of the registry must be refused by Tree, got %v", paths)
	}

	// The ordinary read still works, so the confinement is not just refusing
	// everything.
	if raw, ok, err := src.File(ctx, "repo-b", "secret.txt"); err != nil || !ok || string(raw) != "other session" {
		t.Errorf("the repository's own file = %q ok=%v err=%v", raw, ok, err)
	}
}
