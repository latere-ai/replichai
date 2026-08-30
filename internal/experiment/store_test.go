// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/latere-ai/replichai/internal/publish"
)

// Both implementations must satisfy one contract, so the suite runs against
// each. A divergence between the in-memory fake and the filesystem store is a
// bug the runner would inherit.
func newStores(t *testing.T, kind string) (Store, ArtifactStore) {
	t.Helper()
	switch kind {
	case "mem":
		return NewMemStore(), NewMemArtifactStore()
	case "fs":
		dir := t.TempDir()
		s, err := NewFSStore(dir + "/rows")
		if err != nil {
			t.Fatalf("NewFSStore: %v", err)
		}
		a, err := NewFSArtifactStore(dir + "/blobs")
		if err != nil {
			t.Fatalf("NewFSArtifactStore: %v", err)
		}
		return s, a
	}
	t.Fatalf("unknown store kind %q", kind)
	return nil, nil
}

func sampleExperiment(id string) *Experiment {
	return &Experiment{
		ID:        id,
		ProjectID: "proj-1",
		Ref:       InstrumentRef{Repo: publish.Repo{Owner: "open-hai", Name: "r"}, Commit: "abc123"},
		Source:    SourceReleased,
		Command:   "python src/analyze.py {{INPUT}}",
	}
}

func TestStoreConformance(t *testing.T) {
	for _, kind := range []string{"mem", "fs"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			store, _ := newStores(t, kind)

			// Experiments: create, get, the create rules.
			if err := store.CreateExperiment(ctx, &Experiment{}); !errors.Is(err, ErrNoID) {
				t.Fatalf("empty ID should be ErrNoID, got %v", err)
			}
			e := sampleExperiment("exp-1")
			if err := store.CreateExperiment(ctx, e); err != nil {
				t.Fatalf("CreateExperiment: %v", err)
			}
			if err := store.CreateExperiment(ctx, sampleExperiment("exp-1")); !errors.Is(err, ErrExists) {
				t.Fatalf("duplicate should be ErrExists, got %v", err)
			}
			got, err := store.GetExperiment(ctx, "exp-1")
			if err != nil || got.Command != e.Command || got.Ref.Commit != "abc123" {
				t.Fatalf("GetExperiment = %+v, err %v", got, err)
			}
			if _, err := store.GetExperiment(ctx, "missing"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing should be ErrNotFound, got %v", err)
			}

			// Update records the outcome.
			got.Outcome = &Outcome{Ran: true, Command: got.Command, Recomputed: []Statistic{{Metric: "m", Value: "1"}}}
			if err := store.UpdateExperiment(ctx, got); err != nil {
				t.Fatalf("UpdateExperiment: %v", err)
			}
			if err := store.UpdateExperiment(ctx, sampleExperiment("nope")); !errors.Is(err, ErrNotFound) {
				t.Fatalf("update of a missing row should be ErrNotFound, got %v", err)
			}
			reread, _ := store.GetExperiment(ctx, "exp-1")
			if reread.Outcome == nil || !reread.Outcome.Ran || len(reread.Outcome.Recomputed) != 1 {
				t.Fatalf("outcome did not round-trip: %+v", reread.Outcome)
			}

			// List filters by project.
			_ = store.CreateExperiment(ctx, &Experiment{ID: "exp-2", ProjectID: "proj-2"})
			list, _ := store.ListExperiments(ctx, "proj-1")
			if len(list) != 1 || list[0].ID != "exp-1" {
				t.Fatalf("ListExperiments(proj-1) = %+v, want just exp-1", list)
			}

			// Datasets: create, get, delete.
			d := &Dataset{ID: "ds-1", ProjectID: "proj-1", Name: "data.csv", Columns: []string{"rt", "id"}}
			if err := store.CreateDataset(ctx, d); err != nil {
				t.Fatalf("CreateDataset: %v", err)
			}
			gotD, err := store.GetDataset(ctx, "ds-1")
			if err != nil || gotD.Name != "data.csv" || len(gotD.Columns) != 2 {
				t.Fatalf("GetDataset = %+v, err %v", gotD, err)
			}
			if err := store.DeleteDataset(ctx, "ds-1"); err != nil {
				t.Fatalf("DeleteDataset: %v", err)
			}
			if _, err := store.GetDataset(ctx, "ds-1"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("deleted dataset should be gone, got %v", err)
			}
			if err := store.DeleteDataset(ctx, "ds-1"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("deleting twice should be ErrNotFound, got %v", err)
			}
		})
	}
}

