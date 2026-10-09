// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package verdict_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/verdict"
)

// withClaim is a whole verdict.json, written as text, whose one boundary row
// carries fields as an agent wrote them. It is text rather than a Go value
// because the shapes under test are ones the Go types do not have, and because
// a text field has to come back as exactly the compact form of what was
// written.
func withClaim(schema, fields string) []byte {
	return []byte(`{
  "schema": ` + schema + `,
  "verdict": "partial",
  "summary": "The classifier retrains and matches within 1.8 pp.",
  "graded_at": "2026-07-14T10:41:00Z",
  "components": {"verified": 1, "partial": 0, "blocked": 0, "total": 1},
  "boundary": [{
    "claim": "Posture classifier reaches 91.4% accuracy",
    "loop": "inner", "outcome": "verified", "evidence": "Retrained from released code", "citation": "§6.1"` + fields + `
  }]
}`)
}

const numeric = verdict.Numeric

// Every shape agents wrote a mismatch in during the pilot, each of which
// schema 1 refused unless all four fields were strings. Each one validates and
// reads as the list a reader is shown.
func TestEveryShapeAgentsWroteValidates(t *testing.T) {
	cases := []struct {
		name   string
		fields string
		want   []verdict.Mismatch
	}{
		{
			"all four as strings, which schema 1 accepted",
			`, "mismatch": {"kind": "numeric", "reported": "91.4%", "observed": "89.6%", "delta": "-1.8 pp"}`,
			[]verdict.Mismatch{{Kind: numeric, Reported: "91.4%", Observed: "89.6%", Note: "-1.8 pp"}},
		},
		{
			"reported, observed and delta as objects",
			`, "mismatch": {"kind": "numeric", "reported": {"full_n": 535, "p": [0.765, 0.814]}, "observed": {"full_n": 333, "p": [0.764, 0.8169]}, "delta": {"full_n": -202, "interpretation": "fewer rows survive the filter"}}`,
			[]verdict.Mismatch{{
				Kind: numeric, Reported: `{"full_n":535,"p":[0.765,0.814]}`, Observed: `{"full_n":333,"p":[0.764,0.8169]}`,
				Note: `{"full_n":-202,"interpretation":"fewer rows survive the filter"}`,
			}},
		},
		{
			"objects with units beside them",
			`, "mismatch": {"kind": "numeric", "reported": {"acc": 91.4}, "observed": {"acc": 89.6}, "delta": {"acc": -1.8}, "units": "percentage points"}`,
			[]verdict.Mismatch{{Kind: numeric, Reported: `{"acc":91.4}`, Observed: `{"acc":89.6}`, Note: `{"acc":-1.8}`, Unit: "percentage points"}},
		},
		{
			"only observed as an object",
			`, "mismatch": {"kind": "numeric", "reported": "n = 24", "observed": {"n": 22, "excluded": 2}, "delta": "-2 participants"}`,
			[]verdict.Mismatch{{Kind: numeric, Reported: "n = 24", Observed: `{"n":22,"excluded":2}`, Note: "-2 participants"}},
		},
		{
			"reported and observed as objects, delta a string",
			`, "mismatch": {"kind": "numeric", "reported": {"F1": 0.81}, "observed": {"F1": 0.78}, "delta": "-0.03"}`,
			[]verdict.Mismatch{{Kind: numeric, Reported: `{"F1":0.81}`, Observed: `{"F1":0.78}`, Note: "-0.03"}},
		},
		{
			"reported a string, observed and delta objects",
			`, "mismatch": {"kind": "numeric", "reported": "0.81 F1", "observed": {"F1": 0.78}, "delta": {"F1": -0.03}}`,
			[]verdict.Mismatch{{Kind: numeric, Reported: "0.81 F1", Observed: `{"F1":0.78}`, Note: `{"F1":-0.03}`}},
		},
		{
			"observed a bare number, delta an object",
			`, "mismatch": {"kind": "numeric", "reported": "Approximately 45%", "observed": 43.526, "delta": {"percentage_points": -1.47}}`,
			[]verdict.Mismatch{{Kind: numeric, Reported: "Approximately 45%", Observed: "43.526", Note: `{"percentage_points":-1.47}`}},
		},
		{
			"delta a bare number",
			`, "mismatch": {"kind": "ordering", "reported": "A ranks above B", "observed": "B ranks above A", "delta": -0.03}`,
			[]verdict.Mismatch{{Kind: verdict.Ordering, Reported: "A ranks above B", Observed: "B ranks above A", Note: "-0.03"}},
		},
		{
			"all three bare numbers",
			`, "mismatch": {"kind": "numeric", "reported": 0.914, "observed": 0.896, "delta": -0.018}`,
			[]verdict.Mismatch{{Kind: numeric, Reported: "0.914", Observed: "0.896", Note: "-0.018"}},
		},
		{
			"a second and third disagreement in an invented list",
			`, "mismatch": {"kind": "numeric", "reported": "91.4%", "observed": "89.6%"},
			   "additional_mismatches": [
			     {"kind": "typo", "reported": "Table 3 says 0.71", "observed": "the text says 0.17"},
			     {"kind": "contradiction", "reported": true, "observed": false, "delta": null}
			   ]`,
			[]verdict.Mismatch{
				{Kind: numeric, Reported: "91.4%", Observed: "89.6%"},
				{Kind: verdict.Typo, Reported: "Table 3 says 0.71", Observed: "the text says 0.17"},
				{Kind: verdict.Contradiction, Reported: "true", Observed: "false"},
			},
		},
		{
			"schema 2, every field",
			`, "mismatches": [
			     {"kind": "numeric", "what": "posture accuracy", "reported": "91.4%", "observed": "89.6%",
			      "reported_value": 91.4, "observed_value": 89.6, "unit": "%", "note": "the paper averages before rounding"},
			     {"kind": "numeric", "what": "sample size", "reported": "about 500", "observed": "333",
			      "reported_value": null, "observed_value": 333}
			   ]`,
			[]verdict.Mismatch{
				{
					Kind: numeric, What: "posture accuracy", Reported: "91.4%", Observed: "89.6%",
					ReportedValue: new(91.4), ObservedValue: new(89.6), Unit: "%", Note: "the paper averages before rounding",
				},
				{Kind: numeric, What: "sample size", Reported: "about 500", Observed: "333", ObservedValue: new(333.0)},
			},
		},
		{
			"a delta and a note together",
			`, "mismatches": [{"kind": "numeric", "reported": "91.4%", "observed": "89.6%", "delta": "-1.8 pp", "note": "the paper averages before rounding"}]`,
			[]verdict.Mismatch{{Kind: numeric, Reported: "91.4%", Observed: "89.6%", Note: "-1.8 pp; the paper averages before rounding"}},
		},
		{
			"unit wins over units",
			`, "mismatches": [{"kind": "numeric", "reported": "91.4", "observed": "89.6", "unit": "%", "units": "percent"}]`,
			[]verdict.Mismatch{{Kind: numeric, Reported: "91.4", Observed: "89.6", Unit: "%"}},
		},
		{
			"mismatches as one object rather than a list",
			`, "mismatches": {"kind": "typo", "reported": "Fig. 4", "observed": "Fig. 5"}`,
			[]verdict.Mismatch{{Kind: verdict.Typo, Reported: "Fig. 4", Observed: "Fig. 5"}},
		},
		{
			"what and note as structured values",
			`, "mismatches": [{"kind": "numeric", "what": ["accuracy", "F1"], "reported": "0.91, 0.84", "observed": "0.89, 0.80", "note": 2}]`,
			[]verdict.Mismatch{{Kind: numeric, What: `["accuracy","F1"]`, Reported: "0.91, 0.84", Observed: "0.89, 0.80", Note: "2"}},
		},
		{
			"no disagreement at all",
			`, "mismatch": null, "mismatches": []`,
			nil,
		},
	}
	for _, tc := range cases {
		for _, schema := range []string{"1", "2"} {
			t.Run(tc.name+"/schema "+schema, func(t *testing.T) {
				v, ps, err := verdict.Validate(withClaim(schema, tc.fields))
				if err != nil {
					t.Fatalf("the file failed whole: %v", err)
				}
				if len(ps) != 0 {
					t.Fatalf("problems = %v", ps)
				}
				if got := v.Boundary[0].Mismatches; !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("mismatches =\n%s\nwant\n%s", show(got), show(tc.want))
				}
			})
		}
	}
}

