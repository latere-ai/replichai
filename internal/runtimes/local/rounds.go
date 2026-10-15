// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package local

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/verdict"
)

// roundsDir holds what the host kept of each grading round: the findings file
// as the round left it, and what the system found when it checked that file.
// It is under session/, which no stage may write. The judge may write grade/,
// so a copy kept there could be edited by the grader it was taken for.
const roundsDir = "session/rounds"

// FindingsFile is the name of the copy of a run's findings that round keeps.
// Round 0 is what the worker produced before any revision.
func FindingsFile(round int) string { return fmt.Sprintf("findings-%d.json", round) }

// ChecksFile is the name of the record of what the system found when it
// checked that round's findings.
func ChecksFile(round int) string { return fmt.Sprintf("checks-%d.json", round) }

// Checks is what the system found when it read one round's findings file
// against its schema, before the grader read it. SHA256 identifies the copy
// kept beside it and is empty when the run wrote no file. An empty Problems
// list means the file is readable; whether its content is right is still the
// grader's question.
type Checks struct {
	Round    int      `json:"round"`
	File     string   `json:"file"`
	Present  bool     `json:"present"`
	SHA256   string   `json:"sha256,omitempty"`
	Problems []string `json:"problems,omitempty"`
}

// Rounds is where the session keeps each round's findings and checks.
func (s *Session) Rounds() string { return filepath.Join(s.dir, roundsDir) }

// keepRound copies the findings file the worker-side stage of round left in
// the workspace, checks it, and records both under session/rounds. A missing
// file is a problem rather than a skipped round: a run that wrote no findings
// has nothing the system can read, and that must reach the grader as surely as
// a malformed one.
func (s *Session) keepRound(round int, name string) (Checks, error) {
	checks := Checks{Round: round, File: name}
	source, err := containedPath(s.Workspace(), name)
	if err != nil {
		return Checks{}, err
	}
	body, err := os.ReadFile(source) //nolint:gosec // a path contained in this session's own workspace
	switch {
	case errors.Is(err, fs.ErrNotExist):
		checks.Problems = []string{"the run did not write " + name}
	case err != nil:
		return Checks{}, fmt.Errorf("read %s: %w", name, err)
	default:
		checks.Present = true
		sum := sha256.Sum256(body)
		checks.SHA256 = hex.EncodeToString(sum[:])
		if err := os.MkdirAll(s.Rounds(), 0o700); err != nil {
			return Checks{}, fmt.Errorf("create %s: %w", roundsDir, err)
		}
		if err := writeFileMode(filepath.Join(s.Rounds(), FindingsFile(round)), body, 0o600); err != nil {
			return Checks{}, err
		}
		checks.Problems = findingsProblems(body)
	}
	if err := s.writeJSON(filepath.Join(roundsDir, ChecksFile(round)), checks); err != nil {
		return Checks{}, err
	}
	return checks, nil
}

// readChecks reads what keepRound recorded for round. A round with no record
// is one graded before this check existed, or one whose evaluation named no
// findings file, and reads as a round with nothing to report.
func (s *Session) readChecks(round int) (Checks, error) {
	var checks Checks
	err := s.readJSON(filepath.Join(roundsDir, ChecksFile(round)), &checks)
	if errors.Is(err, managed.ErrNotFound) {
		return Checks{}, nil
	}
	return checks, err
}

// findingsProblems is verdict.Validate's answer in the words a grader and a
// revision are given: one line per problem, naming the field or row.
func findingsProblems(body []byte) []string {
	_, problems, err := verdict.Validate(body)
	if err != nil {
		return []string{err.Error()}
	}
	out := make([]string, 0, len(problems))
	for _, problem := range problems {
		out = append(out, problem.String())
	}
	return out
}

// problemList renders problems as the list a grader or a revision reads.
func problemList(problems []string) string {
	var b strings.Builder
	for _, problem := range problems {
		b.WriteString("\n  - ")
		b.WriteString(problem)
	}
	return b.String()
}

// checksSection is what the judge is told about the system's own check of the
// round's findings. A file the system cannot read fails the rubric criterion
// that asks for readable findings, whatever else the run did, and the round
// cannot pass while any problem remains; the judge is told so, and the runtime
// holds it to that whatever grade the judge writes.
func checksSection(checks Checks) string {
	if checks.File == "" {
		return ""
	}
	if len(checks.Problems) == 0 {
		return fmt.Sprintf("\n\n--- What the system checked ---\n\nBefore you started, the system read %s against its schema and found no problem: every field and row decoded and no rule is broken. Whether what it says is right is still yours to judge.", checks.File)
	}
	return fmt.Sprintf("\n\n--- What the system checked ---\n\nBefore you started, the system read %s against its schema and found the problems below. A findings file the system cannot read fails the criterion that asks for readable findings, so this round cannot pass while any of them remains:%s", checks.File, problemList(checks.Problems))
}

// heldBack is the verdict a round gets when its judge passed it but the system
// could not read its findings. The judge's own reasoning is kept, and the
// problems are added, so whoever reads the verdict learns why a pass became a
// rejection.
func heldBack(judged string, checks Checks) string {
	return strings.TrimSpace(judged) + fmt.Sprintf("\n\nThe system read %s against its schema and found problems the grader passed over, so this round does not pass:%s", checks.File, problemList(checks.Problems))
}

// revisionProblems is what a revision is told about the findings file, beside
// the grader's reasoning: the same problems the judge was given, so the
// revision fixes what the system found and not only what the grader noticed.
func revisionProblems(checks Checks) string {
	if len(checks.Problems) == 0 {
		return ""
	}
	return fmt.Sprintf("\n\n--- The system could not read %s. Fix every problem it found. ---\n%s", checks.File, problemList(checks.Problems))
}
