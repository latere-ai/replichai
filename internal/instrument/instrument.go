// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package instrument is the schema for instrument.json, the machine-readable
// counterpart of what an audit's reports state in prose: the paper's study
// protocol, the analysis entrypoint contract for src/, and a per-component
// servability assessment.
//
// It holds the schema and its validation and nothing else. Executing the
// analysis (007b), driving a cohort (007c), and serving the page (007d) belong
// to later specs and packages, so nothing downstream depends on how the audit
// produced the file.
//
// Refusal is a valid answer. A paper too vague to extract a protocol from
// yields a null protocol with a stated reason, and that passes validation. An
// invented protocol or a null without a reason does not, and Validate exists
// to catch both.
package instrument

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// InputPlaceholder marks where the input data path is substituted into an
// analysis command, so the same pipeline runs over a new dataset without
// editing (007b). A command with a fixed input path is not an instrument.
const InputPlaceholder = "{{INPUT}}"

// Instrument is the whole of instrument.json. Protocol and Analysis are each
// independently nullable with a stated reason in Absent; Servability is never
// absent, because "no component can be served" is itself the answer.
type Instrument struct {
	Protocol    *Protocol         `json:"protocol"`                 // nil when not extractable
	Analysis    *Analysis         `json:"analysis"`                 // nil when not recovered runnably
	Servability Servability       `json:"servability"`              // always present
	Absent      map[string]string `json:"absent_reasons,omitempty"` // field -> why it is null
}

// Protocol is the extracted study design. Design and Trials are prose rather
// than a study-design DSL: the schema covers only what the servability
// criteria admit, not every design a study with people can take.
type Protocol struct {
	Design   string     `json:"design"`   // between/within, factors and levels, counterbalancing
	Trials   string     `json:"trials"`   // structure, counts, stimuli, timing
	Measures []Measure  `json:"measures"` // what is recorded, and at what level
	Plan     []string   `json:"plan"`     // the declared statistical models
	Reported []Reported `json:"reported"` // the paper's own headline results, each cited
}

// Measure is one thing the study records.
type Measure struct {
	Name  string `json:"name"`
	Scale string `json:"scale,omitempty"` // units or response scale
	Level Level  `json:"level,omitempty"` // the level a measure is recorded at
}

// Level is the level a measure is recorded at.
type Level string

// The recording levels a measure can carry.
const (
	PerTrial       Level = "per_trial"
	PerParticipant Level = "per_participant"
)

// Reported is one of the paper's own headline statistics, carried so the rest
// of the 007 track has ground truth to compare against: a released-source
// re-analysis (007b) and a synthetic pilot's calibration (007c) both check
// against these. Every entry cites a table or figure, as the honesty criterion
// requires of every claim about the paper.
type Reported struct {
	Metric   string `json:"metric"`             // what was measured
	Effect   string `json:"effect,omitempty"`   // the reported effect or direction
	Test     string `json:"test,omitempty"`     // the statistical test
	Value    string `json:"value,omitempty"`    // the statistic and p-value
	Interval string `json:"interval,omitempty"` // confidence or credible interval
	N        string `json:"n,omitempty"`        // the sample size
	Citation string `json:"citation"`           // the table, figure, or section; required
}

// Analysis is the entrypoint contract for src/: how to run the analysis on new
// data, what it consumes, and what it produces. This is what turns an
// implementation that ran once into an instrument that runs on any conforming
// dataset.
type Analysis struct {
	Command string   `json:"command"` // must contain InputPlaceholder
	Input   Schema   `json:"input"`   // the columns the command consumes
	Outputs []string `json:"outputs"` // what it produces, and where
}

// CommandFor substitutes an input data path into the command, so a caller can
// run the recovered analysis over a new dataset. When the command does not
// name its input it returns an error rather than running the wrong file.
func (a Analysis) CommandFor(path string) (string, error) {
	if !strings.Contains(a.Command, InputPlaceholder) {
		return "", fmt.Errorf("analysis command does not parameterise its input with %s", InputPlaceholder)
	}
	return strings.ReplaceAll(a.Command, InputPlaceholder, path), nil
}

// Schema is the input contract for an analysis: the columns it expects, so a
// researcher's dataset is validated before execution instead of failing inside
// a script (007b's uploaded source).
type Schema struct {
	Columns []Column `json:"columns"`
}

// Column is one expected input column.
type Column struct {
	Name     string     `json:"name"`
	Type     ColumnType `json:"type"`           // the column's data type
	Unit     string     `json:"unit,omitempty"` // the unit, when the type has one
	Required bool       `json:"required"`
}

// ColumnType is the data type of an input column.
type ColumnType string

// The column data types a schema can declare.
const (
	ColumnNumber      ColumnType = "number"
	ColumnInteger     ColumnType = "integer"
	ColumnString      ColumnType = "string"
	ColumnBoolean     ColumnType = "boolean"
	ColumnCategorical ColumnType = "categorical"
)

// Check reports whether a dataset with the given column names satisfies the
// schema: every required column must be present. The message names the first
// missing column, so a rejection is actionable rather than an opaque failure
// deep in a script.
func (s Schema) Check(present []string) error {
	have := make(map[string]bool, len(present))
	for _, c := range present {
		have[c] = true
	}
	for _, col := range s.Columns {
		if col.Required && !have[col.Name] {
			return fmt.Errorf("required column %q is missing", col.Name)
		}
	}
	return nil
}

// Servability answers, per outer-loop component, whether a machine can serve
// the protocol to participants. The roll-up is derived from the components by
// DeriveAllServable and is never asserted on its own.
type Servability struct {
	Components  []ComponentServability `json:"components"`
	AllServable bool                   `json:"all_servable"`
}

