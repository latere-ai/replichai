// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"context"
	"strings"
	"sync"
)

// MemStore is an in-memory Store for tests and for running before Postgres
// exists. It copies on the way in and out, so a caller cannot mutate stored
// state by holding a pointer it created or received.
type MemStore struct {
	mu          sync.Mutex
	experiments map[string]Experiment
	datasets    map[string]Dataset
}

// NewMemStore builds an empty in-memory store.
func NewMemStore() *MemStore {
	return &MemStore{
		experiments: map[string]Experiment{},
		datasets:    map[string]Dataset{},
	}
}

// CreateExperiment stores a copy of a new experiment row, refusing to
// overwrite one that already exists.
func (s *MemStore) CreateExperiment(_ context.Context, e *Experiment) error {
	if e.ID == "" {
		return ErrNoID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.experiments[e.ID]; ok {
		return ErrExists
	}
	s.experiments[e.ID] = cloneExperiment(*e)
	return nil
}

// GetExperiment returns a copy of one experiment row; a row that is not there
// is ErrNotFound.
func (s *MemStore) GetExperiment(_ context.Context, id string) (*Experiment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.experiments[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := cloneExperiment(e)
	return &c, nil
}

// UpdateExperiment replaces an existing experiment row with a copy of e; it
// never creates one.
func (s *MemStore) UpdateExperiment(_ context.Context, e *Experiment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.experiments[e.ID]; !ok {
		return ErrNotFound
	}
	s.experiments[e.ID] = cloneExperiment(*e)
	return nil
}

// ListExperiments returns copies of every experiment belonging to one project.
// The order follows map iteration, so it is arbitrary and a caller that needs a
// stable one must sort.
func (s *MemStore) ListExperiments(_ context.Context, projectID string) ([]*Experiment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Experiment
	for _, e := range s.experiments {
		if e.ProjectID == projectID {
			c := cloneExperiment(e)
			out = append(out, &c)
		}
	}
	return out, nil
}

// CreateDataset stores a copy of a new dataset row, refusing to overwrite one
// that already exists.
func (s *MemStore) CreateDataset(_ context.Context, d *Dataset) error {
	if d.ID == "" {
		return ErrNoID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.datasets[d.ID]; ok {
		return ErrExists
	}
	s.datasets[d.ID] = cloneDataset(*d)
	return nil
}

// GetDataset returns a copy of one dataset row; a row that is not there is
// ErrNotFound.
func (s *MemStore) GetDataset(_ context.Context, id string) (*Dataset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.datasets[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := cloneDataset(d)
	return &c, nil
}

// DeleteDataset drops one dataset row; a row that is not there is ErrNotFound.
func (s *MemStore) DeleteDataset(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.datasets[id]; !ok {
		return ErrNotFound
	}
	delete(s.datasets, id)
	return nil
}

// MemArtifactStore holds artifact bodies in a map, for tests and for running
// before a blob store exists. Like MemStore it copies on the way in and out, so
// the slice a caller stores or reads back is never the one held here.
type MemArtifactStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

// NewMemArtifactStore builds an empty in-memory artifact store.
func NewMemArtifactStore() *MemArtifactStore {
	return &MemArtifactStore{blobs: map[string][]byte{}}
}

// Put stores a copy of content under key and returns its checksum, so the
// caller keeping the bytes afterwards cannot change what was stored.
func (a *MemArtifactStore) Put(_ context.Context, key string, content []byte) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.blobs[key] = append([]byte(nil), content...)
	return Checksum(content), nil
}

// Get returns a copy of the content stored under key, reporting false when
// there is none.
func (a *MemArtifactStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, ok := a.blobs[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), b...), true, nil
}

// Delete removes the content stored under key; removing a key that is not there
// is a no-op.
func (a *MemArtifactStore) Delete(_ context.Context, key string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.blobs, key)
	return nil
}

// DeletePrefix removes every stored key beginning with prefix — how a dataset
// deletion reaches an experiment's derived artifacts.
func (a *MemArtifactStore) DeletePrefix(_ context.Context, prefix string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for k := range a.blobs {
		if strings.HasPrefix(k, prefix) {
			delete(a.blobs, k)
		}
	}
	return nil
}

func cloneExperiment(e Experiment) Experiment {
	if e.Outcome != nil {
		o := *e.Outcome
		o.Recomputed = append([]Statistic(nil), e.Outcome.Recomputed...)
		o.Artifacts = append([]Artifact(nil), e.Outcome.Artifacts...)
		e.Outcome = &o
	}
	return e
}

func cloneDataset(d Dataset) Dataset {
	d.Columns = append([]string(nil), d.Columns...)
	return d
}
