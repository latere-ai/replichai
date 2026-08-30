// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package instrument

import (
	"encoding/json"
	"strings"
	"testing"
)

// A fully populated, well-formed instrument. Tests mutate a copy to isolate one
// violation at a time.
func validInstrument() Instrument {
	return Instrument{
		Protocol: &Protocol{
			Design:   "2x2 within-subjects, counterbalanced",
			Trials:   "120 trials, 30 per cell",
			Measures: []Measure{{Name: "reaction_time", Scale: "ms", Level: "per_trial"}},
			Plan:     []string{"linear mixed model with crossed random effects"},
			Reported: []Reported{{
				Metric: "RT difference", Effect: "slower under load",
				Test: "LMM", Value: "b=42ms, p=.003", N: "60", Citation: "Table 2",
			}},
		},
		Analysis: &Analysis{
			Command: "python src/analyze.py " + InputPlaceholder,
			Input: Schema{Columns: []Column{
				{Name: "participant", Type: "string", Required: true},
				{Name: "rt", Type: "number", Unit: "ms", Required: true},
				{Name: "note", Type: "string", Required: false},
			}},
			Outputs: []string{"results/table2.csv"},
		},
		Servability: Servability{
			Components: []ComponentServability{
				{Component: "PM task", Servable: true},
				{
					Component: "feed manipulation", Servable: false,
					Failed: []Criterion{CriterionPageAdministrable},
					Why:    "participants spend ten minutes on their own feeds elsewhere",
				},
			},
			AllServable: false,
		},
	}
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func violationOn(vs []Violation, prefix string) bool {
	for _, v := range vs {
		if strings.HasPrefix(v.Field, prefix) {
			return true
		}
	}
	return false
}

func TestValidateAcceptsWellFormed(t *testing.T) {
	vs, err := Validate(marshal(t, validInstrument()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vs) != 0 {
		t.Fatalf("expected no violations, got %v", vs)
	}
}

func TestValidateRejectsNonJSON(t *testing.T) {
	if _, err := Validate([]byte("not json")); err == nil {
		t.Fatal("expected an error for non-JSON input")
	}
}

// AC3: a null protocol or analysis passes with a stated reason and fails
// without one.
func TestNullFieldNeedsReason(t *testing.T) {
	for _, field := range []string{"protocol", "analysis"} {
		inst := validInstrument()
		switch field {
		case "protocol":
			inst.Protocol = nil
		case "analysis":
			inst.Analysis = nil
		}

		vs, err := Validate(marshal(t, inst))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", field, err)
		}
		if !violationOn(vs, field) {
			t.Fatalf("%s null without a reason should fail; got %v", field, vs)
		}

		inst.Absent = map[string]string{field: "the paper does not describe it"}
		vs, err = Validate(marshal(t, inst))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", field, err)
		}
		if violationOn(vs, field) {
			t.Fatalf("%s null with a reason should pass; got %v", field, vs)
		}
	}
}

// AC4: every reported statistic carries a citation.
func TestReportedNeedsCitation(t *testing.T) {
	inst := validInstrument()
	inst.Protocol.Reported[0].Citation = "  "
	vs, err := Validate(marshal(t, inst))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !violationOn(vs, "protocol.reported[0].citation") {
		t.Fatalf("uncited statistic should fail; got %v", vs)
	}
}

// AC5: the analysis command parameterises its input path.
func TestCommandFor(t *testing.T) {
	a := validInstrument().Analysis
	got, err := a.CommandFor("/data/new.csv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "python src/analyze.py /data/new.csv"; got != want {
		t.Fatalf("CommandFor = %q, want %q", got, want)
	}

	baked := Analysis{Command: "python src/analyze.py data/original.csv"}
	if _, err := baked.CommandFor("/data/new.csv"); err == nil {
		t.Fatal("a command with a baked-in path should error")
	}
}

func TestValidateFlagsBakedInCommand(t *testing.T) {
	inst := validInstrument()
	inst.Analysis.Command = "python src/analyze.py data/original.csv"
	vs, err := Validate(marshal(t, inst))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !violationOn(vs, "analysis.command") {
		t.Fatalf("a baked-in command should fail; got %v", vs)
	}
}

// AC6: the input schema validates a conforming dataset and rejects a
// non-conforming one, naming the offending column.
func TestSchemaCheck(t *testing.T) {
	s := validInstrument().Analysis.Input
	if err := s.Check([]string{"participant", "rt", "extra"}); err != nil {
		t.Fatalf("conforming dataset rejected: %v", err)
	}
	err := s.Check([]string{"participant", "note"})
	if err == nil {
		t.Fatal("a dataset missing a required column should be rejected")
	}
	if !strings.Contains(err.Error(), "rt") {
		t.Fatalf("rejection should name the missing column; got %v", err)
	}
	// An optional column may be absent.
	if err := s.Check([]string{"participant", "rt"}); err != nil {
		t.Fatalf("an absent optional column should be allowed: %v", err)
	}
}

