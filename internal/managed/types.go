package managed

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"time"
)

type RuntimeName string

func CanonicalRuntimeName(name string) RuntimeName {
	return RuntimeName(strings.ToLower(strings.TrimSpace(name)))
}

type ModelRef struct {
	Provider       string `json:"provider"`
	ID             string `json:"id"`
	BillingVariant string `json:"billing_variant,omitempty"`
}

func (m ModelRef) Valid() bool {
	return strings.TrimSpace(m.Provider) != "" && strings.TrimSpace(m.ID) != ""
}

type ModelSelector struct {
	Preference string    `json:"preference,omitempty"`
	Exact      *ModelRef `json:"exact,omitempty"`
}

type Model struct {
	Ref         ModelRef `json:"ref"`
	Preferences []string `json:"preferences,omitempty"`
}

type Package struct {
	Manager string `json:"manager"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type NetworkPolicy struct {
	Mode           NetworkMode       `json:"mode"`
	AllowedDomains []string          `json:"allowed_domains,omitempty"`
	SecretRefs     map[string]string `json:"secret_refs,omitempty"`
}

type SandboxSpec struct {
	Packages  []Package     `json:"packages,omitempty"`
	Bootstrap [][]string    `json:"bootstrap,omitempty"`
	Network   NetworkPolicy `json:"network"`
}

type AttachmentResource struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type,omitempty"`
	Content   []byte `json:"content"`
	SHA256    string `json:"sha256"`
}

type RepositoryResource struct {
	URL           string     `json:"url"`
	Revision      string     `json:"revision,omitempty"`
	Destination   string     `json:"destination"`
	Access        AccessMode `json:"access"`
	CredentialRef string     `json:"credential_ref,omitempty"`
}

type Resource struct {
	Kind       ResourceKind        `json:"kind"`
	Attachment *AttachmentResource `json:"attachment,omitempty"`
	Repository *RepositoryResource `json:"repository,omitempty"`
}

type EvaluationSpec struct {
	Rubric       string `json:"rubric"`
	MaxRevisions int    `json:"max_revisions"`
}

type SessionSpec struct {
	Model        ModelSelector   `json:"model"`
	Instructions string          `json:"instructions"`
	Task         string          `json:"task"`
	Sandbox      SandboxSpec     `json:"sandbox"`
	Resources    []Resource      `json:"resources,omitempty"`
	Evaluation   *EvaluationSpec `json:"evaluation,omitempty"`
}

type SessionRef struct {
	Runtime        RuntimeName     `json:"runtime"`
	ID             string          `json:"id"`
	Revision       uint64          `json:"revision"`
	LocatorVersion int             `json:"locator_version"`
	Locator        json.RawMessage `json:"locator,omitempty"`
}

func (r SessionRef) SameIdentity(other SessionRef) bool {
	return r.Runtime == other.Runtime && r.ID == other.ID
}

type Verdict struct {
	Grade       Grade  `json:"grade"`
	Explanation string `json:"explanation,omitempty"`
}

func (v Verdict) Passed() bool { return v.Grade == GradePassed }

type Meter string

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

func (u Usage) TotalTokens() int64 {
	return u.InputFresh + u.InputCached + u.CacheWrite + u.CacheWrite5m + u.CacheWrite1h + u.Output
}

func (u Usage) CacheWritten() int64 { return u.CacheWrite + u.CacheWrite5m + u.CacheWrite1h }

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

func (s Status) Passed() bool { return s.Verdict != nil && s.Verdict.Passed() }

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

type Artifact struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

func (a Artifact) IsCompiled() bool {
	clean := path.Clean(strings.ReplaceAll(a.Path, "\\", "/"))
	return strings.HasSuffix(clean, ".pyc") || strings.HasSuffix(clean, ".pyo") || strings.Contains(clean, "__pycache__/")
}

type Capabilities struct {
	OutputDir          string
	PackageManagers    map[string]bool
	NetworkModes       map[NetworkMode]bool
	RepositoryModes    map[AccessMode]bool
	Evaluation         bool
	DurableEvents      bool
	Artifacts          bool
	Cancellation       bool
	MaxAttachmentBytes int64
	MaxLocatorBytes    int
	BillableMeters     []Meter
}

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
