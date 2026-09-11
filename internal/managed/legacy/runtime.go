// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package legacy adapts the original provider resource graph to the managed
// session contract while the Anthropic implementation is moved behind it.
// Application packages must not import this package.
package legacy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/provider"
)

// Config wires a legacy managed runtime: the canonical name it answers to, the
// model provider label stamped on every ModelRef it reports, and the provider
// resource graph it drives.
type Config struct {
	Name          managed.RuntimeName
	ModelProvider string
	Provider      provider.Provider
}

// Runtime adapts the provider resource graph to the managed session contract,
// so callers hold one managed.Runtime whichever backend sits underneath.
type Runtime struct{ config Config }

// New rejects a non-canonical or empty name, an empty model provider, and a nil
// provider. A non-canonical name would otherwise go unnoticed until a session
// ref carrying it fails managed validation.
func New(config Config) (*Runtime, error) {
	if config.Name == "" || managed.CanonicalRuntimeName(string(config.Name)) != config.Name {
		return nil, fmt.Errorf("legacy managed runtime has invalid name %q", config.Name)
	}
	if strings.TrimSpace(config.ModelProvider) == "" {
		return nil, fmt.Errorf("legacy managed runtime %q has no model provider", config.Name)
	}
	if config.Provider == nil {
		return nil, fmt.Errorf("legacy managed runtime %q has no provider", config.Name)
	}
	return &Runtime{config: config}, nil
}

// Name reports the canonical runtime name the adapter was built with. Every
// session ref Start mints carries it, and the ref-taking methods refuse a ref
// that names a different runtime.
func (r *Runtime) Name() managed.RuntimeName { return r.config.Name }

// Capabilities reports what this adapter supports. The answer is static, so the
// context is unused and the error exists only for interface conformance.
func (r *Runtime) Capabilities(context.Context) (managed.Capabilities, error) {
	return r.capabilities(), nil
}

// capabilities is the context-free half of Capabilities. Validate needs the
// same answer and has no context to pass. Using a background context there
// would imply an I/O boundary this adapter does not have, and would force
// Validate to discard an error it cannot return.
func (r *Runtime) capabilities() managed.Capabilities {
	return managed.Capabilities{
		OutputDir:          r.config.Provider.OutputDir(),
		PackageManagers:    map[string]bool{"pip": true},
		NetworkModes:       map[managed.NetworkMode]bool{managed.NetworkNone: true, managed.NetworkOpen: true},
		RepositoryModes:    map[managed.AccessMode]bool{managed.AccessReadOnly: true, managed.AccessReadWrite: true},
		Evaluation:         true,
		DurableEvents:      true,
		Artifacts:          true,
		Cancellation:       true,
		MaxAttachmentBytes: 64 << 20,
		MaxLocatorBytes:    managed.DefaultMaxLocatorBytes,
	}
}

// Models resolves the best, balanced and fast preferences into concrete model
// ids, one provider round-trip each. A single failed resolve fails the call.
func (r *Runtime) Models(ctx context.Context) ([]managed.Model, error) {
	models := make([]managed.Model, 0, 3)
	for _, preference := range []string{"best", "balanced", "fast"} {
		id, err := r.config.Provider.ResolveModel(ctx, preference)
		if err != nil {
			return nil, err
		}
		models = append(models, managed.Model{Ref: managed.ModelRef{Provider: r.config.ModelProvider, ID: id}, Preferences: []string{preference}})
	}
	return models, nil
}

// Validate checks the spec against the adapter's capabilities and additionally
// refuses an exact model pin: this adapter can only ask the provider to resolve
// a preference.
func (r *Runtime) Validate(spec managed.SessionSpec) error {
	if err := managed.ValidateCapabilities(r.Name(), r.capabilities(), spec); err != nil {
		return err
	}
	if spec.Model.Exact != nil {
		return &managed.UnsupportedError{Runtime: r.Name(), Field: "model.exact", Capability: "exact_model"}
	}
	return nil
}