// show prints mismatches with their values dereferenced, so a failure says
// which number differs rather than which pointer.
func show(ms []verdict.Mismatch) string {
	var b strings.Builder
	val := func(f *float64) string {
		if f == nil {
			return "nil"
		}
		return fmt.Sprint(*f)
	}
	for _, m := range ms {
		fmt.Fprintf(&b, "  %s | %s | %s | %s | %s | %s | %s | %s\n",
			m.Kind, m.What, m.Reported, m.Observed, val(m.ReportedValue), val(m.ObservedValue), m.Unit, m.Note)
	}
	return b.String()
}

// Sessions already in the app hold schema 1 files. Refusing them would turn
// every existing findings pane into an error, so both versions are read; a
// version from neither is a problem.
func TestSchemaOneAndTwoAreRead(t *testing.T) {
	fields := `, "mismatch": {"kind": "numeric", "reported": "91.4%", "observed": "89.6%"}`
	for schema, ok := range map[string]bool{"1": true, "2": true, "0": false, "3": false} {
		_, ps, err := verdict.Validate(withClaim(schema, fields))
		if err != nil {
			t.Fatalf("schema %s: %v", schema, err)
		}
		if got := len(only(ps, "schema")) == 0; got != ok {
			t.Errorf("schema %s accepted = %v, want %v (problems %v)", schema, got, ok, ps)
		}
	}
}

