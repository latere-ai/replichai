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

func (s *MemStore) UpdateExperiment(_ context.Context, e *Experiment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.experiments[e.ID]; !ok {
		return ErrNotFound
	}
	s.experiments[e.ID] = cloneExperiment(*e)
	return nil
}

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

func (s *MemStore) DeleteDataset(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.datasets[id]; !ok {
		return ErrNotFound
	}
	delete(s.datasets, id)
	return nil
}

// MemArtifactStore is an in-memory ArtifactStore.
type MemArtifactStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

// NewMemArtifactStore builds an empty in-memory artifact store.
func NewMemArtifactStore() *MemArtifactStore {
	return &MemArtifactStore{blobs: map[string][]byte{}}
}

func (a *MemArtifactStore) Put(_ context.Context, key string, content []byte) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.blobs[key] = append([]byte(nil), content...)
	return Checksum(content), nil
}

func (a *MemArtifactStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, ok := a.blobs[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), b...), true, nil
}

func (a *MemArtifactStore) Delete(_ context.Context, key string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.blobs, key)
	return nil
}

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
