package local

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
)

// VerdictFile is the name pattern the judge writes under grade/. One file per
// grading round, so a revision cannot quietly edit the verdict that rejected
// it.
func VerdictFile(round int) string { return fmt.Sprintf("verdict-%d.json", round) }

// JudgeVerdict is what a judge stage writes: a decided grade, the reasoning a
// reader is shown, the model that produced it, and the per-criterion detail.
// It is validated before it is believed — a judge returning prose where a
// grade was asked for is a failed round, not an undecided session.
type JudgeVerdict struct {
	Grade       string           `json:"grade"`
	Explanation string           `json:"explanation"`
	Model       managed.ModelRef `json:"model"`
	Criteria    []JudgeCriterion `json:"criteria"`
}

// JudgeCriterion is one rubric criterion's outcome, with the evidence for it.
type JudgeCriterion struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"`
}

// grades maps the judge's vocabulary onto the contract's. The set is closed:
// a grade this build does not know is a failed round rather than a session
// that finishes undecided.
var grades = map[string]managed.Grade{
	"passed":         managed.GradePassed,
	"failed":         managed.GradeFailed,
	"needs_revision": managed.GradeNeedsRevision,
}

// Verdict converts the judge's file into the contract's verdict, appending the
// failing criteria to the explanation so a reader who sees only the verdict
// still learns which of them failed and why.
func (v JudgeVerdict) Verdict() (managed.Verdict, error) {
	grade, ok := grades[strings.ToLower(strings.TrimSpace(v.Grade))]
	if !ok {
		return managed.Verdict{}, fmt.Errorf("judge returned grade %q, which is not one of passed, failed, needs_revision", v.Grade)
	}
	if len(v.Criteria) == 0 {
		return managed.Verdict{}, fmt.Errorf("judge returned no per-criterion detail")
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(v.Explanation))
	var failed []JudgeCriterion
	for _, criterion := range v.Criteria {
		if !criterion.Passed {
			failed = append(failed, criterion)
		}
	}
	if len(failed) > 0 {
		b.WriteString("\n\nFailed criteria:")
		for _, criterion := range failed {
			fmt.Fprintf(&b, "\n  %d. %s — %s", criterion.ID, criterion.Name, strings.TrimSpace(criterion.Evidence))
		}
	}
	if v.Model.Valid() {
		fmt.Fprintf(&b, "\n\nGraded by %s/%s.", v.Model.Provider, v.Model.ID)
	}
	return managed.Verdict{Grade: grade, Explanation: strings.TrimSpace(b.String())}, nil
}

// ReadVerdict reads and validates one grading round's output.
func ReadVerdict(gradeDir string, round int) (JudgeVerdict, managed.Verdict, error) {
	body, err := os.ReadFile(filepath.Join(gradeDir, VerdictFile(round)))
	if os.IsNotExist(err) {
		return JudgeVerdict{}, managed.Verdict{}, fmt.Errorf("the judge wrote no %s", VerdictFile(round))
	}
	if err != nil {
		return JudgeVerdict{}, managed.Verdict{}, err
	}
	var file JudgeVerdict
	if err := json.Unmarshal(body, &file); err != nil {
		return JudgeVerdict{}, managed.Verdict{}, fmt.Errorf("the judge's %s is not valid JSON: %w", VerdictFile(round), err)
	}
	verdict, err := file.Verdict()
	return file, verdict, err
}

// judgeTask is the runtime's own instruction to the grader. It is
// system-authored — the rubric travels with the run, and what to do with it
// does not — and it names the two paths the sandbox has already made true:
// the evidence is read-only and the grade directory is the only writable
// place the judge has.
func judgeTask(rubric, evidence, gradePath string, round int, model managed.ModelRef) string {
	return fmt.Sprintf(`Grade the reproduction in %[2]s against the rubric below. It is mounted read-only: you are grading it, not fixing it.

Read the files. Judge each criterion independently, on the evidence present in the output files, and cite where you found it.

Then write %[3]s and nothing else. It must be exactly this JSON shape:

{
  "grade": "passed" | "failed" | "needs_revision",
  "explanation": "why, in a few sentences",
  "model": {"provider": %[5]q, "id": %[6]q},
  "criteria": [
    {"id": 1, "name": "short name", "passed": true, "evidence": "where you saw it"}
  ]
}

Every rubric criterion gets a row, in order, whether it passed or not. "passed" means every criterion passed. "needs_revision" means the work is close and a further attempt would fix it. "failed" means it would not.

--- Rubric ---

%[1]s`, rubric, evidence, filepath.Join(gradePath, VerdictFile(round)), round,
		model.Provider, model.ID)
}