type locator struct {
	WorkspaceID   string           `json:"workspace_id"`
	WorkerID      string           `json:"worker_id"`
	WorkerVersion int              `json:"worker_version"`
	Model         managed.ModelRef `json:"model"`
	Graded        bool             `json:"graded"`
	FileIDs       []string         `json:"file_ids,omitempty"`
}

// Start creates the workspace, worker and run the spec asks for, and returns a
// ref whose locator carries the ids needed to reach that run again. Every
// failure after the workspace exists releases what was already created, as far
// as the provider allows: one that does not implement provider.ResourceReleaser
// keeps them.
func (r *Runtime) Start(ctx context.Context, spec managed.SessionSpec) (managed.SessionRef, error) {
	if err := r.Validate(spec); err != nil {
		return managed.SessionRef{}, err
	}
	modelID, err := r.config.Provider.ResolveModel(ctx, spec.Model.Preference)
	if err != nil {
		return managed.SessionRef{}, fmt.Errorf("resolve model: %w", err)
	}
	packages := make(map[string][]string)
	for _, pkg := range spec.Sandbox.Packages {
		name := pkg.Name
		if pkg.Version != "" {
			name += "==" + pkg.Version
		}
		packages[pkg.Manager] = append(packages[pkg.Manager], name)
	}
	network := provider.NetworkLimited
	if spec.Sandbox.Network.Mode == managed.NetworkOpen {
		network = provider.NetworkOpen
	}
	workspace, err := r.config.Provider.EnsureWorkspace(ctx, provider.WorkspaceSpec{Name: "managed-session", Packages: packages, Network: network})
	if err != nil {
		return managed.SessionRef{}, fmt.Errorf("create sandbox: %w", err)
	}
	worker, err := r.config.Provider.EnsureWorker(ctx, provider.WorkerSpec{Name: "managed-session", Instructions: spec.Instructions, Model: modelID})
	if err != nil {
		return managed.SessionRef{}, errors.Join(fmt.Errorf("create worker: %w", err), r.releaseResources(ctx, provider.WorkerRef{}, workspace))
	}
	runSpec := provider.RunSpec{Task: spec.Task}
	if spec.Evaluation != nil {
		runSpec.Rubric = spec.Evaluation.Rubric
		runSpec.MaxRevisions = spec.Evaluation.MaxRevisions
	}
	for _, resource := range spec.Resources {
		switch resource.Kind {
		case managed.ResourceAttachment:
			runSpec.Attachments = append(runSpec.Attachments, provider.Attachment{Path: workspacePath(resource.Attachment.Path), Content: resource.Attachment.Content})
		case managed.ResourceRepository:
			repository := resource.Repository
			token, ok := managed.Secret(ctx, repository.CredentialRef)
			if repository.CredentialRef != "" && !ok {
				cause := &managed.InvalidSpecError{Field: "resources.repository.credential_ref", Message: "secret reference is unavailable"}
				return managed.SessionRef{}, errors.Join(cause, r.releaseResources(ctx, worker, workspace))
			}
			runSpec.Mount = provider.RepoMount{
				URL: repository.URL, Token: token, MountPath: workspacePath(repository.Destination), ReadOnly: repository.Access == managed.AccessReadOnly,
			}
		}
	}
	handle, err := r.config.Provider.StartRun(ctx, worker, workspace, runSpec)
	if err != nil {
		return managed.SessionRef{}, errors.Join(fmt.Errorf("start session: %w", err), r.releaseResources(ctx, worker, workspace))
	}
	encoded, err := json.Marshal(locator{
		WorkspaceID: workspace.ID, WorkerID: worker.ID, WorkerVersion: worker.Revision,
		Model: managed.ModelRef{Provider: r.config.ModelProvider, ID: modelID}, Graded: spec.Evaluation != nil,
		FileIDs: append([]string(nil), handle.Files...),
	})
	if err != nil {
		// The session is already running on the backend, so a locator that
		// cannot be returned leaves unreachable state. It is released rather
		// than leaked.
		return managed.SessionRef{}, errors.Join(fmt.Errorf("encode locator: %w", err), r.releaseResources(ctx, worker, workspace))
	}
	return managed.SessionRef{Runtime: r.Name(), ID: handle.ID, Revision: 1, LocatorVersion: 1, Locator: encoded}, nil
}

