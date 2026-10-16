// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package batch runs one replication project: a list of papers, a concurrency
// cap, one isolated session each, resumable state, and a report at the end
// (045h).
//
// Before this package, running a project meant one invocation and one
// terminal per paper. There was no shared report, no state survived a laptop
// sleeping, and a failure in one paper was found by scrolling back through
// output.
package batch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v4"

	managed "latere.ai/x/managed-agents"
)

// Entry is one paper in a project, with the overrides that apply to it.
type Entry struct {
	// Paper is the reference: a URL, a DOI, or an arXiv id.
	Paper string `yaml:"paper"`
	// DOI is an alternative spelling of Paper, so a list of DOIs reads
	// naturally in the file.
	DOI string `yaml:"doi"`
	// Model, Harness, Sandbox and Network override the file's defaults for
	// this paper alone. Network names a network profile.
	Model   string `yaml:"model"`
	Harness string `yaml:"harness"`
	Sandbox string `yaml:"sandbox"`
	Network string `yaml:"network"`
	// File is a copy of the paper the operator supplies, a path relative to
	// the project file, which every run of the paper is handed in place of a
	// copy the system fetches. It is how a paper whose publisher refuses
	// automated clients is run at all.
	File string `yaml:"file"`
	// Artifacts are what the paper's authors released, as the addresses the
	// study snapshots once and hands every run of the paper: a code host's
	// repository, a repository ending in .git, or the direct download address
	// of a file.
	Artifacts []string `yaml:"artifacts"`
	// Runs is how many times the paper is reproduced, each run in its own
	// session with its own repository, which is what a study measuring how
	// much reproductions of one paper vary asks for. Zero means the file's
	// default.
	Runs int `yaml:"runs"`
}

// UnmarshalYAML accepts either spelling of one paper: a bare reference, which
// is how a list of papers reads, or a mapping carrying overrides. A file that
// mixes the two is the normal case, since most papers need no overrides.
func (e *Entry) UnmarshalYAML(node *yaml.Node) error {
	var reference string
	if err := node.Decode(&reference); err == nil {
		e.Paper = strings.TrimSpace(reference)
		return nil
	}
	type plain Entry
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*e = Entry(decoded)
	return nil
}

// Reference is the paper this entry names, whichever spelling was used.
func (e Entry) Reference() string {
	if reference := strings.TrimSpace(e.Paper); reference != "" {
		return reference
	}
	return strings.TrimSpace(e.DOI)
}

// PublishConfig is what the project does with each reproduction.
type PublishConfig struct {
	Owner  string `yaml:"owner"`
	Invite string `yaml:"invite"`
}

// File is a replication project as it is written on disk. The defaults apply
// to every paper; an entry may override any of them.
type File struct {
	Project string `yaml:"project"`
	Model   string `yaml:"model"`
	Harness string `yaml:"harness"`
	Sandbox string `yaml:"sandbox"`
	// Network names the network profile every run is given unless its paper
	// names another. Empty leaves the choice to the command that runs it.
	Network string `yaml:"network"`
	// Runs is how many times each paper is reproduced unless the paper says
	// otherwise. Zero means once.
	Runs    int           `yaml:"runs"`
	Publish PublishConfig `yaml:"publish"`
	Papers  []Entry       `yaml:"papers"`
}

// Resolved is one run of one entry with the file's defaults applied, which is
// what a runner is handed.
type Resolved struct {
	Entry
	// Index is the run's position in the expanded list, and so the one value
	// that tells two runs of one paper apart within a batch.
	Index int
	// Run is the run's ordinal among the runs of the same reference, from 1,
	// in file order.
	Run int
	// Runs is how many runs of the same reference the project has in total.
	// It shadows Entry.Runs, which Entries sets to the same total, so either
	// field reads the same.
	Runs int
}

