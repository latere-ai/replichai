// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package verdict_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/verdict"
)

// good is a file that satisfies every rule, so each test below can break
// exactly one thing and attribute the failure to it.
func good() verdict.Verdict {
	return verdict.Verdict{
		Schema:   verdict.Schema,
		Verdict:  verdict.Partial,
		Summary:  "The classifier retrains from the released code and matches within 1.8 pp.",
		GradedAt: time.Date(2026, 7, 14, 10, 41, 0, 0, time.UTC),
		Components: verdict.Components{
			Verified: 2, Partial: 0, Blocked: 1, Total: 3,
		},
		Boundary: []verdict.Claim{
			{
				Claim: "Posture classifier reaches 91.4% accuracy", Loop: verdict.Inner,
				Outcome: verdict.Verified, Citation: "§6.1",
				Evidence: "Retrained from released code; 89.6%",
				Mismatch: &verdict.Mismatch{Kind: verdict.Numeric, Reported: "91.4%", Observed: "89.6%", Delta: "-1.8 pp"},
			},
			{Claim: "Grip features outperform the IMU-only baseline", Loop: verdict.Inner, Outcome: verdict.Verified, Citation: "§6.2", Evidence: "Ablation reproduced, same ordering"},
			{Claim: "End-to-end latency under 40 ms", Loop: verdict.Inner, Outcome: verdict.Blocked, Citation: "§6.3", Evidence: "Requires the authors' tablet; model unspecified"},
			{Claim: "Technique preferred over baseline (n=24)", Loop: verdict.Outer, Outcome: verdict.NotScored, Citation: "§7", Evidence: "Human study — reported, never scored"},
		},
		Decisions: []verdict.Decision{
			{Question: "Which window length was used?", Where: "§4, unspecified", Assumed: "250 ms, from the figure axis", Sensitivity: "±0.4 pp across 200–300 ms"},
		},
		Scorecard: []verdict.Score{
			{Criterion: verdict.Code, Found: true, URL: "https://github.com/authors/x"},
			{Criterion: verdict.Data, Found: false, Searched: []string{"OSF", "the link in §5"}},
			{Criterion: verdict.License, Found: true, URL: "https://github.com/authors/x/LICENSE"},
			{Criterion: verdict.Preregistration, Found: false, Searched: []string{"OSF registries"}},
			{Criterion: verdict.Supplement, Found: true, URL: "https://dl.acm.org/doi/suppl/10.1145/x"},
		},
		Released: []verdict.Release{
			{Where: "GitHub", What: "code, no data", Result: verdict.Found, URL: "https://github.com/authors/x"},
			{Where: "OSF", What: "searched by title and all four author names", Result: verdict.NothingFound},
			{Where: "the dataset link in §5", What: "https://tabletgrip.example/data", Result: verdict.Dead},
		},
	}
}

func raw(t *testing.T, v verdict.Verdict) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// only reports the problems whose field starts with prefix, so a test asserting
// one rule is not satisfied by an unrelated failure.
func only(ps []verdict.Problem, prefix string) []verdict.Problem {
	var out []verdict.Problem
	for _, p := range ps {
		if strings.HasPrefix(p.Field, prefix) {
			out = append(out, p)
		}
	}
	return out
}

func TestValidateAcceptsAWellFormedFile(t *testing.T) {
	ps, err := verdict.Validate(raw(t, good()))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(ps) != 0 {
		t.Fatalf("a well-formed verdict was rejected: %v", ps)
	}
}

func TestValidateRejectsNonJSON(t *testing.T) {
	if _, err := verdict.Validate([]byte("# Reproducibility\n")); err == nil {
		t.Fatal("markdown validated as verdict.json")
	}
}

// The product's central rule: a file that scores the human study is refused
// rather than rendered.
func TestOuterLoopIsNeverScored(t *testing.T) {
	for _, o := range []verdict.ClaimOutcome{verdict.Verified, verdict.PartlyVerified, verdict.Blocked} {
		v := good()
		v.Boundary[3].Outcome = o
		ps, err := verdict.Validate(raw(t, v))
		if err != nil {
			t.Fatalf("Validate: %v", err)
		}
		if len(only(ps, "boundary[3].outcome")) == 0 {
			t.Fatalf("an outer-loop claim scored %q was accepted", o)
		}
	}
}

// The converse: an inner-loop claim marked not_scored hides a blocker instead
// of recording it.
func TestInnerLoopIsAlwaysScored(t *testing.T) {
	v := good()
	v.Boundary[2].Outcome = verdict.NotScored
	v.Components = verdict.Components{Verified: 2, Blocked: 0, Total: 3}
	ps, _ := verdict.Validate(raw(t, v))
	if len(only(ps, "boundary[2].outcome")) == 0 {
		t.Fatal("an inner-loop claim marked not_scored was accepted")
	}
}

func TestEveryClaimCarriesACitation(t *testing.T) {
	v := good()
	v.Boundary[1].Citation = "  "
	ps, _ := verdict.Validate(raw(t, v))
	if len(only(ps, "boundary[1].citation")) == 0 {
		t.Fatal("a claim with no citation was accepted")
	}
}

func TestEveryClaimCarriesEvidenceOrABlocker(t *testing.T) {
	v := good()
	v.Boundary[2].Evidence = ""
	ps, _ := verdict.Validate(raw(t, v))
	if len(only(ps, "boundary[2].evidence")) == 0 {
		t.Fatal("a blocked claim with no stated blocker was accepted")
	}
}

