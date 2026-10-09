// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package verdict is the schema for verdict.json, the findings of a
// reproduction recorded as data rather than prose.
//
// The rubric already requires everything in it: a per-component reproduction
// table (criterion 1), every component classified inner or outer with a
// citation (2), the unwritten decisions with their sensitivity (3), and code,
// data, license, preregistration and supplement each scored against a fetched
// URL or an explicit "none found" naming where the run looked (4). Before this
// file existed the audit wrote all of it into REPRODUCIBILITY.md and
// SOURCES.md, where nothing downstream could read it, so a per-claim result
// reached the screen as one word and one number.
//
// The package holds the schema and its validation and nothing else, following
// internal/instrument. Two rules are stated here rather than in the field
// comments:
//
// An outer-loop claim is never scored. Defending this rule is the purpose of
// replichai, so it is a schema constraint rather than a convention: a file
// that claims an outcome for a human study is refused, not rendered.
//
// A mismatch is separate from an outcome. A claim can be verified and still
// disagree with the paper, for example 89.6% against a reported 91.4%, and a
// paper can be wrong on its own terms, with a transposed figure or a caption
// that contradicts its table. Folding a mismatch into the outcome would make
// "verified" mean "the paper was right", which is a stronger statement than
// the run can make.
package verdict

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Schema is the version this package writes and the method asks agents for.
// Schema 2 replaced the single mismatch object with a list that also carries
// the numbers, after a pilot in which agents had to invent a second list to
// record a second disagreement and wrote values in shapes schema 1 refused.
const Schema = 2

// oldestSchema is the oldest version Validate still accepts. Sessions already
// in the app hold schema 1 files, and refusing them would turn every existing
// findings pane into an error. Parse reads both the same way, so nothing past
// it sees which one a file was.
const oldestSchema = 1

// File is the path of the findings in a reproduction repository. It is defined
// here rather than in the publisher because a reader of the schema needs the
// name too, and the HTTP layer may not import the engine.
const File = "verdict.json"

// Outcome is the run's overall answer about the inner loop.
type Outcome string

// The overall outcomes. They describe the inner loop only; no value says
// anything about the study with human participants.
const (
	Reproducible    Outcome = "reproducible"
	Partial         Outcome = "partial"
	NotReproducible Outcome = "not_reproducible"
)

// Loop names which half of the paper a claim belongs to.
type Loop string

// The two loops. Where the boundary between them falls is itself a finding.
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

// The five criteria the rubric scores. The list is closed so an audit cannot
// invent a criterion and then pass it.
const (
	Code            Criterion = "code"
	Data            Criterion = "data"
	License         Criterion = "license"
	Preregistration Criterion = "preregistration"
	Supplement      Criterion = "supplement"
)

// Criteria is the closed scorecard list, in the order a reader reads it.
var Criteria = []Criterion{Code, Data, License, Preregistration, Supplement}

// Result is what one search for the authors' artifacts found. NothingFound and
// Dead are distinct: nothing was published, versus something was published and
// no longer resolves. Only the second can be fixed by the author.
//
// NotFetched is distinct from both. The run was not allowed or able to look, as
// when the sandbox refused the domain, a paywall stood in the way, or the fetch
// failed, so it says nothing about what the authors published. Recording it as
// NothingFound would report that an author released nothing when the run never
// saw the page.
type Result string

// The search results.
const (
	Found        Result = "found"
	PartlyFound  Result = "partial"
	NothingFound Result = "none"
	Dead         Result = "dead"
	NotFetched   Result = "not_fetched"
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

// Components is the decomposition the rate is derived from. It is shown
// wherever the rate is shown, because the rubric forbids reading the rate
// without the decomposition it summarizes.
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

	// Mismatches has one entry per quantity on which the run and the paper
	// disagree. Parse folds schema 1's single mismatch object, and the
	// additional_mismatches list agents invented when only one was allowed,
	// into this list, so nothing downstream reads either.
	Mismatches []Mismatch `json:"mismatches,omitempty"`
}

