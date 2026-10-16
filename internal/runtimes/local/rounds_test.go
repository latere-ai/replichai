// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	managed "latere.ai/x/managed-agents"

	"github.com/latere-ai/replichai/internal/runtimes/local"
	"github.com/latere-ai/replichai/internal/runtimes/local/localtest"
	"github.com/latere-ai/replichai/internal/verdict"
)

// readableFindings is the scripted sandbox's readable verdict.json, checked
// here so a test that relies on it fails on the fixture and not on the code.
func readableFindings(t *testing.T) string {
	t.Helper()
	if _, problems, err := verdict.Validate([]byte(localtest.ReadableFindings)); err != nil || len(problems) > 0 {
		t.Fatalf("the readable fixture is not readable: %v %v", problems, err)
	}
	return localtest.ReadableFindings
}

// unreadableFindings breaks one rule: the component counts do not add up.
const unreadableFindings = `{"schema":2,"verdict":"partial","summary":"s","graded_at":"2026-10-15T09:00:00Z",
"components":{"verified":3,"partial":0,"blocked":0,"total":1},"boundary":[],"decisions":[],"scorecard":[],"released":[]}`

// checkedSpec is a graded session that names its findings file, with the
// given revision budget.
func checkedSpec(revisions int) managed.SessionSpec {
	s := spec(true)
	s.Evaluation.MaxRevisions = revisions
	s.Evaluation.Findings = verdict.File
	return s
}

func readChecks(t *testing.T, ref managed.SessionRef, round int) local.Checks {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(sessionDir(t, ref), "session", "rounds", local.ChecksFile(round)))
	if err != nil {
		t.Fatalf("read checks of round %d: %v", round, err)
	}
	var checks local.Checks
	if err := json.Unmarshal(body, &checks); err != nil {
		t.Fatalf("decode checks of round %d: %v", round, err)
	}
	return checks
}

// Every round keeps the findings it was graded on, round 0 being what the
// worker produced before any revision, and records what the system found in
// each.
func TestEveryRoundKeepsTheFindingsItWasGradedOn(t *testing.T) {
	good := readableFindings(t)
	runtime, _ := build(t, localtest.Script{
		Findings: []string{unreadableFindings, good},
		Grades:   []string{"needs_revision", "passed"},
	})
	ref := start(t, runtime, checkedSpec(1))
	status := runToTerminal(t, runtime, ref)
	if status.State != managed.StateDone {
		t.Fatalf("status = %+v", status)
	}
	rounds := filepath.Join(sessionDir(t, ref), "session", "rounds")
	for round, want := range []string{unreadableFindings, good} {
		body, err := os.ReadFile(filepath.Join(rounds, local.FindingsFile(round)))
		if err != nil || string(body) != want {
			t.Fatalf("round %d kept %q, %v; want what that round wrote", round, body, err)
		}
	}
	first, second := readChecks(t, ref, 0), readChecks(t, ref, 1)
	if !first.Present || len(first.Problems) == 0 || first.SHA256 == "" {
		t.Fatalf("round 0 checks = %+v, want the problems of an unreadable file", first)
	}
	if !second.Present || len(second.Problems) != 0 {
		t.Fatalf("round 1 checks = %+v, want a readable file", second)
	}
}

// Findings the system cannot read never pass on the judge's reading of the
// rubric. With no revision left, the round the judge passed ends failed.
func TestFindingsTheSystemCannotReadNeverPass(t *testing.T) {
	runtime, driver := build(t, localtest.Script{Findings: []string{unreadableFindings}, Grades: []string{"passed"}})
	status := runToTerminal(t, runtime, start(t, runtime, checkedSpec(0)))
	if status.State != managed.StateFailed || status.TerminalReason != managed.ReasonEvaluationFailed {
		t.Fatalf("status = %+v, want a failed evaluation", status)
	}
	if status.Verdict == nil || status.Verdict.Grade != managed.GradeExhausted ||
		!strings.Contains(status.Verdict.Explanation, "components") {
		t.Fatalf("verdict = %+v, want the problems the grader passed over", status.Verdict)
	}
	if !strings.Contains(strings.Join(stageNamed(t, driver, "judge").Argv, " "), "components") {
		t.Error("the judge was not told what the system found")
	}
}

