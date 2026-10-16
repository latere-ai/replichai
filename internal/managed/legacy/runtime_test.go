// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package legacy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	managed "latere.ai/x/managed-agents"

	"github.com/latere-ai/replichai/internal/provider"
	providermemory "github.com/latere-ai/replichai/internal/provider/memory"
)

func TestStartMapsOneSessionSpecAndRequestSecret(t *testing.T) {
	backend := providermemory.New()
	runtime := newRuntime(t, backend)
	body := []byte("dataset")
	digest := sha256.Sum256(body)
	spec := managed.SessionSpec{
		Model: managed.ModelSelector{Preference: "best"}, Instructions: "instructions", Task: "task",
		Sandbox: managed.SandboxSpec{
			Packages: []managed.Package{{Manager: "pip", Name: "pandas", Version: "2.0"}},
			Network:  managed.NetworkPolicy{Mode: managed.NetworkOpen},
		},
		Resources: []managed.Resource{
			{Kind: managed.ResourceRepository, Repository: &managed.RepositoryResource{URL: "https://example.com/repo", Destination: "repro", Access: managed.AccessReadOnly, CredentialRef: "repo"}},
			{Kind: managed.ResourceAttachment, Attachment: &managed.AttachmentResource{Path: "input/data", Content: body, SHA256: hex.EncodeToString(digest[:])}},
		},
	}
	ctx := managed.WithSecrets(context.Background(), map[string]string{"repo": "scoped-token"})
	ref, err := runtime.Start(ctx, spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if ref.Runtime != "anthropic-managed" || ref.ID == "" || string(ref.Locator) == "" {
		t.Fatalf("ref = %+v", ref)
	}
	workspaces, workers, runs := backend.Workspaces(), backend.Workers(), backend.Runs()
	if len(workspaces) != 1 || workspaces[0].Packages["pip"][0] != "pandas==2.0" || workspaces[0].Network != provider.NetworkOpen {
		t.Fatalf("workspaces = %+v", workspaces)
	}
	if len(workers) != 1 || workers[0].Instructions != "instructions" || len(runs) != 1 {
		t.Fatalf("workers/runs = %+v / %+v", workers, runs)
	}
	if runs[0].Mount.Token != "scoped-token" || runs[0].Mount.MountPath != "/workspace/repro" || runs[0].Attachments[0].Path != "/workspace/input/data" {
		t.Fatalf("mapped run = %+v", runs[0])
	}
	if string(ref.Locator) == "" || contains(string(ref.Locator), "scoped-token") || contains(string(ref.Locator), "dataset") {
		t.Fatalf("locator leaked request data: %s", ref.Locator)
	}
}

func TestUngradedAndGradedStatusMapping(t *testing.T) {
	for _, graded := range []bool{false, true} {
		t.Run(map[bool]string{false: "ungraded", true: "graded"}[graded], func(t *testing.T) {
			backend := providermemory.New()
			runtime := newRuntime(t, backend)
			spec := baseSpec()
			if graded {
				spec.Evaluation = &managed.EvaluationSpec{Rubric: "rubric", MaxRevisions: 2}
			}
			ref, _ := runtime.Start(context.Background(), spec)
			status, err := runtime.Poll(context.Background(), ref)
			if err != nil || status.State != managed.StateDone || status.Model.Provider != "anthropic" || status.Usage.InputFresh != 1 {
				t.Fatalf("Poll = %+v, %v", status, err)
			}
			if graded != (status.Verdict != nil) {
				t.Fatalf("graded=%v verdict=%+v", graded, status.Verdict)
			}
		})
	}
}

func TestEvidenceCancelAndReleaseDelegateWithCompleteHandle(t *testing.T) {
	backend := providermemory.New()
	backend.Events = []provider.Event{{
		Role: provider.RoleAgent, Kind: provider.KindMessage, Content: "done",
		Usage: &provider.Usage{InputTokens: 3, CachedInputTokens: 2, OutputTokens: 1},
	}}
	backend.Files = map[string][]byte{"result.txt": []byte("result")}
	runtime := newRuntime(t, backend)
	ref, _ := runtime.Start(context.Background(), baseSpec())
	events, err := runtime.Events(context.Background(), ref)
	if err != nil || len(events) != 1 || events[0].ID == "" || events[0].Content != "done" {
		t.Fatalf("Events = %+v, %v", events, err)
	}
	if events[0].Usage == nil || events[0].Usage.Model.ID == "" || events[0].Usage.TotalTokens() != 6 {
		t.Fatalf("normalized event usage = %+v", events[0].Usage)
	}
	artifacts, err := runtime.ListArtifacts(context.Background(), ref)
	if err != nil || len(artifacts) != 1 {
		t.Fatalf("ListArtifacts = %+v, %v", artifacts, err)
	}
	body, err := runtime.FetchArtifact(context.Background(), ref, artifacts[0])
	if err != nil || string(body) != "result" {
		t.Fatalf("FetchArtifact = %q, %v", body, err)
	}
	if err := runtime.Cancel(context.Background(), ref); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := runtime.Release(context.Background(), ref); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := backend.Closed(); len(got) != 2 || got[0] != ref.ID || got[1] != ref.ID {
		t.Fatalf("closed = %v", got)
	}
}

func TestModelsCapabilitiesValidationAndReferenceErrors(t *testing.T) {
	backend := providermemory.New()
	runtime := newRuntime(t, backend)
	models, err := runtime.Models(context.Background())
	if err != nil || len(models) != 3 || models[0].Ref.Provider != "anthropic" {
		t.Fatalf("Models = %+v, %v", models, err)
	}
	capabilities, _ := runtime.Capabilities(context.Background())
	if capabilities.OutputDir != backend.OutputDir() || !capabilities.Evaluation {
		t.Fatalf("Capabilities = %+v", capabilities)
	}
	spec := baseSpec()
	spec.Model = managed.ModelSelector{Exact: &managed.ModelRef{Provider: "anthropic", ID: "model"}}
	if err := runtime.Validate(spec); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatalf("exact model = %v", err)
	}
	spec = baseSpec()
	spec.Evaluation = &managed.EvaluationSpec{Rubric: "passes", MaxRevisions: 0}
	if err := runtime.Validate(spec); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatalf("graded with no revision = %v", err)
	}
	spec = baseSpec()
	spec.Evaluation = &managed.EvaluationSpec{Rubric: "passes", MaxRevisions: 1, Findings: "verdict.json"}
	if err := runtime.Validate(spec); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatalf("findings rounds = %v", err)
	}
	spec = baseSpec()
	spec.Sandbox.Network.Mode = managed.NetworkAllowlist
	spec.Sandbox.Network.AllowedDomains = []string{"api.example.com"}
	if err := runtime.Validate(spec); !errors.Is(err, managed.ErrUnsupported) {
		t.Fatalf("allowlist = %v", err)
	}
	ref, _ := runtime.Start(context.Background(), baseSpec())
	bad := ref
	bad.Runtime = "other"
	if _, err := runtime.Poll(context.Background(), bad); !errors.Is(err, managed.ErrRuntimeMismatch) {
		t.Fatalf("mismatch = %v", err)
	}
	bad = ref
	bad.LocatorVersion = 2
	if _, err := runtime.Events(context.Background(), bad); err == nil {
		t.Fatal("locator version accepted")
	}
	bad = ref
	bad.Locator = json.RawMessage(`{}`)
	if _, err := runtime.ListArtifacts(context.Background(), bad); err == nil {
		t.Fatal("empty locator accepted")
	}
}

