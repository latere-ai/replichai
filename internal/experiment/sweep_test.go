// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"context"
	"strings"
	"testing"
)

// AC3: a sweep over synthetic:generated runs with no gate, no model, and no
// credential, and reports where the instrument's analysis becomes unstable.
func TestSweepFindsInstability(t *testing.T) {
	decl := generatorInstrument()
	base := Generated{N: 200, Seed: 3}
	spec := SpecifiedEffect{Column: "score", GroupColumn: "condition", Treatment: groupTreatment}
	effects := []float64{0, 5, 10, 15, 20}

	// A stable analysis: the recovered mean difference tracks the injected effect.
	stable := func(data []byte) (float64, bool) {
		return meanDifference(t, data, "score", "condition", groupTreatment), true
	}
	rep, err := Sweep(context.Background(), decl, base, effects, spec, stable)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(rep.Points) != len(effects) {
		t.Fatalf("swept %d points, want %d", len(rep.Points), len(effects))
	}
	if len(rep.Unstable) != 0 {
		t.Errorf("a well-behaved analysis should show no instability, got %v", rep.Unstable)
	}

	// An analysis that breaks above an effect of 10; the sweep must report it.
	brittle := func(data []byte) (float64, bool) {
		d := meanDifference(t, data, "score", "condition", groupTreatment)
		if d > 12 {
			return 0, false // the pipeline falls over on large effects
		}
		return d, true
	}
	rep2, err := Sweep(context.Background(), decl, base, effects, spec, brittle)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(rep2.Unstable) == 0 {
		t.Error("the sweep should surface where the analysis breaks")
	}
	for _, p := range rep2.Unstable {
		if p.Ran && p.Effect <= 12 {
			t.Errorf("a stable point was flagged unstable: %+v", p)
		}
	}
}

// The sweep's own framing keeps its findings about the instrument.
func TestSweepSubjectIsAboutTheInstrument(t *testing.T) {
	s := SweepReport{}.Subject()
	if !strings.Contains(s, "instrument") || !strings.Contains(s, "never about the paper") {
		t.Errorf("a sweep must be framed about the instrument, got %q", s)
	}
}
