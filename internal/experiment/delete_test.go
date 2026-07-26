package experiment

import (
	"context"
	"errors"
	"testing"
)

// AC9: deleting a dataset removes the file and its derived artifacts; the
// experiment row survives with inputs absent. Asserted by reading the store,
// against both implementations.
func TestPurgeDataset(t *testing.T) {
	for _, kind := range []string{"mem", "fs"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			store, blobs := newStores(t, kind)

			// A dataset, its bytes, and an experiment that ran against it with a
			// derived artifact.
			d := &Dataset{ID: "ds-1", ProjectID: "proj-1", Name: "data.csv", Columns: []string{"rt"}}
			_ = store.CreateDataset(ctx, d)
			sum, _ := blobs.Put(ctx, DatasetKey("ds-1"), []byte("rt\n1\n"))

			e := &Experiment{ID: "exp-1", ProjectID: "proj-1", Source: SourceUploaded, DatasetID: "ds-1"}
			artSum, _ := blobs.Put(ctx, ArtifactKey("exp-1", "results.csv"), []byte("out"))
			e.Outcome = &Outcome{Ran: true, Artifacts: []Artifact{{Name: "results.csv", Checksum: artSum}}}
			_ = store.CreateExperiment(ctx, e)

			// An unrelated experiment/dataset that must be untouched.
			other := &Dataset{ID: "ds-2", ProjectID: "proj-1", Name: "keep.csv"}
			_ = store.CreateDataset(ctx, other)
			_, _ = blobs.Put(ctx, DatasetKey("ds-2"), []byte("keep"))
			_ = store.CreateExperiment(ctx, &Experiment{ID: "exp-2", ProjectID: "proj-1", DatasetID: "ds-2"})
			_, _ = blobs.Put(ctx, ArtifactKey("exp-2", "r.csv"), []byte("other"))

			if sum == "" {
				t.Fatal("precondition: dataset bytes stored")
			}

			if err := PurgeDataset(ctx, store, blobs, "ds-1"); err != nil {
				t.Fatalf("PurgeDataset: %v", err)
			}

			// The dataset row and bytes are gone.
			if _, err := store.GetDataset(ctx, "ds-1"); !errors.Is(err, ErrNotFound) {
				t.Errorf("dataset row should be gone, got %v", err)
			}
			if _, ok, _ := blobs.Get(ctx, DatasetKey("ds-1")); ok {
				t.Error("dataset bytes should be gone")
			}
			// The derived artifact bytes are gone.
			if _, ok, _ := blobs.Get(ctx, ArtifactKey("exp-1", "results.csv")); ok {
				t.Error("derived artifact bytes should be gone")
			}
			// The experiment row survives, with its inputs (checksums) absent.
			survivor, err := store.GetExperiment(ctx, "exp-1")
			if err != nil {
				t.Fatalf("experiment row should survive: %v", err)
			}
			if !survivor.Outcome.Ran {
				t.Error("the record that a run happened should stand")
			}
			if len(survivor.Outcome.Artifacts) != 0 {
				t.Errorf("inputs should be absent, got artifacts %v", survivor.Outcome.Artifacts)
			}

			// The unrelated dataset, its bytes, and its artifacts are untouched.
			if _, err := store.GetDataset(ctx, "ds-2"); err != nil {
				t.Errorf("unrelated dataset should survive: %v", err)
			}
			if _, ok, _ := blobs.Get(ctx, ArtifactKey("exp-2", "r.csv")); !ok {
				t.Error("unrelated artifacts should survive")
			}
		})
	}
}

func TestPurgeMissingDataset(t *testing.T) {
	store, blobs := newStores(t, "mem")
	if err := PurgeDataset(context.Background(), store, blobs, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purging a missing dataset should be ErrNotFound, got %v", err)
	}
}