// AC7: servability is answered per component against the closed criteria list,
// and the roll-up is derived, not asserted.
func TestServabilityConsistency(t *testing.T) {
	t.Run("unknown criterion", func(t *testing.T) {
		inst := validInstrument()
		inst.Servability.Components[1].Failed = []Criterion{"invented_bar"}
		vs, _ := Validate(marshal(t, inst))
		if !violationOn(vs, "servability.components[1].failed_criteria") {
			t.Fatalf("an unknown criterion should fail; got %v", vs)
		}
	})

	t.Run("servable but failed", func(t *testing.T) {
		inst := validInstrument()
		inst.Servability.Components[0].Servable = true
		inst.Servability.Components[0].Failed = []Criterion{CriterionScreenBased}
		vs, _ := Validate(marshal(t, inst))
		if !violationOn(vs, "servability.components[0]") {
			t.Fatalf("servable-with-failures should fail; got %v", vs)
		}
	})

	t.Run("not servable but no reason", func(t *testing.T) {
		inst := validInstrument()
		inst.Servability.Components[1].Servable = false
		inst.Servability.Components[1].Failed = nil
		vs, _ := Validate(marshal(t, inst))
		if !violationOn(vs, "servability.components[1]") {
			t.Fatalf("not-servable with no failed criterion should fail; got %v", vs)
		}
	})

	t.Run("roll-up must be derived", func(t *testing.T) {
		inst := validInstrument()
		inst.Servability.AllServable = true // components include a non-servable one
		vs, _ := Validate(marshal(t, inst))
		if !violationOn(vs, "servability.all_servable") {
			t.Fatalf("an asserted roll-up diverging from components should fail; got %v", vs)
		}
	})
}

func TestDeriveAllServable(t *testing.T) {
	all := []ComponentServability{{Servable: true}, {Servable: true}}
	if !DeriveAllServable(all) {
		t.Fatal("all servable should derive true")
	}
	mixed := []ComponentServability{{Servable: true}, {Servable: false, Failed: []Criterion{CriterionScreenBased}, Why: "rig"}}
	if DeriveAllServable(mixed) {
		t.Fatal("a mixed set should derive false")
	}
	if !DeriveAllServable(nil) {
		t.Fatal("no components should derive true (vacuous)")
	}
}

// AC10 (salvaged): Validate reports every violation in one pass, so a single
// revision can fix them all.
func TestValidateIsExhaustive(t *testing.T) {
	inst := validInstrument()
	inst.Protocol.Reported[0].Citation = ""                             // one
	inst.Analysis.Command = "python run.py in.csv"                      // two
	inst.Servability.Components[1].Failed = []Criterion{"invented_bar"} // three
	inst.Servability.AllServable = true                                 // four

	vs, err := Validate(marshal(t, inst))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vs) < 4 {
		t.Fatalf("expected every violation in one pass, got %d: %v", len(vs), vs)
	}
}

// The criteria list is closed and every constant is in it — a guard against a
// new criterion constant being added without joining Criteria.
func TestCriteriaClosed(t *testing.T) {
	for _, c := range []Criterion{
		CriterionSingleSession, CriterionScreenBased, CriterionNoExperimenter,
		CriterionBrowserMeasures, CriterionPageAdministrable, CriterionApparatusRecovered,
	} {
		if !knownCriterion[c] {
			t.Fatalf("criterion %q is not in the closed Criteria list", c)
		}
	}
	if len(Criteria) != len(knownCriterion) {
		t.Fatal("Criteria has duplicates or knownCriterion is out of sync")
	}
}

// A field of the wrong shape is the failure a real run produced: the model
// wrote prose where the schema wants a list. Reporting it as invalid JSON sends
// the reader looking for a missing comma, and refusing to read the document at
// all denies the revision the one thing it could have fixed.
func TestAFieldOfTheWrongShapeIsAViolationNamingIt(t *testing.T) {
	raw := []byte(`{
	  "protocol": {"design": "within", "trials": "5 tasks", "measures": [],
	    "plan": "Descriptive statistics only; no inferential model is declared.",
	    "reported": []},
	  "analysis": null,
	  "servability": {"components": []},
	  "absent_reasons": {"analysis": "no runnable analysis was recovered"}
	}`)
	vs, err := Validate(raw)
	if err != nil {
		t.Fatalf("a readable document must not fail to parse: %v", err)
	}
	if len(vs) != 1 {
		t.Fatalf("violations = %+v, want exactly one", vs)
	}
	if vs[0].Field != "protocol.plan" {
		t.Errorf("field = %q, want protocol.plan", vs[0].Field)
	}
	if !strings.Contains(vs[0].Message, "string") || !strings.Contains(vs[0].Message, "[]string") {
		t.Errorf("message = %q, want it to name what was written and what is wanted", vs[0].Message)
	}

	// Genuinely malformed JSON is still unreadable, and still an error.
	if _, err := Validate([]byte(`{"protocol":`)); err == nil {
		t.Error("truncated JSON must still fail to parse")
	}
}
