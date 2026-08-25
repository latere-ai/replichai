package harness

import (
	"strings"
	"testing"

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
func (s stub) Decode(Stage, int, []byte) ([]managed.Event, error)      { return nil, nil }

// An id is derived from position, not content: two identical tool results are
// two events, and a content hash would merge them into one.
func TestEventIDIsPositional(t *testing.T) {
	if a, b := EventID(StageWorker, 3), EventID(StageWorker, 3); a != b {
		t.Fatalf("id is not stable across reads: %q, %q", a, b)
	}
	if a, b := EventID(StageWorker, 3), EventID(StageJudge, 3); a == b {
		t.Fatal("two stages share an id at the same index")
	}
	if a, b := EventID(StageWorker, 3), EventID(StageWorker, 4); a == b {
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

// Instructions precede the task in the combined form. The contract permits
// combining them and forbids reordering them.
func TestPrependKeepsTheOrder(t *testing.T) {
	combined := Prepend("ROLE", "TASK")
	if strings.Index(combined, "ROLE") > strings.Index(combined, "TASK") {
		t.Fatalf("task came first:\n%s", combined)
	}
	if !strings.Contains(combined, InstructionHeader) {
		t.Fatal("the combined form is not marked as one")
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