// Entries applies the file's defaults to every paper and expands each paper
// into its runs. An entry that names no paper is an error rather than a
// skipped line: a project of twenty papers that runs nineteen without notice
// is worse than one that refuses to run. A negative run count is an error for
// the same reason.
//
// A reference listed twice counts as two runs of that paper, so `runs: 2` and
// listing the paper twice resolve to the same entries, and so to the same
// state. Ordinals are assigned per reference in file order.
func (f File) Entries() ([]Resolved, error) {
	if f.Runs < 0 {
		return nil, fmt.Errorf("runs is %d; a project runs each paper at least once", f.Runs)
	}
	resolved := make([]Resolved, 0, len(f.Papers))
	for index, entry := range f.Papers {
		if entry.Reference() == "" {
			return nil, fmt.Errorf("papers[%d]: names no paper", index)
		}
		if entry.Runs < 0 {
			return nil, fmt.Errorf("papers[%d]: runs is %d; a paper runs at least once", index, entry.Runs)
		}
		if entry.Model == "" {
			entry.Model = f.Model
		}
		if entry.Harness == "" {
			entry.Harness = f.Harness
		}
		if entry.Sandbox == "" {
			entry.Sandbox = f.Sandbox
		}
		if entry.Network == "" {
			entry.Network = f.Network
		}
		runs := cmp.Or(entry.Runs, f.Runs, 1)
		for range runs {
			resolved = append(resolved, Resolved{Entry: entry, Index: len(resolved)})
		}
	}

	// The ordinals and the totals are counted over the expanded list, because
	// the runs of one reference may be spread across several entries.
	totals := map[string]int{}
	for _, entry := range resolved {
		totals[entry.Reference()]++
	}
	ordinals := map[string]int{}
	for index := range resolved {
		reference := resolved[index].Reference()
		ordinals[reference]++
		resolved[index].Run = ordinals[reference]
		resolved[index].Runs = totals[reference]
		resolved[index].Entry.Runs = totals[reference]
	}
	return resolved, nil
}

// ReadFile reads a project. A file whose first non-comment content is a plain
// reference is read as a list, so the smallest useful input is one paper per
// line; anything else is YAML.
func ReadFile(path string) (File, error) {
	//nolint:gosec // the path is the project file the operator named
	body, err := os.ReadFile(path)
	if err != nil {
		return File{}, fmt.Errorf("read project: %w", err)
	}
	if list, ok := parseList(string(body)); ok {
		return File{Papers: list}, nil
	}
	var file File
	if err := yaml.Unmarshal(body, &file); err != nil {
		return File{}, fmt.Errorf("parse project %s: %w", path, err)
	}
	if len(file.Papers) == 0 {
		return File{}, fmt.Errorf("project %s lists no papers", path)
	}
	return file, nil
}

// parseList reads the plain form: one reference per line, # for comments. It
// reports false where the content is structured, so a YAML project is not
// misread as a list of references.
func parseList(body string) ([]Entry, bool) {
	var entries []Entry
	for raw := range strings.SplitSeq(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.ContainsAny(line, ":-") && looksStructured(line) {
			return nil, false
		}
		entries = append(entries, Entry{Paper: line})
	}
	return entries, len(entries) > 0
}

// looksStructured reports whether a line is YAML rather than a reference. A
// reference carries a scheme or a DOI prefix; a mapping key does not.
func looksStructured(line string) bool {
	switch {
	case strings.HasPrefix(line, "http://"), strings.HasPrefix(line, "https://"):
		return false
	case strings.HasPrefix(line, "10."):
		return false
	case strings.HasPrefix(line, "-"), strings.HasPrefix(line, " "):
		return true
	}
	key, _, found := strings.Cut(line, ":")
	return found && !strings.Contains(key, "/") && !strings.Contains(key, " ")
}

// Phase is the progress of one entry.
type Phase string

// The phases an entry moves through. A batch is resumed by skipping the
// terminal ones and reattaching the rest.
const (
	Pending Phase = "pending"
	Running Phase = "running"
	Done    Phase = "done"
	Failed  Phase = "failed"
)

