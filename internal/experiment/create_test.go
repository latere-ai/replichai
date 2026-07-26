package experiment

import (
	"errors"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/instrument"
)

func runnableInstrument() Instrument {
	return Instrument{
		Ref: InstrumentRef{Repo: testRepo, Commit: "abc"},
		Decl: instrument.Instrument{
			Analysis: &instrument.Analysis{
				Command: "python src/analyze.py " + instrument.InputPlaceholder,
				Input: instrument.Schema{Columns: []instrument.Column{
					{Name: "rt", Type: "number", Required: true},
					{Name: "id", Type: "string", Required: true},
				}},
			},
		},
	}
}

// AC7: an instrument with a null analysis is refused at creation.
func TestCreateRefusesNullAnalysis(t *testing.T) {
	inst := Instrument{Ref: InstrumentRef{Repo: testRepo, Commit: "abc"}} // Analysis nil
	if _, err := Create(inst, SourceReleased, nil, "proj-1"); !errors.Is(err, ErrNoAnalysis) {
		t.Fatalf("null analysis should be ErrNoAnalysis, got %v", err)
	}
}

// AC5: a non-conforming uploaded dataset is refused before any run, naming the
// offending column; a conforming one is accepted.
func TestCreateValidatesUploadedColumns(t *testing.T) {
	inst := runnableInstrument()

	bad := &Dataset{ID: "ds-bad", Columns: []string{"id"}} // missing required "rt"
	_, err := Create(inst, SourceUploaded, bad, "proj-1")
	if err == nil {
		t.Fatal("a dataset missing a required column should be refused")
	}
	if !strings.Contains(err.Error(), "rt") {
		t.Errorf("refusal should name the offending column, got %v", err)
	}

	good := &Dataset{ID: "ds-good", Columns: []string{"rt", "id", "extra"}}
	e, err := Create(inst, SourceUploaded, good, "proj-1")
	if err != nil {
		t.Fatalf("a conforming dataset should be accepted: %v", err)
	}
	if e.DatasetID != "ds-good" || e.Source != SourceUploaded {
		t.Errorf("experiment = %+v, want the dataset bound", e)
	}
	if !strings.Contains(e.Command, instrument.InputPlaceholder) {
		t.Errorf("command should keep the input placeholder for the runner, got %q", e.Command)
	}
}

func TestCreateReleasedNeedsNoDataset(t *testing.T) {
	e, err := Create(runnableInstrument(), SourceReleased, nil, "proj-1")
	if err != nil {
		t.Fatalf("released source should not require a dataset: %v", err)
	}
	if e.DatasetID != "" {
		t.Errorf("released experiment should have no dataset, got %q", e.DatasetID)
	}
	if e.ID == "" {
		t.Error("Create should mint an ID")
	}
}

func TestCreateRefusals(t *testing.T) {
	t.Run("uploaded needs a dataset", func(t *testing.T) {
		if _, err := Create(runnableInstrument(), SourceUploaded, nil, "p"); !errors.Is(err, ErrDatasetRequired) {
			t.Fatalf("want ErrDatasetRequired, got %v", err)
		}
	})

	t.Run("unparameterised command", func(t *testing.T) {
		inst := runnableInstrument()
		inst.Decl.Analysis.Command = "python src/analyze.py data/original.csv"
		if _, err := Create(inst, SourceReleased, nil, "p"); !errors.Is(err, ErrUnparameterisedCommand) {
			t.Fatalf("want ErrUnparameterisedCommand, got %v", err)
		}
	})

	t.Run("non-offline source is unsupported", func(t *testing.T) {
		for _, s := range []Source{SourceSynthetic, SourceHuman} {
			if _, err := Create(runnableInstrument(), s, nil, "p"); !errors.Is(err, ErrSourceUnsupported) {
				t.Fatalf("%s should be ErrSourceUnsupported, got %v", s, err)
			}
		}
	})
}
