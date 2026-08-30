// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"errors"

	"github.com/latere-ai/replichai/internal/instrument"
)

// A synthetic cohort drives an extracted protocol with no people in it. It is a
// pilot — by construction it precedes a study and never is one — and everything
// it produces is a claim about the instrument, never about the paper or about
// people. Two sub-kinds, named as cohort kinds:

// CohortKind is which synthetic cohort produced a pilot's data.
type CohortKind string

const (
	// CohortGenerated is a deterministic data generator: it reads the protocol
	// and emits conforming trial data from a recorded seed, with no model and no
	// credential. It carries the shakedown and the positive control, the two uses
	// that hold weight, and it is ungated because it claims nothing about a
	// responder.
	CohortGenerated CohortKind = "synthetic:generated"
	// CohortModel is a model cohort: N sessions driven through the protocol,
	// answering as instructed. Its only marginal claim over the generator is how
	// a plausible responder behaves, so it is gated on ground truth.
	CohortModel CohortKind = "synthetic:model"
)

// Errors the model-cohort gate returns. Each is a server-side refusal at
// creation, never a run that discovers the problem after the fact.
var (
	// ErrNoReleasedData reports that the platform holds no released dataset to
	// calibrate a model cohort against. Availability is a store fact — a 007b
	// dataset row exists or it does not — not something instrument.json declares,
	// because the audit cannot verify what data a deployment happens to hold.
	ErrNoReleasedData = errors.New("a model cohort needs the paper's released data to calibrate against")
	// ErrNoReportedGroundTruth reports an instrument that declares no reported
	// results, so a cohort's numbers could not be checked against a real answer.
	ErrNoReportedGroundTruth = errors.New("a model cohort needs the instrument's reported ground truth")
	// ErrNoServableComponent reports a protocol no component of which a model can
	// be driven through — a page cannot serve it, and neither can a cohort.
	ErrNoServableComponent = errors.New("a model cohort needs at least one servable component")
)

// Coverage is which components a model cohort may drive and which it must skip.
// A model cohort covers the servable components and refuses the rest: a
// manipulation the page cannot administer is one a model cannot undergo either,
// and describing it in a prompt and recording what the model does is exactly the
// fabrication this spec exists to prevent.
type Coverage struct {
	Servable []string                          // component names a cohort may cover
	Skipped  []instrument.ComponentServability // unservable components, with their failed criteria
}

// GateModelCohort decides whether a model cohort may run, and over which
// components. The gate is an intersection — released data held by the platform,
// reported ground truth in the instrument, and at least one servable component —
// so a model cohort the platform runs at first can always be checked against a
// real answer. It is a gate to relax deliberately once calibration has been
// measured, not a permanent property.
//
// releasedAvailable is the platform's own answer to "do we hold this paper's
// released data?", supplied by the caller because it is a store fact.
func GateModelCohort(inst Instrument, releasedAvailable bool) (Coverage, error) {
	if !releasedAvailable {
		return Coverage{}, ErrNoReleasedData
	}
	if inst.Decl.Protocol == nil || len(inst.Decl.Protocol.Reported) == 0 {
		return Coverage{}, ErrNoReportedGroundTruth
	}
	var cov Coverage
	for _, c := range inst.Decl.Servability.Components {
		if c.Servable {
			cov.Servable = append(cov.Servable, c.Component)
		} else {
			cov.Skipped = append(cov.Skipped, c)
		}
	}
	if len(cov.Servable) == 0 {
		return Coverage{}, ErrNoServableComponent
	}
	return cov, nil
}
