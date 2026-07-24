// Package provider defines the contract between the audit orchestrator and
// whatever backend actually runs the work.
//
// A provider is any backend that can run a long-lived, tool-using worker inside
// a sandbox and grade its output against a rubric. The orchestrator talks only
// to this interface, so supporting a new backend means writing one adapter.
package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrUnknownProvider is returned by Get for a name that is not registered.
var ErrUnknownProvider = errors.New("unknown provider")

// WorkspaceSpec describes the sandbox a worker runs inside.
type WorkspaceSpec struct {
	Name     string
	Packages map[string][]string
	Network  Network
}

// WorkerSpec describes the worker itself.
type WorkerSpec struct {
	Name         string
	Instructions string
	Model        string
	Description  string
}

// RunSpec is one unit of work: a task, the bar it is graded against, and a
// budget for how many times the worker may revise before giving up.
type RunSpec struct {
	Task         string
	Rubric       string
	MaxRevisions int
}

// WorkspaceRef identifies a sandbox. It, WorkerRef, and RunHandle are opaque:
// only the adapter that produced an ID interprets it.
type WorkspaceRef struct{ ID string }

// WorkerRef identifies a worker and the revision of it that is pinned.
type WorkerRef struct {
	ID       string
	Revision int
}

// RunHandle identifies one run in flight.
type RunHandle struct {
	ID        string
	Worker    WorkerRef
	Workspace WorkspaceRef
}

// Verdict is the grader's decision about a run.
type Verdict struct {
	Grade       Grade
	Explanation string
}

// Passed reports whether the run met the rubric.
func (v Verdict) Passed() bool { return v.Grade == GradePassed }

// Usage is what a run consumed, in tokens.
//
// Every backend meters tokens, so these fields are neutral rather than
// vendor-specific. A backend that does not report one leaves it zero.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
	// CachedInputTokens were served from cache rather than processed fresh.
	CachedInputTokens int64
}

// Total is every token the run accounted for.
func (u Usage) Total() int64 {
	return u.InputTokens + u.OutputTokens + u.CachedInputTokens
}

// RunStatus is one observation of a run.
type RunStatus struct {
	State   RunState
	Verdict *Verdict
	Usage   Usage
}

// Passed reports whether the run finished and met the rubric. A run with no
// recorded verdict has not passed, whatever its state.
func (s RunStatus) Passed() bool { return s.Verdict != nil && s.Verdict.Passed() }

// Artifact is one file a run produced.
type Artifact struct {
	ID   string
	Path string
}

// IsCompiled reports whether the artifact is build residue. Such files are
// machine-specific and must never reach a reproduction repository.
func (a Artifact) IsCompiled() bool {
	return strings.HasSuffix(a.Path, ".pyc") ||
		strings.HasSuffix(a.Path, ".pyo") ||
		strings.Contains(a.Path, "__pycache__/")
}

// Provider is a backend capable of running graded, sandboxed, tool-using work.
//
// Every method that can block takes a context and must honor its
// cancellation, so an audit can be stopped from outside.
type Provider interface {
	// Name identifies the adapter.
	Name() string

	// OutputDir is where inside the sandbox a worker should write its
	// deliverable. The orchestrator substitutes this into the instructions so
	// the method never hard-codes one backend's filesystem layout.
	OutputDir() string

	// ResolveModel turns a preference such as "best" into a concrete model
	// identifier. Adapters with no notion of tiers may return pref unchanged.
	ResolveModel(ctx context.Context, pref string) (string, error)

	EnsureWorkspace(ctx context.Context, spec WorkspaceSpec) (WorkspaceRef, error)
	EnsureWorker(ctx context.Context, spec WorkerSpec) (WorkerRef, error)

	// StartRun begins one run and returns without waiting for it.
	StartRun(ctx context.Context, worker WorkerRef, ws WorkspaceRef, spec RunSpec) (RunHandle, error)

	// Poll reports the current state of a run without blocking.
	Poll(ctx context.Context, h RunHandle) (RunStatus, error)

	// ListArtifacts lists what a run produced. Call only once it is terminal.
	ListArtifacts(ctx context.Context, h RunHandle) ([]Artifact, error)

	// FetchArtifact downloads one artifact's bytes.
	FetchArtifact(ctx context.Context, h RunHandle, a Artifact) ([]byte, error)

	// CloseRun releases a finished run. It must be safe to call more than once.
	CloseRun(ctx context.Context, h RunHandle) error
}

// Factory builds a provider.
type Factory func() (Provider, error)

// DefaultName is the provider used when none is specified.
const DefaultName = "anthropic"

var registry = map[string]Factory{}

// Register adds a provider factory under name. Adapters call this from init.
func Register(name string, f Factory) {
	registry[strings.ToLower(name)] = f
}

// Available lists the registered provider names, sorted.
func Available() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Get instantiates a provider by name. An empty name selects DefaultName.
func Get(name string) (Provider, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		key = DefaultName
	}
	f, ok := registry[key]
	if !ok {
		return nil, fmt.Errorf("%w %q; available: %s",
			ErrUnknownProvider, key, strings.Join(Available(), ", "))
	}
	return f()
}
