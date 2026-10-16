// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/runtimes"

	managed "latere.ai/x/managed-agents"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/instrument"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/verdict"
)

const repositoryCredentialRef = "audit-repository"

// SessionMount is a git repository mounted into a managed session so the agent
// commits its reproduction into it and pushes as it works, with that history
// published as part of the result (006f).
type SessionMount struct {
	URL       string
	Token     string
	MountPath string
	ReadOnly  bool
}

// Set reports whether a repository is to be mounted. A whitespace-only URL
// counts as unset, so a blank value read from configuration does not request a
// mount.
func (mount SessionMount) Set() bool { return strings.TrimSpace(mount.URL) != "" }

// SessionResult is everything one audit produced on the managed path, where the
// run is a durable runtimes.Controller session rather than a provider run.
type SessionResult struct {
	Paper        string
	Ref          managed.SessionRef
	Status       managed.Status
	Events       []managed.Event
	Artifacts    []managed.Artifact
	ArtifactDir  string
	MethodDigest string
	// PromptDigest identifies the instructions, task and rubric the session
	// was given. It is empty for a resumed session, whose specification was
	// fixed by the process that created it.
	PromptDigest         string
	InstrumentPresent    bool
	InstrumentViolations []instrument.Violation
	Released             bool
}

// Passed reports whether the run met the rubric.
func (result SessionResult) Passed() bool { return result.Status.Passed() }

// Grade reports the verdict's grade, or GradeUnknown when none was recorded.
func (result SessionResult) Grade() managed.Grade {
	if result.Status.Verdict == nil {
		return managed.GradeUnknown
	}
	return result.Status.Verdict.Grade
}

// SessionAuditor runs the same audit as Auditor, but through
// runtimes.Controller: the run is a durable session that can be started
// detached and resumed later, and the controller owns waiting and release.
type SessionAuditor struct {
	controller   *runtimes.Controller
	method       method.Method
	model        string
	maxRevisions int
	timeout      time.Duration
	unbounded    bool
	mount        SessionMount
	hooks        runtimes.Hooks
	// profile fixes the session's egress to a named profile, and paper is the
	// copy of the paper the session is handed. Both are set for every run of
	// a study, so its runs have the same inputs (046).
	profile   NetworkProfile
	paper     *paper.Copy
	artifacts []paper.Artifact
	// workspaceIsARepository reports that the runtime gives the session a git
	// repository without mounting one. This is the local path, where the host
	// pushes what was committed once the session has ended.
	workspaceIsARepository bool
}

// SessionOption configures a SessionAuditor.
type SessionOption func(*SessionAuditor)

// WithSessionModel overrides the model tier requested for the session.
func WithSessionModel(preference string) SessionOption {
	return func(a *SessionAuditor) { a.model = preference }
}

// ModelSelector reads a model choice as the command line and a job give it:
// a preference in either spelling (best, balanced, fast, or the Effort levels
// sustained, thorough, quick) or one exact model as provider/id. Anything
// else passes through as a preference, which is how an operator names a
// vendor's bare model id to a runtime that resolves one.
func ModelSelector(model string) managed.ModelSelector {
	model = strings.TrimSpace(model)
	if preference, ok := credit.ParsePreference(model); ok {
		return managed.ModelSelector{Preference: preference.String()}
	}
	if provider, id, ok := strings.Cut(model, "/"); ok && provider != "" && id != "" {
		return managed.ModelSelector{Exact: &managed.ModelRef{Provider: provider, ID: id}}
	}
	return managed.ModelSelector{Preference: model}
}

// WithSessionMaxRevisions overrides how many times the worker may revise. Zero
// grades the first attempt and never revises it, which is how a study keeps
// what a run produced before the rubric pushed it (046). A negative value is
// ignored, leaving DefaultMaxRevisions in place.
func WithSessionMaxRevisions(maximum int) SessionOption {
	return func(a *SessionAuditor) {
		if maximum >= 0 {
			a.maxRevisions = maximum
		}
	}
}