// Terminal reports whether an entry needs no further work.
func (p Phase) Terminal() bool { return p == Done || p == Failed }

// EntryState is what happened to one run of one paper.
type EntryState struct {
	Paper string `json:"paper"`
	// Run is the run's ordinal among the runs of Paper, from 1. With Paper it
	// is the key a resume matches on. A state file written before runs were
	// recorded has none, and a resume numbers its entries by occurrence.
	Run int `json:"run,omitempty"`
	// PaperKey is the offline paper identity, carried so a push files the
	// record against the paper without resolving it again.
	PaperKey  string             `json:"paper_key,omitempty"`
	Phase     Phase              `json:"state"`
	Session   string             `json:"session,omitempty"`
	Directory string             `json:"directory,omitempty"`
	Repo      string             `json:"repo,omitempty"`
	Grade     string             `json:"grade,omitempty"`
	Model     managed.ModelRef   `json:"model"`
	Usage     managed.Usage      `json:"usage"`
	Started   time.Time          `json:"started_at"`
	Finished  time.Time          `json:"finished_at"`
	Error     string             `json:"error,omitempty"`
	Ref       managed.SessionRef `json:"ref,omitzero"`
}

// absorb records what one attempt reported. Where the attempt reports no
// value, because it failed before it reached a session or its repository, the
// entry keeps what an earlier attempt recorded, so a retry that fails early
// cannot erase the repository the next retry must return to.
func (e *EntryState) absorb(outcome Outcome) {
	e.Grade, e.Model, e.Usage = outcome.Grade, outcome.Model, outcome.Usage
	if outcome.Session != "" {
		e.Session = outcome.Session
	}
	if outcome.Directory != "" {
		e.Directory = outcome.Directory
	}
	if outcome.PaperKey != "" {
		e.PaperKey = outcome.PaperKey
	}
	if outcome.Repo != "" {
		e.Repo = outcome.Repo
	}
	if outcome.Ref.ID != "" {
		e.Ref = outcome.Ref
	}
}

// Wall is how long the entry took, or zero where it has not finished.
func (e EntryState) Wall() time.Duration {
	if e.Started.IsZero() || e.Finished.IsZero() {
		return 0
	}
	return e.Finished.Sub(e.Started)
}

// Outcome is what a runner reports about one paper.
type Outcome struct {
	// PaperKey is the offline paper identity the session targeted. It is
	// carried so a later push files the record against the paper without
	// resolving it over the network a second time.
	PaperKey  string
	Session   string
	Directory string
	Repo      string
	Grade     string
	Model     managed.ModelRef
	Usage     managed.Usage
	Ref       managed.SessionRef
}

// Runner runs one paper. The batch serializes Prepare and does not serialize
// Execute; that is the whole concurrency contract. Allocating a repository
// name is a read followed by a decision, and two concurrent readers would
// decide the same name.
type Runner interface {
	// Prepare claims whatever must be claimed exactly once before the work
	// starts, such as a repository name or a repository. prior is what an
	// earlier attempt at the same run of the same paper recorded, or the zero
	// value for a first attempt. A resumed run is expected to return to the
	// repository its first attempt created rather than claim a second one.
	Prepare(ctx context.Context, entry Resolved, prior EntryState) error
	// Execute runs the audit and publishes it.
	Execute(ctx context.Context, entry Resolved) (Outcome, error)
}

// Options configure one run of a batch.
type Options struct {
	// Parallel is how many papers run at once. Zero or less selects the
	// default: half the machine's cores, capped at four. A session mostly
	// waits on a model and a little on a compiler, and a wider pool costs
	// more in disk contention than it saves in batch time.
	Parallel int
	// StatePath is where the resumable state is written.
	StatePath string
	// Resume skips entries that already finished and re-runs the rest.
	Resume bool
	// Clock is the time source.
	Clock func() time.Time
	// Publishing reports that the project publishes each reproduction, so a
	// report can distinguish a run that produced no repository from one that
	// was never asked for one.
	Publishing bool
	// Progress is called after every state change, so a caller can print
	// progress and a test can observe it.
	Progress func(State)
}