func TestConstructionAndStartFailures(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("empty config accepted")
	}
	if _, err := New(Config{Name: "Bad", ModelProvider: "anthropic", Provider: providermemory.New()}); err == nil {
		t.Fatal("noncanonical name accepted")
	}
	if _, err := New(Config{Name: "good", Provider: providermemory.New()}); err == nil {
		t.Fatal("empty model provider accepted")
	}
	backend := providermemory.New()
	backend.Err = errors.New("offline")
	runtime := newRuntime(t, backend)
	if _, err := runtime.Start(context.Background(), baseSpec()); err == nil {
		t.Fatal("backend error ignored")
	}
	backend = providermemory.New()
	runtime = newRuntime(t, backend)
	spec := baseSpec()
	spec.Resources = []managed.Resource{{Kind: managed.ResourceRepository, Repository: &managed.RepositoryResource{
		URL: "https://example.com/repo", Destination: "repo", Access: managed.AccessReadOnly, CredentialRef: "missing",
	}}}
	if _, err := runtime.Start(context.Background(), spec); !errors.Is(err, managed.ErrInvalidSpec) {
		t.Fatalf("missing secret = %v", err)
	}
}

type observationProvider struct {
	provider.Provider
	status provider.RunStatus
	err    error
}

func (p observationProvider) Poll(context.Context, provider.RunHandle) (provider.RunStatus, error) {
	return p.status, p.err
}

