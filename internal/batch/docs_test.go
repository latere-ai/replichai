// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package batch

import (
	"os"
	"strings"
	"testing"
)

// docGuide is the guide whose example project file the test reads, rather
// than a copy of it. An example that no longer parses makes the document
// wrong, and the only way to detect that is to parse it.
const docGuide = "../../docs/local-runs.md"

func TestTheDocumentedProjectFileWorks(t *testing.T) {
	document, err := os.ReadFile(docGuide)
	if err != nil {
		t.Fatalf("read the guide: %v", err)
	}
	yaml := yamlBlockAfter(t, string(document), "## A whole project at once")

	path := t.TempDir() + "/papers.yaml"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	file, err := ReadFile(path)
	if err != nil {
		t.Fatalf("the documented project does not parse: %v", err)
	}
	if file.Project != "chi-2026-audit" || file.Publish.Owner != "open-hai" {
		t.Fatalf("project = %+v", file)
	}
	entries, err := file.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	// The second paper runs three times, so two papers are four runs.
	if len(entries) != 4 {
		t.Fatalf("%d runs, want 4: one of the first paper and three of the second", len(entries))
	}
	// The guide says the file's defaults apply to every paper and a paper
	// may override any of them. Both halves are in the example.
	if entries[0].Model != "best" || entries[0].Harness != "claude-code" || entries[0].Runs != 1 || entries[0].Network != "artifacts" {
		t.Errorf("the file's defaults did not reach the first paper: %+v", entries[0])
	}
	for run, entry := range entries[1:] {
		if entry.Model != "fast" || entry.Reference() != "10.1145/3654777.3676417" || entry.File != "papers/uist-2024.pdf" {
			t.Errorf("the per-paper override did not apply: %+v", entry)
		}
		if entry.Run != run+1 || entry.Runs != 3 {
			t.Errorf("the second paper's run %d is numbered %d of %d, want %d of 3", run+1, entry.Run, entry.Runs, run+1)
		}
	}
}

// yamlBlockAfter returns the first fenced yaml block following heading.
func yamlBlockAfter(t *testing.T, document, heading string) string {
	t.Helper()
	index := strings.Index(document, heading)
	if index < 0 {
		t.Fatalf("the guide has no %q section", heading)
	}
	rest := document[index:]
	open := strings.Index(rest, "```yaml\n")
	if open < 0 {
		t.Fatalf("no yaml example under %q", heading)
	}
	rest = rest[open+len("```yaml\n"):]
	end := strings.Index(rest, "```")
	if end < 0 {
		t.Fatalf("an unterminated yaml example under %q", heading)
	}
	return rest[:end]
}
