package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/latere-ai/replichai/internal/publish"
)

// FSSource reads reproductions from a directory where each subdirectory is a
// repository holding a replication.json — the layout FSForge writes, so the
// whole audit→publish→registry loop runs offline against one directory.
type FSSource struct{ Root string }

// NewFSSource returns a source rooted at dir.
func NewFSSource(dir string) *FSSource { return &FSSource{Root: dir} }

// List returns the repository (subdirectory) names, or nothing when the root
// does not yet exist.
func (s *FSSource) List(_ context.Context) ([]string, error) {
	entries, err := os.ReadDir(s.Root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// Manifest reads and parses one repository's replication.json. A repository
// that has none is ErrNoManifest — an unfinalized run, not a read failure.
func (s *FSSource) Manifest(_ context.Context, repo string) (publish.Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(s.Root, repo, publish.ManifestFile))
	if os.IsNotExist(err) {
		return publish.Manifest{}, fmt.Errorf("%s: %w", repo, ErrNoManifest)
	}
	if err != nil {
		return publish.Manifest{}, err
	}
	var m publish.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return publish.Manifest{}, fmt.Errorf("malformed manifest in %s: %w", repo, err)
	}
	return m, nil
}

// File reads one file from a repository directory.
func (s *FSSource) File(_ context.Context, repo, path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(filepath.Join(s.Root, repo, path))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

// Tree walks a repository directory and returns its file paths, forward-slashed.
func (s *FSSource) Tree(_ context.Context, repo string) ([]string, error) {
	root := filepath.Join(s.Root, repo)
	var paths []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return paths, err
}
