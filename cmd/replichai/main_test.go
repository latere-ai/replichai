package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/provider"
)

// capture runs the CLI with stdout and stderr redirected to temp files, and
// returns what each received. The command writes to *os.File, so real files
// are simpler here than plumbing an io.Writer through.
func capture(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	outFile, errFile := newTemp(t), newTemp(t)
	err = run(context.Background(), args, outFile, errFile)
	return readAll(t, outFile), readAll(t, errFile), err
}

func newTemp(t *testing.T) *os.File {
	t.Helper()
	f, createErr := os.CreateTemp(t.TempDir(), "cli")
	if createErr != nil {
		t.Fatalf("temp file: %v", createErr)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func readAll(t *testing.T, f *os.File) string {
	t.Helper()
	body, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read %s: %v", f.Name(), err)
	}
	return string(body)
}

// writeMethod creates a minimal valid method directory.
func writeMethod(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "method")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	files := map[string]string{
		"instructions.md":  "audit papers",
		"rubric.md":        "1. verdict",
		"task-template.md": "audit {{PAPER}}",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestProvidersCommandListsProviders(t *testing.T) {
	stdout, _, err := capture(t, "providers")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// The concrete provider names itself; assert against that name rather than
	// hardcoding it, so this test stays free of the vendor term.
	for _, want := range []string{provider.DefaultName(), "memory", "(default)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestAuditReportsTheVerdict(t *testing.T) {
	stdout, _, err := capture(t, "audit",
		"https://arxiv.org/abs/1",
		"--provider", "memory",
		"--method", writeMethod(t),
		"--out", t.TempDir(),
		"--interval", "0",
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"verdict:", "passed", "artifacts:", "elapsed:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestAuditReportsABadMethodDirectory(t *testing.T) {
	_, _, err := capture(t, "audit", "paper",
		"--provider", "memory", "--method", filepath.Join(t.TempDir(), "absent"))
	if err == nil {
		t.Fatal("expected an error for a missing method directory")
	}
}

func TestAuditReportsAnUnknownProvider(t *testing.T) {
	_, _, err := capture(t, "audit", "paper", "--provider", "nope",
		"--method", writeMethod(t))
	if err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("want an unknown-provider error, got %v", err)
	}
}

func TestAuditRequiresExactlyOnePaper(t *testing.T) {
	for _, args := range [][]string{
		{"audit"},
		{"audit", "one", "two"},
	} {
		_, stderr, err := capture(t, args...)
		if !errors.Is(err, errExitQuiet) {
			t.Errorf("%v: want errExitQuiet, got %v", args, err)
		}
		if !strings.Contains(stderr, "exactly one paper") {
			t.Errorf("%v: stderr should explain the problem, got %q", args, stderr)
		}
	}
}

func TestUnknownCommandAndHelp(t *testing.T) {
	t.Run("no arguments", func(t *testing.T) {
		_, stderr, err := capture(t)
		if !errors.Is(err, errExitQuiet) {
			t.Errorf("want errExitQuiet, got %v", err)
		}
		if !strings.Contains(stderr, "usage:") {
			t.Errorf("stderr should show usage, got %q", stderr)
		}
	})

	t.Run("unknown command", func(t *testing.T) {
		_, stderr, err := capture(t, "frobnicate")
		if !errors.Is(err, errExitQuiet) {
			t.Errorf("want errExitQuiet, got %v", err)
		}
		if !strings.Contains(stderr, "unknown command") {
			t.Errorf("stderr should name the problem, got %q", stderr)
		}
	})

	t.Run("help exits cleanly", func(t *testing.T) {
		stdout, _, err := capture(t, "--help")
		if err != nil {
			t.Errorf("help should not error, got %v", err)
		}
		if !strings.Contains(stdout, "usage:") {
			t.Errorf("help should print usage, got %q", stdout)
		}
	})
}

func TestResolveReportsAnIdentity(t *testing.T) {
	// A DOI reference resolves offline — no fetch — so this exercises the
	// command's wiring and output without touching the network.
	stdout, _, err := capture(t, "resolve", "10.1145/3772318.3791265")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{
		"identity:  doi:10.1145/3772318.3791265",
		"scheme:    doi",
		"slug:      10-1145-3772318-3791265",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestResolveRequiresExactlyOneReference(t *testing.T) {
	for _, args := range [][]string{{"resolve"}, {"resolve", "a", "b"}} {
		_, stderr, err := capture(t, args...)
		if !errors.Is(err, errExitQuiet) {
			t.Errorf("%v: want errExitQuiet, got %v", args, err)
		}
		if !strings.Contains(stderr, "exactly one paper reference") {
			t.Errorf("%v: stderr should explain the problem, got %q", args, stderr)
		}
	}
}

func TestResolveReportsAnUnresolvableReference(t *testing.T) {
	_, _, err := capture(t, "resolve", "not-a-paper")
	if err == nil {
		t.Fatal("expected an error for a reference with no identifier")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"https://arxiv.org/abs/2603.01795": "https-arxiv-org-abs-2603-01795",
		"  Mixed CASE  ":                   "mixed-case",
		"///":                              "paper",
		"":                                 "paper",
	}
	for input, want := range cases {
		if got := slugify(input); got != want {
			t.Errorf("slugify(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSlugifyIsBounded(t *testing.T) {
	got := slugify(strings.Repeat("x", 500))
	if len(got) > 80 {
		t.Errorf("slug length %d exceeds the 80-character bound", len(got))
	}
	if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
		t.Errorf("slug %q has a leading or trailing hyphen", got)
	}
}

func TestSlugifyNeverEscapesADirectory(t *testing.T) {
	// The slug names a directory under --out; it must not contain separators
	// or traversal sequences.
	for _, hostile := range []string{"../../etc/passwd", "a/b/c", "..", "./x"} {
		got := slugify(hostile)
		if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") {
			t.Errorf("slugify(%q) = %q, which is not a safe directory name", hostile, got)
		}
	}
}

func TestFlagsAreAcceptedAfterThePaperReference(t *testing.T) {
	// Regression: flag stops parsing at the first non-flag argument, so a
	// plain fs.Parse silently drops every flag when the paper comes first --
	// which is how anyone would naturally type it.
	out := t.TempDir()
	stdout, _, err := capture(t, "audit",
		"https://arxiv.org/abs/1",
		"--provider", "memory",
		"--method", writeMethod(t),
		"--out", out,
		"--interval", "0",
	)
	if err != nil {
		t.Fatalf("flags after the paper should be honored: %v", err)
	}
	if !strings.Contains(stdout, out) {
		t.Errorf("--out was not applied; output was:\n%s", stdout)
	}
}
