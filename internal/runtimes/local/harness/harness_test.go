// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package harness

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/latere-ai/replichai/internal/managed"
)

type stub struct {
	name     Name
	dialects []Dialect
}

func (s stub) Name() Name            { return s.name }
func (s stub) Binary() string        { return string(s.name) }
func (s stub) Dialects() []Dialect   { return s.dialects }
func (s stub) VersionArgv() []string { return []string{string(s.name), "--version"} }
func (s stub) Argv(Stage, Request) ([]string, error) {
	return []string{string(s.name)}, nil
}
func (s stub) Env(Stage, Request) (map[string]string, []string, error) { return nil, nil, nil }
func (s stub) Files(Stage, Request) ([]File, error)                    { return nil, nil }
func (s stub) Decode(string, int, []byte) ([]managed.Event, error)     { return nil, nil }
func (s stub) Needs(Machine) Needs                                     { return Needs{} }

// An id is derived from position, not content: two identical tool results are
// two events, and a content hash would merge them into one.
func TestEventIDIsPositional(t *testing.T) {
	if a, b := EventID("worker-0", 3), EventID("worker-0", 3); a != b {
		t.Fatalf("id is not stable across reads: %q, %q", a, b)
	}
	if a, b := EventID("judge-1", 3), EventID("judge-3", 3); a == b {
		t.Fatal("two stages of the same role share an id at the same index")
	}
	if a, b := EventID("worker-0", 3), EventID("worker-0", 4); a == b {
		t.Fatal("two positions share an id")
	}
}

// Truncation marks what it removed and says where the whole value still is.
func TestTruncateMarksAndPointsAtTheRawLog(t *testing.T) {
	if got := Truncate("short"); got != "short" {
		t.Fatalf("short content was altered: %q", got)
	}
	long := strings.Repeat("x", MaxContentBytes+100)
	got := Truncate(long)
	if len(got) <= MaxContentBytes || !strings.Contains(got, "elided") || !strings.Contains(got, "raw log") {
		t.Fatalf("truncation lost its marker: %q", got[len(got)-120:])
	}
}

// A cut that lands inside a multi-byte rune must move back to the rune
// boundary: the truncated content is persisted and re-sent to a model.
func TestTruncateNeverSplitsARune(t *testing.T) {
	long := strings.Repeat("語", MaxContentBytes/3+1) // 3 bytes each, so the cap falls mid-rune
	got := Truncate(long)
	if !utf8.ValidString(got) {
		t.Fatal("truncation produced invalid UTF-8")
	}
	if !strings.HasPrefix(got, strings.Repeat("語", MaxContentBytes/3)) {
		t.Fatal("truncation dropped more than the partial rune")
	}
}

func TestRegistryRefusesDuplicatesAndListsChoices(t *testing.T) {
	if _, err := NewRegistry(stub{name: "a"}, stub{name: "a"}); err == nil {
		t.Fatal("a duplicate harness name was accepted")
	}
	if _, err := NewRegistry(stub{name: "Claude-Code"}); err == nil {
		t.Fatal("a non-canonical harness name was accepted")
	}
	registry, err := NewRegistry(stub{name: "codex"}, stub{name: "claude-code"})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if names := registry.Names(); len(names) != 2 || names[0] != "claude-code" {
		t.Fatalf("Names = %v, want sorted", names)
	}
	if _, err := registry.Lookup("  CLAUDE-CODE "); err != nil {
		t.Fatalf("canonicalisation failed: %v", err)
	}
	_, err = registry.Lookup("nope")
	if err == nil || !strings.Contains(err.Error(), "claude-code, codex") {
		t.Fatalf("an unknown harness did not list the installed ones: %v", err)
	}
}

func TestSpeaksGatesTheDialectPairing(t *testing.T) {
	claude := stub{name: "claude-code", dialects: []Dialect{DialectAnthropic}}
	if !Speaks(claude, DialectAnthropic) || Speaks(claude, DialectOpenAIChat) {
		t.Fatal("dialect pairing is not enforced")
	}
}

// Only the judge is barred from writing the workspace, and that is the whole
// mechanism behind a grader that cannot edit what it grades.
func TestOnlyTheJudgeIsReadOnly(t *testing.T) {
	if !StageWorker.Writes() || !StageRevision.Writes() || StageJudge.Writes() {
		t.Fatal("the writable stages are wrong")
	}
}

func TestOutsideTheWorkspace(t *testing.T) {
	for _, c := range []struct {
		destination string
		want        bool
	}{
		{"/s/workspace/CLAUDE.md", false},
		{"/s/workspace", false},
		{"/s/workspace/nested/AGENTS.md", false},
		{"/s/home/.claude/settings.json", true},
		{"/s/workspace-other/file", true},
	} {
		if got := Outside("/s/workspace", c.destination); got != c.want {
			t.Errorf("Outside(%q) = %v", c.destination, got)
		}
	}
}

// Folding is an elementwise maximum, so a harness that repeats its running
// total cannot double-count and two consecutive observations never decrease.
func TestFoldIsMonotonicAndDoesNotDoubleCount(t *testing.T) {
	model := managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5"}
	total := managed.Usage{}
	for range 3 {
		total = Fold(total, managed.Usage{Model: model, InputFresh: 100, Output: 20,
			Extra:   map[managed.Meter]int64{"stage": 2},
			Details: map[string]int64{"vendor_cost_micro_usd": 5}})
	}
	if total.InputFresh != 100 || total.Output != 20 || total.Extra["stage"] != 2 || total.Details["vendor_cost_micro_usd"] != 5 {
		t.Fatalf("a repeated total was counted more than once: %+v", total)
	}
	grown := Fold(total, managed.Usage{Model: model, InputFresh: 300, Output: 90})
	if !grown.MonotonicFrom(total) || grown.InputFresh != 300 {
		t.Fatalf("growth = %+v", grown)
	}
	// A late report that is smaller does not walk the total backwards.
	shrunk := Fold(grown, managed.Usage{Model: model, InputFresh: 1, Output: 1})
	if !shrunk.MonotonicFrom(grown) {
		t.Fatalf("a smaller report reduced the total: %+v", shrunk)
	}
}