// WithSessionTimeout overrides how long a run may take before it is abandoned,
// re-arming the deadline if WithoutSessionTimeout had removed it.
func WithSessionTimeout(timeout time.Duration) SessionOption {
	return func(a *SessionAuditor) { a.timeout, a.unbounded = timeout, false }
}

// WithoutSessionTimeout removes the wall-clock deadline. The run then ends
// only when the managed session ends. The session is the source of truth for
// whether the work succeeded, and a deadline is only a weaker second signal.
func WithoutSessionTimeout() SessionOption {
	return func(a *SessionAuditor) { a.unbounded = true }
}

// WithLocalWorkspace declares that the session's output directory is a git
// repository with no remote, which the host publishes after the run. It
// selects the workspace protocol instead of the mount protocol. The two are
// mutually exclusive: a session either pushes from inside the sandbox or is
// pushed from outside it.
func WithLocalWorkspace() SessionOption {
	return func(a *SessionAuditor) { a.workspaceIsARepository = true }
}

// WithSessionMount mounts a repository into the session so the worker commits
// its reproduction directly, with the history published as part of the result.
// It applies when the session is created, so it affects Start and Run only.
func WithSessionMount(mount SessionMount) SessionOption {
	return func(a *SessionAuditor) { a.mount = mount }
}

// WithSessionHooks installs the caller's lifecycle hooks. Run and Resume wrap
// the Retain hook: artifacts and the instrument check land in the result first,
// and the caller's own Retain runs after them.
func WithSessionHooks(hooks runtimes.Hooks) SessionOption {
	return func(a *SessionAuditor) { a.hooks = hooks }
}

// NewSessionAuditor builds a SessionAuditor over controller, applying options
// on top of the package defaults.
func NewSessionAuditor(controller *runtimes.Controller, m method.Method, options ...SessionOption) *SessionAuditor {
	auditor := &SessionAuditor{
		controller: controller, method: m, model: DefaultModelPreference,
		maxRevisions: DefaultMaxRevisions, timeout: DefaultTimeout,
	}
	for _, option := range options {
		option(auditor)
	}
	return auditor
}

// Start creates the durable session for paper and returns its ref without
// waiting. This is the detached path: no deadline applies and no evidence is
// retained, so the caller that resumes the session collects the artifacts.
func (a *SessionAuditor) Start(ctx context.Context, paper string) (managed.SessionRef, error) {
	spec, ctx, err := a.spec(ctx, paper)
	if err != nil {
		return managed.SessionRef{}, err
	}
	return a.controller.Start(ctx, spec, a.hooks)
}

// Run audits paper end to end and writes the collected artifacts under
// destination. A deadline overrun is reported as ErrTimedOut joined to the
// context error, unless the auditor is unbounded. The result is populated even
// when err is non-nil, so a failed run still carries what was retained.
func (a *SessionAuditor) Run(ctx context.Context, paper, destination string) (SessionResult, error) {
	spec, startCtx, err := a.spec(ctx, paper)
	if err != nil {
		return SessionResult{Paper: paper}, err
	}
	runCtx, cancel := a.runContext(startCtx)
	defer cancel()
	result := SessionResult{Paper: paper, ArtifactDir: destination, MethodDigest: a.method.Digest(), PromptDigest: promptDigest(spec)}
	hooks := a.retentionHooks(&result, destination)
	managedResult, err := a.controller.Execute(runCtx, spec, hooks)
	a.apply(&result, managedResult)
	if errors.Is(err, context.DeadlineExceeded) && !a.unbounded {
		err = errors.Join(ErrTimedOut, err)
	}
	return result, err
}

// Resume picks up an already-created session and waits for it as Run does. The
// specification is fixed when the session is created, so the mount and model
// options have no effect here; the hooks, destination and timeout still do.
func (a *SessionAuditor) Resume(ctx context.Context, ref managed.SessionRef, paper, destination string) (SessionResult, error) {
	runCtx, cancel := a.runContext(ctx)
	defer cancel()
	result := SessionResult{Paper: paper, Ref: ref, ArtifactDir: destination, MethodDigest: a.method.Digest()}
	managedResult, err := a.controller.Resume(runCtx, ref, a.retentionHooks(&result, destination))
	a.apply(&result, managedResult)
	if errors.Is(err, context.DeadlineExceeded) && !a.unbounded {
		err = errors.Join(ErrTimedOut, err)
	}
	return result, err
}

