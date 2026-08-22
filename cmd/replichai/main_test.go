package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/github"
	"github.com/latere-ai/replichai/internal/instrument"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/paper"
)

func TestHTTPFetcherRetriesTransientFailures(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 2 {
			http.Error(w, "slow", http.StatusServiceUnavailable) // transient
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(server.Close)

	body, err := httpFetcher{timeout: 5 * time.Second}.Fetch(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != "ok" || calls != 2 {
		t.Errorf("expected a retry to succeed: body=%q calls=%d", body, calls)
	}
}

func TestReadRecordedIdentity(t *testing.T) {
	want := paper.Identity{Key: "10.1145/x", Scheme: paper.SchemeDOI, Title: "A Paper", Slug: "a-paper"}
	raw, _ := json.Marshal(want)

	present := github.New("t", github.WithBaseURL(httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"content": base64.StdEncoding.EncodeToString(raw), "encoding": "base64",
			})
		})).URL))
	got, err := readRecordedIdentity(context.Background(), present, "open-hai", "x")
	if err != nil {
		t.Fatalf("readRecordedIdentity: %v", err)
	}
	if got.Key != want.Key || got.Scheme != want.Scheme || got.Slug != want.Slug {
		t.Errorf("read identity = %+v, want %+v", got, want)
	}

	// Absent (no recorded file) → zero identity, so the caller re-resolves.
	absent := github.New("t", github.WithBaseURL(httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })).URL))
	if got, err := readRecordedIdentity(context.Background(), absent, "open-hai", "x"); err != nil || got.Key != "" {
		t.Errorf("absent identity should be zero: got=%+v err=%v", got, err)
	}
}

func TestHTTPFetcherDoesNotRetryNotFound(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		http.Error(w, "nope", http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	if _, err := (httpFetcher{timeout: 5 * time.Second}).Fetch(context.Background(), server.URL); err == nil {
		t.Error("a 404 should error")
	}
	if calls != 1 {
		t.Errorf("a 404 is not retryable, want 1 call, got %d", calls)
	}
}

func TestReportInstrument(t *testing.T) {
	cases := []struct {
		name       string
		present    bool
		violations []instrument.Violation
		want       string
	}{
		{"absent", false, nil, "instrument: none written"},
		{"valid", true, nil, "instrument: valid"},
		{"invalid", true, []instrument.Violation{{Field: "protocol", Message: "is null without a reason"}},
			"instrument: 1 schema violation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTemp(t)
			reportInstrument(f, tc.present, tc.violations)
			if got := readAll(t, f); !strings.Contains(got, tc.want) {
				t.Errorf("reportInstrument output = %q, want to contain %q", got, tc.want)
			}
		})
	}
}

func TestVerifyInstrument(t *testing.T) {
	serve := func(status int, body []byte) *github.Client {
		return github.New("t", github.WithBaseURL(httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) {
				if status != http.StatusOK {
					w.WriteHeader(status)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]string{
					"content": base64.StdEncoding.EncodeToString(body), "encoding": "base64",
				})
			})).URL))
	}

	// A committed, well-formed instrument validates.
	present, vs, err := verifyInstrument(context.Background(),
		serve(http.StatusOK, []byte(validInstrumentJSON)), "open-hai", "x")
	if err != nil || !present || len(vs) != 0 {
		t.Fatalf("valid instrument: present=%v vs=%v err=%v", present, vs, err)
	}

	// A malformed one reports a violation without erroring.
	present, vs, err = verifyInstrument(context.Background(),
		serve(http.StatusOK, []byte(`{"protocol":null,"analysis":null,"servability":{"components":[],"all_servable":true}}`)),
		"open-hai", "x")
	if err != nil || !present || len(vs) == 0 {
		t.Fatalf("malformed instrument: present=%v vs=%v err=%v", present, vs, err)
	}

	// Absent → not present, no error, so the caller reports it and moves on.
	present, _, err = verifyInstrument(context.Background(),
		serve(http.StatusNotFound, nil), "open-hai", "x")
	if err != nil || present {
		t.Fatalf("absent instrument: present=%v err=%v", present, err)
	}
}

// A well-formed instrument.json for the cmd-level tests above.
const validInstrumentJSON = `{
  "protocol": {"design":"2x2","trials":"120","measures":[{"name":"rt"}],"plan":["LMM"],
    "reported":[{"metric":"RT","value":"p=.003","citation":"Table 2"}]},
  "analysis": {"command":"python src/analyze.py {{INPUT}}",
    "input":{"columns":[{"name":"rt","type":"number","required":true}]},"outputs":["out.csv"]},
  "servability": {"components":[{"component":"task","servable":true}],"all_servable":true}
}`

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
	for _, want := range []string{"anthropic-managed", "memory", "(default)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestAuditReportsTheVerdict(t *testing.T) {
	stdout, _, err := capture(t, "audit",
		"https://arxiv.org/abs/1",
		"--runtime", "memory",
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
		"--runtime", "memory", "--method", filepath.Join(t.TempDir(), "absent"))
	if err == nil {
		t.Fatal("expected an error for a missing method directory")
	}
}

func TestAuditReportsAnUnknownRuntime(t *testing.T) {
	_, _, err := capture(t, "audit", "paper", "--runtime", "nope",
		"--method", writeMethod(t))
	if err == nil || !strings.Contains(err.Error(), "is unavailable") {
		t.Fatalf("want an unknown-runtime error, got %v", err)
	}
}