// A round held back for its findings is revised, and the revision is told what
// the system found.
func TestARevisionIsToldWhatTheSystemFound(t *testing.T) {
	runtime, driver := build(t, localtest.Script{
		Findings: []string{unreadableFindings, readableFindings(t)},
		Grades:   []string{"passed", "passed"},
	})
	status := runToTerminal(t, runtime, start(t, runtime, checkedSpec(1)))
	if status.State != managed.StateDone {
		t.Fatalf("status = %+v", status)
	}
	revision := strings.Join(stageNamed(t, driver, "revision").Argv, " ")
	if !strings.Contains(revision, "components") {
		t.Fatalf("the revision was not told what the system found: %s", revision)
	}
}

// A rejection for other reasons still hands the revision the problems, beside
// the grader's reasoning.
func TestARejectedRoundCarriesItsProblemsToTheRevision(t *testing.T) {
	runtime, driver := build(t, localtest.Script{
		Findings: []string{unreadableFindings, readableFindings(t)},
		Grades:   []string{"needs_revision", "passed"},
	})
	runToTerminal(t, runtime, start(t, runtime, checkedSpec(1)))
	revision := strings.Join(stageNamed(t, driver, "revision").Argv, " ")
	if !strings.Contains(revision, "Fix every problem it found") || !strings.Contains(revision, "components") {
		t.Fatalf("the revision was not given the problems: %s", revision)
	}
}

// A run that wrote no findings has a problem the judge is told of, and a round
// 0 with nothing kept.
func TestMissingFindingsAreAProblem(t *testing.T) {
	runtime, _ := build(t, localtest.Script{Grades: []string{"passed"}})
	ref := start(t, runtime, checkedSpec(0))
	status := runToTerminal(t, runtime, ref)
	if status.State != managed.StateFailed {
		t.Fatalf("status = %+v, want a failed evaluation", status)
	}
	checks := readChecks(t, ref, 0)
	if checks.Present || len(checks.Problems) != 1 || !strings.Contains(checks.Problems[0], "did not write verdict.json") {
		t.Fatalf("checks = %+v", checks)
	}
	if _, err := os.Stat(filepath.Join(sessionDir(t, ref), "session", "rounds", local.FindingsFile(0))); !os.IsNotExist(err) {
		t.Fatalf("a copy was kept of a file that was never written: %v", err)
	}
}

// Graded with no revision, a rejection ends the session after one worker and
// one judge, and round 0 is still kept.
func TestAGradedSessionWithNoRevisionRevisesNothing(t *testing.T) {
	runtime, driver := build(t, localtest.Script{Findings: []string{readableFindings(t)}, Grades: []string{"needs_revision"}})
	ref := start(t, runtime, checkedSpec(0))
	status := runToTerminal(t, runtime, ref)
	if status.State != managed.StateFailed || status.Verdict == nil || status.Verdict.Grade != managed.GradeExhausted {
		t.Fatalf("status = %+v", status)
	}
	for _, name := range names(driver.Stages()) {
		if strings.HasPrefix(name, "revision") {
			t.Fatalf("a revision ran under a budget of zero: %v", names(driver.Stages()))
		}
	}
	if checks := readChecks(t, ref, 0); !checks.Present || len(checks.Problems) != 0 {
		t.Fatalf("round 0 checks = %+v", checks)
	}
}

// The check is asked for, not assumed: a session that names no findings file
// keeps no rounds, which is what an experiment or any other graded session
// that writes no verdict.json needs.
func TestASessionThatNamesNoFindingsKeepsNoRounds(t *testing.T) {
	runtime, _ := build(t, localtest.Script{Findings: []string{unreadableFindings}})
	ref := start(t, runtime, spec(true))
	if status := runToTerminal(t, runtime, ref); status.State != managed.StateDone {
		t.Fatalf("status = %+v", status)
	}
	if _, err := os.Stat(filepath.Join(sessionDir(t, ref), "session", "rounds")); !os.IsNotExist(err) {
		t.Fatalf("rounds were kept for a session that named no findings file: %v", err)
	}
}

// The runtime declares that it keeps rounds, which is what lets an audit ask
// for the check.
func TestTheLocalRuntimeDeclaresItKeepsRounds(t *testing.T) {
	runtime, _ := build(t, localtest.Script{})
	capabilities, err := runtime.Capabilities(t.Context())
	if err != nil || !capabilities.FindingsRounds {
		t.Fatalf("capabilities = %+v, %v", capabilities, err)
	}
}