// Poll translates the provider's run status and folds the evaluation verdict
// into it: on a graded session, any grade other than passed turns a finished run
// into a failure with ReasonEvaluationFailed.
func (r *Runtime) Poll(ctx context.Context, ref managed.SessionRef) (managed.Status, error) {
	handle, metadata, err := r.handle(ref)
	if err != nil {
		return managed.Status{}, err
	}
	status, err := r.config.Provider.Poll(ctx, handle)
	if err != nil {
		return managed.Status{}, err
	}
	result := managed.Status{
		Ref: ref, Model: metadata.Model, State: translateState(status.State),
		Usage: managed.Usage{
			Model: metadata.Model, InputFresh: status.Usage.InputTokens, InputCached: status.Usage.CachedInputTokens,
			CacheWrite: status.Usage.CacheWriteTokens, CacheWrite5m: status.Usage.CacheWrite5mTokens,
			CacheWrite1h: status.Usage.CacheWrite1hTokens, Output: status.Usage.OutputTokens,
		},
	}
	if status.Model != "" {
		result.Model.ID = status.Model
		result.Usage.Model = result.Model
	}
	if metadata.Graded && status.Verdict != nil {
		result.Verdict = &managed.Verdict{Grade: translateGrade(status.Verdict.Grade), Explanation: status.Verdict.Explanation}
	}
	switch result.State {
	case managed.StateDone:
		result.TerminalReason = managed.ReasonSucceeded
		if result.Verdict != nil && result.Verdict.Grade != managed.GradePassed {
			result.State = managed.StateFailed
			result.TerminalReason = managed.ReasonEvaluationFailed
		}
	case managed.StateFailed:
		result.TerminalReason = managed.ReasonRuntimeFailed
	}
	return result, nil
}

// Events returns the whole transcript on every call, because the provider offers
// no cursor. Transcript entries arrive unidentified, so event ids are synthesised
// from the session id and the position in the transcript.
func (r *Runtime) Events(ctx context.Context, ref managed.SessionRef) ([]managed.Event, error) {
	handle, metadata, err := r.handle(ref)
	if err != nil {
		return nil, err
	}
	events, err := r.config.Provider.Transcript(ctx, handle)
	if err != nil {
		return nil, err
	}
	result := make([]managed.Event, 0, len(events))
	for index, event := range events {
		translated := managed.Event{
			ID: fmt.Sprintf("%s-%d", ref.ID, index+1), Role: managed.Role(event.Role), Kind: managed.EventKind(event.Kind),
			Tool: event.Tool, Content: event.Content, Time: event.Time, Raw: event.Raw,
		}
		if event.Usage != nil {
			translated.Usage = &managed.Usage{
				Model:      metadata.Model,
				InputFresh: event.Usage.InputTokens, InputCached: event.Usage.CachedInputTokens,
				CacheWrite: event.Usage.CacheWriteTokens, CacheWrite5m: event.Usage.CacheWrite5mTokens,
				CacheWrite1h: event.Usage.CacheWrite1hTokens, Output: event.Usage.OutputTokens,
			}
		}
		result = append(result, translated)
	}
	return result, nil
}

// ListArtifacts returns every file the run produced, unfiltered, compiled Python
// output included. Callers that do not want it screen the result with
// managed.Artifact.IsCompiled.
func (r *Runtime) ListArtifacts(ctx context.Context, ref managed.SessionRef) ([]managed.Artifact, error) {
	handle, _, err := r.handle(ref)
	if err != nil {
		return nil, err
	}
	artifacts, err := r.config.Provider.ListArtifacts(ctx, handle)
	if err != nil {
		return nil, err
	}
	result := make([]managed.Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		result = append(result, managed.Artifact{ID: artifact.ID, Path: artifact.Path})
	}
	return result, nil
}

