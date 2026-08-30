// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package batch

import (
	"os"
	"strings"
	"testing"
)

// The project file the guide shows is read out of the document rather than
// copied here. An example that stops parsing is a document that lies, and the
// only way to know is to run it.
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
	if len(entries) != 2 {
		t.Fatalf("%d papers, want 2", len(entries))
	}
	// The guide says the file's defaults apply to every paper and a paper
	// may override any of them. Both halves are in the example.
	if entries[0].Model != "best" || entries[0].Harness != "claude-code" {
		t.Errorf("the file's defaults did not reach the first paper: %+v", entries[0])
	}
	if entries[1].Model != "fast" || entries[1].Reference() != "10.1145/3654777.3676417" {
		t.Errorf("the per-paper override did not apply: %+v", entries[1])
	}
}

// yamlBlockAfter is the first fenced yaml block following a heading.
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
