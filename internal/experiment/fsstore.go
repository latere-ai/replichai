package experiment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FSStore persists rows as JSON files under a directory, so an experiment
// survives a restart before Postgres exists. One file per record; the layout is
// an implementation detail the interface hides.
type FSStore struct {
	root string
}

// NewFSStore builds a filesystem store rooted at dir, creating it if needed.
func NewFSStore(dir string) (*FSStore, error) {
	for _, sub := range []string{"experiments", "datasets"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("create store dir: %w", err)
		}
	}
	return &FSStore{root: dir}, nil
}

func (s *FSStore) experimentPath(id string) string {
	return filepath.Join(s.root, "experiments", id+".json")
}
func (s *FSStore) datasetPath(id string) string {
	return filepath.Join(s.root, "datasets", id+".json")
}

func (s *FSStore) CreateExperiment(_ context.Context, e *Experiment) error {
	if e.ID == "" {
		return ErrNoID
	}
	if _, err := os.Stat(s.experimentPath(e.ID)); err == nil {
		return ErrExists
	}
	return writeJSON(s.experimentPath(e.ID), e)
}

func (s *FSStore) GetExperiment(_ context.Context, id string) (*Experiment, error) {
	var e Experiment
	if err := readJSON(s.experimentPath(id), &e); err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *FSStore) UpdateExperiment(_ context.Context, e *Experiment) error {
	if _, err := os.Stat(s.experimentPath(e.ID)); err != nil {
		return ErrNotFound
	}
	return writeJSON(s.experimentPath(e.ID), e)
}

func (s *FSStore) ListExperiments(_ context.Context, projectID string) ([]*Experiment, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "experiments"))
	if err != nil {
		return nil, fmt.Errorf("list experiments: %w", err)
	}
	var out []*Experiment
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var e Experiment
		if err := readJSON(filepath.Join(s.root, "experiments", entry.Name()), &e); err != nil {
			return nil, err
		}
		if e.ProjectID == projectID {
			ec := e
			out = append(out, &ec)
		}
	}
	return out, nil
}

func (s *FSStore) CreateDataset(_ context.Context, d *Dataset) error {
	if d.ID == "" {
		return ErrNoID
	}
	if _, err := os.Stat(s.datasetPath(d.ID)); err == nil {
		return ErrExists
	}
	return writeJSON(s.datasetPath(d.ID), d)
}

func (s *FSStore) GetDataset(_ context.Context, id string) (*Dataset, error) {
	var d Dataset
	if err := readJSON(s.datasetPath(id), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *FSStore) DeleteDataset(_ context.Context, id string) error {
	if err := os.Remove(s.datasetPath(id)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return fmt.Errorf("delete dataset: %w", err)
	}
	return nil
}

// FSArtifactStore holds bytes under a directory, one file per key. Keys may
// contain slashes and become nested directories.
type FSArtifactStore struct {
	root string
}

// NewFSArtifactStore builds a filesystem artifact store rooted at dir.
func NewFSArtifactStore(dir string) (*FSArtifactStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact dir: %w", err)
	}
	return &FSArtifactStore{root: dir}, nil
}

// keyPath resolves key under root, refusing any key that would escape it. Keys
// are internally generated, but a store must never write outside its root.
func (a *FSArtifactStore) keyPath(key string) (string, error) {
	clean := filepath.Clean(key)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("artifact key escapes the store: %q", key)
	}
	target := filepath.Join(a.root, clean)
	if target != a.root && !strings.HasPrefix(target, a.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("artifact key escapes the store: %q", key)
	}
	return target, nil
}

func (a *FSArtifactStore) Put(_ context.Context, key string, content []byte) (string, error) {
	path, err := a.keyPath(key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create artifact parent: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", fmt.Errorf("write artifact: %w", err)
	}
	return Checksum(content), nil
}

func (a *FSArtifactStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	path, err := a.keyPath(key)
	if err != nil {
		return nil, false, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read artifact: %w", err)
	}
	return content, true, nil
}

func (a *FSArtifactStore) Delete(_ context.Context, key string) error {
	path, err := a.keyPath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete artifact: %w", err)
	}
	return nil
}

// DeletePrefix removes every stored key beginning with prefix — how a dataset
// deletion reaches an experiment's derived artifacts. It walks rather than
// RemoveAll'ing a directory, so a prefix that is not a whole directory boundary
// still deletes exactly the matching keys.
func (a *FSArtifactStore) DeletePrefix(_ context.Context, prefix string) error {
	return filepath.WalkDir(a.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(a.root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.ToSlash(rel), prefix) {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("delete artifact: %w", err)
			}
		}
		return nil
	})
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return json.Unmarshal(raw, v)
}
