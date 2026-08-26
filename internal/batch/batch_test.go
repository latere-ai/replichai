package batch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
)

// runner is a scripted project runner: it records the order and the overlap of
// what the batch asked it to do.
type runner struct {
	mu         sync.Mutex
	prepared   []string
	executed   []string
	overlap    atomic.Int32
	maxOverlap atomic.Int32
	// prepareOverlap counts how many Prepare calls ever ran at once.
	prepareOverlap    atomic.Int32
	maxPrepareOverlap atomic.Int32
	fail              map[string]error
	hold              chan struct{}
	repoless          bool
}

func newRunner() *runner { return &runner{fail: map[string]error{}} }

func (r *runner) Prepare(_ context.Context, entry Resolved) error {
	current := r.prepareOverlap.Add(1)
	for {
		peak := r.maxPrepareOverlap.Load()
		if current <= peak || r.maxPrepareOverlap.CompareAndSwap(peak, current) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	r.prepareOverlap.Add(-1)
	r.mu.Lock()
	r.prepared = append(r.prepared, entry.Reference())
	r.mu.Unlock()
	if err, ok := r.fail["prepare:"+entry.Reference()]; ok {
		return err
	}
	return nil
}

func (r *runner) Execute(_ context.Context, entry Resolved) (Outcome, error) {
	current := r.overlap.Add(1)
	for {
		peak := r.maxOverlap.Load()
		if current <= peak || r.maxOverlap.CompareAndSwap(peak, current) {
			break
		}
	}
	if r.hold != nil {
		<-r.hold
	}
	time.Sleep(2 * time.Millisecond)
	r.overlap.Add(-1)
	r.mu.Lock()
	r.executed = append(r.executed, entry.Reference())
	r.mu.Unlock()
	if err, ok := r.fail[entry.Reference()]; ok {
		return Outcome{}, err
	}
	repo := "open-hai/" + strings.TrimPrefix(entry.Reference(), "10.")
	if r.repoless {
		repo = ""
	}
	return Outcome{
		Session: "s-" + entry.Reference(), Repo: repo,
		Grade: "passed", Model: managed.ModelRef{Provider: "anthropic", ID: entry.Model},
		Usage: managed.Usage{InputFresh: 100, Output: 20, Details: map[string]int64{"vendor_cost_micro_usd": 1000}},
	}, nil
}

func project(t *testing.T, body, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// Both input forms are accepted, and per-paper overrides beat the file's
// defaults.
func TestBothInputFormsAndTheOverrideOrder(t *testing.T) {
	list, err := ReadFile(project(t, "# a project\n\nhttps://arxiv.org/abs/2603.01795\n10.1145/3654777.3676417\n", "papers.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(list.Papers) != 2 || list.Papers[0].Paper != "https://arxiv.org/abs/2603.01795" {
		t.Fatalf("list = %+v", list.Papers)
	}

	structured, err := ReadFile(project(t, `
project: chi-2026-audit
harness: claude-code
model: best
publish:
  owner: open-hai
  invite: someone
papers:
  - https://arxiv.org/abs/2603.01795
  - doi: 10.1145/3654777.3676417
    model: fast
`, "project.yaml"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if structured.Project != "chi-2026-audit" || structured.Publish.Owner != "open-hai" {
		t.Fatalf("project = %+v", structured)
	}
	entries, err := structured.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if entries[0].Model != "best" || entries[0].Harness != "claude-code" {
		t.Errorf("the file's defaults did not reach the first paper: %+v", entries[0])
	}
	if entries[1].Model != "fast" {
		t.Errorf("the per-paper override did not beat the default: %+v", entries[1])
	}
	if entries[1].Reference() != "10.1145/3654777.3676417" {
		t.Errorf("the doi spelling was not read: %+v", entries[1])
	}

	// A paper naming nothing fails that project rather than being quietly
	// skipped: nineteen of twenty papers running is worse than a refusal.
	empty := File{Papers: []Entry{{Model: "best"}}}
	if _, err := empty.Entries(); err == nil {
		t.Fatal("an entry naming no paper was accepted")
	}
}

// The pool never exceeds its cap, and Prepare is serialised even when Execute
// is not: allocating a repository name is a read followed by a decision, and
// two concurrent readers decide the same one.
func TestPreparationIsSerialisedAndExecutionIsCapped(t *testing.T) {
	papers := make([]Entry, 8)
	for index := range papers {
		papers[index] = Entry{Paper: "10.1145/paper-" + string(rune('a'+index))}
	}
	r := newRunner()
	state, err := Run(context.Background(), File{Papers: papers}, r, Options{
		Parallel: 3, StatePath: filepath.Join(t.TempDir(), "batch.json"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := r.maxOverlap.Load(); got > 3 {
		t.Fatalf("%d papers ran at once with --parallel 3", got)
	}
	if got := r.maxPrepareOverlap.Load(); got > 1 {
		t.Fatalf("%d preparations overlapped; a name would be allocated twice", got)
	}
	if len(r.executed) != 8 || state.Summarise().Done != 8 {
		t.Fatalf("executed %d, state %+v", len(r.executed), state.Summarise())
	}
}

// One paper failing never fails the rest, and the batch still reports a
// non-zero outcome so CI reads it.
func TestOneFailureDoesNotStopTheProject(t *testing.T) {
	r := newRunner()
	r.fail["10.1/b"] = errors.New("the sandbox is not ready")
	r.fail["prepare:10.1/c"] = errors.New("allocate name: rate limited")
	statePath := filepath.Join(t.TempDir(), "batch.json")
	state, err := Run(context.Background(), File{Papers: []Entry{
		{Paper: "10.1/a"}, {Paper: "10.1/b"}, {Paper: "10.1/c"}, {Paper: "10.1/d"},
	}}, r, Options{Parallel: 2, StatePath: statePath})
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Run = %v", err)
	}
	totals := state.Summarise()
	if totals.Done != 2 || totals.Failed != 2 {
		t.Fatalf("totals = %+v", totals)
	}
	// A paper whose preparation failed never ran.
	for _, executed := range r.executed {
		if executed == "10.1/c" {
			t.Fatal("a paper whose preparation failed was executed")
		}
	}
	report := state.Report()
	for _, want := range []string{"Not done", "the sandbox is not ready", "rate limited"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report hides %q:\n%s", want, report)
		}
	}
}

// The state file is valid at every point, so a batch killed at any moment
// leaves something its own resume path can read.
func TestTheStateFileIsValidAtEveryPoint(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "batch.json")
	r := newRunner()
	checks := 0
	_, err := Run(context.Background(), File{Papers: []Entry{{Paper: "10.1/a"}, {Paper: "10.1/b"}}}, r, Options{
		Parallel: 1, StatePath: statePath,
		Progress: func(State) {
			checks++
			if _, err := LoadState(statePath); err != nil {
				t.Errorf("the state file is unreadable mid-run: %v", err)
			}
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if checks < 4 {
		t.Fatalf("the state was written %d times for two papers", checks)
	}
}

// A resumed batch skips what finished and re-runs the rest.
func TestResumeSkipsWhatFinished(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "batch.json")
	file := File{Papers: []Entry{{Paper: "10.1/a"}, {Paper: "10.1/b"}, {Paper: "10.1/c"}}}

	first := newRunner()
	first.fail["10.1/b"] = errors.New("interrupted")
	if _, err := Run(context.Background(), file, first, Options{Parallel: 1, StatePath: statePath}); err == nil {
		t.Fatal("the first pass should have reported a failure")
	}
	// Pretend the third never ran: a batch killed mid-flight leaves an
	// entry that is not terminal.
	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	state.Entries[2] = EntryState{Paper: "10.1/c", Phase: Running, Session: "s-old"}
	if err := state.Save(statePath); err != nil {
		t.Fatalf("Save: %v", err)
	}

	second := newRunner()
	resumed, err := Run(context.Background(), file, second, Options{Parallel: 1, StatePath: statePath, Resume: true})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	// The paper that finished is not re-run; the failed one and the
	// interrupted one are.
	if len(second.executed) != 2 {
		t.Fatalf("resume executed %v", second.executed)
	}
	for _, executed := range second.executed {
		if executed == "10.1/a" {
			t.Fatal("a finished paper was run again")
		}
	}
	if resumed.Summarise().Done != 3 {
		t.Fatalf("after resuming: %+v", resumed.Summarise())
	}
}

// An interrupted batch keeps what ran and leaves the rest pending rather than
// failed: nothing is lost to a Ctrl-C, and nothing is blamed on one.
func TestAnInterruptedBatchKeepsWhatRan(t *testing.T) {
	r := newRunner()
	r.hold = make(chan struct{})
	statePath := filepath.Join(t.TempDir(), "batch.json")
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan State, 1)
	go func() {
		state, _ := Run(ctx, File{Papers: []Entry{{Paper: "10.1/a"}, {Paper: "10.1/b"}, {Paper: "10.1/c"}}}, r,
			Options{Parallel: 1, StatePath: statePath})
		done <- state
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	close(r.hold)

	state := <-done
	totals := state.Summarise()
	if totals.Failed != 0 {
		t.Fatalf("an interruption was recorded as failure: %+v", totals)
	}
	if _, err := LoadState(statePath); err != nil {
		t.Fatalf("the state was not written before exit: %v", err)
	}
}

// A project that was never asked to publish is not reported as having failed
// to publish.
func TestAnUnpublishedRunIsNotAnEmptyRepository(t *testing.T) {
	r := newRunner()
	r.repoless = true
	state, err := Run(context.Background(), File{Papers: []Entry{{Paper: "10.1/a"}}}, r,
		Options{Parallel: 1, StatePath: filepath.Join(t.TempDir(), "batch.json")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := state.Summarise().Empty; got != 0 {
		t.Fatalf("a run with no publishing reported %d empty repositories", got)
	}
	if strings.Contains(state.Report(), "without publishing") {
		t.Fatalf("the report blames a run for not publishing:\n%s", state.Report())
	}
	// Asked to publish and producing nothing is a different thing, and is
	// reported.
	state, _ = Run(context.Background(), File{Papers: []Entry{{Paper: "10.1/a"}}}, r,
		Options{Parallel: 1, Publishing: true, StatePath: filepath.Join(t.TempDir(), "batch.json")})
	if got := state.Summarise().Empty; got != 1 {
		t.Fatalf("a publishing run that produced nothing reported %d empty repositories", got)
	}
}

// The report's totals equal the entries, and the vendor's cost is labelled as
// the vendor's rather than as something replichai charged.
func TestTheReportTotalsTheEntries(t *testing.T) {
	r := newRunner()
	state, err := Run(context.Background(), File{Project: "chi-2026", Papers: []Entry{{Paper: "10.1/a"}, {Paper: "10.1/b"}}},
		r, Options{Parallel: 2, StatePath: filepath.Join(t.TempDir(), "batch.json")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	totals := state.Summarise()
	// The pass count reads the grade's name. Grade is a uint8, so a bare
	// conversion yields the rune at that code point and every batch reports
	// zero passes.
	if totals.Passed != 2 {
		t.Fatalf("passed = %d of %d", totals.Passed, totals.Papers)
	}
	if totals.Usage.InputFresh != 200 || totals.Usage.Output != 40 {
		t.Fatalf("totals = %+v", totals.Usage)
	}
	report := state.Report()
	for _, want := range []string{"chi-2026", "2 papers", "open-hai/", "vendor cost", "Nothing here was billed by replichai"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report is missing %q:\n%s", want, report)
		}
	}
}

// The default pool is half the cores, capped, and never zero: an unset
// --parallel must not mean one paper at a time on a sixteen-core machine, and
// must not mean none at all.
func TestTheDefaultPoolIsSized(t *testing.T) {
	if got := DefaultParallel(); got < 1 || got > 4 {
		t.Fatalf("DefaultParallel() = %d", got)
	}
	r := newRunner()
	state, err := Run(context.Background(), File{Papers: []Entry{{Paper: "10.1/a"}, {Paper: "10.1/b"}}}, r,
		Options{StatePath: filepath.Join(t.TempDir(), "batch.json")})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.Summarise().Done != 2 {
		t.Fatalf("an unset --parallel ran %+v", state.Summarise())
	}
	if got := r.maxOverlap.Load(); got > int32(DefaultParallel()) {
		t.Fatalf("%d papers ran at once with the default of %d", got, DefaultParallel())
	}
}
