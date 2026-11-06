// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// 007b ships before Postgres (006b) in the build sequence, so persistence is an
// interface with in-memory and filesystem implementations, as 006b does for
// jobs. The Postgres implementation, its migration, and the job-kind dispatch
// land with 006b against these contracts, so the engine is complete and
// testable first.

// Errors the stores return.
var (
	// ErrNotFound reports a missing experiment or dataset.
	ErrNotFound = errors.New("not found")
	// ErrExists reports a create against an ID already taken.
	ErrExists = errors.New("already exists")
	// ErrNoID reports a create with no ID set.
	ErrNoID = errors.New("record has no ID")
)

// Experiment is the durable record of what was run against what. Unlike a job
// it is not disposable: it is a finding only the platform holds, so it outlives
// the job that produced it. The instrument is kept as a reference, not a copy;
// the declaration is re-resolved from the pinned commit.
type Experiment struct {
	ID        string        `json:"id"`
	ProjectID string        `json:"project_id"`
	Ref       InstrumentRef `json:"instrument"` // repo + commit
	Source    Source        `json:"source"`
	DatasetID string        `json:"dataset_id,omitempty"` // empty for released
	Command   string        `json:"command"`              // resolved from analysis
	Outcome   *Outcome      `json:"outcome,omitzero"`     // nil until the run completes
	// CohortKind is the synthetic label (007c), CohortGenerated or
	// CohortModel, and is empty for a non-synthetic source. It is required on
	// every synthetic experiment and becomes a NOT NULL column when 006b
	// lands, so no synthetic result can reach a reader without it.
	CohortKind CohortKind `json:"cohort_kind,omitempty"`
}

// Dataset is an uploaded or collected file's record. Its bytes live in the
// ArtifactStore under DatasetKey(ID); this row carries the metadata and the
// header the server validates uploads against.
type Dataset struct {
	ID        string   `json:"id"`
	ProjectID string   `json:"project_id"`
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`  // the dataset header, for input validation
	Checksum  string   `json:"checksum"` // "sha256:<hex>" of the bytes
}

// Store persists experiment and dataset rows. It is the metadata plane; bytes
// live in the ArtifactStore. Deleting a dataset removes it (AC9) and cascades
// to the bytes through the ArtifactStore, which the caller wires; the Store
// owns only the rows.
type Store interface {
	CreateExperiment(ctx context.Context, e *Experiment) error
	GetExperiment(ctx context.Context, id string) (*Experiment, error)
	UpdateExperiment(ctx context.Context, e *Experiment) error
	ListExperiments(ctx context.Context, projectID string) ([]*Experiment, error)

	CreateDataset(ctx context.Context, d *Dataset) error
	GetDataset(ctx context.Context, id string) (*Dataset, error)
	DeleteDataset(ctx context.Context, id string) error
}

// ArtifactStore holds the bytes an experiment consumes and produces, keyed by
// string. Put returns the content checksum so a row can attribute a result to
// exact bytes. DeletePrefix removes everything under a key prefix, which is how
// a dataset deletion reaches its derived artifacts.
type ArtifactStore interface {
	Put(ctx context.Context, key string, content []byte) (checksum string, err error)
	Get(ctx context.Context, key string) (content []byte, ok bool, err error)
	Delete(ctx context.Context, key string) error
	DeletePrefix(ctx context.Context, prefix string) error
}

// DatasetKey is where a dataset's bytes live in an ArtifactStore.
func DatasetKey(datasetID string) string { return "datasets/" + datasetID }

// ArtifactPrefix is the key prefix for one experiment's result artifacts, so
// they delete together.
func ArtifactPrefix(experimentID string) string { return "experiments/" + experimentID + "/" }

// ArtifactKey is where one named result artifact lives.
func ArtifactKey(experimentID, name string) string { return ArtifactPrefix(experimentID) + name }

// Checksum is the content address these stores agree on: "sha256:<hex>".
func Checksum(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// NewID mints a random identifier for an experiment or dataset. Creation logic
// assigns it; the stores only persist what they are given.
func NewID() string {
	var b [16]byte
	// crypto/rand.Read never returns an error since Go 1.24: it fills the
	// buffer or aborts the process.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