func TestPollMapsEveryLegacyStateAndGrade(t *testing.T) {
	states := []struct {
		legacy provider.RunState
		want   managed.State
	}{
		{provider.StateUnknown, managed.StateUnknown},
		{provider.StatePending, managed.StatePending},
		{provider.StateRunning, managed.StateRunning},
		{provider.StateDone, managed.StateDone},
		{provider.StateFailed, managed.StateFailed},
	}
	grades := []struct {
		legacy provider.Grade
		want   managed.Grade
	}{
		{provider.GradeUnknown, managed.GradeUnknown},
		{provider.GradePending, managed.GradePending},
		{provider.GradePassed, managed.GradePassed},
		{provider.GradeNeedsRevision, managed.GradeNeedsRevision},
		{provider.GradeExhausted, managed.GradeExhausted},
		{provider.GradeFailed, managed.GradeFailed},
	}
	for _, state := range states {
		if got := translateState(state.legacy); got != state.want {
			t.Errorf("translateState(%v) = %v", state.legacy, got)
		}
	}
	for _, grade := range grades {
		if got := translateGrade(grade.legacy); got != grade.want {
			t.Errorf("translateGrade(%v) = %v", grade.legacy, got)
		}
	}

	base := providermemory.New()
	starter := newRuntime(t, base)
	ref, _ := starter.Start(context.Background(), func() managed.SessionSpec {
		spec := baseSpec()
		spec.Evaluation = &managed.EvaluationSpec{Rubric: "rubric", MaxRevisions: 1}
		return spec
	}())
	for _, test := range []struct {
		name   string
		status provider.RunStatus
		state  managed.State
		reason managed.TerminalReason
	}{
		{"pending", provider.RunStatus{State: provider.StatePending}, managed.StatePending, ""},
		{"running model", provider.RunStatus{State: provider.StateRunning, Model: "resolved-later"}, managed.StateRunning, ""},
		{"passed", provider.RunStatus{State: provider.StateDone, Verdict: &provider.Verdict{Grade: provider.GradePassed}}, managed.StateDone, managed.ReasonSucceeded},
		{"evaluation failed", provider.RunStatus{State: provider.StateDone, Verdict: &provider.Verdict{Grade: provider.GradeFailed}}, managed.StateFailed, managed.ReasonEvaluationFailed},
		{"runtime failed", provider.RunStatus{State: provider.StateFailed}, managed.StateFailed, managed.ReasonRuntimeFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := observationProvider{Provider: base, status: test.status}
			runtime := newRuntime(t, backend)
			status, err := runtime.Poll(context.Background(), ref)
			if err != nil || status.State != test.state || status.TerminalReason != test.reason {
				t.Fatalf("Poll = %+v, %v", status, err)
			}
		})
	}
	backend := observationProvider{Provider: base, err: errors.New("poll failed")}
	if _, err := newRuntime(t, backend).Poll(context.Background(), ref); err == nil {
		t.Fatal("poll failure ignored")
	}
}

type operationFailureProvider struct {
	provider.Provider
	eventsErr error
	listErr   error
	fetchErr  error
	closeErr  error
}

type resourceTrackingProvider struct {
	*providermemory.Provider
	workerErr error
	released  []string
}

func (p *resourceTrackingProvider) EnsureWorker(ctx context.Context, spec provider.WorkerSpec) (provider.WorkerRef, error) {
	if p.workerErr != nil {
		return provider.WorkerRef{}, p.workerErr
	}
	return p.Provider.EnsureWorker(ctx, spec)
}

func (p *resourceTrackingProvider) StartRun(ctx context.Context, worker provider.WorkerRef, workspace provider.WorkspaceRef, spec provider.RunSpec) (provider.RunHandle, error) {
	handle, err := p.Provider.StartRun(ctx, worker, workspace, spec)
	handle.Files = []string{"file-1"}
	return handle, err
}

func (p *resourceTrackingProvider) ReleaseResources(_ context.Context, worker provider.WorkerRef, workspace provider.WorkspaceRef) error {
	p.released = append(p.released, worker.ID+"/"+workspace.ID)
	return nil
}

