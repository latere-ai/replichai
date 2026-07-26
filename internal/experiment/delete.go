package experiment

import (
	"context"
	"fmt"
)

// PurgeDataset deletes a researcher's dataset for real: the uploaded file, every
// artifact derived from it, and the checksums that pointed at them. It keeps the
// experiment rows — a run happened, and that record stands — but strips their
// inputs, so what survives is the fact of the run, not the data behind it.
//
// This is deletion, not a flag (007b's rejected soft-delete): after it, nothing
// the researcher asked us to remove is still on disk. It runs before the row so
// it can find the experiments that used the dataset through its project.
func PurgeDataset(ctx context.Context, store Store, blobs ArtifactStore, datasetID string) error {
	d, err := store.GetDataset(ctx, datasetID)
	if err != nil {
		return err // ErrNotFound if already gone
	}

	exps, err := store.ListExperiments(ctx, d.ProjectID)
	if err != nil {
		return fmt.Errorf("list experiments for purge: %w", err)
	}
	for _, e := range exps {
		if e.DatasetID != datasetID {
			continue
		}
		if err := blobs.DeletePrefix(ctx, ArtifactPrefix(e.ID)); err != nil {
			return fmt.Errorf("delete artifacts of %s: %w", e.ID, err)
		}
		if e.Outcome != nil && len(e.Outcome.Artifacts) > 0 {
			e.Outcome.Artifacts = nil // the checksums go with the bytes
			if err := store.UpdateExperiment(ctx, e); err != nil {
				return fmt.Errorf("strip inputs from %s: %w", e.ID, err)
			}
		}
	}

	if err := blobs.Delete(ctx, DatasetKey(datasetID)); err != nil {
		return fmt.Errorf("delete dataset bytes: %w", err)
	}
	return store.DeleteDataset(ctx, datasetID)
}