func TestAMismatchNeedsBothNumbers(t *testing.T) {
	v := good()
	v.Boundary[0].Mismatch.Observed = ""
	ps, _ := verdict.Validate(raw(t, v))
	if len(only(ps, "boundary[0].mismatch")) == 0 {
		t.Fatal("a mismatch with no observed value was accepted")
	}

	v = good()
	v.Boundary[0].Mismatch.Kind = "wrong"
	ps, _ = verdict.Validate(raw(t, v))
	if len(only(ps, "boundary[0].mismatch.kind")) == 0 {
		t.Fatal("a mismatch of an unknown kind was accepted")
	}
}

func TestEveryDecisionStatesItsSensitivity(t *testing.T) {
	v := good()
	v.Decisions[0].Sensitivity = ""
	ps, _ := verdict.Validate(raw(t, v))
	if len(only(ps, "decisions[0].sensitivity")) == 0 {
		t.Fatal("a decision with no stated sensitivity was accepted")
	}
}

func TestScorecardNeedsAURLOrThePlacesItLooked(t *testing.T) {
	v := good()
	v.Scorecard[0].URL = ""
	ps, _ := verdict.Validate(raw(t, v))
	if len(only(ps, "scorecard[0].url")) == 0 {
		t.Fatal("a criterion marked found with no URL was accepted")
	}

	v = good()
	v.Scorecard[1].Searched = nil
	ps, _ = verdict.Validate(raw(t, v))
	if len(only(ps, "scorecard[1].searched")) == 0 {
		t.Fatal("a criterion marked not found without naming where it looked was accepted")
	}
}

func TestScorecardCoversEveryCriterionExactlyOnce(t *testing.T) {
	v := good()
	v.Scorecard = v.Scorecard[:4] // drop the supplement
	ps, _ := verdict.Validate(raw(t, v))
	if len(only(ps, "scorecard")) == 0 {
		t.Fatal("a scorecard missing a criterion was accepted")
	}

	v = good()
	v.Scorecard = append(v.Scorecard, verdict.Score{Criterion: verdict.Code, Found: true, URL: "https://x"})
	ps, _ = verdict.Validate(raw(t, v))
	if len(only(ps, "scorecard[5].criterion")) == 0 {
		t.Fatal("a criterion scored twice was accepted")
	}
}

// An empty scorecard is a different failure from an incomplete one and is
// outside this rule: a run that scored nothing has no rows to check for a
// missing criterion.
func TestAnEmptyScorecardIsNotAMissingCriterion(t *testing.T) {
	v := good()
	v.Scorecard = nil
	ps, _ := verdict.Validate(raw(t, v))
	if len(only(ps, "scorecard")) != 0 {
		t.Fatalf("an empty scorecard reported per-criterion problems: %v", only(ps, "scorecard"))
	}
}

func TestComponentsMustAddUpAndMatchTheInnerRows(t *testing.T) {
	v := good()
	v.Components.Total = 9
	ps, _ := verdict.Validate(raw(t, v))
	if len(only(ps, "components")) == 0 {
		t.Fatal("counts that do not add up were accepted")
	}

	// The counts add up but claim more components than the boundary has
	// inner rows, so the rate would summarize a decomposition the reader
	// cannot see.
	v = good()
	v.Components = verdict.Components{Verified: 5, Partial: 0, Blocked: 1, Total: 6}
	ps, _ = verdict.Validate(raw(t, v))
	if len(only(ps, "components.total")) == 0 {
		t.Fatal("a total disagreeing with the inner-loop rows was accepted")
	}
}

func TestTheBoundaryIsRequired(t *testing.T) {
	v := good()
	v.Boundary = nil
	v.Components = verdict.Components{}
	ps, _ := verdict.Validate(raw(t, v))
	if len(only(ps, "boundary")) == 0 {
		t.Fatal("a verdict with no per-component table was accepted; the table is the result")
	}
}

func TestHeaderFieldsAreRequired(t *testing.T) {
	v := good()
	v.Verdict = "mostly"
	v.Summary = " "
	v.GradedAt = time.Time{}
	v.Schema = 99
	ps, _ := verdict.Validate(raw(t, v))
	for _, f := range []string{"verdict", "summary", "graded_at", "schema"} {
		if len(only(ps, f)) == 0 {
			t.Errorf("%s was accepted as invalid", f)
		}
	}
}

func TestAReleaseThatFoundSomethingLinksToIt(t *testing.T) {
	v := good()
	v.Released[0].URL = ""
	ps, _ := verdict.Validate(raw(t, v))
	if len(only(ps, "released[0].url")) == 0 {
		t.Fatal("a found artifact with no URL was accepted")
	}

	// A dead link is recorded in What. Requiring a URL as well would make the
	// audit repeat an address it has reported as unresolvable.
	v = good()
	ps, _ = verdict.Validate(raw(t, v))
	if len(only(ps, "released[2]")) != 0 {
		t.Fatalf("a dead result was rejected: %v", only(ps, "released[2]"))
	}
}

func TestParseRoundTrips(t *testing.T) {
	want := good()
	got, err := verdict.Parse(raw(t, want))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Verdict != want.Verdict || len(got.Boundary) != len(want.Boundary) || got.Boundary[0].Mismatch == nil {
		t.Fatalf("round trip lost content: %+v", got)
	}
	if !got.GradedAt.Equal(want.GradedAt) {
		t.Fatalf("graded_at round-tripped as %v, want %v", got.GradedAt, want.GradedAt)
	}
}
