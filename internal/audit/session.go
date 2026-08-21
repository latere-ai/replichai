package audit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/instrument"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/method"
)

const repositoryCredentialRef = "audit-repository"

type SessionMount struct {
	URL       string
	Token     string
	MountPath string
	ReadOnly  bool
}

func (mount SessionMount) Set() bool { return strings.TrimSpace(mount.URL) != "" }

type SessionResult struct {
	Paper                string
	Ref                  managed.SessionRef
	Status               managed.Status
	Events               []managed.Event
	Artifacts            []managed.Artifact
	ArtifactDir          string
	MethodDigest         string
	InstrumentPresent    bool
	InstrumentViolations []instrument.Violation
	Released             bool
}

func (result SessionResult) Passed() bool { return result.Status.Passed() }
func (result SessionResult) Grade() managed.Grade {
	if result.Status.Verdict == nil {
		return managed.GradeUnknown
	}
	return result.Status.Verdict.Grade
}
func (result SessionResult) InstrumentOK() bool {
	return result.InstrumentPresent && len(result.InstrumentViolations) == 0
}

type SessionAuditor struct {
	controller   *managed.Controller
	method       method.Method
	model        string
	maxRevisions int
	timeout      time.Duration
	unbounded    bool
	mount        SessionMount
	hooks        managed.Hooks
}

type SessionOption func(*SessionAuditor)

func WithSessionModel(preference string) SessionOption {
	return func(a *SessionAuditor) { a.model = preference }
}

func WithSessionMaxRevisions(maximum int) SessionOption {
	return func(a *SessionAuditor) {
		if maximum > 0 {
			a.maxRevisions = maximum
		}
	}
}

func WithSessionTimeout(timeout time.Duration) SessionOption {
	return func(a *SessionAuditor) { a.timeout, a.unbounded = timeout, false }
}

func WithoutSessionTimeout() SessionOption {
	return func(a *SessionAuditor) { a.unbounded = true }
}

func WithSessionMount(mount SessionMount) SessionOption {
	return func(a *SessionAuditor) { a.mount = mount }
}

func WithSessionHooks(hooks managed.Hooks) SessionOption {
	return func(a *SessionAuditor) { a.hooks = hooks }
}

func NewSessionAuditor(controller *managed.Controller, m method.Method, options ...SessionOption) *SessionAuditor {
	auditor := &SessionAuditor{
		controller: controller, method: m, model: DefaultModelPreference,
		maxRevisions: DefaultMaxRevisions, timeout: DefaultTimeout,
	}
	for _, option := range options {
		option(auditor)
	}
	return auditor
}

func (a *SessionAuditor) Start(ctx context.Context, paper string) (managed.SessionRef, error) {
	spec, ctx, err := a.spec(ctx, paper)
	if err != nil {
		return managed.SessionRef{}, err
	}
	return a.controller.Start(ctx, spec, a.hooks)
}

func (a *SessionAuditor) Run(ctx context.Context, paper, destination string) (SessionResult, error) {
	spec, startCtx, err := a.spec(ctx, paper)
	if err != nil {
		return SessionResult{Paper: paper}, err
	}
	runCtx, cancel := a.runContext(startCtx)
	defer cancel()
	result := SessionResult{Paper: paper, ArtifactDir: destination, MethodDigest: a.method.Digest()}
	hooks := a.retentionHooks(&result, destination)
	managedResult, err := a.controller.Execute(runCtx, spec, hooks)
	a.apply(&result, managedResult)
	if errors.Is(err, context.DeadlineExceeded) && !a.unbounded {
		err = errors.Join(ErrTimedOut, err)
	}
	return result, err
}

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
	base := managed.SessionSpec{Model: managed.ModelSelector{Preference: a.model}}
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
	instructions := a.method.InstructionsFor(outputDir)
	if mount.Set() {
		instructions += mountProtocol(mount.MountPath)
	}
	spec := managed.SessionSpec{
		Model: managed.ModelSelector{Preference: a.model}, Instructions: instructions, Task: task,
		Sandbox: managed.SandboxSpec{
			Packages: []managed.Package{{Manager: "pip", Name: "pypdf"}, {Manager: "pip", Name: "requests"}},
			Network:  managed.NetworkPolicy{Mode: managed.NetworkOpen},
		},
		Evaluation: &managed.EvaluationSpec{Rubric: a.method.Rubric, MaxRevisions: a.maxRevisions},
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

func (a *SessionAuditor) retentionHooks(result *SessionResult, destination string) managed.Hooks {
	hooks := a.hooks
	externalRetain := hooks.Retain
	hooks.Retain = func(ctx context.Context, evidence managed.EvidenceReader) error {
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
			return externalRetain(ctx, evidence)
		}
		return nil
	}
	return hooks
}

func collectManagedArtifacts(ctx context.Context, evidence managed.EvidenceReader, destination string) ([]managed.Artifact, error) {
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
			return nil, fmt.Errorf("fetch %s: %w", artifact.Path, err)
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

func (a *SessionAuditor) apply(result *SessionResult, managedResult managed.Result) {
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
