// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package batch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
)

const stateVersion = 1

// State is a project's progress, rewritten after every change so a batch
// killed at any point leaves a file that is both valid and current.
type State struct {
	Version int    `json:"version"`
	Project string `json:"project,omitempty"`
	// Publishing says the batch was asked to publish. Without it an
	// ordinary local run would be reported as having failed to publish
	// something it was never asked to.
	Publishing bool         `json:"publishing,omitempty"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt time.Time    `json:"finished_at,omitzero"`
	Entries    []EntryState `json:"entries"`
}

// Save writes the state atomically: a batch killed mid-write must not leave a
// half-written file where its own resume path reads.
func (s State) Save(path string) error {
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(body, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// LoadState reads a project's progress back.
func LoadState(path string) (State, error) {
	//nolint:gosec // the path is this batch's own state file
	body, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(body, &state); err != nil {
		return State{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return state, nil
}

// mergedWith carries a previous run's outcomes onto the current entry list,
// matching by paper reference so an edited project file resumes rather than
// starting over.
//
// Only a finished paper is skipped. A failed one is retried, because retrying
// what failed is the reason to resume at all — but it keeps its repository and
// its session reference, so the retry publishes into the repository the first
// attempt created rather than allocating a second one for the same paper.
func (s State) mergedWith(entries []Resolved, now time.Time) State {
	merged := State{Version: stateVersion, Project: s.Project, StartedAt: now, Entries: make([]EntryState, len(entries))}
	for index, entry := range entries {
		merged.Entries[index] = EntryState{Paper: entry.Reference(), Phase: Pending}
		for _, previous := range s.Entries {
			if previous.Paper != entry.Reference() {
				continue
			}
			if previous.Phase == Done {
				merged.Entries[index] = previous
				break
			}
			// Failed, running or pending: keep what is needed to
			// reattach and drop the phase, so the paper is tried again
			// against the repository it already has.
			merged.Entries[index].PaperKey = previous.PaperKey
			merged.Entries[index].Session = previous.Session
			merged.Entries[index].Directory = previous.Directory
			merged.Entries[index].Repo = previous.Repo
			merged.Entries[index].Ref = previous.Ref
			break
		}
	}
	return merged
}

func (s State) clone() State {
	out := s
	out.Entries = slices.Clone(s.Entries)
	return out
}

// Totals is the summary a person actually asks for at the end.
type Totals struct {
	Papers   int
	Done     int
	Failed   int
	Pending  int
	Passed   int
	Empty    int
	Usage    managed.Usage
	Wall     time.Duration
	VendorUS int64
}

// Summarise folds the entries. The totals count what happened rather than what
// succeeded, because a report that shows only successes reads as completeness.
func (s State) Summarise() Totals {
	totals := Totals{Papers: len(s.Entries)}
	for _, entry := range s.Entries {
		switch entry.Phase {
		case Done:
			totals.Done++
		case Failed:
			totals.Failed++
		default:
			totals.Pending++
		}
		// Grade.String(), not a conversion: Grade is a uint8, and
		// string(GradePassed) is the rune at that code point rather than
		// the word "passed" — which counted every batch as zero passes.
		if entry.Grade == managed.GradePassed.String() {
			totals.Passed++
		}
		if s.Publishing && entry.Phase == Done && entry.Repo == "" {
			totals.Empty++
		}
		totals.Usage.Add(entry.Usage)
		totals.Wall += entry.Wall()
		totals.VendorUS += entry.Usage.Details["vendor_cost_micro_usd"]
	}
	return totals
}

// Report renders what the batch did, in the form a person reads. It names what
// it could not do as prominently as what it did.
func (s State) Report() string {
	var b strings.Builder
	totals := s.Summarise()
	if s.Project != "" {
		fmt.Fprintf(&b, "# %s\n\n", s.Project)
	} else {
		b.WriteString("# Replication batch\n\n")
	}
	fmt.Fprintf(&b, "%d papers: %d done, %d failed, %d not run. %d passed the rubric.\n\n",
		totals.Papers, totals.Done, totals.Failed, totals.Pending, totals.Passed)

	b.WriteString("| paper | state | verdict | repository | model | tokens | wall |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, entry := range s.Entries {
		model := entry.Model.ID
		if model == "" {
			model = "—"
		}
		repo := entry.Repo
		if repo == "" {
			repo = "—"
		}
		grade := entry.Grade
		if grade == "" {
			grade = "—"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %d | %s |\n",
			entry.Paper, entry.Phase, grade, repo, model, entry.Usage.TotalTokens(), round(entry.Wall()))
	}

	fmt.Fprintf(&b, "\nTotal: %d tokens across %d papers, %s of wall clock.\n",
		totals.Usage.TotalTokens(), totals.Papers, round(totals.Wall))
	if totals.VendorUS > 0 {
		fmt.Fprintf(&b, "The harness reported $%.2f of vendor cost. Nothing here was billed by replichai.\n",
			float64(totals.VendorUS)/1e6)
	}

	// What did not happen is stated as plainly as what did. A summary that
	// lists only successes reads as completeness.
	if totals.Failed > 0 || totals.Pending > 0 || totals.Empty > 0 {
		b.WriteString("\n## Not done\n\n")
		for _, entry := range s.Entries {
			switch {
			case entry.Phase == Failed:
				fmt.Fprintf(&b, "- %s failed: %s\n", entry.Paper, oneLine(entry.Error))
			case !entry.Phase.Terminal():
				fmt.Fprintf(&b, "- %s did not run.\n", entry.Paper)
			case s.Publishing && entry.Repo == "":
				fmt.Fprintf(&b, "- %s finished without publishing a repository.\n", entry.Paper)
			}
		}
	}
	return b.String()
}

func round(d time.Duration) string {
	if d == 0 {
		return "—"
	}
	return d.Round(time.Second).String()
}

func oneLine(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\n", " "))
	if len(value) > 200 {
		return value[:200] + "…"
	}
	return value
}