// The store must not mutate through a pointer the caller keeps or receives.
func TestStoreCopiesOnBoundary(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	e := sampleExperiment("exp-iso")
	_ = store.CreateExperiment(ctx, e)
	e.Command = "mutated after create" // must not reach the store
	got, _ := store.GetExperiment(ctx, "exp-iso")
	if got.Command == "mutated after create" {
		t.Fatal("the store aliased the caller's pointer")
	}
	got.Command = "mutated on the returned copy" // must not reach the store either
	again, _ := store.GetExperiment(ctx, "exp-iso")
	if again.Command == "mutated on the returned copy" {
		t.Fatal("the store returned an aliased pointer")
	}
}

func TestArtifactStoreConformance(t *testing.T) {
	for _, kind := range []string{"mem", "fs"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			_, blobs := newStores(t, kind)

			sum, err := blobs.Put(ctx, ArtifactKey("exp-1", "results.csv"), []byte("a,b\n1,2\n"))
			if err != nil || sum != Checksum([]byte("a,b\n1,2\n")) {
				t.Fatalf("Put checksum = %q, err %v", sum, err)
			}
			_, _ = blobs.Put(ctx, ArtifactKey("exp-1", "log.txt"), []byte("ran"))
			_, _ = blobs.Put(ctx, DatasetKey("ds-1"), []byte("dataset bytes"))

			body, ok, err := blobs.Get(ctx, ArtifactKey("exp-1", "results.csv"))
			if err != nil || !ok || string(body) != "a,b\n1,2\n" {
				t.Fatalf("Get = %q ok=%v err=%v", body, ok, err)
			}
			if _, ok, _ := blobs.Get(ctx, "nope"); ok {
				t.Fatal("a missing key should report ok=false")
			}

			// DeletePrefix removes one experiment's artifacts, leaving others.
			if err := blobs.DeletePrefix(ctx, ArtifactPrefix("exp-1")); err != nil {
				t.Fatalf("DeletePrefix: %v", err)
			}
			if _, ok, _ := blobs.Get(ctx, ArtifactKey("exp-1", "results.csv")); ok {
				t.Fatal("exp-1 artifacts should be gone")
			}
			if _, ok, _ := blobs.Get(ctx, DatasetKey("ds-1")); !ok {
				t.Fatal("the dataset must survive an unrelated prefix delete")
			}

			// Delete of a single key, idempotent.
			if err := blobs.Delete(ctx, DatasetKey("ds-1")); err != nil {
				t.Fatalf("Delete: %v", err)
			}
			if err := blobs.Delete(ctx, DatasetKey("ds-1")); err != nil {
				t.Fatalf("deleting a missing key should be a no-op, got %v", err)
			}
		})
	}
}

// The filesystem artifact store must refuse a key that escapes its root, on
// every operation that resolves one.
func TestFSArtifactStoreRefusesEscape(t *testing.T) {
	blobs, err := NewFSArtifactStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFSArtifactStore: %v", err)
	}
	ctx := context.Background()
	if _, err := blobs.Put(ctx, "../escape", []byte("x")); err == nil {
		t.Error("Put: a traversing key should be refused")
	}
	if _, _, err := blobs.Get(ctx, "/etc/passwd"); err == nil {
		t.Error("Get: an absolute key should be refused")
	}
	if err := blobs.Delete(ctx, "../escape"); err == nil {
		t.Error("Delete: a traversing key should be refused")
	}
}

// A corrupt row on disk surfaces as an error, not a zero value silently.
func TestFSStoreSurfacesCorruptRow(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFSStore(dir)
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	if err := os.WriteFile(store.experimentPath("bad"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("seed corrupt row: %v", err)
	}
	if _, err := store.GetExperiment(context.Background(), "bad"); err == nil {
		t.Error("a corrupt row should error")
	}
	if _, err := store.ListExperiments(context.Background(), "any"); err == nil {
		t.Error("listing over a corrupt row should error")
	}
}
