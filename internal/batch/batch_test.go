// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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

	"github.com/latere-ai/replichai/internal/managed"
)

// runner is a scripted project runner. It records the order and the overlap
// of what the batch asked it to do.
//
// Concurrency is observed and never timed. Entering and leaving Prepare and
// Execute are counted; holdPrepare and holdExecute, when set, keep a call in
// flight until the test closes them; and entered reports each Execute as it
// begins. The test controls every window, so nothing here sleeps and relies
// on the scheduler.
type runner struct {
	mu       sync.Mutex
	prepared []string
	executed []string
	// inExecute and maxExecute count the Execute calls in flight and the most
	// there ever were; inPrepare and maxPrepare do the same for Prepare.
	inExecute  atomic.Int32
	maxExecute atomic.Int32
	inPrepare  atomic.Int32
	maxPrepare atomic.Int32
	fail       map[string]error
	// holdPrepare keeps every Prepare in flight until it is closed.
	holdPrepare chan struct{}
	// holdExecute keeps every Execute in flight until it is closed.
	holdExecute chan struct{}
	// preparing and entered receive each paper as its Prepare and its Execute
	// begin. They are buffered for more papers than any test runs, so a
	// runner never blocks on them.
	preparing chan string
	entered   chan string
	repoless  bool
	// priors is what Prepare was told an earlier attempt recorded, by paper.
	priors map[string]EntryState
}

func newRunner() *runner {
	return &runner{
		fail: map[string]error{}, priors: map[string]EntryState{},
		preparing: make(chan string, 64), entered: make(chan string, 64),
	}
}

// peak raises high to current where current is a new maximum.
func peak(high *atomic.Int32, current int32) {
	for {
		seen := high.Load()
		if current <= seen || high.CompareAndSwap(seen, current) {
			return
		}
	}
}

func (r *runner) Prepare(_ context.Context, entry Resolved, prior EntryState) error {
	peak(&r.maxPrepare, r.inPrepare.Add(1))
	r.preparing <- entry.Reference()
	if r.holdPrepare != nil {
		<-r.holdPrepare
	}
	r.inPrepare.Add(-1)
	r.mu.Lock()
	r.prepared = append(r.prepared, entry.Reference())
	r.priors[entry.Reference()] = prior
	r.mu.Unlock()
	if err, ok := r.fail["prepare:"+entry.Reference()]; ok {
		return err
	}
	return nil
}

func (r *runner) Execute(_ context.Context, entry Resolved) (Outcome, error) {
	peak(&r.maxExecute, r.inExecute.Add(1))
	r.entered <- entry.Reference()
	if r.holdExecute != nil {
		<-r.holdExecute
	}
	r.inExecute.Add(-1)
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

// TestBothInputFormsAndTheOverrideOrder checks that both input forms are
// accepted and that per-paper overrides take precedence over the file's
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

	// An entry naming no paper fails the project rather than being skipped:
	// nineteen of twenty papers running is worse than a refusal.
	empty := File{Papers: []Entry{{Model: "best"}}}
	if _, err := empty.Entries(); err == nil {
		t.Fatal("an entry naming no paper was accepted")
	}
}