func (a *SessionAuditor) spec(ctx context.Context, paper string) (managed.SessionSpec, context.Context, error) {
	task, err := a.method.TaskFor(paper)
	if err != nil {
		return managed.SessionSpec{}, ctx, err
	}
	base := managed.SessionSpec{Model: ModelSelector(a.model)}
	capabilities, err := a.controller.Capabilities(ctx, base)
	if err != nil {
		return managed.SessionSpec{}, ctx, fmt.Errorf("read runtime capabilities: %w", err)
	}
	outputDir := capabilities.OutputDir
	if outputDir == "" {
		return managed.SessionSpec{}, ctx, errors.New("managed runtime has no output directory")
	}
	mount := a.mount
	if mount.Set() && mount.MountPath == "" {
		mount.MountPath = DefaultMountPath
	}
	if mount.Set() {
		outputDir = mount.MountPath
	}
	network, err := a.network(capabilities)
	if err != nil {
		return managed.SessionSpec{}, ctx, err
	}
	instructions := a.method.InstructionsFor(outputDir)
	var resources []managed.Resource
	if a.paper != nil {
		input, path, err := a.paperInput(capabilities)
		if err != nil {
			return managed.SessionSpec{}, ctx, err
		}
		resources = append(resources, input)
		instructions += paperProtocol(path)
	}
	if len(a.artifacts) > 0 {
		inputs, handed, err := a.artifactInputs(capabilities)
		if err != nil {
			return managed.SessionSpec{}, ctx, err
		}
		resources = append(resources, inputs...)
		instructions += artifactsProtocol(handed)
	}
	switch {
	case mount.Set():
		instructions += mountProtocol(mount.MountPath)
	case a.workspaceIsARepository:
		// There is no mount, and the runtime's output directory is a git
		// repository that the host publishes afterwards (045g).
		instructions += workspaceProtocol(outputDir)
	}
	spec := managed.SessionSpec{
		Model: ModelSelector(a.model), Instructions: instructions, Task: task,
		Sandbox: managed.SandboxSpec{
			Packages: []managed.Package{{Manager: "pip", Name: "pypdf"}, {Manager: "pip", Name: "requests"}},
			Network:  network,
		},
		Resources:  resources,
		Evaluation: &managed.EvaluationSpec{Rubric: a.method.Rubric, MaxRevisions: a.maxRevisions},
	}
	// A runtime that runs the grading loop itself keeps each round's
	// findings and checks them before the grader reads them. One that grades
	// on a vendor's side cannot, and its findings are checked once, when the
	// run is finalized.
	if capabilities.FindingsRounds {
		spec.Evaluation.Findings = verdict.File
	}
	if mount.Set() {
		destination := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(mount.MountPath)), "/workspace/")
		if destination == mount.MountPath || destination == "." || strings.HasPrefix(destination, "../") {
			return managed.SessionSpec{}, ctx, fmt.Errorf("%w: invalid managed mount %q", ErrUnsafePath, mount.MountPath)
		}
		access := managed.AccessReadWrite
		if mount.ReadOnly {
			access = managed.AccessReadOnly
		}
		spec.Resources = append(spec.Resources, managed.Resource{Kind: managed.ResourceRepository, Repository: &managed.RepositoryResource{
			URL: mount.URL, Destination: destination, Access: access, CredentialRef: repositoryCredentialRef,
		}})
		ctx = managed.WithSecrets(ctx, map[string]string{repositoryCredentialRef: mount.Token})
	}
	return spec, ctx, spec.Validate()
}

