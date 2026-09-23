// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package registry

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestFSSourceConfinesRepoAndPath checks that neither a repository name nor an
// artifact path can read anything the caller was not authorized to see. Both
// arrive from the read surface: the name from a session record, the path from
// a query string. The source resolves them inside nested roots, and the
// operating system refuses a path that escapes.
//
// Joining and cleaning the two into one path is not enough, which these cases
// cover: "repo-a" + "../repo-b/secret.txt" collapses to a path that is still
// inside the registry directory, so a check that only asked whether the path
// stays under the root would have served another session's repository.
func TestFSSourceConfinesRepoAndPath(t *testing.T) {
	registryRoot := t.TempDir()
	writeRepo(t, registryRoot, "repo-a", []byte("{}"))
	writeRepo(t, registryRoot, "repo-b", []byte("{}"))
	if err := os.WriteFile(filepath.Join(registryRoot, "repo-b", "secret.txt"), []byte("other session"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file next to the registry, to check that the registry root itself
	// holds.
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

	// The ordinary read still works, so the confinement does not refuse every
	// read.
	if raw, ok, err := src.File(ctx, "repo-b", "secret.txt"); err != nil || !ok || string(raw) != "other session" {
		t.Errorf("the repository's own file = %q ok=%v err=%v", raw, ok, err)
	}
}