// TestPreparationIsSerialisedAndExecutionIsCapped checks that the pool never
// exceeds its cap and that Prepare is serialized even though Execute is not.
// Allocating a repository name is a read followed by a decision, and two
// concurrent readers would decide the same name.
//
// The test holds the window open rather than sleeping. The first Prepare is
// kept in flight until the batch has marked every slot's paper running, so
// any second Prepare that was going to overlap has had that whole time to do
// so. Then every Execute is kept in flight until the pool is observed full.
func TestPreparationIsSerialisedAndExecutionIsCapped(t *testing.T) {
	papers := make([]Entry, 8)
	for index := range papers {
		papers[index] = Entry{Paper: "10.1145/paper-" + string(rune('a'+index))}
	}
	r := newRunner()
	r.holdPrepare = make(chan struct{})
	r.holdExecute = make(chan struct{})
	running := make(chan int, 64)
	done := make(chan struct{})
	var (
		state State
		err   error
	)
	go func() {
		defer close(done)
		state, err = Run(context.Background(), File{Papers: papers}, r, Options{
			Parallel: 3, StatePath: filepath.Join(t.TempDir(), "batch.json"),
			Progress: func(s State) {
				count := 0
				for _, entry := range s.Entries {
					if entry.Phase == Running {
						count++
					}
				}
				running <- count
			},
		})
	}()

	// One Prepare is inside and held; every other slot has claimed a paper
	// and is about to call Prepare. Exactly one may be inside.
	<-r.preparing
	for count := 0; count < 3; count = <-running {
	}
	if got := r.inPrepare.Load(); got != 1 {
		t.Fatalf("%d preparations in flight with three slots busy; want exactly one", got)
	}
	close(r.holdPrepare)

	// The pool fills to its cap and no further.
	for range 3 {
		<-r.entered
	}
	if got := r.inExecute.Load(); got != 3 {
		t.Fatalf("%d papers in flight with --parallel 3 and three entered", got)
	}
	select {
	case extra := <-r.entered:
		t.Fatalf("a fourth paper (%s) entered a pool of three", extra)
	default:
	}
	close(r.holdExecute)
	<-done

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := r.maxExecute.Load(); got > 3 {
		t.Fatalf("%d papers ran at once with --parallel 3", got)
	}
	if got := r.maxPrepare.Load(); got > 1 {
		t.Fatalf("%d preparations overlapped; a name would be allocated twice", got)
	}
	if len(r.executed) != 8 || state.Summarise().Done != 8 {
		t.Fatalf("executed %d, state %+v", len(r.executed), state.Summarise())
	}
}

// TestOneFailureDoesNotStopTheProject checks that one paper failing does not
// fail the rest, and that the batch still reports a non-zero outcome for CI.
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

// TestTheStateFileIsValidAtEveryPoint checks that the state file is valid
// after every write, so a batch killed at any moment leaves a file its own
// resume path can read.
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

// TestResumeSkipsWhatFinished checks that a resumed batch skips what finished
// and re-runs the rest.
func TestResumeSkipsWhatFinished(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "batch.json")
	file := File{Papers: []Entry{{Paper: "10.1/a"}, {Paper: "10.1/b"}, {Paper: "10.1/c"}}}

	first := newRunner()
	first.fail["10.1/b"] = errors.New("interrupted")
	if _, err := Run(context.Background(), file, first, Options{Parallel: 1, StatePath: statePath}); err == nil {
		t.Fatal("the first pass should have reported a failure")
	}
	// Mark the third as still running: a batch killed in flight leaves an
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

// TestAResumedPaperIsHandedItsFirstAttempt checks that a retried paper is
// passed what its first attempt recorded, so the runner can return to the
// repository that attempt created instead of allocating a second one for the
// same paper. That is what --resume promises.
func TestAResumedPaperIsHandedItsFirstAttempt(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "batch.json")
	file := File{Papers: []Entry{{Paper: "10.1/a"}, {Paper: "10.1/b"}}}
	first := State{Version: stateVersion, Entries: []EntryState{
		{Paper: "10.1/a", Phase: Done, Repo: "open-hai/a-1111"},
		{Paper: "10.1/b", Phase: Failed, Repo: "open-hai/b-2222", Session: "s-first", Error: "interrupted"},
	}}
	if err := first.Save(statePath); err != nil {
		t.Fatalf("Save: %v", err)
	}

	r := newRunner()
	if _, err := Run(context.Background(), file, r, Options{Parallel: 1, StatePath: statePath, Resume: true}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	prior, ok := r.priors["10.1/b"]
	if !ok {
		t.Fatalf("the failed paper was not prepared: %v", r.prepared)
	}
	if prior.Repo != "open-hai/b-2222" || prior.Session != "s-first" {
		t.Fatalf("Prepare was handed %+v; want the first attempt's repository and session", prior)
	}
	// A first attempt is handed nothing, so a runner can tell the two apart.
	fresh := newRunner()
	if _, err := Run(context.Background(), File{Papers: []Entry{{Paper: "10.1/c"}}}, fresh,
		Options{Parallel: 1, StatePath: filepath.Join(t.TempDir(), "batch.json")}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fresh.priors["10.1/c"]; got.Repo != "" || got.Session != "" {
		t.Fatalf("a first attempt was handed a prior record: %+v", got)
	}
}

// TestAnEarlyFailureKeepsTheRepositoryOnRecord checks that a retry that fails
// before it reaches its repository does not erase the repository from the
// state, because the next retry has to find it there.
func TestAnEarlyFailureKeepsTheRepositoryOnRecord(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "batch.json")
	first := State{Version: stateVersion, Entries: []EntryState{
		{Paper: "10.1/a", Phase: Failed, Repo: "open-hai/a-1111", Session: "s-first", Directory: "runs/a"},
	}}
	if err := first.Save(statePath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	r := newRunner()
	r.fail["prepare:10.1/a"] = errors.New("the forge is unreachable")
	state, err := Run(context.Background(), File{Papers: []Entry{{Paper: "10.1/a"}}}, r,
		Options{Parallel: 1, StatePath: statePath, Resume: true})
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Run = %v", err)
	}
	entry := state.Entries[0]
	if entry.Phase != Failed || entry.Repo != "open-hai/a-1111" || entry.Session != "s-first" || entry.Directory != "runs/a" {
		t.Fatalf("an early failure erased the first attempt's record: %+v", entry)
	}
	// A later attempt that does reach a session replaces the old one.
	second := newRunner()
	state, err = Run(context.Background(), File{Papers: []Entry{{Paper: "10.1/a"}}}, second,
		Options{Parallel: 1, StatePath: statePath, Resume: true})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if state.Entries[0].Session != "s-10.1/a" {
		t.Fatalf("a successful retry did not record its own session: %+v", state.Entries[0])
	}
}

