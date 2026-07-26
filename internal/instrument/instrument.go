// Package instrument is the schema for instrument.json — the declared,
// machine-readable half of what an audit's reports state in prose: the paper's
// study protocol, the analysis entrypoint contract for src/, and a per-component
// servability assessment.
//
// It holds the schema and its validation and nothing else. Executing the
// analysis (007b), driving a cohort (007c), and serving the page (007d) live in
// later specs and behind later packages, so nothing downstream depends on how
// the audit produced the file.
//
// Refusal is a first-class answer. A paper too vague to extract a protocol from
// yields a null protocol with a stated reason, and that passes: the wall is the
// finding. What does not pass is an invented protocol or an unstated null, which
// is what Validate is built to catch.
package instrument

import (
	"encoding/json"
	"fmt"
	"strings"
)

// InputPlaceholder marks where the input data path is substituted into an
// analysis command, so the same pipeline runs over a new dataset without
// editing (007b). A command that bakes its input path in is not an instrument.
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
// than a study-design DSL on purpose: this schema covers only what the
// servability criteria admit, not every design in HCI.
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
	Level string `json:"level,omitempty"` // "per_trial" | "per_participant"
}

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
	Citation string `json:"citation"`           // the table, figure, or section — required
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

// CommandFor substitutes an input data path into the command, so a caller runs
// the recovered analysis over a new dataset. It errors rather than silently
// running the wrong file when the command does not name its input.
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
	Name     string `json:"name"`
	Type     string `json:"type"`           // "number" | "integer" | "string" | "boolean" | "categorical"
	Unit     string `json:"unit,omitempty"` // the unit, when the type has one
	Required bool   `json:"required"`
}

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
// the protocol to participants. The roll-up is derived from the components,
// never asserted on its own — see DeriveAllServable.
type Servability struct {
	Components  []ComponentServability `json:"components"`
	AllServable bool                   `json:"all_servable"`
}

// ComponentServability is the answer for one component of the boundary table.
// A component is servable only if it fails no criterion; when it is not
// servable, Failed enumerates which criteria it fails.
type ComponentServability struct {
	Component string   `json:"component"`
	Servable  bool     `json:"servable"`
	Failed    []string `json:"failed_criteria"` // drawn from Criteria
	Why       string   `json:"why"`
}

// Criteria is the closed list a component is judged servable against. A
// component is servable only if it fails none of these; naming any criterion
// outside this list is a schema violation, so the audit cannot invent a bar.
var Criteria = []string{
	CriterionSingleSession,
	CriterionScreenBased,
	CriterionNoExperimenter,
	CriterionBrowserMeasures,
	CriterionPageAdministrable,
	CriterionApparatusRecovered,
}

// The servability criteria, keyed as they appear in failed_criteria.
const (
	CriterionSingleSession      = "single_session"      // one session, no follow-up
	CriterionScreenBased        = "screen_based"        // no physical apparatus
	CriterionNoExperimenter     = "no_experimenter"     // no experimenter or confederate in the loop
	CriterionBrowserMeasures    = "browser_measures"    // every measure capturable in a browser
	CriterionPageAdministrable  = "page_administrable"  // the manipulation is administrable by the page
	CriterionApparatusRecovered = "apparatus_recovered" // the apparatus is recovered or fully specified
)

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

var knownCriterion = func() map[string]bool {
	m := make(map[string]bool, len(Criteria))
	for _, c := range Criteria {
		m[c] = true
	}
	return m
}()

// Validate reports every way raw departs from the schema, in one pass, so a
// worker's revision receives all of them together. A nil, empty result means
// the instrument is well-formed. The returned error is non-nil only when raw is
// not JSON at all — a structural problem the caller cannot fix by revising
// content.
//
// What it enforces is invariants, not study design: a null protocol or analysis
// carries a reason; every reported statistic carries a citation; an analysis
// command parameterises its input; servability answers each component against
// the closed criteria list and the roll-up matches its components. Judging
// whether an extracted protocol is faithful to the paper is the grader's honesty
// criterion, not this function's.
func Validate(raw []byte) ([]Violation, error) {
	var inst Instrument
	if err := json.Unmarshal(raw, &inst); err != nil {
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