// The two values are the strict part of a mismatch: a value that is not a
// number cannot be computed with. It is a problem on that field, and the
// disagreement it belongs to is still recorded.
func TestAValueThatIsNotANumberIsAProblemOnThatField(t *testing.T) {
	v, ps, err := verdict.Validate(withClaim("2",
		`, "mismatches": [{"kind": "numeric", "reported": "91.4%", "observed": "89.6%", "reported_value": "abc", "observed_value": 89.6}]`))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(ps) != 1 || ps[0].Field != "boundary[0].mismatches[0].reported_value" {
		t.Fatalf("problems = %v, want exactly one, at the value", ps)
	}
	ms := v.Boundary[0].Mismatches
	if len(ms) != 1 || ms[0].ReportedValue != nil || ms[0].ObservedValue == nil || *ms[0].ObservedValue != 89.6 {
		t.Fatalf("the row was not kept without the value: %s", show(ms))
	}

	_, ps, _ = verdict.Validate(withClaim("2",
		`, "mismatches": [{"kind": "numeric", "reported": "1", "observed": "2", "observed_value": 1e400}]`))
	if len(ps) != 1 || !strings.Contains(ps[0].Message, "out of range") {
		t.Fatalf("problems = %v, want the value named as out of range", ps)
	}
}

// A mismatch the decoder cannot read is a problem at its own path. The claim
// it belongs to keeps its row and its other mismatches.
func TestAMismatchThatDoesNotDecodeKeepsItsClaim(t *testing.T) {
	v, ps, err := verdict.Validate(withClaim("2", `, "mismatches": [
	  {"kind": 1, "reported": "91.4%", "observed": "89.6%"},
	  "the paper rounds",
	  {"kind": "typo", "reported": "Fig. 4", "observed": "Fig. 5"}
	]`))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(ps) != 2 || ps[0].Field != "boundary[0].mismatches[0].kind" || ps[1].Field != "boundary[0].mismatches[1]" {
		t.Fatalf("problems = %v, want the kind of the first and the second as a whole", ps)
	}
	if len(v.Boundary) != 1 || len(v.Boundary[0].Mismatches) != 1 || v.Boundary[0].Mismatches[0].Kind != verdict.Typo {
		t.Fatalf("the claim lost its row or its readable mismatch: %+v", v.Boundary)
	}

	_, ps, _ = verdict.Validate(withClaim("2", `, "mismatches": "see REPRODUCIBILITY.md"`))
	if len(ps) != 1 || ps[0].Field != "boundary[0].mismatches" {
		t.Fatalf("problems = %v, want one at the list", ps)
	}
}

// A problem names the field the author wrote. In a schema 1 file that is
// mismatch, not the mismatches[0] it was read into.
func TestAProblemInALegacyMismatchNamesTheLegacyField(t *testing.T) {
	_, ps, _ := verdict.Validate(withClaim("1", `,
	  "mismatch": {"kind": "numeric", "reported": "91.4%", "observed": ""},
	  "additional_mismatches": [{"kind": "rounding", "reported": "0.71", "observed": "0.17"}]`))
	want := []string{"boundary[0].mismatch", "boundary[0].additional_mismatches[0].kind"}
	if len(ps) != 2 || ps[0].Field != want[0] || ps[1].Field != want[1] {
		t.Fatalf("problems = %v, want %v", ps, want)
	}
}
