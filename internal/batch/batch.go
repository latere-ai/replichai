// Package batch runs one replication project: a list of papers, a concurrency
// cap, one isolated session each, resumable state, and a report at the end
// (045h).
//
// A replication project is a list of papers, and running it has meant one
// invocation and one terminal per paper. Nothing shared a report, nothing
// survived a laptop sleeping, and a failure in the fourth was discovered by
// scrolling back.
package batch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v4"

	"github.com/latere-ai/replichai/internal/managed"
)

// Entry is one paper in a project, with the overrides that apply to it.
type Entry struct {
	// Paper is the reference: a URL, a DOI, or an arXiv id.
	Paper string `yaml:"paper"`
	// DOI is an alternative spelling of Paper, so a list of DOIs reads
	// naturally in the file.
	DOI string `yaml:"doi"`
	// Model, Harness and Sandbox override the file's defaults for this
	// paper alone.
	Model   string `yaml:"model"`
	Harness string `yaml:"harness"`
	Sandbox string `yaml:"sandbox"`
}

// UnmarshalYAML accepts either spelling of one paper: a bare reference, which
// is what a list of papers reads like, or a mapping carrying overrides. A file
// that mixes the two is the normal case — most papers need no overrides.
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
	Project string        `yaml:"project"`
	Model   string        `yaml:"model"`
	Harness string        `yaml:"harness"`
	Sandbox string        `yaml:"sandbox"`
	Publish PublishConfig `yaml:"publish"`
	Papers  []Entry       `yaml:"papers"`
}

// Resolved is one entry with the file's defaults applied, which is what a
// runner is handed.
type Resolved struct {
	Entry
	Index int
}

// Entries applies the file's defaults to every paper. A paper naming nothing
// is an error rather than a silently skipped line: a project of twenty papers
// that quietly runs nineteen is worse than one that refuses.
func (f File) Entries() ([]Resolved, error) {
	resolved := make([]Resolved, 0, len(f.Papers))
	for index, entry := range f.Papers {
		if entry.Reference() == "" {
			return nil, fmt.Errorf("papers[%d]: names no paper", index)
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
		resolved = append(resolved, Resolved{Entry: entry, Index: index})
	}
	return resolved, nil
}

// ReadFile reads a project. A file whose first non-comment content is a plain
// reference is read as a list, so the smallest useful input is one paper per
// line; anything else is YAML.
func ReadFile(path string) (File, error) {
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
// reports false where the content is structured, so a YAML project is never
// misread as a list of odd-looking references.
func parseList(body string) ([]Entry, bool) {
	var entries []Entry
	for _, raw := range strings.Split(body, "\n") {
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

// Phase is where one entry has got to.
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

// EntryState is what happened to one paper.
type EntryState struct {
	Paper string `json:"paper"`
	// PaperKey is the offline paper identity, carried so a push files the
	// record against the paper without resolving it again.
	PaperKey  string             `json:"paper_key,omitempty"`
	Phase     Phase              `json:"state"`
	Session   string             `json:"session,omitempty"`
	Directory string             `json:"directory,omitempty"`
	Repo      string             `json:"repo,omitempty"`
	Grade     string             `json:"grade,omitempty"`
	Model     managed.ModelRef   `json:"model,omitempty"`
	Usage     managed.Usage      `json:"usage,omitempty"`
	Started   time.Time          `json:"started_at,omitempty"`
	Finished  time.Time          `json:"finished_at,omitempty"`
	Error     string             `json:"error,omitempty"`
	Ref       managed.SessionRef `json:"ref,omitzero"`
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

// Runner runs one paper. Prepare is serialised by the batch and Execute is
// not, which is the whole of the concurrency contract: allocating a
// repository name is a read followed by a decision, and two concurrent
// readers decide the same name.
type Runner interface {
	// Prepare claims whatever must be claimed exactly once — a repository
	// name, a repository — before the work starts.
	Prepare(ctx context.Context, entry Resolved) error
	// Execute runs the audit and publishes it.
	Execute(ctx context.Context, entry Resolved) (Outcome, error)
}

// Options configure one run of a batch.
type Options struct {
	// Parallel is how many papers run at once.
	Parallel int
	// StatePath is where the resumable state is written.
	StatePath string
	// Resume skips entries that already finished and re-runs the rest.
	Resume bool
	// Clock is the time source.
	Clock func() time.Time
	// Publishing says the project publishes each reproduction, so a report
	// can tell a run that produced no repository from one that was never
	// asked for one.
	Publishing bool
	// Progress is called after every state change, so a caller can print
	// and a test can watch.
	Progress func(State)
}

// ErrIncomplete reports that at least one paper failed. The batch itself
// completed: one paper failing never fails the rest, and the exit code is what
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
		parallel = 1
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
			state.Entries[index] = EntryState{Paper: entry.Reference(), Phase: Pending}
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
			state.Entries[index].Phase = Running
			state.Entries[index].Started = options.Clock()
			save()
			mu.Unlock()

			// Serialised: allocating a name is a read of what exists
			// followed by a decision, so two concurrent papers whose
			// slugs collide would decide the same one.
			prepare.Lock()
			err := runner.Prepare(ctx, entry)
			prepare.Unlock()

			var outcome Outcome
			if err == nil {
				outcome, err = runner.Execute(ctx, entry)
			}

			mu.Lock()
			record := &state.Entries[index]
			record.Finished = options.Clock()
			record.Session, record.Directory = outcome.Session, outcome.Directory
			record.PaperKey = outcome.PaperKey
			record.Repo, record.Grade = outcome.Repo, outcome.Grade
			record.Model, record.Usage, record.Ref = outcome.Model, outcome.Usage, outcome.Ref
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
	// An interrupted batch keeps whatever ran; nothing is lost to a Ctrl-C,
	// and the entries still pending are pending rather than failed.
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