// ComponentServability is the answer for one component of the boundary table.
// A component is servable only if it fails no criterion; when it is not
// servable, Failed enumerates which criteria it fails.
type ComponentServability struct {
	Component string      `json:"component"`
	Servable  bool        `json:"servable"`
	Failed    []Criterion `json:"failed_criteria"` // drawn from Criteria
	Why       string      `json:"why"`
}

// Criterion is one servability bar a component is judged against.
type Criterion string

// The servability criteria, keyed as they appear in failed_criteria.
const (
	CriterionSingleSession      Criterion = "single_session"      // one session, no follow-up
	CriterionScreenBased        Criterion = "screen_based"        // no physical apparatus
	CriterionNoExperimenter     Criterion = "no_experimenter"     // no experimenter or confederate in the loop
	CriterionBrowserMeasures    Criterion = "browser_measures"    // every measure capturable in a browser
	CriterionPageAdministrable  Criterion = "page_administrable"  // the manipulation is administrable by the page
	CriterionApparatusRecovered Criterion = "apparatus_recovered" // the apparatus is recovered or fully specified
)

// Criteria is the closed list a component is judged servable against. A
// component is servable only if it fails none of these; naming any criterion
// outside this list is a schema violation, so the audit cannot invent a bar.
var Criteria = []Criterion{
	CriterionSingleSession,
	CriterionScreenBased,
	CriterionNoExperimenter,
	CriterionBrowserMeasures,
	CriterionPageAdministrable,
	CriterionApparatusRecovered,
}

// DeriveAllServable is the roll-up: every component must be servable. Validate
// checks a decoded Instrument's AllServable against this, so the audit's own
// asserted value can never diverge from its components.
func DeriveAllServable(components []ComponentServability) bool {
	for _, c := range components {
		if !c.Servable {
			return false
		}
	}
	return true
}

// Violation is one schema failure, named by the field it concerns so a single
// revision can fix every reported problem at once.
type Violation struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (v Violation) String() string { return v.Field + ": " + v.Message }

var knownCriterion = func() map[Criterion]bool {
	m := make(map[Criterion]bool, len(Criteria))
	for _, c := range Criteria {
		m[c] = true
	}
	return m
}()

// Validate reports every way raw departs from the schema in one pass, so a
// worker's revision receives all of them together. An empty result means the
// instrument is well-formed. The returned error is non-nil only when raw is
// not JSON at all, which the caller cannot fix by revising content.
//
// It enforces invariants only: a null protocol or analysis carries a reason;
// every reported statistic carries a citation; an analysis command
// parameterises its input; servability answers each component against the
// closed criteria list and the roll-up matches its components. Whether an
// extracted protocol is faithful to the paper is judged by the grader's
// honesty criterion, not here.
func Validate(raw []byte) ([]Violation, error) {
	var inst Instrument
	if err := json.Unmarshal(raw, &inst); err != nil {
		// A field of the wrong shape is not a syntax error; reporting it as
		// one sends the reader looking for a missing comma. A revision can
		// fix it, so it is reported as a violation naming the field rather
		// than as a refusal to read the document.
		var mismatch *json.UnmarshalTypeError
		if errors.As(err, &mismatch) {
			return []Violation{{
				Field:   mismatch.Field,
				Message: fmt.Sprintf("is a JSON %s and must be %s", mismatch.Value, mismatch.Type),
			}}, nil
		}
		return nil, fmt.Errorf("instrument.json is not valid JSON: %w", err)
	}
	var vs []Violation
	add := func(field, msg string) { vs = append(vs, Violation{Field: field, Message: msg}) }

	// A null protocol or analysis is a passing answer only with a stated reason.
	if inst.Protocol == nil && strings.TrimSpace(inst.Absent["protocol"]) == "" {
		add("protocol", "is null without a reason in absent_reasons")
	}
	if inst.Analysis == nil && strings.TrimSpace(inst.Absent["analysis"]) == "" {
		add("analysis", "is null without a reason in absent_reasons")
	}

	if inst.Protocol != nil {
		for i, r := range inst.Protocol.Reported {
			if strings.TrimSpace(r.Citation) == "" {
				add(fmt.Sprintf("protocol.reported[%d].citation", i),
					"reported statistic has no section, table, or figure citation")
			}
		}
	}

	if inst.Analysis != nil {
		if !strings.Contains(inst.Analysis.Command, InputPlaceholder) {
			add("analysis.command",
				"does not parameterise its input path with "+InputPlaceholder)
		}
		if len(inst.Analysis.Input.Columns) == 0 {
			add("analysis.input.columns", "declares no input columns")
		}
	}

	validateServability(inst.Servability, add)
	return vs, nil
}

func validateServability(s Servability, add func(field, msg string)) {
	for i, c := range s.Components {
		field := fmt.Sprintf("servability.components[%d]", i)
		for _, f := range c.Failed {
			if !knownCriterion[f] {
				add(field+".failed_criteria", fmt.Sprintf("unknown servability criterion %q", f))
			}
		}
		switch {
		case c.Servable && len(c.Failed) > 0:
			add(field, "is marked servable but lists failed criteria")
		case !c.Servable && len(c.Failed) == 0:
			add(field, "is marked not servable but names no failed criterion")
		}
		if !c.Servable && strings.TrimSpace(c.Why) == "" {
			add(field+".why", "a non-servable component must say why")
		}
	}
	if got, want := s.AllServable, DeriveAllServable(s.Components); got != want {
		add("servability.all_servable",
			fmt.Sprintf("is %v but its components derive %v; it must be derived, not asserted", got, want))
	}
}
