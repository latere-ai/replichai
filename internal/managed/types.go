// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package managed

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"time"
)

// RuntimeName identifies one managed runtime adapter, which is the component
// that executes a session. It is not the same as the vendor that bills for the
// model the session uses. Every durable reference carries one, so the name
// must be canonical for a persisted session to route back to the adapter that
// started it.
type RuntimeName string

// CanonicalRuntimeName folds operator-supplied text into the single spelling a
// RuntimeName may take. Routes, catalog entries, and references are compared
// as plain strings, so "Anthropic" and " anthropic " must not become two
// runtimes; validation rejects any name that is not already its own canonical
// form.
func CanonicalRuntimeName(name string) RuntimeName {
	return RuntimeName(strings.ToLower(strings.TrimSpace(name)))
}

// ModelRef is the exact, provider-qualified model a session ran on, and the key
// pricing resolves a rate against. Provider is the inference and cost source
// rather than the runtime, so a session may run on one runtime and bill against
// another vendor's model. BillingVariant records a price-affecting applied
// class such as a service tier, and stays empty where the provider has one
// price.
type ModelRef struct {
	Provider       string `json:"provider"`
	ID             string `json:"id"`
	BillingVariant string `json:"billing_variant,omitempty"`
}

// Valid reports whether the reference names a model concretely enough to price
// and to record. Both halves are required: an id without a provider cannot be
// matched to a rate card, because two vendors may publish the same id.
func (m ModelRef) Valid() bool {
	return strings.TrimSpace(m.Provider) != "" && strings.TrimSpace(m.ID) != ""
}

// ModelSelector is how a specification asks for a model. Exactly one form is
// set: a preference tier the runtime resolves for itself (best, balanced,
// fast), or one exact reference the caller insists on. Either way the runtime
// reports the model it actually resolved in Status, so evidence never depends
// on the tier being re-resolved later.
type ModelSelector struct {
	Preference string    `json:"preference,omitempty"`
	Exact      *ModelRef `json:"exact,omitempty"`
}

// Model is one model a runtime offers, with the preference tiers it can serve.
// Tier resolution matches a requested preference against Preferences, so one
// reference may answer to several tiers where a runtime offers only one
// model.
type Model struct {
	Ref         ModelRef `json:"ref"`
	Preferences []string `json:"preferences,omitempty"`
}

// Package is one dependency installed into the sandbox before the session runs.
// Manager is drawn from the set an adapter declares it supports: a manager the
// adapter cannot install fails validation rather than being skipped so the
// session runs without its dependency. An empty Version leaves the choice to
// the manager.
type Package struct {
	Manager string `json:"manager"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// NetworkPolicy is the sandbox's egress rule. Mode governs the other two
// fields: none rejects both, allowlist requires at least one domain, and open
// refuses host-bound secrets. SecretRefs maps an environment variable name to a
// "domain:name" vault reference whose domain must itself be allowlisted; a
// specification carries references only, never secret values.
type NetworkPolicy struct {
	Mode           NetworkMode       `json:"mode"`
	AllowedDomains []string          `json:"allowed_domains,omitempty"`
	SecretRefs     map[string]string `json:"secret_refs,omitempty"`
}

// SandboxSpec is the machine the work happens on, stated explicitly so an
// adapter can refuse what it cannot provide before anything remote is created.
// Bootstrap entries are argv arrays rather than shell fragments, so nothing in
// a specification is handed to a shell to interpret.
type SandboxSpec struct {
	// Environment names what the machine runs, such as "science": a
	// published image with the tools a kind of work needs already in it.
	// Empty is the runtime's default. A runtime that does not offer the
	// named environment refuses the specification rather than running the
	// session on another.
	Environment string        `json:"environment,omitempty"`
	Packages    []Package     `json:"packages,omitempty"`
	Bootstrap   [][]string    `json:"bootstrap,omitempty"`
	Network     NetworkPolicy `json:"network"`
}

// AttachmentResource is one file placed into the sandbox outside any mounted
// repository, such as an input dataset the session must read but must not
// commit. SHA256 is verified against Content during validation, so a corrupted
// or substituted payload fails the specification instead of the run.
type AttachmentResource struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type,omitempty"`
	Content   []byte `json:"content"`
	SHA256    string `json:"sha256"`
}

