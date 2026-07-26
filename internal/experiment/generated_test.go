package experiment

import (
	"bytes"
	"context"
	"encoding/csv"
	"math"
	"strconv"
	"testing"

	"github.com/latere-ai/replichai/internal/instrument"
	"github.com/latere-ai/replichai/internal/provider/memory"
)

func generatorInstrument() instrument.Instrument {
	return instrument.Instrument{
		Analysis: &instrument.Analysis{
			Command: "python src/analyze.py " + instrument.InputPlaceholder,
			Input: instrument.Schema{Columns: []instrument.Column{
				{Name: "participant", Type: "string", Required: true},
				{Name: "condition", Type: "categorical", Required: true},
				{Name: "score", Type: "number", Required: true},
			}},
		},
	}
}

// AC1: generated data conforms to analysis.input and is deterministic from the
// seed, so the instrument's own pipeline runs on it end to end.
func TestGeneratedConformsAndIsDeterministic(t *testing.T) {
	decl := generatorInstrument()
	g := Generated{N: 50, Seed: 7}

	a, err := g.Collect(context.Background(), decl)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	// It conforms: every required column is present.
	if err := decl.Analysis.Input.Check(a.Header); err != nil {
		t.Fatalf("generated data does not conform to the input schema: %v", err)
	}
	// It is deterministic: same seed, identical bytes.
	b, _ := g.Collect(context.Background(), decl)
	if !bytes.Equal(a.CSV, b.CSV) {
		t.Error("same seed must produce identical bytes")
	}
	// A different seed changes the data.
	other, _ := Generated{N: 50, Seed: 8}.Collect(context.Background(), decl)
	if bytes.Equal(a.CSV, other.CSV) {
		t.Error("a different seed should produce different data")
	}
	// It produced N rows plus the header.
	rows, _ := csv.NewReader(bytes.NewReader(a.CSV)).ReadAll()
	if len(rows) != 51 {
		t.Errorf("got %d rows, want 50 + header", len(rows))
	}
}

// AC1 (end to end): generated data feeds the instrument's own pipeline — the
// 007b runner — with no model and no credential. This closes the loop the
// generator exists for: its CSV must actually be consumable by the executor.
func TestGeneratedFeedsTheRunnerEndToEnd(t *testing.T) {
	decl := generatorInstrument()
	data, err := (Generated{N: 20, Seed: 5}).Collect(context.Background(), decl)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	p := memory.New()
	p.Files = map[string][]byte{ResultFile: []byte(`{"ran":true,"recomputed":[{"metric":"score","value":"50"}]}`)}
	inst := mountedInstrument()
	inst.Decl = decl // the generator's schema is the instrument's analysis input
	e := &Experiment{ID: "exp-gen", Source: SourceSynthetic, CohortKind: CohortGenerated}

	out, err := newTestRunner(t, p, NewMemArtifactStore()).Run(context.Background(), e, inst, data.CSV)
	if err != nil {
		t.Fatalf("Run on generated data: %v", err)
	}
	if !out.Ran {
		t.Error("the instrument's pipeline should run on generated data")
	}
	// The generated CSV is what the runner attached, outside the mount.
	att := p.Runs()[0].Attachments
	if len(att) != 1 || !bytes.Equal(att[0].Content, data.CSV) {
		t.Error("the generated data should be the run's attached input")
	}
}

func TestGeneratedRefusesNoAnalysis(t *testing.T) {
	if _, err := (Generated{N: 5}).Collect(context.Background(), instrument.Instrument{}); err == nil {
		t.Fatal("a generator with no analysis input should error")
	}
}

// meanDifference is a real analysis stand-in for CI: it computes the actual
// treatment-minus-control mean of an outcome column. It is deliberately a
// genuine computation, not a canned verdict — a test that asserted "detected"
// through a memory provider returning a fixed string would fabricate exactly the
// result 007c exists to prevent. The live analysis is the paper's Python in the
// sandbox; this stands in for it where CI has no Python.
func meanDifference(t *testing.T, data []byte, outcome, groupCol, treatment string) float64 {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil || len(rows) < 2 {
		t.Fatalf("parse generated data: %v", err)
	}
	oi, gi := -1, -1
	for i, h := range rows[0] {
		if h == outcome {
			oi = i
		}
		if h == groupCol {
			gi = i
		}
	}
	if oi < 0 || gi < 0 {
		t.Fatalf("columns %q/%q not found in %v", outcome, groupCol, rows[0])
	}
	var tSum, cSum float64
	var tN, cN int
	for _, row := range rows[1:] {
		v, _ := strconv.ParseFloat(row[oi], 64)
		if row[gi] == treatment {
			tSum += v
			tN++
		} else {
			cSum += v
			cN++
		}
	}
	if tN == 0 || cN == 0 {
		t.Fatal("a control and a treatment group are both required")
	}
	return tSum/float64(tN) - cSum/float64(cN)
}

// AC2: a cohort carrying a specified effect is detected by the analysis, and a
// null cohort is not — the positive and negative control, asserted as a pair.
func TestGeneratedControlPair(t *testing.T) {
	decl := generatorInstrument()
	const detectThreshold = 3.0 // noise sd is 10, N/group is 100, so SE ≈ 1

	effect := &SpecifiedEffect{Column: "score", By: 12, GroupColumn: "condition", Treatment: groupTreatment}
	positive, _ := Generated{N: 200, Seed: 1, Effect: effect}.Collect(context.Background(), decl)
	if d := meanDifference(t, positive.CSV, "score", "condition", groupTreatment); math.Abs(d) <= detectThreshold {
		t.Errorf("a specified effect of 12 should be detected, got difference %.2f", d)
	}

	null, _ := Generated{N: 200, Seed: 1, Effect: nil}.Collect(context.Background(), decl)
	if d := meanDifference(t, null.CSV, "score", "condition", groupTreatment); math.Abs(d) > detectThreshold {
		t.Errorf("a null cohort should show no effect, got difference %.2f", d)
	}
}