// Mismatch records that the run and the paper disagree on one quantity,
// whatever the outcome.
//
// Reported and Observed are text because what a paper states is often not one
// number: a range, a row of a table, "approximately 45%". ReportedValue and
// ObservedValue carry the number when it is one, so a reader can compute with
// it without parsing prose, and are nil when it is not.
//
// Every text field accepts any JSON value. An agent asked what the paper
// reported often wrote the table row itself as an object, and that row, kept as
// its compact JSON, says more to a reader than a refusal does. The two values
// are strict, since a value that is not a number cannot be computed with.
type Mismatch struct {
	Kind          MismatchKind `json:"kind"`
	What          string       `json:"what,omitempty"` // which quantity is compared
	Reported      string       `json:"reported"`       // as the paper states it
	Observed      string       `json:"observed"`       // as the run got it
	ReportedValue *float64     `json:"reported_value,omitempty"`
	ObservedValue *float64     `json:"observed_value,omitempty"`
	Unit          string       `json:"unit,omitempty"`
	Note          string       `json:"note,omitempty"` // the interpretation, or the cause
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
	knownResult  = map[Result]bool{Found: true, PartlyFound: true, NothingFound: true, Dead: true, NotFetched: true}
	knownCrit    = func() map[Criterion]bool {
		m := make(map[Criterion]bool, len(Criteria))
		for _, c := range Criteria {
			m[c] = true
		}
		return m
	}()
)

// Parse decodes verdict.json without judging it. Use Validate for the rules.
//
// The error is non-nil only when raw is not JSON, or is JSON but not an
// object, since then there is nothing to read a field from. Anything else is
// read as far as it goes: each header field and each row of boundary,
// decisions, scorecard and released is decoded on its own, a field or row that
// cannot be decoded is reported as a Problem at its path, and every other row
// survives. One mistyped field therefore costs its own row and nothing else,
// and the file is never called invalid JSON when it is valid JSON of the wrong
// shape.
//
// A row that does not decode is left out of the returned verdict, so a caller
// that receives problems is holding a partial file.
func Parse(raw []byte) (Verdict, []Problem, error) {
	p, err := parse(raw)
	if err != nil {
		return Verdict{}, nil, err
	}
	return p.v, p.problems, nil
}

// Validate decodes raw as Parse does and reports every way it departs from the
// schema in one pass, so a worker's revision receives all of them together: the
// fields and rows that did not decode, then every rule the decoded content
// breaks. An empty list means the file is well-formed and the returned verdict
// is all of it. The error is the one Parse returns, for a file that is not a
// JSON object at all, which no revision of a field can fix.
//
// It enforces invariants only: an outer-loop claim is never scored, every
// claim carries a citation, every decision carries its sensitivity, a scored
// criterion carries either the URL it fetched or the places it searched, and
// the component counts add up to the total they claim. Whether the boundary
// was drawn in the right place is judged by the grader's honesty criterion,
// not here.
func Validate(raw []byte) (Verdict, []Problem, error) {
	p, err := parse(raw)
	if err != nil {
		return Verdict{}, nil, err
	}
	p.check()
	return p.v, p.problems, nil
}

// parsed is a decoded file and what the decoding found wrong with it.
type parsed struct {
	v        Verdict
	problems []Problem

	// incomplete names the header fields and lists that did not decode in
	// full. Their rules are not checked: the decode problem already names
	// them, and a list missing a row would fail a count that the file as
	// written may well pass.
	incomplete map[string]bool

	// The path each kept row was read from. A row that does not decode is left
	// out, so a later row's place in the slice is not its place in the file,
	// and a problem has to name the one the author can find. mismatchAt holds,
	// for each kept claim, the path each of its mismatches was read from,
	// which for a schema 1 file is mismatch rather than mismatches[0].
	claimAt, decisionAt, scoreAt, releaseAt []string
	mismatchAt                              [][]string
}

func (p *parsed) add(field, msg string) {
	p.problems = append(p.problems, Problem{Field: field, Message: msg})
}

func parse(raw []byte) (*parsed, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		if !json.Valid(raw) {
			return nil, fmt.Errorf("%s is not JSON: %w", File, err)
		}
		return nil, fmt.Errorf("%s is %s, not a JSON object", File, describe(raw))
	}
	if top == nil {
		return nil, fmt.Errorf("%s is null, not a JSON object", File)
	}

	p := &parsed{incomplete: map[string]bool{}}
	header := func(name string, dst any) {
		if !p.field(top, "", name, dst) {
			p.incomplete[name] = true
		}
	}
	header("schema", &p.v.Schema)
	header("verdict", &p.v.Verdict)
	header("summary", &p.v.Summary)
	header("graded_at", &p.v.GradedAt)
	p.components(top)
	p.v.Boundary, p.claimAt = rows(p, top, "boundary", p.claim)
	p.v.Decisions, p.decisionAt = rows(p, top, "decisions", p.decision)
	p.v.Scorecard, p.scoreAt = rows(p, top, "scorecard", p.score)
	p.v.Released, p.releaseAt = rows(p, top, "released", p.release)
	return p, nil
}