// RepositoryResource is a version-control repository the session works in. A
// runtime may mount it, clone it, or stage it some other way, as long as the
// resulting filesystem and write authority match Access. CredentialRef names a
// credential and never carries one; read-write access requires it, because an
// unauthenticated write mount would fail only once the session was already
// running.
type RepositoryResource struct {
	URL           string     `json:"url"`
	Revision      string     `json:"revision,omitempty"`
	Destination   string     `json:"destination"`
	Access        AccessMode `json:"access"`
	CredentialRef string     `json:"credential_ref,omitempty"`
}

// Resource is the tagged union of things a session can be given. Kind selects
// which payload is set, and exactly one must be. An unknown kind fails
// validation rather than being ignored, so a session never runs quietly missing
// an input the caller asked for.
type Resource struct {
	Kind       ResourceKind        `json:"kind"`
	Attachment *AttachmentResource `json:"attachment,omitempty"`
	Repository *RepositoryResource `json:"repository,omitempty"`
	Input      *InputResource      `json:"input,omitempty"`
}

// InputResource is a file the session reads and may not change, such as the
// copy of a paper every run of a study is given (046). The runtime places it
// at Path under Capabilities.InputDir, outside the workspace, and mounts it
// read-only, so the agent can neither edit it nor commit it into the
// reproduction. SHA256 is verified against Content during validation, as for
// an attachment, and is what a run's record cites as the input it was given.
type InputResource struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type,omitempty"`
	Content   []byte `json:"content"`
	SHA256    string `json:"sha256"`
}

// EvaluationSpec makes a session graded. A nil *EvaluationSpec on SessionSpec
// is the only way to say "ungraded", which is why a present one must carry a
// non-empty rubric: a zero value must not be readable as grading against
// nothing. MaxRevisions may be zero, which grades the first attempt and never
// revises it, so a study can keep what a run produced before a rubric pushed
// it (046).
//
// Findings names the workspace file that holds the run's findings as data,
// verdict.json for an audit (021a). A runtime that declares FindingsRounds
// copies it after every worker-side stage, checks it against its schema, and
// gives the problems to the grader and to the revision, so a file the system
// cannot read is never passed on a model's reading of the rubric. Empty asks
// for none of this.
type EvaluationSpec struct {
	Rubric       string `json:"rubric"`
	MaxRevisions int    `json:"max_revisions"`
	Findings     string `json:"findings,omitempty"`
}

// SessionSpec is the complete product intent for one managed session,
// independent of the runtime that will execute it. Instructions is the worker's
// durable role and Task is this session's request; an adapter with no
// persistent worker may combine the two on the wire, but may not reorder them
// or change what they mean.
type SessionSpec struct {
	Model        ModelSelector   `json:"model"`
	Instructions string          `json:"instructions"`
	Task         string          `json:"task"`
	Sandbox      SandboxSpec     `json:"sandbox"`
	Resources    []Resource      `json:"resources,omitempty"`
	Evaluation   *EvaluationSpec `json:"evaluation,omitempty"`
}

// SessionRef is the durable handle to a session: everything needed to poll it,
// read its evidence, cancel it, and release it after the process that started
// it has gone. Runtime and ID are fixed for the session's life and Revision
// only ever increases, so a checkpoint can be persisted with compare-and-swap.
// Locator is adapter-owned recovery metadata that core code stores and returns
// byte-for-byte but never parses; LocatorVersion versions its shape, and it is
// size-limited and forbidden from carrying credentials or user content.
type SessionRef struct {
	Runtime        RuntimeName     `json:"runtime"`
	ID             string          `json:"id"`
	Revision       uint64          `json:"revision"`
	LocatorVersion int             `json:"locator_version"`
	Locator        json.RawMessage `json:"locator,omitempty"`
}

// SameIdentity reports whether two references name the same session, ignoring
// the checkpoint. It is the guard that stops a status observation or a
// persisted update from being applied to a different session: only Revision and
// Locator are allowed to move between observations.
func (r SessionRef) SameIdentity(other SessionRef) bool {
	return r.Runtime == other.Runtime && r.ID == other.ID
}

// Verdict is the evaluator's decision about a graded session. Explanation is
// the reasoning a reader is shown alongside the grade; a runtime that gives
// none leaves it empty rather than inventing one.
type Verdict struct {
	Grade       Grade  `json:"grade"`
	Explanation string `json:"explanation,omitempty"`
}

// Passed reports whether the evaluator decided for the session. Every
// other grade, undecided ones included, is not a pass, so the question is safe
// to ask before the evaluation has finished.
func (v Verdict) Passed() bool { return v.Grade == GradePassed }