// The runtime is named by one flag. A --provider that still parsed would keep
// two names for one choice alive in every script that copies this invocation.
func TestTheDeprecatedProviderFlagIsGone(t *testing.T) {
	for _, command := range [][]string{
		{"audit", "paper", "--provider", "memory", "--method", writeMethod(t)},
		{"rates", "--provider", "memory"},
	} {
		if _, _, err := capture(t, command...); err == nil {
			t.Errorf("%v: --provider was accepted", command)
		}
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
	// A DOI's identity (key, scheme) is derived offline; its title and slug are
	// enriched from Crossref when the network is reachable and degrade to the
	// key-based slug when it is not. So this asserts only the network-independent
	// identity — the command's wiring and stable output.
	stdout, _, err := capture(t, "resolve", "10.1145/3772318.3791265")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{
		"identity:  doi:10.1145/3772318.3791265",
		"scheme:    doi",
		"key:       10.1145/3772318.3791265",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestPublishPreviewThenLive(t *testing.T) {
	src := t.TempDir()
	files := map[string]string{
		"README.md":          "# reproduction",
		"REPRODUCIBILITY.md": "verdict: passed",
		"src/main.py":        "print(1)",
		"data/benchmark.csv": "gated,rows", // third-party
		"paper.pdf":          "PDF",        // third-party
	}
	for rel, content := range files {
		full := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	forgeRoot := t.TempDir()
	const doi = "10.1145/3025453.3025912" // resolves offline, no network

	// Preview creates nothing.
	stdout, _, err := capture(t, "publish", src, doi, "--out", forgeRoot)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	for _, want := range []string{"included:    3 files", "quarantined: 2 files", "preview only"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("preview missing %q:\n%s", want, stdout)
		}
	}
	if entries, _ := os.ReadDir(forgeRoot); len(entries) != 0 {
		t.Errorf("preview wrote %d entries", len(entries))
	}

	// Live writes exactly one repository with the worker files, never the
	// third-party ones.
	stdout, _, err = capture(t, "publish", src, doi, "--out", forgeRoot, "--live")
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	if !strings.Contains(stdout, "written:") {
		t.Errorf("live output missing 'written:'\n%s", stdout)
	}
	entries, _ := os.ReadDir(forgeRoot)
	if len(entries) != 1 {
		t.Fatalf("want one repository directory, got %d", len(entries))
	}
	repo := filepath.Join(forgeRoot, entries[0].Name())
	for _, f := range []string{"replication.json", "README.md", "REPRODUCIBILITY.md", "src/main.py", "EXCLUDED.md"} {
		if _, err := os.Stat(filepath.Join(repo, filepath.FromSlash(f))); err != nil {
			t.Errorf("expected %s in the published repository", f)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "data", "benchmark.csv")); err == nil {
		t.Error("a third-party file was published")
	}
}

func TestAuditPublishNeedsAToken(t *testing.T) {
	// --publish is guarded before any run: without a GitHub token it fails
	// early, so this exercises the wiring entirely offline.
	t.Setenv("REPLICHAI_GITHUB_TOKEN", "")
	_, _, err := capture(t, "audit", "10.1145/3025453.3025912",
		"--runtime", "memory", "--method", writeMethod(t), "--publish")
	if err == nil || !strings.Contains(err.Error(), "REPLICHAI_GITHUB_TOKEN") {
		t.Fatalf("want a missing-token error, got %v", err)
	}
}

func TestAuditDetachNeedsPublish(t *testing.T) {
	_, stderr, err := capture(t, "audit", "paper",
		"--runtime", "memory", "--method", writeMethod(t), "--detach")
	if !errors.Is(err, errExitQuiet) {
		t.Errorf("want errExitQuiet, got %v", err)
	}
	if !strings.Contains(stderr, "needs --publish") {
		t.Errorf("stderr should explain the problem, got %q", stderr)
	}
}

func TestFinishNeedsRepoAndPaper(t *testing.T) {
	_, stderr, err := capture(t, "finish", "only-one")
	if !errors.Is(err, errExitQuiet) {
		t.Errorf("want errExitQuiet, got %v", err)
	}
	if !strings.Contains(stderr, "owner>/<repo") {
		t.Errorf("stderr should explain the usage, got %q", stderr)
	}
}

func TestFinishRejectsAnInvalidVerdictBeforePublishing(t *testing.T) {
	t.Setenv("REPLICHAI_GITHUB_TOKEN", "")
	_, _, err := capture(t, "finish", "--verdict", "definitely-not-a-grade", "owner/repo", "doi:10.1234/a")
	if err == nil || !strings.Contains(err.Error(), "--verdict") {
		t.Fatalf("finish error = %v, want invalid --verdict", err)
	}
}

func TestPublishRequiresTwoPositionals(t *testing.T) {
	for _, args := range [][]string{{"publish"}, {"publish", "only-one-arg"}} {
		_, stderr, err := capture(t, args...)
		if !errors.Is(err, errExitQuiet) {
			t.Errorf("%v: want errExitQuiet, got %v", args, err)
		}
		if !strings.Contains(stderr, "artifact directory and a paper reference") {
			t.Errorf("%v: stderr should explain the problem, got %q", args, stderr)
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
		"--runtime", "memory",
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

func TestDetachedSessionReferenceRoundTripsOpaqueLocator(t *testing.T) {
	ref := managed.SessionRef{
		Runtime: "memory", ID: "session-1", Revision: 2, LocatorVersion: 1,
		Locator: json.RawMessage(`{"future":{"container":"c-1"}}`),
	}
	encoded, err := encodeSessionRef(ref)
	if err != nil {
		t.Fatalf("encodeSessionRef: %v", err)
	}
	got, err := decodeSessionRef(encoded)
	if err != nil || got.Revision != ref.Revision || string(got.Locator) != string(ref.Locator) {
		t.Fatalf("decodeSessionRef = %+v, %v", got, err)
	}
	if _, err := decodeSessionRef("not base64!"); err == nil {
		t.Fatal("invalid session reference accepted")
	}
}
