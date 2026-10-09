// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package batch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
)

// runner is a scripted project runner. It records the order and the overlap
// of what the batch asked it to do. What it records, fails and reports is
// keyed by run, as runName names one.
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
	// priors is what Prepare was told an earlier attempt recorded, by run.
	priors map[string]EntryState
}

// runName names one run for the scripted runner: the reference alone for a
// paper that runs once, so the single-run tests read as plain references, and
// the reference with its ordinal, as in 10.1/a#2, for a paper that runs more
// than once.
func runName(entry Resolved) string {
	if entry.Runs > 1 {
		return fmt.Sprintf("%s#%d", entry.Reference(), entry.Run)
	}
	return entry.Reference()
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
	r.prepared = append(r.prepared, runName(entry))
	r.priors[runName(entry)] = prior
	r.mu.Unlock()
	if err, ok := r.fail["prepare:"+runName(entry)]; ok {
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
	r.executed = append(r.executed, runName(entry))
	r.mu.Unlock()
	if err, ok := r.fail[runName(entry)]; ok {
		return Outcome{}, err
	}
	repo := "open-hai/" + strings.TrimPrefix(runName(entry), "10.")
	if r.repoless {
		repo = ""
	}
	return Outcome{
		Session: "s-" + runName(entry), Repo: repo,
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

// TestRepeatRunsReadTheSameEitherWay checks that the three ways to ask for
// repeat runs of one paper describe the same project: runs on the paper, runs
// as the file's default, and the paper listed once per run. A repeat-run study
// may be written any of the three ways, and its state must not depend on which.
func TestRepeatRunsReadTheSameEitherWay(t *testing.T) {
	spellings := map[string]string{
		"runs on the paper":   "papers:\n  - paper: 10.1/a\n    runs: 3\n  - paper: 10.1/b\n    runs: 1\n",
		"runs as the default": "runs: 3\npapers:\n  - 10.1/a\n",
		"listed three times":  "papers:\n  - 10.1/a\n  - 10.1/a\n  - 10.1/a\n  - 10.1/b\n",
	}
	resolved := map[string][]Resolved{}
	for name, body := range spellings {
		file, err := ReadFile(project(t, body, "project.yaml"))
		if err != nil {
			t.Fatalf("%s: ReadFile: %v", name, err)
		}
		entries, err := file.Entries()
		if err != nil {
			t.Fatalf("%s: Entries: %v", name, err)
		}
		resolved[name] = entries
	}
	want := resolved["listed three times"]
	if len(want) != 4 {
		t.Fatalf("listing a paper three times gave %d entries, want 4: %+v", len(want), want)
	}
	if got := resolved["runs on the paper"]; !reflect.DeepEqual(got, want) {
		t.Errorf("runs on the paper resolved differently from listing it:\n got %+v\nwant %+v", got, want)
	}
	// The default applies to every paper, so its file holds only the one.
	if got := resolved["runs as the default"]; !reflect.DeepEqual(got, want[:3]) {
		t.Errorf("runs as the default resolved differently from listing it:\n got %+v\nwant %+v", got, want[:3])
	}
	for index, entry := range want {
		wantRun, wantRuns := index+1, 3
		if entry.Reference() == "10.1/b" {
			wantRun, wantRuns = 1, 1
		}
		if entry.Index != index || entry.Run != wantRun || entry.Runs != wantRuns {
			t.Errorf("entry %d is %s index %d, run %d of %d; want index %d, run %d of %d",
				index, entry.Reference(), entry.Index, entry.Run, entry.Runs, index, wantRun, wantRuns)
		}
	}

	// Ordinals follow the file, so a paper listed apart from its other runs
	// still counts them in the order they were written.
	interleaved, err := File{Papers: []Entry{{Paper: "10.1/a"}, {Paper: "10.1/b"}, {Paper: "10.1/a", Runs: 2}}}.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	var order []string
	for _, entry := range interleaved {
		order = append(order, fmt.Sprintf("%s %d/%d", entry.Reference(), entry.Run, entry.Runs))
	}
	if got := strings.Join(order, ", "); got != "10.1/a 1/3, 10.1/b 1/1, 10.1/a 2/3, 10.1/a 3/3" {
		t.Fatalf("interleaved runs = %s", got)
	}
}

// TestANegativeRunCountIsRefused checks that a negative runs value fails the
// project, on a paper and as the file's default. Read as zero it would run
// the paper once, which is not what was written.
func TestANegativeRunCountIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"on a paper":     "papers:\n  - 10.1/a\n  - paper: 10.1/b\n    runs: -1\n",
		"as the default": "runs: -2\npapers:\n  - 10.1/a\n",
	} {
		file, err := ReadFile(project(t, body, "project.yaml"))
		if err != nil {
			t.Fatalf("%s: ReadFile: %v", name, err)
		}
		if entries, err := file.Entries(); err == nil || !strings.Contains(err.Error(), "runs") {
			t.Errorf("%s: a negative runs was accepted as %d entries (err %v)", name, len(entries), err)
		}
		r := newRunner()
		if _, err := Run(context.Background(), file, r, Options{Parallel: 1}); err == nil {
			t.Errorf("%s: Run started a project with a negative runs", name)
		}
		if len(r.executed) != 0 {
			t.Errorf("%s: Run executed %v for a refused project", name, r.executed)
		}
	}
}

// TestPreparationIsSerializedAndExecutionIsCapped checks that the pool never
// exceeds its cap and that Prepare is serialized even though Execute is not.
// Allocating a repository name is a read followed by a decision, and two
// concurrent readers would decide the same name.
//
// The test holds the window open rather than sleeping. The first Prepare is
// kept in flight until the batch has marked every slot's paper running, so
// any second Prepare that was going to overlap has had that whole time to do
// so. Then every Execute is kept in flight until the pool is observed full.
func TestPreparationIsSerializedAndExecutionIsCapped(t *testing.T) {
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
	if len(r.executed) != 8 || state.Summarize().Done != 8 {
		t.Fatalf("executed %d, state %+v", len(r.executed), state.Summarize())
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
	totals := state.Summarize()
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
	if resumed.Summarize().Done != 3 {
		t.Fatalf("after resuming: %+v", resumed.Summarize())
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

// resumeOnce saves previous as the state of an earlier pass, resumes file
// against it, and returns the scripted runner and what the resume recorded,
// with the runs the resume executed sorted, because the pool does not fix
// their order.
func resumeOnce(t *testing.T, file File, previous []byte) (*runner, State, []string) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(statePath, previous, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	r := newRunner()
	state, err := Run(context.Background(), file, r, Options{Parallel: 1, StatePath: statePath, Resume: true})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	saved, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !reflect.DeepEqual(saved.Entries, state.Entries) {
		t.Fatalf("the saved state differs from the returned one:\n%+v\n%+v", saved.Entries, state.Entries)
	}
	return r, state, slices.Sorted(slices.Values(r.executed))
}

// checkThreeRunsResumed checks a resume of three runs of 10.1/a whose earlier
// pass left run 1 done, run 2 failed and run 3 pending: the finished run is
// skipped, the failed run is retried into its own repository, the pending run
// starts fresh, and every run keeps a record of its own.
func checkThreeRunsResumed(t *testing.T, r *runner, state State, executed []string) {
	t.Helper()
	if got := strings.Join(executed, ", "); got != "10.1/a#2, 10.1/a#3" {
		t.Fatalf("resume executed [%s], want the failed and the pending run", got)
	}
	if prior := r.priors["10.1/a#2"]; prior.Repo != "open-hai/a-2222" || prior.Session != "s2" {
		t.Errorf("the failed run was handed %+v; want its own repository and session", prior)
	}
	if prior := r.priors["10.1/a#3"]; prior.Repo != "" || prior.Session != "" {
		t.Errorf("the pending run was handed another run's record: %+v", prior)
	}
	if first := state.Entries[0]; first.Phase != Done || first.Repo != "open-hai/a-1111" || first.Session != "s1" {
		t.Errorf("the finished run lost its record: %+v", first)
	}
	for index, entry := range state.Entries {
		if entry.Paper != "10.1/a" || entry.Run != index+1 || entry.Phase != Done {
			t.Errorf("entry %d = %s run %d %s, want 10.1/a run %d done", index, entry.Paper, entry.Run, entry.Phase, index+1)
		}
	}
	if state.Entries[1].Session == state.Entries[2].Session {
		t.Errorf("two runs share session %s", state.Entries[1].Session)
	}
}

// TestRepeatRunsResumeAsSeparateRuns checks that a resume of three runs of one
// paper treats them as three runs. Matched by paper alone, every run took the
// first run's record, so the resume ran nothing and reported the failed and
// the pending run as done, with the first run's repository.
func TestRepeatRunsResumeAsSeparateRuns(t *testing.T) {
	previous, err := json.Marshal(State{Version: stateVersion, Entries: []EntryState{
		{Paper: "10.1/a", Run: 1, Phase: Done, Repo: "open-hai/a-1111", Session: "s1"},
		{Paper: "10.1/a", Run: 2, Phase: Failed, Repo: "open-hai/a-2222", Session: "s2", Error: "interrupted"},
		{Paper: "10.1/a", Run: 3, Phase: Pending},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r, state, executed := resumeOnce(t, File{Papers: []Entry{{Paper: "10.1/a", Runs: 3}}}, previous)
	checkThreeRunsResumed(t, r, state, executed)
}

// TestALegacyStateResumesByOccurrence checks that a state file written before
// runs were recorded, which names a paper once per run and no run at all, is
// read in occurrence order: the first entry for a paper is its first run.
func TestALegacyStateResumesByOccurrence(t *testing.T) {
	previous := []byte(`{"version": 1, "started_at": "2026-09-01T00:00:00Z", "entries": [
  {"paper": "10.1/a", "state": "done", "session": "s1", "repo": "open-hai/a-1111"},
  {"paper": "10.1/a", "state": "failed", "session": "s2", "repo": "open-hai/a-2222", "error": "interrupted"},
  {"paper": "10.1/a", "state": "pending"}
]}`)
	file := File{Papers: []Entry{{Paper: "10.1/a"}, {Paper: "10.1/a"}, {Paper: "10.1/a"}}}
	r, state, executed := resumeOnce(t, file, previous)
	checkThreeRunsResumed(t, r, state, executed)
}

// TestMoreRunsOfAFinishedPaperRunOnlyTheNewOnes checks that raising the runs
// of a paper that already finished once runs only the runs added. The one
// finished record is the first run's, and it is not also the second's.
func TestMoreRunsOfAFinishedPaperRunOnlyTheNewOnes(t *testing.T) {
	previous := []byte(`{"version": 1, "started_at": "2026-09-01T00:00:00Z", "entries": [
  {"paper": "10.1/a", "state": "done", "session": "s1", "repo": "open-hai/a-1111"}
]}`)
	r, state, executed := resumeOnce(t, File{Papers: []Entry{{Paper: "10.1/a", Runs: 2}}}, previous)
	if got := strings.Join(executed, ", "); got != "10.1/a#2" {
		t.Fatalf("resume executed [%s], want only the added run", got)
	}
	if prior := r.priors["10.1/a#2"]; prior.Repo != "" || prior.Session != "" {
		t.Errorf("the added run was handed the finished run's record: %+v", prior)
	}
	if first := state.Entries[0]; first.Run != 1 || first.Repo != "open-hai/a-1111" || first.Session != "s1" {
		t.Errorf("the finished run lost its record: %+v", first)
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
	totals := state.Summarize()
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
	if got := state.Summarize().Empty; got != 0 {
		t.Fatalf("a run with no publishing reported %d empty repositories", got)
	}
	if strings.Contains(state.Report(), "without publishing") {
		t.Fatalf("the report blames a run for not publishing:\n%s", state.Report())
	}
	// A project asked to publish that produces nothing is a different case,
	// and is reported.
	state, _ = Run(context.Background(), File{Papers: []Entry{{Paper: "10.1/a"}}}, r,
		Options{Parallel: 1, Publishing: true, StatePath: filepath.Join(t.TempDir(), "batch.json")})
	if got := state.Summarize().Empty; got != 1 {
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
	totals := state.Summarize()
	// The pass count reads the grade's name. Grade is a uint8, so a bare
	// conversion yields the rune at that code point and every batch reports
	// zero passes.
	if totals.Passed != 2 {
		t.Fatalf("passed = %d of %d", totals.Passed, totals.Runs)
	}
	if totals.Usage.InputFresh != 200 || totals.Usage.Output != 40 {
		t.Fatalf("totals = %+v", totals.Usage)
	}
	report := state.Report()
	for _, want := range []string{"chi-2026", "2 runs", "open-hai/", "vendor cost", "Nothing here was billed by replichai"} {
		if !strings.Contains(report, want) {
			t.Errorf("the report is missing %q:\n%s", want, report)
		}
	}
}

// TestTheReportNamesTheRunOfARepeatedPaper checks that the report tells the
// runs of one paper apart, in the table and in what did not happen, and that
// a paper that runs once reads as it always did. Three rows naming the same
// paper, one of them failed, do not say which run to look at.
func TestTheReportNamesTheRunOfARepeatedPaper(t *testing.T) {
	r := newRunner()
	r.fail["10.1/a#2"] = errors.New("the sandbox is not ready")
	state, err := Run(context.Background(), File{Papers: []Entry{{Paper: "10.1/a", Runs: 3}, {Paper: "10.1/b"}}}, r,
		Options{Parallel: 1, StatePath: filepath.Join(t.TempDir(), "batch.json")})
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Run = %v", err)
	}
	report := state.Report()
	for _, want := range []string{
		"4 runs: 3 done, 1 failed, 0 not run.",
		"| 10.1/a (run 1 of 3) | done |",
		"| 10.1/a (run 2 of 3) | failed |",
		"| 10.1/a (run 3 of 3) | done |",
		"| 10.1/b | done |",
		"- 10.1/a (run 2 of 3) failed: the sandbox is not ready",
		"across 4 runs",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report is missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "10.1/b (run") {
		t.Errorf("a paper that runs once is labeled with a run:\n%s", report)
	}

	// The runs that did not run, and a run that published nothing, are
	// named the same way.
	unfinished := State{Publishing: true, Entries: []EntryState{
		{Paper: "10.1/a", Run: 1, Phase: Pending},
		{Paper: "10.1/a", Run: 2, Phase: Done},
	}}
	report = unfinished.Report()
	for _, want := range []string{
		"- 10.1/a (run 1 of 2) did not run.",
		"- 10.1/a (run 2 of 2) finished without publishing a repository.",
	} {
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
	if state.Summarize().Done != 2 {
		t.Fatalf("an unset --parallel ran %+v", state.Summarize())
	}
	if got := r.maxExecute.Load(); got > int32(DefaultParallel()) {
		t.Fatalf("%d papers ran at once with the default of %d", got, DefaultParallel())
	}
}
