// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"strings"
	"time"
)

// Event is one entry in a run's transcript: the backend's record of what
// happened during the run, translated to a neutral shape. Raw holds the
// backend's original event, so the translation loses nothing.
type Event struct {
	Role    Role            `json:"role"`
	Kind    EventKind       `json:"kind"`
	Tool    string          `json:"tool,omitempty"` // the tool for a tool_use, else empty
	Content string          `json:"content,omitempty"`
	Time    time.Time       `json:"time"`
	Usage   *Usage          `json:"usage,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"` // the backend's original event
}

// Role is who an event is attributed to. The set is closed: every backend event
// is mapped to exactly one of these when it is translated.
type Role string

// The event roles.
const (
	RoleAgent  Role = "agent"
	RoleUser   Role = "user"
	RoleSystem Role = "system"
)

// EventKind is what an event is. The well-known kinds below are named so
// callers compare against a constant rather than a bare string. The set is
// open: a backend-native kind such as "model_request_end" passes through
// unchanged rather than being dropped or coerced.
type EventKind string

// The well-known event kinds. Others pass through as the backend names them.
const (
	KindMessage    EventKind = "message"
	KindThinking   EventKind = "thinking"
	KindToolUse    EventKind = "tool_use"
	KindToolResult EventKind = "tool_result"
)

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
// An audit uses Task, Rubric, MaxRevisions and Mount. An experiment (007b)
// reuses the same contract with an empty rubric, no revision budget, a
// read-only mount, and a dataset in Attachments, so there is one contract
// rather than a second execution path.
type RunSpec struct {
	Task         string
	Rubric       string
	MaxRevisions int
	Mount        RepoMount
	Attachments  []Attachment
}

// Attachment is a file placed into a run's sandbox outside any mounted
// repository, such as an experiment's input dataset or a file it must read
// without committing. Because the file is outside the mount, a researcher's
// data cannot be committed into a reproduction repository. This is the primary
// control behind 007's rule that uploaded data is not ours to redistribute.
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
// ReadOnly requests a mount the worker must not push to: an experiment reads an
// instrument and must not write to the reproduction (007b). The field records
// the intent; enforcement is the token's GitHub scope, so a read-only mount
// must be given a read-only-scoped token. That scoping is the two-token split
// still open in 006f, so ReadOnly is currently a signal and not a guarantee
// the backend enforces.
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
	Files     []string
}

// ResourceReleaser is implemented by transitional adapters that can release
// the worker and workspace created before a run handle exists.
type ResourceReleaser interface {
	ReleaseResources(context.Context, WorkerRef, WorkspaceRef) error
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
//
// The classes are separate because they are priced separately (011): a cache
// write costs more than fresh input and a cache read costs far less, so a
// single input number cannot be priced. A backend that reports only a total for
// one class must not fold it into another; CacheWriteTokens exists for that
// case.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
	// CachedInputTokens were served from cache rather than processed fresh.
	CachedInputTokens int64
	// CacheWrite5mTokens and CacheWrite1hTokens wrote the prompt cache, split by
	// the lifetime bought. The two are priced differently and the longer
	// lifetime costs more, so combining them would under-bill one and
	// overcharge the other.
	CacheWrite5mTokens int64
	CacheWrite1hTokens int64
	// CacheWriteTokens wrote the cache with the lifetime unreported, which is
	// what a per-request span carries; the session total is broken down. It is
	// a separate field rather than added to one of the two above so that the
	// unknown lifetime stays visible. Pricing charges it at the dearer rate,
	// the same rule that applies to an unknown model.
	CacheWriteTokens int64
}

// Total is every token the run accounted for.
func (u Usage) Total() int64 {
	return u.InputTokens + u.OutputTokens + u.CachedInputTokens +
		u.CacheWrite5mTokens + u.CacheWrite1hTokens + u.CacheWriteTokens
}

// CacheWritten is every cache-write token regardless of the lifetime bought,
// for readers who do not need the split.
func (u Usage) CacheWritten() int64 {
	return u.CacheWrite5mTokens + u.CacheWrite1hTokens + u.CacheWriteTokens
}

// Add accumulates another observation into this one, class by class. It is the
// only place usages are summed, so a class added to the struct cannot be missed
// by a caller summing fields by hand.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CachedInputTokens += o.CachedInputTokens
	u.CacheWrite5mTokens += o.CacheWrite5mTokens
	u.CacheWrite1hTokens += o.CacheWrite1hTokens
	u.CacheWriteTokens += o.CacheWriteTokens
}

// RunStatus is one observation of a run.
type RunStatus struct {
	State   RunState
	Verdict *Verdict
	Usage   Usage
	// RunID is the backend's identifier for the run in flight, the
	// managed-agents session id. The worker persists it so that after a
	// restart it can close a session the previous process was driving;
	// otherwise a run left in flight keeps running on the backend and cannot
	// be reached.
	RunID string `json:"run_id,omitempty"`
	// Runtime and SessionRef are the transition envelope used while durable job
	// storage moves to the managed-session contract. SessionRef is the complete,
	// opaque managed.SessionRef JSON; core code persists it without parsing its
	// locator. A missing value is decoded only by job's explicit legacy path.
	Runtime    string          `json:"runtime,omitempty"`
	SessionRef json.RawMessage `json:"session_ref,omitempty"`
	// Model is the model the backend ran, in the backend's own naming. The
	// engine asks for a preference such as "best" and the backend resolves
	// it, so this is the only place the concrete model appears, and
	// settlement prices the usage by it (011). It is empty where a backend
	// does not report it, and such usage is charged at the dearest rates on
	// the card.
	Model          string `json:"model,omitempty"`
	ModelProvider  string `json:"model_provider,omitempty"`
	BillingVariant string `json:"billing_variant,omitempty"`
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
// Every method that can block takes a context and must honour its
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

	// Transcript returns the run's full event stream, the backend's record of
	// what the worker did, kept as evidence (005). Fetch it before CloseRun,
	// which may make the run unreadable.
	Transcript(ctx context.Context, h RunHandle) ([]Event, error)

	// CloseRun stops any live work and releases the run. It must be safe to call
	// more than once and on a run that has already finished.
	CloseRun(ctx context.Context, h RunHandle) error
}
