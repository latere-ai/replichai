// Package verdict is the schema for verdict.json — what a reproduction found,
// as data rather than as prose.
//
// The rubric already demands all of it: a per-component reproduction table
// (criterion 1), every component classified inner or outer with a citation (2),
// the unwritten decisions with their sensitivity (3), and code, data, licence,
// preregistration and supplement each scored against a fetched URL or an
// explicit "none found" naming where it looked (4). Until this file existed the
// audit wrote every one of them into REPRODUCIBILITY.md and SOURCES.md, where
// nothing downstream could read them — so the product's whole claim, which is
// per-claim, reached the screen as one word and one number.
//
// It holds the schema and its validation and nothing else, on the model of
// internal/instrument. Two rules are worth stating here rather than leaving to
// the field comments:
//
// An outer-loop claim is never scored. That is the one thing replichai exists
// to defend, so it is a schema constraint and not a convention: a file claiming
// an outcome for a human study is refused, not rendered.
//
// A mismatch is separate from an outcome. A claim can be verified and still
// disagree with the paper — 89.6% against a reported 91.4% — and a paper can
// simply be wrong, a transposed figure or a caption that contradicts its own
// table. Folding that into the outcome would make "verified" mean "the paper
// was right", which is a different and much stronger statement than the one the
// run is entitled to make.
package verdict

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Schema is the version this package reads and writes.
const Schema = 1

// File is where the findings live in a reproduction repository. It sits with
// the schema rather than with the publisher because a reader that knows the
// shape needs the name too, and the HTTP layer may not import the engine.
const File = "verdict.json"

// Outcome is the run's overall answer about the inner loop.
type Outcome string

// The overall outcomes. They are the inner loop's, always: no value here says
// anything about the study with human participants.
const (
	Reproducible    Outcome = "reproducible"
	Partial         Outcome = "partial"
	NotReproducible Outcome = "not_reproducible"
)

// Loop names which half of the paper a claim belongs to.
type Loop string

// The two loops. The boundary between them is the finding, not a formality.
const (
	Inner Loop = "inner"
	Outer Loop = "outer"
)

// ClaimOutcome is what became of one claim.
type ClaimOutcome string

// The per-claim outcomes. NotScored is the only legal outcome for an outer-loop
// claim, and the only one that is not a result.
const (
	Verified       ClaimOutcome = "verified"
	PartlyVerified ClaimOutcome = "partial"
	Blocked        ClaimOutcome = "blocked"
	NotScored      ClaimOutcome = "not_scored"
)

// MismatchKind names how the run's observation departs from the paper.
type MismatchKind string

// The kinds of departure. Typo and Contradiction are findings about the paper
// rather than about the reproduction, which is why they are named separately
// from a numeric gap.
const (
	Numeric       MismatchKind = "numeric"       // a number differs
	Ordering      MismatchKind = "ordering"      // a ranking or direction differs
	Contradiction MismatchKind = "contradiction" // the paper disagrees with itself
	Typo          MismatchKind = "typo"          // a plain error in the paper
)

// Criterion is one row of the open-science scorecard.
type Criterion string

// The five the rubric scores. The list is closed so an audit cannot invent a
// bar it then passes.
const (
	Code            Criterion = "code"
	Data            Criterion = "data"
	Licence         Criterion = "license"
	Preregistration Criterion = "preregistration"
	Supplement      Criterion = "supplement"
)

// Criteria is the closed scorecard list, in the order a reader reads it.
var Criteria = []Criterion{Code, Data, Licence, Preregistration, Supplement}

// Result is what one search for the authors' artifacts turned up. None and Dead
// are deliberately distinct: nothing was ever published, versus something was
// published and no longer resolves. The second is the one an author can fix.
type Result string

// The search results.
const (
	Found        Result = "found"
	PartlyFound  Result = "partial"
	NothingFound Result = "none"
	Dead         Result = "dead"
)

// Verdict is the whole of verdict.json.
type Verdict struct {
	Schema     int        `json:"schema"`
	Verdict    Outcome    `json:"verdict"`
	Summary    string     `json:"summary"` // the paragraph a reader reads first
	GradedAt   time.Time  `json:"graded_at"`
	Components Components `json:"components"`
	Boundary   []Claim    `json:"boundary"`
	Decisions  []Decision `json:"decisions"`
	Scorecard  []Score    `json:"scorecard"`
	Released   []Release  `json:"released"`
}