// field decodes obj[name] into dst and reports whether it could. An absent or
// null field leaves dst at its zero value for the rules to judge; a field of
// the wrong type is a problem at its path.
func (p *parsed) field(obj map[string]json.RawMessage, at, name string, dst any) bool {
	raw, ok := obj[name]
	if !ok || isNull(raw) {
		return true
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		p.add(join(at, name), mistyped(raw, dst))
		return false
	}
	return true
}

// object decodes raw as a JSON object, or reports at the path that it is not
// one.
func (p *parsed) object(raw json.RawMessage, at string) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		p.add(at, fmt.Sprintf("is %s, want an object", describe(raw)))
		return nil, false
	}
	return obj, true
}

// rows decodes the list top[name] one row at a time, keeping each row that
// decodes and the path it was read from.
func rows[T any](p *parsed, top map[string]json.RawMessage, name string, decode func(json.RawMessage, string) (T, bool)) ([]T, []string) {
	raw, ok := top[name]
	if !ok || isNull(raw) {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		p.add(name, fmt.Sprintf("is %s, want a list", describe(raw)))
		p.incomplete[name] = true
		return nil, nil
	}
	var out []T
	var paths []string
	for i, item := range items {
		at := fmt.Sprintf("%s[%d]", name, i)
		row, ok := decode(item, at)
		if !ok {
			p.incomplete[name] = true
			continue
		}
		out = append(out, row)
		paths = append(paths, at)
	}
	return out, paths
}

func (p *parsed) components(top map[string]json.RawMessage) {
	raw, ok := top["components"]
	if !ok || isNull(raw) {
		return
	}
	obj, ok := p.object(raw, "components")
	if !ok {
		p.incomplete["components"] = true
		return
	}
	c := &p.v.Components
	for _, f := range []struct {
		name string
		dst  *int
	}{{"verified", &c.Verified}, {"partial", &c.Partial}, {"blocked", &c.Blocked}, {"total", &c.Total}} {
		if !p.field(obj, "components", f.name, f.dst) {
			p.incomplete["components"] = true
		}
	}
}

// mismatchKeys are the claim fields a disagreement is read from, in the order
// their entries join the list: schema 1's single object, schema 2's list, and
// the list agents added beside schema 1's object when they had a second
// disagreement to record.
var mismatchKeys = []string{"mismatch", "mismatches", "additional_mismatches"}

// claim decodes one boundary row. A mismatch that does not decode is a problem
// at its own path and does not cost the claim its row.
func (p *parsed) claim(raw json.RawMessage, at string) (Claim, bool) {
	obj, ok := p.object(raw, at)
	if !ok {
		return Claim{}, false
	}
	var c Claim
	ok = p.field(obj, at, "claim", &c.Claim)
	ok = p.field(obj, at, "loop", &c.Loop) && ok
	ok = p.field(obj, at, "outcome", &c.Outcome) && ok
	ok = p.field(obj, at, "evidence", &c.Evidence) && ok
	ok = p.field(obj, at, "citation", &c.Citation) && ok

	var paths []string
	for _, key := range mismatchKeys {
		raw, present := obj[key]
		if !present || isNull(raw) {
			continue
		}
		items, itemPaths, listed := p.objects(raw, join(at, key))
		if !listed {
			continue
		}
		for j, item := range items {
			if m, ok := p.mismatch(item, itemPaths[j]); ok {
				c.Mismatches = append(c.Mismatches, m)
				paths = append(paths, itemPaths[j])
			}
		}
	}
	// rows keeps this claim exactly when ok is true, so the paths are recorded
	// under the same condition to stay aligned with p.claimAt.
	if ok {
		p.mismatchAt = append(p.mismatchAt, paths)
	}
	return c, ok
}

// objects reads raw as a list of values, or as a single object standing for a
// list of one, which is how schema 1 wrote a mismatch. Each value comes back
// with the path it was read from.
func (p *parsed) objects(raw json.RawMessage, at string) ([]json.RawMessage, []string, bool) {
	if describe(raw) == "an object" {
		return []json.RawMessage{raw}, []string{at}, true
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		p.add(at, fmt.Sprintf("is %s, want a list of objects", describe(raw)))
		return nil, nil, false
	}
	paths := make([]string, len(items))
	for j := range items {
		paths[j] = fmt.Sprintf("%s[%d]", at, j)
	}
	return items, paths, true
}

