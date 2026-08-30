// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package experiment

import (
	"reflect"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/instrument"
)

func TestComparePairsByMetric(t *testing.T) {
	reported := []instrument.Reported{
		{Metric: "effect_size", Value: "0.42", Citation: "Table 2"},
		{Metric: "p_value", Value: "b=0.003", Citation: "Table 2"}, // free-form, not numeric
		{Metric: "accuracy", Value: "0.9", Citation: "Table 1"},    // not recomputed
	}
	o := Outcome{Ran: true, Recomputed: []Statistic{
		{Metric: "effect_size", Value: "0.40"},
		{Metric: "p_value", Value: "b=0.004"},
		{Metric: "extra_stat", Value: "1.0"}, // not reported
	}}

	c := Compare(o, reported)
	byMetric := map[string]ComparisonRow{}
	for _, r := range c.Rows {
		byMetric[r.Metric] = r
	}

	// A clean numeric pair carries a delta.
	if got := byMetric["effect_size"]; got.Delta == "" || got.Reported != "0.42" || got.Recomputed != "0.40" {
		t.Errorf("effect_size row = %+v, want a numeric delta with both values", got)
	}
	// A free-form pair stands side by side with a note, no delta.
	if got := byMetric["p_value"]; got.Delta != "" || got.Note == "" {
		t.Errorf("p_value row = %+v, want no delta and a note", got)
	}
	// Reported but not recomputed is still a row.
	if got := byMetric["accuracy"]; got.Recomputed != "" || !strings.Contains(got.Note, "not recomputed") {
		t.Errorf("accuracy row = %+v, want reported-but-not-recomputed", got)
	}
	// Recomputed but not reported is appended.
	if got := byMetric["extra_stat"]; got.Reported != "" || !strings.Contains(got.Note, "not reported") {
		t.Errorf("extra_stat row = %+v, want recomputed-but-not-reported", got)
	}
	// The citation travels from the reported entry.
	if byMetric["effect_size"].Citation != "Table 2" {
		t.Errorf("citation not carried: %+v", byMetric["effect_size"])
	}
}

// CompareFor is what wires Compare to a run: only a released experiment with an
// outcome and a reported baseline is compared.
func TestCompareFor(t *testing.T) {
	inst := Instrument{Decl: instrument.Instrument{Protocol: &instrument.Protocol{
		Reported: []instrument.Reported{{Metric: "effect_size", Value: "0.42", Citation: "Table 2"}},
	}}}
	outcome := &Outcome{Ran: true, Recomputed: []Statistic{{Metric: "effect_size", Value: "0.40"}}}

	t.Run("released is compared", func(t *testing.T) {
		e := &Experiment{Source: SourceReleased, Outcome: outcome}
		c, ok := CompareFor(e, inst)
		if !ok || len(c.Rows) != 1 || c.Rows[0].Delta == "" {
			t.Fatalf("a released outcome should compare, got ok=%v rows=%v", ok, c.Rows)
		}
	})

	t.Run("uploaded is not compared", func(t *testing.T) {
		e := &Experiment{Source: SourceUploaded, Outcome: outcome}
		if _, ok := CompareFor(e, inst); ok {
			t.Error("an uploaded finding has no paper baseline; it must not be compared")
		}
	})

	t.Run("no outcome, nothing to compare", func(t *testing.T) {
		if _, ok := CompareFor(&Experiment{Source: SourceReleased}, inst); ok {
			t.Error("a run with no outcome cannot be compared")
		}
	})

	t.Run("no reported baseline, nothing to compare", func(t *testing.T) {
		bare := Instrument{Decl: instrument.Instrument{}} // nil Protocol
		if _, ok := CompareFor(&Experiment{Source: SourceReleased, Outcome: outcome}, bare); ok {
			t.Error("an instrument with no reported results has no baseline")
		}
	})
}

// AC8: a synthetic comparison renders as calibration of the cohort, a released
// one as re-analysis of the paper — distinguished by Source, both tested.
func TestComparisonSubjectBylSource(t *testing.T) {
	released := Comparison{Source: SourceReleased}
	if !strings.Contains(released.Subject(), "re-analysis of the paper") {
		t.Errorf("released should read as re-analysis, got %q", released.Subject())
	}
	synthetic := Comparison{Source: SourceSynthetic}
	subj := synthetic.Subject()
	if !strings.Contains(subj, "calibration") || strings.Contains(subj, "re-analysis of the paper") {
		t.Errorf("synthetic should read as calibration, never re-analysis, got %q", subj)
	}
	if !strings.Contains(subj, "never about the paper") {
		t.Errorf("synthetic subject must disclaim speaking about the paper, got %q", subj)
	}
}

func TestCompareForCarriesSource(t *testing.T) {
	inst := Instrument{Decl: instrument.Instrument{Protocol: &instrument.Protocol{
		Reported: []instrument.Reported{{Metric: "m", Value: "1", Citation: "T1"}},
	}}}
	out := &Outcome{Ran: true}
	for _, src := range []Source{SourceReleased, SourceSynthetic} {
		c, ok := CompareFor(&Experiment{Source: src, Outcome: out}, inst)
		if !ok || c.Source != src {
			t.Errorf("%s: CompareFor should carry the source, got ok=%v source=%s", src, ok, c.Source)
		}
	}
}

func TestCompareEmptyIsEmpty(t *testing.T) {
	if c := Compare(Outcome{Ran: true}, nil); len(c.Rows) != 0 {
		t.Errorf("no reported and no recomputed should yield no rows, got %v", c.Rows)
	}
}

// AC8: Compare emits no pass/fail judgement — asserted on the type, so a later
// change cannot add one silently. No field of Comparison or its rows may be a
// boolean or named like a verdict.
func TestComparisonHasNoVerdictField(t *testing.T) {
	verdictish := []string{"pass", "fail", "verdict", "judg", "reproduc", "success", "ok", "valid"}
	for _, typ := range []reflect.Type{
		reflect.TypeFor[Comparison](),
		reflect.TypeFor[ComparisonRow](),
	} {
		for f := range typ.Fields() {
			if f.Type.Kind() == reflect.Bool {
				t.Errorf("%s.%s is a bool — a comparison must carry no verdict", typ.Name(), f.Name)
			}
			lower := strings.ToLower(f.Name)
			for _, bad := range verdictish {
				if strings.Contains(lower, bad) {
					t.Errorf("%s.%s reads like a verdict — the comparison must not judge", typ.Name(), f.Name)
				}
			}
		}
	}
}