// Components is the decomposition the rate is derived from. It travels with the
// rate everywhere the rate is shown, because a rate without the decomposition
// it summarises is the number the rubric forbids reading alone.
type Components struct {
	Verified int `json:"verified"`
	Partial  int `json:"partial"`
	Blocked  int `json:"blocked"`
	Total    int `json:"total"`
}

// Claim is one row of the inner/outer boundary.
type Claim struct {
	Claim    string       `json:"claim"`
	Loop     Loop         `json:"loop"`
	Outcome  ClaimOutcome `json:"outcome"`
	Evidence string       `json:"evidence"` // the evidence, or the specific blocker
	Citation string       `json:"citation"` // section, figure, or table
	Mismatch *Mismatch    `json:"mismatch,omitempty"`
}

// Mismatch records that the run and the paper disagree, whatever the outcome.
type Mismatch struct {
	Kind     MismatchKind `json:"kind"`
	Reported string       `json:"reported"`
	Observed string       `json:"observed"`
	Delta    string       `json:"delta,omitempty"`
}

// Decision is one thing the paper left unwritten. All four fields are required:
// an assumption with no stated sensitivity reaches a reader as a decision with
// no stated risk, which is worse than not recording it.
type Decision struct {
	Question    string `json:"question"`
	Where       string `json:"where"`       // where the paper leaves it open
	Assumed     string `json:"assumed"`     // what the reproduction did instead
	Sensitivity string `json:"sensitivity"` // how far the result moves if it is wrong
}

// Score is one scorecard row: a URL that was fetched, or the places searched.
type Score struct {
	Criterion Criterion `json:"criterion"`
	Found     bool      `json:"found"`
	URL       string    `json:"url,omitempty"`
	Searched  []string  `json:"searched,omitempty"`
}

// Release is one place the run looked for what the authors put out.
type Release struct {
	Where  string `json:"where"` // GitHub, OSF, the ACM DL supplement, the link in §5
	What   string `json:"what"`
	Result Result `json:"result"`
	URL    string `json:"url,omitempty"`
}

// Problem is one schema failure, named by the field it concerns so a single
// revision can fix every reported problem at once.
type Problem struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (p Problem) String() string { return p.Field + ": " + p.Message }

var (
	knownOutcome = map[Outcome]bool{Reproducible: true, Partial: true, NotReproducible: true}
	knownLoop    = map[Loop]bool{Inner: true, Outer: true}
	knownClaim   = map[ClaimOutcome]bool{Verified: true, PartlyVerified: true, Blocked: true, NotScored: true}
	knownKind    = map[MismatchKind]bool{Numeric: true, Ordering: true, Contradiction: true, Typo: true}
	knownResult  = map[Result]bool{Found: true, PartlyFound: true, NothingFound: true, Dead: true}
	knownCrit    = func() map[Criterion]bool {
		m := make(map[Criterion]bool, len(Criteria))
		for _, c := range Criteria {
			m[c] = true
		}
		return m
	}()
)

// Parse decodes verdict.json without judging it. Use Validate for the rules.
func Parse(raw []byte) (Verdict, error) {
	var v Verdict
	if err := json.Unmarshal(raw, &v); err != nil {
		return Verdict{}, fmt.Errorf("verdict.json is not valid JSON: %w", err)
	}
	return v, nil
}