// mismatch decodes one disagreement, reading schema 1's delta into the note
// and its units into the unit. Only a kind that is not a string costs the
// entry, since without a kind there is nothing to say about it; a value that
// is not a number is a problem on that field and the entry is kept without it.
func (p *parsed) mismatch(raw json.RawMessage, at string) (Mismatch, bool) {
	obj, ok := p.object(raw, at)
	if !ok {
		return Mismatch{}, false
	}
	var m Mismatch
	ok = p.field(obj, at, "kind", &m.Kind)
	m.What = p.text(obj, at, "what")
	m.Reported = p.text(obj, at, "reported")
	m.Observed = p.text(obj, at, "observed")
	m.ReportedValue = p.number(obj, at, "reported_value")
	m.ObservedValue = p.number(obj, at, "observed_value")
	if m.Unit = p.text(obj, at, "unit"); m.Unit == "" {
		m.Unit = p.text(obj, at, "units")
	}
	m.Note = note(p.text(obj, at, "delta"), p.text(obj, at, "note"))
	return m, ok
}

// text reads a field that holds words. A string is kept as written, and any
// other value as its compact JSON, so a table row an agent wrote as an object
// reaches the reader as that row.
func (p *parsed) text(obj map[string]json.RawMessage, at, name string) string {
	raw, ok := obj[name]
	if !ok || isNull(raw) {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		p.add(join(at, name), "could not be read as text: "+err.Error())
		return ""
	}
	return b.String()
}

// number reads a field that holds one number, or null when the quantity is
// not a single number.
func (p *parsed) number(obj map[string]json.RawMessage, at, name string) *float64 {
	raw, ok := obj[name]
	if !ok || isNull(raw) {
		return nil
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		if describe(raw) == "a number" {
			p.add(join(at, name), "is a number out of range")
		} else {
			p.add(join(at, name), fmt.Sprintf("is %s, want a number, or null when the quantity is not a single number", describe(raw)))
		}
		return nil
	}
	return &f
}

// note joins schema 1's delta and a note into one line, the delta first, since
// the delta is the figure and the note says what it means.
func note(delta, note string) string {
	switch {
	case delta == "":
		return note
	case note == "":
		return delta
	}
	return delta + "; " + note
}

func (p *parsed) decision(raw json.RawMessage, at string) (Decision, bool) {
	obj, ok := p.object(raw, at)
	if !ok {
		return Decision{}, false
	}
	var d Decision
	ok = p.field(obj, at, "question", &d.Question)
	ok = p.field(obj, at, "where", &d.Where) && ok
	ok = p.field(obj, at, "assumed", &d.Assumed) && ok
	ok = p.field(obj, at, "sensitivity", &d.Sensitivity) && ok
	return d, ok
}

func (p *parsed) score(raw json.RawMessage, at string) (Score, bool) {
	obj, ok := p.object(raw, at)
	if !ok {
		return Score{}, false
	}
	var s Score
	ok = p.field(obj, at, "criterion", &s.Criterion)
	ok = p.field(obj, at, "found", &s.Found) && ok
	ok = p.field(obj, at, "url", &s.URL) && ok
	ok = p.field(obj, at, "searched", &s.Searched) && ok
	return s, ok
}

func (p *parsed) release(raw json.RawMessage, at string) (Release, bool) {
	obj, ok := p.object(raw, at)
	if !ok {
		return Release{}, false
	}
	var r Release
	ok = p.field(obj, at, "where", &r.Where)
	ok = p.field(obj, at, "what", &r.What) && ok
	ok = p.field(obj, at, "result", &r.Result) && ok
	ok = p.field(obj, at, "url", &r.URL) && ok
	return r, ok
}