// TestAnInterruptedBatchKeepsWhatRan checks that an interrupted batch keeps
// what ran and leaves the rest pending rather than failed. Nothing is lost to
// an interrupt, and no paper is recorded as failed because of one.
func TestAnInterruptedBatchKeepsWhatRan(t *testing.T) {
	r := newRunner()
	r.holdExecute = make(chan struct{})
	statePath := filepath.Join(t.TempDir(), "batch.json")
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan State, 1)
	go func() {
		state, _ := Run(ctx, File{Papers: []Entry{{Paper: "10.1/a"}, {Paper: "10.1/b"}, {Paper: "10.1/c"}}}, r,
			Options{Parallel: 1, StatePath: statePath})
		done <- state
	}()
	// One paper is in flight and the other two are waiting for its slot when
	// the interruption arrives.
	<-r.entered
	cancel()
	close(r.holdExecute)

	state := <-done
	totals := state.Summarise()
	if totals.Failed != 0 {
		t.Fatalf("an interruption was recorded as failure: %+v", totals)
	}
	// The paper in flight finishes, and what never started stays pending
	// rather than failed. A waiting paper may still win the freed slot
	// against the cancelled context, so only the bounds are fixed.
	if totals.Done < 1 || totals.Done+totals.Pending != 3 {
		t.Fatalf("after an interruption: %+v", totals)
	}
	if _, err := LoadState(statePath); err != nil {
		t.Fatalf("the state was not written before exit: %v", err)
	}
}

// TestAnUnpublishedRunIsNotAnEmptyRepository checks that a project that was
// never asked to publish is not reported as having failed to publish.
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
	// A project asked to publish that produces nothing is a different case,
	// and is reported.
	state, _ = Run(context.Background(), File{Papers: []Entry{{Paper: "10.1/a"}}}, r,
		Options{Parallel: 1, Publishing: true, StatePath: filepath.Join(t.TempDir(), "batch.json")})
	if got := state.Summarise().Empty; got != 1 {
		t.Fatalf("a publishing run that produced nothing reported %d empty repositories", got)
	}
}

// TestTheReportTotalsTheEntries checks that the report's totals equal the sum
// of the entries, and that the vendor's cost is labeled as the vendor's
// rather than as a replichai charge.
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

// TestTheDefaultPoolIsSized checks that the default pool is half the cores,
// capped, and never zero. An unset --parallel must not mean one paper at a
// time on a sixteen-core machine, and must not mean no papers at all.
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
	if got := r.maxExecute.Load(); got > int32(DefaultParallel()) {
		t.Fatalf("%d papers ran at once with the default of %d", got, DefaultParallel())
	}
}
