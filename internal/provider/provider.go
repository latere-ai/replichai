// Package provider defines the contract between the audit orchestrator and
// whatever backend actually runs the work.
//
// A provider is any backend that can run a long-lived, tool-using worker inside
// a sandbox and grade its output against a rubric. The orchestrator talks only
// to this interface, so supporting a new backend means writing one adapter.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Event is one entry in a run's transcript — the backend's own record of what
// happened during the run, translated to a neutral shape. Raw preserves the
// backend's original event losslessly, so nothing is lost to the translation.
type Event struct {
	Role    string          `json:"role"`           // "agent" | "user" | "system"
	Kind    string          `json:"kind"`           // "message" | "thinking" | "tool_use" | "tool_result" | ...
	Tool    string          `json:"tool,omitempty"` // the tool for a tool_use, else empty
	Content string          `json:"content,omitempty"`
	Time    time.Time       `json:"time"`
	Raw     json.RawMessage `json:"raw,omitempty"` // the backend's original event
}

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

// RunSpec is one unit of work: a task, the bar it is graded against, a budget
// for how many times the worker may revise before giving up, optionally a
// repository to mount, and optionally files to attach outside that mount.
//
// An audit uses Task/Rubric/MaxRevisions/Mount. An experiment (007b) reuses the
// same contract with an empty rubric and no revision budget, a read-only mount,
// and a dataset in Attachments — one substrate, not a second execution path.
type RunSpec struct {
	Task         string
	Rubric       string
	MaxRevisions int
	Mount        RepoMount
	Attachments  []Attachment
}

// Attachment is a file placed into a run's sandbox OUTSIDE any mounted
// repository — an experiment's input dataset, or a file it must read without
// committing. Keeping it outside the mount is structural: a researcher's data
// physically cannot be committed into a reproduction repository, which is the
// primary control behind 007's "uploaded data is not ours to redistribute".
type Attachment struct {
	// Path is where the file lands in the sandbox. It must be outside any mount.
	// Empty lets the backend choose a location, which is outside the mount by
	// construction.
	Path    string
	Content []byte
}

// RepoMount is a version-control repository to mount into the run's sandbox so
// the worker commits into it as it works. It is host-agnostic: the adapter maps
// it to its backend's resource (the anthropic adapter to a github_repository
// session resource). The zero value mounts nothing, so an audit not destined
// for publication runs unchanged.
//
// Token is a write credential that lives only inside the sandbox; it must be
// scoped to this one repository, never a broad organisation token.
//
// ReadOnly requests a mount the worker must not push to — an experiment reads an
// instrument, it has no business writing to the reproduction (007b). The seam
// carries the intent; the real enforcement is the token's GitHub scope, so a
// read-only mount must be handed a read-only-scoped token. That scoping is
// 006f's still-open two-token split, so today ReadOnly is an honest signal, not
// a guarantee the backend imposes on its own.
type RepoMount struct {
	URL       string
	Token     string
	MountPath string
	ReadOnly  bool
}

// Set reports whether a repository is to be mounted.
func (m RepoMount) Set() bool { return m.URL != "" }

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

	// Transcript returns the run's full event stream — the backend's record of
	// what the worker did, as evidence (005). Fetch it before CloseRun, which
	// may make the run unreadable.
	Transcript(ctx context.Context, h RunHandle) ([]Event, error)

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
