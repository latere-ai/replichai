package experiment

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"math/rand"
	"strconv"

	"github.com/latere-ai/replichai/internal/instrument"
)

// Cohort is a source of trial data with no people in it (007c) — and the seam
// 007d's human cohort becomes a third implementation of, rather than a parallel
// mechanism. Collect emits data conforming to the instrument's analysis input,
// so the instrument's own pipeline runs on it unchanged.
type Cohort interface {
	Kind() CohortKind
	Collect(ctx context.Context, decl instrument.Instrument) (CohortData, error)
}

// The seam is only a seam if the compiler checks it. Nothing holds a Cohort
// yet — the kind travels as Pilot.Kind and each caller knows its concrete
// cohort — so without these the two implementations could drift apart and the
// interface would still compile.
var (
	_ Cohort = Generated{}
	_ Cohort = (*ModelCohort)(nil)
)

// CohortData is the trial data a cohort produced: the CSV bytes the analysis
// consumes, and the header so a caller can check conformance before running.
type CohortData struct {
	Header []string
	CSV    []byte
}

// SpecifiedEffect is a mean shift a generated cohort carries, so a positive
// control can test whether the instrument's analysis detects an effect it is
// known to contain. It is a claim about the instrument, never about the paper.
type SpecifiedEffect struct {
	Column      string  // the outcome column the shift acts on
	By          float64 // the shift added to the treatment group
	GroupColumn string  // the categorical column that splits the groups
	Treatment   string  // the GroupColumn value that receives the shift
}

// Generated is a deterministic data generator: it reads the analysis input
// schema and emits conforming trial data from a recorded seed, with no model and
// no credential. Same seed, same bytes — the run is reproducible from the seed
// alone, which is what keeps the shakedown and the positive control honest.
type Generated struct {
	N      int
	Seed   int64
	Effect *SpecifiedEffect // nil for a null cohort
}

// Kind identifies the cohort.
func (Generated) Kind() CohortKind { return CohortGenerated }

// The two levels a categorical column is filled with, so a generated dataset
// has a control and a treatment group to carry an effect between.
const (
	groupControl   = "control"
	groupTreatment = "treatment"
)

// Collect emits N rows conforming to decl's analysis input, deterministic from
// the seed. Numeric columns are drawn from a fixed distribution; categorical
// columns alternate control/treatment; a string column carries a participant id.
// When an effect is specified, treatment-group rows are shifted on its column.
func (g Generated) Collect(_ context.Context, decl instrument.Instrument) (CohortData, error) {
	if decl.Analysis == nil {
		return CohortData{}, ErrNoAnalysis
	}
	cols := decl.Analysis.Input.Columns
	if len(cols) == 0 {
		return CohortData{}, fmt.Errorf("analysis input declares no columns to generate")
	}
	n := g.N
	if n <= 0 {
		n = 30
	}

	header := make([]string, len(cols))
	for i, c := range cols {
		header[i] = c.Name
	}

	rng := rand.New(rand.NewSource(g.Seed)) //nolint:gosec // G404: synthetic data, not security
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(header); err != nil {
		return CohortData{}, err
	}

	for row := 0; row < n; row++ {
		group := groupControl
		if row%2 == 1 {
			group = groupTreatment
		}
		record := make([]string, len(cols))
		for i, c := range cols {
			record[i] = g.cell(rng, c, row, group)
		}
		if err := w.Write(record); err != nil {
			return CohortData{}, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return CohortData{}, err
	}
	return CohortData{Header: header, CSV: buf.Bytes()}, nil
}

// cell produces one deterministic value for a column, applying the specified
// effect to the treatment group's outcome column.
func (g Generated) cell(rng *rand.Rand, c instrument.Column, row int, group string) string {
	switch c.Type {
	case "string":
		return fmt.Sprintf("p%04d", row)
	case "categorical":
		// Categorical columns carry the control/treatment split, so an effect has
		// two groups to sit between.
		return group
	case "integer":
		return strconv.Itoa(int(rng.NormFloat64()*10 + 50))
	case "boolean":
		return strconv.FormatBool(row%2 == 0)
	default: // "number" and anything else numeric
		v := rng.NormFloat64()*10 + 50
		if g.Effect != nil && c.Name == g.Effect.Column && group == g.Effect.Treatment {
			v += g.Effect.By
		}
		return strconv.FormatFloat(v, 'f', 4, 64)
	}
}