func TestStartRollbackAndLocatorPreserveOwnedResources(t *testing.T) {
	failing := &resourceTrackingProvider{Provider: providermemory.New(), workerErr: errors.New("worker failed")}
	if _, err := newRuntime(t, failing).Start(context.Background(), baseSpec()); err == nil {
		t.Fatal("worker failure ignored")
	}
	if len(failing.released) != 1 || failing.released[0] == "/" {
		t.Fatalf("workspace rollback = %v", failing.released)
	}

	tracking := &resourceTrackingProvider{Provider: providermemory.New()}
	runtime := newRuntime(t, tracking)
	ref, err := runtime.Start(context.Background(), baseSpec())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !contains(string(ref.Locator), "file-1") {
		t.Fatalf("locator lost uploaded file ownership: %s", ref.Locator)
	}
	if err := runtime.Release(context.Background(), ref); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if len(tracking.released) != 1 || strings.HasPrefix(tracking.released[0], "/") {
		t.Fatalf("resource release = %v", tracking.released)
	}
}

func (p operationFailureProvider) Transcript(context.Context, provider.RunHandle) ([]provider.Event, error) {
	return nil, p.eventsErr
}
func (p operationFailureProvider) ListArtifacts(context.Context, provider.RunHandle) ([]provider.Artifact, error) {
	return nil, p.listErr
}
func (p operationFailureProvider) FetchArtifact(context.Context, provider.RunHandle, provider.Artifact) ([]byte, error) {
	return nil, p.fetchErr
}
func (p operationFailureProvider) CloseRun(context.Context, provider.RunHandle) error {
	return p.closeErr
}

func TestOperationFailuresAndInvalidHandlesSurface(t *testing.T) {
	base := providermemory.New()
	starter := newRuntime(t, base)
	ref, _ := starter.Start(context.Background(), baseSpec())
	injected := errors.New("injected")
	runtime := newRuntime(t, operationFailureProvider{Provider: base, eventsErr: injected, listErr: injected, fetchErr: injected, closeErr: injected})
	calls := []func() error{
		func() error { _, err := runtime.Events(context.Background(), ref); return err },
		func() error { _, err := runtime.ListArtifacts(context.Background(), ref); return err },
		func() error {
			_, err := runtime.FetchArtifact(context.Background(), ref, managed.Artifact{})
			return err
		},
		func() error { return runtime.Cancel(context.Background(), ref) },
		func() error { return runtime.Release(context.Background(), ref) },
	}
	for index, call := range calls {
		if err := call(); !errors.Is(err, injected) {
			t.Errorf("operation %d = %v", index, err)
		}
	}
	bad := ref
	bad.Locator = json.RawMessage(`not-json`)
	for index, call := range []func() error{
		func() error {
			_, err := runtime.FetchArtifact(context.Background(), bad, managed.Artifact{})
			return err
		},
		func() error { return runtime.Cancel(context.Background(), bad) },
		func() error { return runtime.Release(context.Background(), bad) },
	} {
		if err := call(); err == nil {
			t.Errorf("invalid-handle operation %d succeeded", index)
		}
	}
}

func TestHistoricalIDOnlyReferenceCanResume(t *testing.T) {
	base := providermemory.New()
	runtime := newRuntime(t, base)
	ref, _ := runtime.Start(context.Background(), baseSpec())
	legacy := managed.SessionRef{Runtime: runtime.Name(), ID: ref.ID, Revision: 1, LocatorVersion: 1}
	handle, metadata, err := runtime.handle(legacy)
	if err != nil || handle.ID != ref.ID || !metadata.Graded || !metadata.Model.Valid() {
		t.Fatalf("legacy handle = %+v, %+v, %v", handle, metadata, err)
	}
}

func newRuntime(t *testing.T, backend provider.Provider) *Runtime {
	t.Helper()
	runtime, err := New(Config{Name: "anthropic-managed", ModelProvider: "anthropic", Provider: backend})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runtime
}

func baseSpec() managed.SessionSpec {
	return managed.SessionSpec{
		Model: managed.ModelSelector{Preference: "best"}, Instructions: "instructions", Task: "task",
		Sandbox: managed.SandboxSpec{Network: managed.NetworkPolicy{Mode: managed.NetworkOpen}},
	}
}

func contains(value, part string) bool {
	for index := 0; index+len(part) <= len(value); index++ {
		if value[index:index+len(part)] == part {
			return true
		}
	}
	return false
}