// networkFor returns the egress policy the runtime can enforce.
//
// The artifact search should reach any domain, and a runtime that offers open
// egress is given the open policy. A runtime that cannot, such as a host
// sandbox that accepts only an allowlist and refuses a wildcard, is given
// ArtifactDomains instead of a specification it would refuse. The alternative
// is a local audit that cannot start, which is worse than a search whose
// limits are recorded.
func networkFor(capabilities managed.Capabilities) managed.NetworkPolicy {
	if capabilities.NetworkModes[managed.NetworkOpen] {
		return managed.NetworkPolicy{Mode: managed.NetworkOpen}
	}
	if capabilities.NetworkModes[managed.NetworkAllowlist] {
		return managed.NetworkPolicy{Mode: managed.NetworkAllowlist, AllowedDomains: ArtifactDomains()}
	}
	return managed.NetworkPolicy{Mode: managed.NetworkNone}
}

func (a *SessionAuditor) retentionHooks(result *SessionResult, destination string) runtimes.Hooks {
	hooks := a.hooks
	externalRetain := hooks.Retain
	hooks.Retain = func(ctx context.Context, evidence runtimes.EvidenceReader) error {
		result.Ref = evidence.Status.Ref
		result.Status = evidence.Status
		result.Events = append([]managed.Event(nil), evidence.Events...)
		artifacts, err := collectManagedArtifacts(ctx, evidence, destination)
		if err != nil {
			return err
		}
		result.Artifacts = artifacts
		present, violations, err := validateInstrument(destination)
		if err != nil {
			return err
		}
		result.InstrumentPresent, result.InstrumentViolations = present, violations
		if externalRetain != nil {
			return externalRetain(context.WithValue(ctx, promptDigestKey{}, result.PromptDigest), evidence)
		}
		return nil
	}
	return hooks
}

// promptDigestKey carries the session's prompt digest to the caller's Retain
// hook. The hook is built before the session is specified and finalizes the
// run before Run returns, so the context it is called with is the one place
// both can reach.
type promptDigestKey struct{}

// PromptDigest returns the prompt digest of the session a Retain hook is
// retaining, or the empty string when the session was resumed and its
// specification is not known.
func PromptDigest(ctx context.Context) string {
	digest, _ := ctx.Value(promptDigestKey{}).(string)
	return digest
}

// promptDigest identifies exactly what a session was given, as "sha256:<hex>":
// its instructions, its task and its rubric. The method digest covers the
// method's files, and the instructions here are those files with the
// repository protocol the runtime added, so this digest moves when that
// protocol does and the method digest does not. Each part is framed with its
// name and length, as the method digest frames its documents.
func promptDigest(spec managed.SessionSpec) string {
	rubric := ""
	if spec.Evaluation != nil {
		rubric = spec.Evaluation.Rubric
	}
	h := sha256.New()
	for _, part := range []struct{ name, text string }{
		{"instructions", spec.Instructions},
		{"task", spec.Task},
		{"rubric", rubric},
	} {
		// hash.Hash.Write never returns an error, so neither can this.
		_, _ = fmt.Fprintf(h, "%s:%d:", part.name, len(part.text))
		h.Write([]byte(part.text))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func collectManagedArtifacts(ctx context.Context, evidence runtimes.EvidenceReader, destination string) ([]managed.Artifact, error) {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return nil, fmt.Errorf("create destination: %w", err)
	}
	collected := make([]managed.Artifact, 0, len(evidence.Artifacts))
	for _, artifact := range evidence.Artifacts {
		if artifact.IsCompiled() {
			continue
		}
		target, err := safeJoin(destination, artifact.Path)
		if err != nil {
			return nil, err
		}
		body, err := evidence.Fetch(ctx, artifact)
		if err != nil {
			return nil, fmt.Errorf("read artifact %s: %w", artifact.Path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("create %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", target, err)
		}
		collected = append(collected, artifact)
	}
	return collected, nil
}

func (a *SessionAuditor) runContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if a.unbounded {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, a.timeout)
}

func (a *SessionAuditor) apply(result *SessionResult, managedResult runtimes.Result) {
	if managedResult.Ref.ID != "" {
		result.Ref = managedResult.Ref
	}
	if managedResult.Status.Ref.ID != "" {
		result.Status = managedResult.Status
	}
	if result.Events == nil {
		result.Events = managedResult.Events
	}
	if result.Artifacts == nil {
		result.Artifacts = managedResult.Artifacts
	}
	result.Released = managedResult.Released
}
