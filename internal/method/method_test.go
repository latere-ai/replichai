package method

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMethod(t *testing.T, root string, overrides map[string]string) string {
	t.Helper()
	files := map[string]string{
		"instructions.md":  "You audit papers. Write to {{OUTPUT_DIR}}.",
		"rubric.md":        "1. Verdict.",
		"task-template.md": "Audit {{PAPER}} and report.",
	}
	for name, text := range overrides {
		files[name] = text
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

func TestLoadExperiment(t *testing.T) {
	root := writeMethod(t, filepath.Join(t.TempDir(), "method"), nil)

	// Absent experiment.md → error.
	if _, err := LoadExperiment(root); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing experiment.md should be ErrInvalid, got %v", err)
	}

	// Present and non-empty → returned.
	if err := os.WriteFile(filepath.Join(root, "experiment.md"), []byte("Run the command exactly as given."), 0o644); err != nil {
		t.Fatalf("write experiment.md: %v", err)
	}
	got, err := LoadExperiment(root)
	if err != nil || got != "Run the command exactly as given." {
		t.Fatalf("LoadExperiment = %q, err %v", got, err)
	}

	// Present but empty → error.
	if err := os.WriteFile(filepath.Join(root, "experiment.md"), []byte("   "), 0o644); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := LoadExperiment(root); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty experiment.md should be ErrInvalid, got %v", err)
	}
}

func TestLoadReadsAllThreeDocuments(t *testing.T) {
	m, err := Load(writeMethod(t, filepath.Join(t.TempDir(), "method"), nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m.Rubric != "1. Verdict." {
		t.Errorf("rubric = %q", m.Rubric)
	}
	if !strings.Contains(m.TaskTemplate, PaperPlaceholder) {
		t.Errorf("task template lost its placeholder: %q", m.TaskTemplate)
	}
}

func TestTaskForSubstitutesThePaper(t *testing.T) {
	m, _ := Load(writeMethod(t, filepath.Join(t.TempDir(), "method"), nil))
	got, err := m.TaskFor("https://arxiv.org/abs/1")
	if err != nil {
		t.Fatalf("TaskFor: %v", err)
	}
	if got != "Audit https://arxiv.org/abs/1 and report." {
		t.Errorf("got %q", got)
	}
}

func TestTaskForRejectsAnEmptyReference(t *testing.T) {
	m, _ := Load(writeMethod(t, filepath.Join(t.TempDir(), "method"), nil))
	if _, err := m.TaskFor("   "); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestTaskForRequiresThePlaceholder(t *testing.T) {
	m := Method{TaskTemplate: "no placeholder here"}
	if _, err := m.TaskFor("paper"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestInstructionsForResolvesTheOutputDirectory(t *testing.T) {
	// The method must never hard-code one backend's filesystem layout.
	m, _ := Load(writeMethod(t, filepath.Join(t.TempDir(), "method"), nil))
	got := m.InstructionsFor("/somewhere/outputs/")
	if strings.Contains(got, OutputDirPlaceholder) {
		t.Errorf("placeholder left unsubstituted: %q", got)
	}
	if !strings.Contains(got, "/somewhere/outputs/") {
		t.Errorf("output dir missing from instructions: %q", got)
	}
}

func TestLoadRejectsMissingAndEmptyFiles(t *testing.T) {
	t.Run("missing directory", func(t *testing.T) {
		if _, err := Load(filepath.Join(t.TempDir(), "absent")); !errors.Is(err, ErrInvalid) {
			t.Fatalf("want ErrInvalid, got %v", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		root := writeMethod(t, filepath.Join(t.TempDir(), "method"), nil)
		os.Remove(filepath.Join(root, "rubric.md"))
		_, err := Load(root)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "rubric.md") {
			t.Fatalf("error should name the missing file, got %v", err)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		root := writeMethod(t, filepath.Join(t.TempDir(), "method"),
			map[string]string{"rubric.md": "   \n"})
		if _, err := Load(root); !errors.Is(err, ErrInvalid) {
			t.Fatalf("want ErrInvalid, got %v", err)
		}
	})
}

func TestDiscoverWalksUpwardFromTheWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	writeMethod(t, filepath.Join(root, "method"), nil)
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(nested)

	if _, err := Load(""); err != nil {
		t.Fatalf("Load should find method/ by walking up: %v", err)
	}
}

func TestDiscoverFailsCleanlyWhenNothingIsAbove(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := Load(""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
}

func TestDigestIsStableAndDistinguishing(t *testing.T) {
	m := Method{Instructions: "audit", Rubric: "1. verdict", TaskTemplate: "do {{PAPER}}"}
	d := m.Digest()
	if !strings.HasPrefix(d, "sha256:") {
		t.Errorf("digest %q lacks the sha256: prefix", d)
	}
	if d != m.Digest() {
		t.Error("digest is not stable across calls")
	}
	// A change to any document changes the digest.
	changed := m
	changed.Rubric = "2. verdict"
	if changed.Digest() == d {
		t.Error("changing the rubric must change the digest")
	}
	// The length-prefixed framing keeps a byte from crossing a boundary
	// unnoticed: moving text from one document to the next is a different digest.
	shifted := Method{Instructions: "auditrubric", Rubric: "", TaskTemplate: "do {{PAPER}}"}
	joined := Method{Instructions: "audit", Rubric: "rubric", TaskTemplate: "do {{PAPER}}"}
	if shifted.Digest() == joined.Digest() {
		t.Error("the boundary between documents must be part of the digest")
	}
}

func TestTheShippedMethodLoads(t *testing.T) {
	// The repository's own method/ directory must always be valid.
	t.Chdir("..")
	m, err := Load("")
	if err != nil {
		t.Fatalf("the shipped method must load: %v", err)
	}
	if !strings.Contains(strings.ToLower(m.Instructions), "inner") {
		t.Error("instructions should describe the inner/outer loop doctrine")
	}
	// The method is scoped by what makes the outer loop irreducible — human
	// participants — not by one field that happens to have them. Naming a
	// field here would tell the auditor a psychology or social-science paper
	// is out of scope, which the product promises it is not.
	lower := strings.ToLower(m.Instructions)
	for _, field := range []string{"human-computer interaction", "hci"} {
		if strings.Contains(lower, field) {
			t.Errorf("instructions scope themselves to %q; the method is not field-specific", field)
		}
	}
	if !strings.Contains(lower, "human participants") {
		t.Error("instructions should scope themselves by human participants")
	}
	task, err := m.TaskFor("X")
	if err != nil || !strings.Contains(task, "X") {
		t.Errorf("shipped task template did not render: %q, %v", task, err)
	}
}
