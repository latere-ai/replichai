// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"context"
	"fmt"
)

// PurgeDataset deletes a researcher's dataset: the uploaded file, every artifact
// derived from it, and the checksums that pointed at them. The experiment rows
// are kept, because the run happened, but their inputs are stripped, so only
// the record of the run survives.
//
// This is a deletion and not a flag; 007b rejected soft-delete. Afterwards
// nothing the researcher asked to remove remains on disk. The bytes are removed
// before the row so the experiments that used the dataset can still be found
// through its project.
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
