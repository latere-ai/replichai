// Package memory provides an in-process provider that runs nothing.
//
// It exists to exercise the orchestrator, to keep the test suite free of
// network access, and to demonstrate the contract a real adapter satisfies.
package memory

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/latere-ai/replichai/internal/provider"
)

// Provider is a deterministic stand-in for a real backend. It is safe for
// concurrent use.
type Provider struct {
	// Model is returned by ResolveModel.
	Model string
	// PollsBeforeDone is how many Poll calls a run takes to finish.
	PollsBeforeDone int
	// Grade is the verdict recorded once a run finishes.
	Grade provider.Grade
	// Files are the artifacts every run produces, keyed by path.
	Files map[string][]byte
	// Events is the transcript every run returns.
	Events []provider.Event
	// Err, when set, is returned by every contract method.
	Err error

	mu         sync.Mutex
	workspaces []provider.WorkspaceSpec
	workers    []provider.WorkerSpec
	runs       []provider.RunSpec
	closed     []string
	polls      map[string]int
	counter    int
}

// New builds a Provider whose runs finish on the first poll and pass.
func New() *Provider {
	return &Provider{
		Model:           "memory-model",
		PollsBeforeDone: 1,
		Grade:           provider.GradePassed,
		Files:           map[string][]byte{},
		polls:           map[string]int{},
	}
}

// Name identifies the adapter.
func (p *Provider) Name() string { return "memory" }

// OutputDir is the sandbox path a worker writes its deliverable to.
func (p *Provider) OutputDir() string { return "/workspace/outputs/" }

// Workspaces returns the workspace specs the provider was asked to create.
func (p *Provider) Workspaces() []provider.WorkspaceSpec {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.WorkspaceSpec(nil), p.workspaces...)
}

// Workers returns the worker specs the provider was asked to create.
func (p *Provider) Workers() []provider.WorkerSpec {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.WorkerSpec(nil), p.workers...)
}

// Runs returns the run specs the provider was asked to start.
func (p *Provider) Runs() []provider.RunSpec {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]provider.RunSpec(nil), p.runs...)
}

// Closed returns the IDs of runs that were released.
func (p *Provider) Closed() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.closed...)
}

// ResolveModel returns the configured Model.
func (p *Provider) ResolveModel(ctx context.Context, pref string) (string, error) {
	if err := p.check(ctx); err != nil {
		return "", err
	}
	return p.Model, nil
}

// EnsureWorkspace records the spec and returns a synthetic reference.
func (p *Provider) EnsureWorkspace(ctx context.Context, spec provider.WorkspaceSpec) (provider.WorkspaceRef, error) {
	if err := p.check(ctx); err != nil {
		return provider.WorkspaceRef{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.workspaces = append(p.workspaces, spec)
	return provider.WorkspaceRef{ID: fmt.Sprintf("workspace-%d", len(p.workspaces))}, nil
}

// EnsureWorker records the spec and returns a synthetic reference.
func (p *Provider) EnsureWorker(ctx context.Context, spec provider.WorkerSpec) (provider.WorkerRef, error) {
	if err := p.check(ctx); err != nil {
		return provider.WorkerRef{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.workers = append(p.workers, spec)
	return provider.WorkerRef{ID: fmt.Sprintf("worker-%d", len(p.workers)), Revision: 1}, nil
}

// StartRun records the spec and returns a handle with a fresh identity.
func (p *Provider) StartRun(ctx context.Context, worker provider.WorkerRef, ws provider.WorkspaceRef, spec provider.RunSpec) (provider.RunHandle, error) {
	if err := p.check(ctx); err != nil {
		return provider.RunHandle{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runs = append(p.runs, spec)
	p.counter++
	h := provider.RunHandle{
		ID:        fmt.Sprintf("run-%d", p.counter),
		Worker:    worker,
		Workspace: ws,
	}
	p.polls[h.ID] = 0
	return h, nil
}

// Poll reports Running until PollsBeforeDone calls have been made, then
// reports the configured Grade.
func (p *Provider) Poll(ctx context.Context, h provider.RunHandle) (provider.RunStatus, error) {
	if err := p.check(ctx); err != nil {
		return provider.RunStatus{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.polls[h.ID]++
	count := p.polls[h.ID]
	if count < p.PollsBeforeDone {
		return provider.RunStatus{State: provider.StateRunning, RunID: h.ID}, nil
	}
	state := provider.StateDone
	if p.Grade == provider.GradeFailed {
		state = provider.StateFailed
	}
	return provider.RunStatus{
		State:   state,
		Verdict: &provider.Verdict{Grade: p.Grade, Explanation: "recorded by memory provider"},
		RunID:   h.ID,
		Usage:   provider.Usage{InputTokens: int64(count)},
	}, nil
}

// ListArtifacts returns Files as artifacts, ordered by path.
func (p *Provider) ListArtifacts(ctx context.Context, h provider.RunHandle) ([]provider.Artifact, error) {
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	paths := slices.Sorted(maps.Keys(p.Files))
	arts := make([]provider.Artifact, 0, len(paths))
	for i, path := range paths {
		arts = append(arts, provider.Artifact{ID: fmt.Sprintf("artifact-%d", i+1), Path: path})
	}
	return arts, nil
}

// FetchArtifact returns the bytes recorded for the artifact's path.
func (p *Provider) FetchArtifact(ctx context.Context, h provider.RunHandle, a provider.Artifact) ([]byte, error) {
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	body, ok := p.Files[a.Path]
	if !ok {
		return nil, fmt.Errorf("no such artifact: %s", a.Path)
	}
	return body, nil
}

// Transcript returns the configured events, or nothing.
func (p *Provider) Transcript(ctx context.Context, h provider.RunHandle) ([]provider.Event, error) {
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	return p.Events, nil
}

// CloseRun records the release. It never fails.
func (p *Provider) CloseRun(ctx context.Context, h provider.RunHandle) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = append(p.closed, h.ID)
	return nil
}

// check honors cancellation and the injected error.
func (p *Provider) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.Err
}