// check adds a problem for every rule the decoded content breaks.
func (p *parsed) check() {
	v := p.v
	add := p.add

	if !p.incomplete["schema"] && (v.Schema < oldestSchema || v.Schema > Schema) {
		add("schema", fmt.Sprintf("is %d, want %d", v.Schema, Schema))
	}
	if !p.incomplete["verdict"] && !knownOutcome[v.Verdict] {
		add("verdict", fmt.Sprintf("%q is not one of reproducible, partial, not_reproducible", v.Verdict))
	}
	if !p.incomplete["summary"] && strings.TrimSpace(v.Summary) == "" {
		add("summary", "is empty; the verdict has to be stated in words as well as scored")
	}
	if !p.incomplete["graded_at"] && v.GradedAt.IsZero() {
		add("graded_at", "is missing")
	}

	if !p.incomplete["boundary"] && len(v.Boundary) == 0 {
		add("boundary", "is empty; the per-component table is the result, not the rate")
	}
	inner := 0
	for i, c := range v.Boundary {
		at := func(f string) string { return join(p.claimAt[i], f) }
		if strings.TrimSpace(c.Claim) == "" {
			add(at("claim"), "is empty")
		}
		if !knownLoop[c.Loop] {
			add(at("loop"), fmt.Sprintf("%q is not inner or outer", c.Loop))
		}
		if !knownClaim[c.Outcome] {
			add(at("outcome"), fmt.Sprintf("%q is not one of verified, partial, blocked, not_scored", c.Outcome))
		}
		// The product's central rule, enforced as a schema constraint.
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
		for j, m := range c.Mismatches {
			mat := p.mismatchAt[i][j]
			if !knownKind[m.Kind] {
				add(join(mat, "kind"), fmt.Sprintf("%q is not one of numeric, ordering, contradiction, typo", m.Kind))
			}
			if strings.TrimSpace(m.Reported) == "" || strings.TrimSpace(m.Observed) == "" {
				add(mat, "records a disagreement without both what the paper reported and what the run observed")
			}
		}
		if c.Loop == Inner {
			inner++
		}
	}

	// The counts summarize the inner rows, so they cannot disagree with them.
	// A row that did not decode has no loop to count, so the second check
	// waits for a boundary that decodes in full.
	if !p.incomplete["components"] {
		if got := v.Components.Verified + v.Components.Partial + v.Components.Blocked; got != v.Components.Total {
			add("components", fmt.Sprintf("verified+partial+blocked is %d but total is %d", got, v.Components.Total))
		}
		if !p.incomplete["boundary"] && len(v.Boundary) > 0 && v.Components.Total != inner {
			add("components.total", fmt.Sprintf("is %d but the boundary has %d inner-loop claims", v.Components.Total, inner))
		}
	}

	for i, d := range v.Decisions {
		at := func(f string) string { return join(p.decisionAt[i], f) }
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
		at := func(f string) string { return join(p.scoreAt[i], f) }
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
	// A row that did not decode may be the criterion that looks missing.
	if len(v.Scorecard) > 0 && !p.incomplete["scorecard"] {
		for _, c := range Criteria {
			if !seen[c] {
				add("scorecard", fmt.Sprintf("does not score %q", c))
			}
		}
	}

	for i, r := range v.Released {
		at := func(f string) string { return join(p.releaseAt[i], f) }
		if strings.TrimSpace(r.Where) == "" {
			add(at("where"), "does not say where it looked")
		}
		if !knownResult[r.Result] {
			add(at("result"), fmt.Sprintf("%q is not one of found, partial, none, dead, not_fetched", r.Result))
		}
		if (r.Result == Found || r.Result == PartlyFound) && strings.TrimSpace(r.URL) == "" {
			add(at("url"), "found something with no URL to it")
		}
	}
}

// join names a field inside the value at path.
func join(path, field string) string {
	if path == "" {
		return field
	}
	return path + "." + field
}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

// describe names the kind of a JSON value the way a problem reads it.
func describe(raw json.RawMessage) string {
	s := bytes.TrimSpace(raw)
	if len(s) == 0 {
		return "empty"
	}
	switch s[0] {
	case '{':
		return "an object"
	case '[':
		return "a list"
	case '"':
		return "a string"
	case 't', 'f':
		return "a boolean"
	case 'n':
		return "null"
	}
	return "a number"
}

var timeType = reflect.TypeFor[time.Time]()

// mistyped says why raw could not be decoded into dst, in terms of what was
// written and what the schema wants there.
func mistyped(raw json.RawMessage, dst any) string {
	t := reflect.TypeOf(dst).Elem()
	if t == timeType {
		if describe(raw) == "a string" {
			return "is not a time written like 2026-07-14T10:41:00Z"
		}
		return fmt.Sprintf("is %s, want a time written like 2026-07-14T10:41:00Z", describe(raw))
	}
	var want string
	switch t.Kind() {
	case reflect.String:
		want = "a string"
	case reflect.Bool:
		want = "a boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		want = "a whole number"
	case reflect.Float32, reflect.Float64:
		want = "a number"
	case reflect.Slice:
		want = "a list"
	default:
		want = "an object"
	}
	got := describe(raw)
	if got == want {
		return fmt.Sprintf("is %s out of range", got)
	}
	return fmt.Sprintf("is %s, want %s", got, want)
}
