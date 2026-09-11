// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"errors"
	"fmt"
)

// Errors returned at creation, before any sandbox exists, so a run is never
// started only to discover inside a script that the instrument had nothing to
// run or that the data did not fit.
var (
	// ErrNoAnalysis reports an instrument whose analysis could not be
	// recovered, 007a's null analysis. It cannot be experimented on, and it is
	// refused before a run rather than after one (AC7).
	ErrNoAnalysis = errors.New("instrument has no runnable analysis")
	// ErrUnparameterisedCommand reports an analysis command that does not name
	// its input, so it cannot be pointed at new data.
	ErrUnparameterisedCommand = errors.New("analysis command does not parameterise its input")
	// ErrDatasetRequired reports an uploaded-source experiment created without a
	// dataset to run on.
	ErrDatasetRequired = errors.New("an uploaded-source experiment needs a dataset")
	// ErrSourceUnsupported reports a source this spec does not implement.
	// synthetic (007c) and human (007d) extend creation when they land.
	ErrSourceUnsupported = errors.New("data source not supported by this build")
)

// Create builds an experiment ready to run. It refuses everything that can be
// known without a sandbox: an instrument with no analysis (AC7), a command that
// cannot take new data, and, for an uploaded dataset, columns that do not match
// the analysis input, naming the offending column (AC5).
//
// The command is kept as the instrument declares it, input placeholder intact;
// the runner substitutes the real path when it knows where the data landed.
func Create(inst Instrument, source Source, dataset *Dataset, projectID string) (*Experiment, error) {
	if !source.Offline() {
		return nil, fmt.Errorf("%w: %s", ErrSourceUnsupported, source)
	}
	if inst.Decl.Analysis == nil {
		return nil, ErrNoAnalysis
	}
	cmd := inst.Decl.Analysis.Command
	if _, err := inst.Decl.Analysis.CommandFor("probe"); err != nil {
		return nil, ErrUnparameterisedCommand
	}

	var datasetID string
	if source == SourceUploaded {
		if dataset == nil {
			return nil, ErrDatasetRequired
		}
		if err := inst.Decl.Analysis.Input.Check(dataset.Columns); err != nil {
			return nil, fmt.Errorf("dataset does not match the instrument input: %w", err)
		}
		datasetID = dataset.ID
	}

	id := NewID()
	return &Experiment{
		ID:        id,
		ProjectID: projectID,
		Ref:       inst.Ref,
		Source:    source,
		DatasetID: datasetID,
		Command:   cmd,
	}, nil
}