// FetchArtifact reads one artifact body whole. There is no streaming path, so
// the caller holds the entire file in memory.
func (r *Runtime) FetchArtifact(ctx context.Context, ref managed.SessionRef, artifact managed.Artifact) ([]byte, error) {
	handle, _, err := r.handle(ref)
	if err != nil {
		return nil, err
	}
	return r.config.Provider.FetchArtifact(ctx, handle, provider.Artifact{ID: artifact.ID, Path: artifact.Path})
}

// Cancel stops the run but keeps the worker and workspace, so the transcript and
// artifacts of a cancelled session stay reachable. Release is what tears them
// down.
func (r *Runtime) Cancel(ctx context.Context, ref managed.SessionRef) error {
	handle, _, err := r.handle(ref)
	if err != nil {
		return err
	}
	return r.config.Provider.CloseRun(ctx, handle)
}

// Release closes the run and releases the worker and workspace behind it. Use
// Cancel instead when the evidence still has to be read.
func (r *Runtime) Release(ctx context.Context, ref managed.SessionRef) error {
	handle, _, err := r.handle(ref)
	if err != nil {
		return err
	}
	return errors.Join(r.config.Provider.CloseRun(ctx, handle), r.releaseResources(ctx, handle.Worker, handle.Workspace))
}

func (r *Runtime) releaseResources(ctx context.Context, worker provider.WorkerRef, workspace provider.WorkspaceRef) error {
	releaser, ok := r.config.Provider.(provider.ResourceReleaser)
	if !ok {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return releaser.ReleaseResources(cleanupCtx, worker, workspace)
}

func (r *Runtime) handle(ref managed.SessionRef) (provider.RunHandle, locator, error) {
	if ref.Runtime != r.Name() {
		return provider.RunHandle{}, locator{}, &managed.RuntimeMismatchError{Expected: r.Name(), Actual: ref.Runtime}
	}
	if err := ref.Validate(managed.DefaultMaxLocatorBytes); err != nil {
		return provider.RunHandle{}, locator{}, err
	}
	if ref.LocatorVersion != 1 {
		return provider.RunHandle{}, locator{}, fmt.Errorf("unsupported %s locator version %d", r.Name(), ref.LocatorVersion)
	}
	if len(ref.Locator) == 0 {
		// Historical durable rows and detached CLI output carried only the
		// vendor session id. Poll, evidence, cancel and release need no worker
		// or workspace id, and the first poll supplies the exact model before
		// the controller accepts an observation. New starts always emit the
		// complete locator above.
		return provider.RunHandle{ID: ref.ID}, locator{Model: managed.ModelRef{Provider: r.config.ModelProvider, ID: "legacy-unresolved"}, Graded: true}, nil
	}
	var metadata locator
	if err := json.Unmarshal(ref.Locator, &metadata); err != nil || metadata.WorkspaceID == "" || metadata.WorkerID == "" || !metadata.Model.Valid() {
		return provider.RunHandle{}, locator{}, fmt.Errorf("invalid %s session locator", r.Name())
	}
	return provider.RunHandle{
		ID: ref.ID, Workspace: provider.WorkspaceRef{ID: metadata.WorkspaceID},
		Worker: provider.WorkerRef{ID: metadata.WorkerID, Revision: metadata.WorkerVersion},
		Files:  append([]string(nil), metadata.FileIDs...),
	}, metadata, nil
}

func workspacePath(relative string) string { return path.Join("/workspace", relative) }

func translateState(state provider.RunState) managed.State {
	switch state {
	case provider.StatePending:
		return managed.StatePending
	case provider.StateRunning:
		return managed.StateRunning
	case provider.StateDone:
		return managed.StateDone
	case provider.StateFailed:
		return managed.StateFailed
	default:
		return managed.StateUnknown
	}
}

func translateGrade(grade provider.Grade) managed.Grade {
	switch grade {
	case provider.GradePending:
		return managed.GradePending
	case provider.GradePassed:
		return managed.GradePassed
	case provider.GradeNeedsRevision:
		return managed.GradeNeedsRevision
	case provider.GradeExhausted:
		return managed.GradeExhausted
	case provider.GradeFailed:
		return managed.GradeFailed
	default:
		return managed.GradeUnknown
	}
}

var _ managed.Runtime = (*Runtime)(nil)
