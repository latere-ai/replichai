package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"

	"github.com/latere-ai/replichai/internal/publish"
)

// FSSource reads reproductions from a directory where each subdirectory is a
// repository holding a replication.json — the layout FSForge writes, so the
// whole audit→publish→registry loop runs offline against one directory.
type FSSource struct{ Root string }

// NewFSSource returns a source rooted at dir.
func NewFSSource(dir string) *FSSource { return &FSSource{Root: dir} }

// openRepo opens one repository directory as a confined root, reporting false
// when the registry root or the repository itself is absent.
//
// Both names that reach this source arrive from the read surface: the
// repository comes from a session record and the file path from a query
// string. Nesting one root inside the other is what makes those names safe —
// the repository cannot climb out of the registry, and a file path cannot climb
// out of its repository into a sibling one the caller was never authorised to
// read. Confinement is enforced by the operating system against the open
// directory, so it holds against a symlink swapped in mid-read, which a
// string check cannot.
func (s *FSSource) openRepo(repo string) (*os.Root, bool, error) {
	root, err := os.OpenRoot(s.Root)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	// The nested root holds its own descriptor, so the registry root is done.
	defer func() { _ = root.Close() }()
	sub, err := root.OpenRoot(repo)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return sub, true, nil
}

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
	root, ok, err := s.openRepo(repo)
	if err != nil {
		return publish.Manifest{}, err
	}
	if !ok {
		return publish.Manifest{}, fmt.Errorf("%s: %w", repo, ErrNoManifest)
	}
	defer func() { _ = root.Close() }()
	raw, err := root.ReadFile(publish.ManifestFile)
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

// File reads one file from a repository directory. A path that would leave the
// repository is an error, not a miss: the caller asked for something it was
// never authorised to see, and saying "not found" would hide that.
func (s *FSSource) File(_ context.Context, repo, path string) ([]byte, bool, error) {
	root, ok, err := s.openRepo(repo)
	if err != nil || !ok {
		return nil, false, err
	}
	defer func() { _ = root.Close() }()
	raw, err := root.ReadFile(path)
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
	root, ok, err := s.openRepo(repo)
	if err != nil || !ok {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	var paths []string
	// Walking the root's own fs.FS yields slash-separated names relative to the
	// repository, which is exactly what the read surface publishes.
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	return paths, err
}