// Meter names a billable quantity that is not a token bucket, such as a
// container second or a hosted tool call. The set is open because each runtime
// emits its own, but a meter must be priced on the rate card before a session
// using it can settle.
type Meter string

// MeterChargedCost is the micro-USD a runtime's own platform has already
// charged someone for the session, such as the Latere platform charging the
// person who started it. It is reported, never priced: a rate card that
// priced it would charge the same money a second time.
const MeterChargedCost Meter = "charged.cost_usd_micro"

// Usage is the cumulative, provider-neutral cost of a session so far. The six
// token buckets are mutually exclusive so that each is priced exactly once.
// Extra holds billable non-token meters, and Details holds a non-billable
// breakdown, such as reasoning tokens already counted in Output, which pricing
// never iterates. Anything the runtime did not report is zero; it is never an
// estimate presented as provider data.
type Usage struct {
	Model        ModelRef         `json:"model"`
	InputFresh   int64            `json:"input_fresh"`
	InputCached  int64            `json:"input_cached"`
	CacheWrite   int64            `json:"cache_write"`
	CacheWrite5m int64            `json:"cache_write_5m"`
	CacheWrite1h int64            `json:"cache_write_1h"`
	Output       int64            `json:"output"`
	Extra        map[Meter]int64  `json:"extra,omitempty"`
	Details      map[string]int64 `json:"details,omitempty"`
}

// DetailVendorCost is the Usage.Details key under which a harness reports what
// the session cost at its vendor, in millionths of a dollar. It is a report,
// not a charge: nothing here bills from it.
const DetailVendorCost = "vendor_cost_micro_usd"

// TotalTokens sums every token bucket. Because the buckets are mutually
// exclusive it double-counts nothing, but it is a figure for display and sanity
// checks rather than for billing: the buckets carry different rates.
func (u Usage) TotalTokens() int64 {
	return u.InputFresh + u.InputCached + u.CacheWrite + u.CacheWrite5m + u.CacheWrite1h + u.Output
}

// CacheWritten is every cache-write bucket together, whatever cache lifetime
// the provider charged for. A caller asking how much was written to cache
// should not have to know how many time-to-live variants exist.
func (u Usage) CacheWritten() int64 { return u.CacheWrite + u.CacheWrite5m + u.CacheWrite1h }

// Add accumulates another usage into u, bucket by bucket and meter by meter, so
// a session's stages can be totaled. A zero Usage is a usable accumulator: the
// maps are allocated only when the other side has something to put in them.
func (u *Usage) Add(other Usage) {
	u.InputFresh += other.InputFresh
	u.InputCached += other.InputCached
	u.CacheWrite += other.CacheWrite
	u.CacheWrite5m += other.CacheWrite5m
	u.CacheWrite1h += other.CacheWrite1h
	u.Output += other.Output
	if len(other.Extra) > 0 && u.Extra == nil {
		u.Extra = make(map[Meter]int64, len(other.Extra))
	}
	for meter, value := range other.Extra {
		u.Extra[meter] += value
	}
	if len(other.Details) > 0 && u.Details == nil {
		u.Details = make(map[string]int64, len(other.Details))
	}
	for name, value := range other.Details {
		u.Details[name] += value
	}
}

// MonotonicFrom reports whether u is a valid successor to previous: no token
// bucket and no extra meter may decrease. Usage is defined as the session
// total, so a decrease means the runtime sent a per-call delta or reset its
// counters, and the controller treats that as a contract violation rather than
// accepting it.
func (u Usage) MonotonicFrom(previous Usage) bool {
	if u.InputFresh < previous.InputFresh || u.InputCached < previous.InputCached ||
		u.CacheWrite < previous.CacheWrite || u.CacheWrite5m < previous.CacheWrite5m ||
		u.CacheWrite1h < previous.CacheWrite1h || u.Output < previous.Output {
		return false
	}
	for meter, value := range previous.Extra {
		if u.Extra[meter] < value {
			return false
		}
	}
	return true
}

// Status is one observation of a session: where it has got to, what it has cost
// so far, and once terminal, why it stopped. Between observations only the
// reference's Revision and Locator may advance, state moves forwards only, and
// a terminal observation stays terminal however often the session is polled. An
// ungraded session finishes with no Verdict at all; a graded one may not finish
// without a decided verdict.
type Status struct {
	Ref            SessionRef     `json:"ref"`
	Model          ModelRef       `json:"model"`
	State          State          `json:"state"`
	StartedAt      time.Time      `json:"started_at"`
	FinishedAt     time.Time      `json:"finished_at"`
	Usage          Usage          `json:"usage"`
	TerminalReason TerminalReason `json:"terminal_reason,omitempty"`
	Error          string         `json:"error,omitempty"`
	Verdict        *Verdict       `json:"verdict,omitempty"`
}

