// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"errors"
	"testing"

	"github.com/latere-ai/replichai/internal/instrument"
)

// caseTwoInstrument is the shape of case-02's instrument: a servable
// prospective-memory task, an unservable feed manipulation, and reported ground
// truth. It is the golden fixture 007c is exercised against, so the model-cohort
// path runs in CI on a real instrument with a real answer.
func caseTwoInstrument() Instrument {
	return Instrument{
		Ref: InstrumentRef{Repo: testRepo, Commit: "c2"},
		Decl: instrument.Instrument{
			Protocol: &instrument.Protocol{
				Measures: []instrument.Measure{
					{Name: "pm_accuracy", Scale: "proportion", Level: "per_trial"},
					{Name: "rt", Scale: "ms", Level: "per_trial"},
				},
				Reported: []instrument.Reported{
					{Metric: "pm_accuracy", Value: "0.82", Citation: "Table 1"},
				},
			},
			Analysis: &instrument.Analysis{
				Command: "python src/analyze.py " + instrument.InputPlaceholder,
				Input: instrument.Schema{Columns: []instrument.Column{
					{Name: "participant", Type: "string", Required: true},
					{Name: "condition", Type: "categorical", Required: true},
					{Name: "pm_accuracy", Type: "number", Required: true},
				}},
			},
			Servability: instrument.Servability{
				Components: []instrument.ComponentServability{
					{Component: "prospective-memory task", Servable: true},
					{
						Component: "feed manipulation", Servable: false,
						Failed: []instrument.Criterion{instrument.CriterionPageAdministrable},
						Why:    "participants spend ten minutes on their own feeds elsewhere",
					},
				},
			},
		},
	}
}

// AC10: the gate admits case-02's PM task, so the model-cohort path has a real
// instrument with real ground truth to run against in CI.
func TestGateAdmitsCaseTwo(t *testing.T) {
	cov, err := GateModelCohort(caseTwoInstrument(), true)
	if err != nil {
		t.Fatalf("case-02 must pass the gate: %v", err)
	}
	if len(cov.Servable) != 1 || cov.Servable[0] != "prospective-memory task" {
		t.Errorf("servable = %v, want the PM task", cov.Servable)
	}
	if len(cov.Skipped) != 1 || cov.Skipped[0].Component != "feed manipulation" {
		t.Fatalf("skipped = %v, want the feed manipulation", cov.Skipped)
	}
	if len(cov.Skipped[0].Failed) == 0 {
		t.Error("a skipped component must carry its failed criteria for the pilot to cite")
	}
}

// AC9: the gate refuses on each missing condition, server-side.
func TestGateRefusals(t *testing.T) {
	t.Run("no released data", func(t *testing.T) {
		if _, err := GateModelCohort(caseTwoInstrument(), false); !errors.Is(err, ErrNoReleasedData) {
			t.Fatalf("want ErrNoReleasedData, got %v", err)
		}
	})

	t.Run("no reported ground truth", func(t *testing.T) {
		inst := caseTwoInstrument()
		inst.Decl.Protocol.Reported = nil
		if _, err := GateModelCohort(inst, true); !errors.Is(err, ErrNoReportedGroundTruth) {
			t.Fatalf("want ErrNoReportedGroundTruth, got %v", err)
		}
		// A null protocol fails the same way.
		inst.Decl.Protocol = nil
		if _, err := GateModelCohort(inst, true); !errors.Is(err, ErrNoReportedGroundTruth) {
			t.Fatalf("null protocol: want ErrNoReportedGroundTruth, got %v", err)
		}
	})

	t.Run("no servable component", func(t *testing.T) {
		inst := caseTwoInstrument()
		for i := range inst.Decl.Servability.Components {
			inst.Decl.Servability.Components[i].Servable = false
		}
		if _, err := GateModelCohort(inst, true); !errors.Is(err, ErrNoServableComponent) {
			t.Fatalf("want ErrNoServableComponent, got %v", err)
		}
	})
}