// DefaultParallel returns how many papers run at once when Options.Parallel
// is not set: half the cores, capped at four, and at least one.
func DefaultParallel() int {
	half := runtime.NumCPU() / 2
	if half > 4 {
		return 4
	}
	if half < 1 {
		return 1
	}
	return half
}

// ErrIncomplete reports that at least one paper failed. The batch itself
// completed: one paper failing does not fail the rest, and the exit code
// carries the difference to CI.
var ErrIncomplete = errors.New("one or more papers failed")

// Run drives a whole project. Every paper gets its own session; a failure in
// one is recorded and the pool goes on.
func Run(ctx context.Context, file File, runner Runner, options Options) (State, error) {
	entries, err := file.Entries()
	if err != nil {
		return State{}, err
	}
	if options.Clock == nil {
		options.Clock = func() time.Time { return time.Now().UTC() }
	}
	parallel := options.Parallel
	if parallel <= 0 {
		parallel = DefaultParallel()
	}

	state := State{Version: stateVersion, Project: file.Project, Publishing: options.Publishing, StartedAt: options.Clock()}
	if options.Resume {
		if previous, err := LoadState(options.StatePath); err == nil {
			state = previous.mergedWith(entries, options.Clock())
			state.Publishing = options.Publishing
		}
	}
	if len(state.Entries) != len(entries) {
		state.Entries = make([]EntryState, len(entries))
		for index, entry := range entries {
			state.Entries[index] = EntryState{Paper: entry.Reference(), Run: entry.Run, Phase: Pending}
		}
	}

	var (
		mu       sync.Mutex
		prepare  sync.Mutex
		wg       sync.WaitGroup
		slots    = make(chan struct{}, parallel)
		anyFails bool
	)
	save := func() {
		if options.StatePath != "" {
			_ = state.Save(options.StatePath)
		}
		if options.Progress != nil {
			options.Progress(state.clone())
		}
	}
	mu.Lock()
	save()
	mu.Unlock()

	for index, entry := range entries {
		mu.Lock()
		skip := state.Entries[index].Phase.Terminal()
		mu.Unlock()
		if skip {
			continue
		}
		wg.Add(1)
		go func(index int, entry Resolved) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()

			mu.Lock()
			// What an earlier attempt recorded is passed to the runner with
			// the entry, so a retry can return to its repository.
			prior := state.Entries[index]
			state.Entries[index].Phase = Running
			state.Entries[index].Started = options.Clock()
			save()
			mu.Unlock()

			// Prepare is serialized: allocating a name is a read of what
			// exists followed by a decision, so two concurrent papers whose
			// slugs collide would decide the same name.
			prepare.Lock()
			err := runner.Prepare(ctx, entry, prior)
			prepare.Unlock()

			var outcome Outcome
			if err == nil {
				outcome, err = runner.Execute(ctx, entry)
			}

			mu.Lock()
			record := &state.Entries[index]
			record.Finished = options.Clock()
			record.absorb(outcome)
			if err != nil {
				record.Phase, record.Error = Failed, err.Error()
				anyFails = true
			} else {
				record.Phase = Done
			}
			save()
			mu.Unlock()
		}(index, entry)
	}
	wg.Wait()

	mu.Lock()
	// An interrupted batch keeps whatever ran. Nothing is lost to an
	// interrupt, and the entries still pending are recorded as pending rather
	// than failed.
	state.FinishedAt = options.Clock()
	save()
	final := state.clone()
	failed := anyFails
	mu.Unlock()

	if ctx.Err() != nil {
		return final, ctx.Err()
	}
	if failed {
		return final, ErrIncomplete
	}
	return final, nil
}

// Root is the directory a project's state and report live in.
func Root(projectPath string) string { return filepath.Dir(projectPath) }