// Passed reports whether the session finished with a passing verdict. A session
// with no verdict was never graded, which is not the same as success, so it
// does not pass.
func (s Status) Passed() bool { return s.Verdict != nil && s.Verdict.Passed() }

// Event is one entry in a session's transcript, normalized across runtimes. ID
// is stable, so re-reading the transcript neither duplicates entries nor
// changes what an existing one means. Raw keeps the runtime's own payload
// intact, because the transcript is the evidence for a run and must not be
// reduced to this package's vocabulary.
type Event struct {
	ID      string          `json:"id"`
	Role    Role            `json:"role"`
	Kind    EventKind       `json:"kind"`
	Tool    string          `json:"tool,omitempty"`
	Content string          `json:"content,omitempty"`
	Time    time.Time       `json:"time"`
	Usage   *Usage          `json:"usage,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

// Artifact is one file the session produced. Path is a clean, relative,
// slash-separated path, unique within the session, and is what a reader sees;
// ID is the runtime's own handle for the same file and is passed back when
// fetching it, since not every backend addresses a file by path.
type Artifact struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// IsCompiled reports whether the artifact is build residue rather than a
// result. Such files are machine-specific and must never reach a reproduction
// repository. The path is normalized before the test, so a backslash-separated
// path cannot smuggle a __pycache__ entry past it.
func (a Artifact) IsCompiled() bool {
	clean := path.Clean(strings.ReplaceAll(a.Path, "\\", "/"))
	return strings.HasSuffix(clean, ".pyc") || strings.HasSuffix(clean, ".pyo") || strings.Contains(clean, "__pycache__/")
}

// Capabilities is what a runtime can actually do, stated as semantic flags and
// limits rather than vendor product names. It exists so a specification is
// refused before anything remote is created: there is no best-effort mode in
// which an unsupported package manager, network mode, or oversized attachment
// is silently dropped. OutputDir is the sandbox path a session is told to write
// its artifacts to, which instructions are templated against.
type Capabilities struct {
	OutputDir string
	// InputDir is the sandbox path inputs are placed under, for the session
	// OutputDir names. Empty means the runtime cannot hand a session a file
	// it can read and not change, and a specification carrying an input is
	// refused.
	InputDir        string
	PackageManagers map[string]bool
	// Environments are the environments the runtime offers by name. A
	// runtime that offers none runs every session on its default, and
	// refuses a specification that names one.
	Environments       map[string]bool
	NetworkModes       map[NetworkMode]bool
	RepositoryModes    map[AccessMode]bool
	Evaluation         bool
	DurableEvents      bool
	Artifacts          bool
	Cancellation       bool
	MaxAttachmentBytes int64
	MaxLocatorBytes    int
	// FindingsRounds reports that the runtime keeps the findings file of
	// every grading round and checks it before the grader reads it, which an
	// EvaluationSpec asks for by naming Findings. A runtime that grades on a
	// vendor's side cannot reach inside that loop and declares false.
	FindingsRounds bool
	// LiveArtifacts reports that ListArtifacts and FetchArtifact may be
	// called while the session runs, and return what it has saved so far.
	// The controller then takes snapshots of the work as the session runs
	// (049); a runtime that does not declare it is asked for its files only
	// once the session has ended.
	LiveArtifacts bool
}

// Runtime is one backend able to execute a managed session. The interface is
// deliberately small enough to fake, because every adapter is held to the same
// conformance suite. Validate is pure and mandatory, and Start repeats it
// defensively before any remote write; Start returns a reference only once that
// reference alone is enough to recover the session. Evidence stays readable
// after a session goes terminal or is cancelled until Release completes, which
// is why capture must precede release.
type Runtime interface {
	Name() RuntimeName
	Capabilities(context.Context) (Capabilities, error)
	Models(context.Context) ([]Model, error)
	Validate(SessionSpec) error
	Start(context.Context, SessionSpec) (SessionRef, error)
	Poll(context.Context, SessionRef) (Status, error)
	Events(context.Context, SessionRef) ([]Event, error)
	ListArtifacts(context.Context, SessionRef) ([]Artifact, error)
	FetchArtifact(context.Context, SessionRef, Artifact) ([]byte, error)
	Cancel(context.Context, SessionRef) error
	Release(context.Context, SessionRef) error
}