// Validate reports every way raw departs from the schema, in one pass, so a
// worker's revision receives all of them together. A nil, empty result means the
// file is well-formed. The returned error is non-nil only when raw is not JSON
// at all — a structural problem no revision of the content can fix.
//
// What it enforces is invariants, not judgement: an outer-loop claim is never
// scored, every claim carries a citation, every decision carries its
// sensitivity, a scored criterion carries either the URL it fetched or the
// places it searched, and the component counts add up to the total they claim.
// Whether the boundary was drawn in the right place is the grader's honesty
// criterion, not this function's.
func Validate(raw []byte) ([]Problem, error) {
	v, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	var ps []Problem
	add := func(field, msg string) { ps = append(ps, Problem{Field: field, Message: msg}) }

	if v.Schema != Schema {
		add("schema", fmt.Sprintf("is %d, want %d", v.Schema, Schema))
	}
	if !knownOutcome[v.Verdict] {
		add("verdict", fmt.Sprintf("%q is not one of reproducible, partial, not_reproducible", v.Verdict))
	}
	if strings.TrimSpace(v.Summary) == "" {
		add("summary", "is empty; the verdict has to be stated in words as well as scored")
	}
	if v.GradedAt.IsZero() {
		add("graded_at", "is missing")
	}

	if len(v.Boundary) == 0 {
		add("boundary", "is empty; the per-component table is the result, not the rate")
	}
	inner := 0
	for i, c := range v.Boundary {
		at := func(f string) string { return fmt.Sprintf("boundary[%d].%s", i, f) }
		if strings.TrimSpace(c.Claim) == "" {
			add(at("claim"), "is empty")
		}
		if !knownLoop[c.Loop] {
			add(at("loop"), fmt.Sprintf("%q is not inner or outer", c.Loop))
		}
		if !knownClaim[c.Outcome] {
			add(at("outcome"), fmt.Sprintf("%q is not one of verified, partial, blocked, not_scored", c.Outcome))
		}
		// The one rule the product exists to defend, as a schema constraint.
		if c.Loop == Outer && c.Outcome != NotScored {
			add(at("outcome"), fmt.Sprintf("an outer-loop claim is %q; the study with human participants is never scored", c.Outcome))
		}
		if c.Loop == Inner && c.Outcome == NotScored {
			add(at("outcome"), "an inner-loop claim is not_scored; a mechanically checkable claim is verified, partial, or blocked with its blocker")
		}
		if strings.TrimSpace(c.Citation) == "" {
			add(at("citation"), "has no section, figure, or table citation")
		}
		if strings.TrimSpace(c.Evidence) == "" {
			add(at("evidence"), "has neither evidence nor a specific blocker")
		}
		if c.Mismatch != nil {
			m := c.Mismatch
			if !knownKind[m.Kind] {
				add(at("mismatch.kind"), fmt.Sprintf("%q is not one of numeric, ordering, contradiction, typo", m.Kind))
			}
			if strings.TrimSpace(m.Reported) == "" || strings.TrimSpace(m.Observed) == "" {
				add(at("mismatch"), "records a disagreement without both what the paper reported and what the run observed")
			}
		}
		if c.Loop == Inner {
			inner++
		}
	}

	// The counts summarise the inner rows, so they cannot disagree with them.
	if got := v.Components.Verified + v.Components.Partial + v.Components.Blocked; got != v.Components.Total {
		add("components", fmt.Sprintf("verified+partial+blocked is %d but total is %d", got, v.Components.Total))
	}
	if len(v.Boundary) > 0 && v.Components.Total != inner {
		add("components.total", fmt.Sprintf("is %d but the boundary has %d inner-loop claims", v.Components.Total, inner))
	}

	for i, d := range v.Decisions {
		at := func(f string) string { return fmt.Sprintf("decisions[%d].%s", i, f) }
		if strings.TrimSpace(d.Question) == "" {
			add(at("question"), "is empty")
		}
		if strings.TrimSpace(d.Assumed) == "" {
			add(at("assumed"), "does not say what the reproduction assumed instead")
		}
		if strings.TrimSpace(d.Sensitivity) == "" {
			add(at("sensitivity"), "does not say how far the result moves if the assumption is wrong")
		}
	}

	seen := map[Criterion]bool{}
	for i, s := range v.Scorecard {
		at := func(f string) string { return fmt.Sprintf("scorecard[%d].%s", i, f) }
		if !knownCrit[s.Criterion] {
			add(at("criterion"), fmt.Sprintf("%q is not one of code, data, license, preregistration, supplement", s.Criterion))
			continue
		}
		if seen[s.Criterion] {
			add(at("criterion"), fmt.Sprintf("%q is scored twice", s.Criterion))
		}
		seen[s.Criterion] = true
		if s.Found && strings.TrimSpace(s.URL) == "" {
			add(at("url"), "is marked found with no URL")
		}
		if !s.Found && len(s.Searched) == 0 {
			add(at("searched"), "is marked not found without naming where it looked")
		}
	}
	if len(v.Scorecard) > 0 {
		for _, c := range Criteria {
			if !seen[c] {
				add("scorecard", fmt.Sprintf("does not score %q", c))
			}
		}
	}

	for i, r := range v.Released {
		at := func(f string) string { return fmt.Sprintf("released[%d].%s", i, f) }
		if strings.TrimSpace(r.Where) == "" {
			add(at("where"), "does not say where it looked")
		}
		if !knownResult[r.Result] {
			add(at("result"), fmt.Sprintf("%q is not one of found, partial, none, dead", r.Result))
		}
		if (r.Result == Found || r.Result == PartlyFound) && strings.TrimSpace(r.URL) == "" {
			add(at("url"), "found something with no URL to it")
		}
	}

	return ps, nil
}
