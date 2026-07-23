// Package hosted adapts the hosted agent backend currently in use.
//
// This is the only package that knows the backend's vocabulary, endpoints, or
// payload shapes. Everything above it speaks the neutral contract in
// internal/provider; porting to another backend means writing a sibling of
// this package and changing one setting.
//
// Configuration comes from the environment:
//
//	REPLICHAI_API_KEY   credential (required)
//	REPLICHAI_API_BASE  API root (optional; for tests and self-hosted routing)
package hosted

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/latere-ai/replichai/internal/provider"
)

// ErrNoCredential reports that no credential was configured.
var ErrNoCredential = errors.New("no credential: set REPLICHAI_API_KEY")

// modelTiers maps neutral preferences onto substrings of backend model IDs.
var modelTiers = map[string]string{
	"best":     "opus",
	"balanced": "sonnet",
	"fast":     "haiku",
}

// grades maps backend grade names onto neutral grades.
var grades = map[string]provider.Grade{
	"satisfied":              provider.GradePassed,
	"needs_revision":         provider.GradeNeedsRevision,
	"max_iterations_reached": provider.GradeExhausted,
	"failed":                 provider.GradeFailed,
	"interrupted":            provider.GradeFailed,
	"pending":                provider.GradePending,
}

// states maps backend run states onto neutral states.
var states = map[string]provider.RunState{
	"idle":         provider.StateDone,
	"running":      provider.StateRunning,
	"rescheduling": provider.StateRunning,
	"terminated":   provider.StateDone,
}

func init() {
	provider.Register("hosted", func() (provider.Provider, error) { return New() })
}

// Provider talks to the hosted backend through its official SDK.
type Provider struct {
	client anthropic.Client
}

// Option configures a Provider.
type Option func(*config)

type config struct {
	apiKey  string
	apiBase string
	extra   []option.RequestOption
}

// WithAPIKey sets the credential explicitly instead of reading the environment.
func WithAPIKey(key string) Option { return func(c *config) { c.apiKey = key } }

// WithAPIBase overrides the API root. Tests point this at a local server.
func WithAPIBase(base string) Option { return func(c *config) { c.apiBase = base } }

// WithRequestOptions passes SDK options through, for anything this adapter
// does not surface directly.
func WithRequestOptions(opts ...option.RequestOption) Option {
	return func(c *config) { c.extra = append(c.extra, opts...) }
}

// New builds a Provider, failing if no credential is available.
func New(opts ...Option) (*Provider, error) {
	cfg := config{
		apiKey:  os.Getenv("REPLICHAI_API_KEY"),
		apiBase: os.Getenv("REPLICHAI_API_BASE"),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.apiKey == "" {
		return nil, ErrNoCredential
	}

	// The SDK sets the managed-agents beta header on every Beta.* call and
	// retries 429s and 5xx on its own, so neither belongs here.
	requestOpts := []option.RequestOption{option.WithAPIKey(cfg.apiKey)}
	if cfg.apiBase != "" {
		requestOpts = append(requestOpts, option.WithBaseURL(cfg.apiBase))
	}
	requestOpts = append(requestOpts, cfg.extra...)

	return &Provider{client: anthropic.NewClient(requestOpts...)}, nil
}

// Name identifies the adapter.
func (p *Provider) Name() string { return "hosted" }

// OutputDir is the sandbox path a worker writes its deliverable to.
func (p *Provider) OutputDir() string { return "/mnt/session/outputs/" }

// ResolveModel picks a concrete model for a neutral preference.
func (p *Provider) ResolveModel(ctx context.Context, pref string) (string, error) {
	page, err := p.client.Models.List(ctx, anthropic.ModelListParams{})
	if err != nil {
		return "", fmt.Errorf("list models: %w", err)
	}
	tier, ok := modelTiers[pref]
	if !ok {
		tier = pref
	}
	var first string
	for _, entry := range page.Data {
		if first == "" {
			first = entry.ID
		}
		if strings.Contains(entry.ID, tier) {
			return entry.ID, nil
		}
	}
	if first == "" {
		return "", errors.New("backend returned no models")
	}
	return first, nil
}

// EnsureWorkspace creates the sandbox.
func (p *Provider) EnsureWorkspace(ctx context.Context, spec provider.WorkspaceSpec) (provider.WorkspaceRef, error) {
	cloud := anthropic.BetaCloudConfigParams{}
	if spec.Network == provider.NetworkLimited {
		cloud.Networking = anthropic.BetaCloudConfigParamsNetworkingUnion{
			OfLimited: &anthropic.BetaLimitedNetworkParams{},
		}
	} else {
		cloud.Networking = anthropic.BetaCloudConfigParamsNetworkingUnion{
			OfUnrestricted: &anthropic.BetaUnrestrictedNetworkParam{},
		}
	}
	if pip := spec.Packages["pip"]; len(pip) > 0 {
		cloud.Packages.Pip = pip
	}

	env, err := p.client.Beta.Environments.New(ctx, anthropic.BetaEnvironmentNewParams{
		Name:   spec.Name,
		Config: anthropic.BetaEnvironmentNewParamsConfigUnion{OfCloud: &cloud},
	})
	if err != nil {
		return provider.WorkspaceRef{}, fmt.Errorf("create environment: %w", err)
	}
	return provider.WorkspaceRef{ID: env.ID}, nil
}

// EnsureWorker creates the worker.
func (p *Provider) EnsureWorker(ctx context.Context, spec provider.WorkerSpec) (provider.WorkerRef, error) {
	params := anthropic.BetaAgentNewParams{
		Name:  spec.Name,
		Model: anthropic.BetaManagedAgentsModelConfigParams{ID: anthropic.BetaManagedAgentsModel(spec.Model)},
		Tools: []anthropic.BetaAgentNewParamsToolUnion{{
			OfAgentToolset20260401: &anthropic.BetaManagedAgentsAgentToolset20260401Params{
				Type: anthropic.BetaManagedAgentsAgentToolset20260401ParamsTypeAgentToolset20260401,
			},
		}},
	}
	if spec.Instructions != "" {
		params.System = param.NewOpt(spec.Instructions)
	}
	if spec.Description != "" {
		params.Description = param.NewOpt(spec.Description)
	}

	agent, err := p.client.Beta.Agents.New(ctx, params)
	if err != nil {
		return provider.WorkerRef{}, fmt.Errorf("create agent: %w", err)
	}
	revision := int(agent.Version)
	if revision == 0 {
		revision = 1
	}
	return provider.WorkerRef{ID: agent.ID, Revision: revision}, nil
}

// StartRun creates a run and hands it the task, the rubric, and its budget.
func (p *Provider) StartRun(ctx context.Context, worker provider.WorkerRef, ws provider.WorkspaceRef, spec provider.RunSpec) (provider.RunHandle, error) {
	session, err := p.client.Beta.Sessions.New(ctx, anthropic.BetaSessionNewParams{
		Agent: anthropic.BetaSessionNewParamsAgentUnion{
			OfString: param.NewOpt(worker.ID),
		},
		EnvironmentID: ws.ID,
		Title:         param.NewOpt("reproducibility audit"),
	})
	if err != nil {
		return provider.RunHandle{}, fmt.Errorf("create session: %w", err)
	}

	// Every discriminator is set explicitly: these fields are omitempty in the
	// SDK's param structs, so leaving them zero drops "type" from the payload
	// and the union arrives on the wire unidentified.
	outcome := &anthropic.BetaManagedAgentsUserDefineOutcomeEventParams{
		Type:        anthropic.BetaManagedAgentsUserDefineOutcomeEventParamsTypeUserDefineOutcome,
		Description: spec.Task,
		Rubric: anthropic.BetaManagedAgentsUserDefineOutcomeEventParamsRubricUnion{
			OfText: &anthropic.BetaManagedAgentsTextRubricParams{
				Type:    anthropic.BetaManagedAgentsTextRubricParamsTypeText,
				Content: spec.Rubric,
			},
		},
	}
	if spec.MaxRevisions > 0 {
		outcome.MaxIterations = param.NewOpt(int64(spec.MaxRevisions))
	}

	if _, err := p.client.Beta.Sessions.Events.Send(ctx, session.ID, anthropic.BetaSessionEventSendParams{
		Events: []anthropic.BetaManagedAgentsEventParamsUnion{{OfUserDefineOutcome: outcome}},
	}); err != nil {
		return provider.RunHandle{}, fmt.Errorf("send outcome: %w", err)
	}

	return provider.RunHandle{ID: session.ID, Worker: worker, Workspace: ws}, nil
}

// Poll reports one observation of a run.
func (p *Provider) Poll(ctx context.Context, h provider.RunHandle) (provider.RunStatus, error) {
	session, err := p.client.Beta.Sessions.Get(ctx, h.ID, anthropic.BetaSessionGetParams{})
	if err != nil {
		return provider.RunStatus{}, fmt.Errorf("get session %s: %w", h.ID, err)
	}

	state, ok := states[string(session.Status)]
	if !ok {
		state = provider.StateRunning
	}

	var verdict *provider.Verdict
	if n := len(session.OutcomeEvaluations); n > 0 {
		last := session.OutcomeEvaluations[n-1]
		grade, ok := grades[string(last.Result)]
		if !ok {
			grade = provider.GradePending
		}
		verdict = &provider.Verdict{Grade: grade, Explanation: last.Explanation}
	}

	switch {
	case verdict == nil && state == provider.StateDone:
		// The backend parks a run in the same resting state both before work
		// begins and after it ends, so resting plus no grader record means
		// "not started yet", not "finished". Calling it terminal here would
		// abort a healthy run whose first poll lands in the window between
		// kickoff and pickup.
		state = provider.StatePending
	case verdict != nil && !verdict.Grade.Decided():
		// A recorded but undecided grade means the grader is still working.
		state = provider.StateRunning
	}

	usage := map[string]any{
		"input_tokens":  session.Usage.InputTokens,
		"output_tokens": session.Usage.OutputTokens,
	}
	return provider.RunStatus{State: state, Verdict: verdict, Usage: usage}, nil
}

// ListArtifacts lists what a run produced.
func (p *Provider) ListArtifacts(ctx context.Context, h provider.RunHandle) ([]provider.Artifact, error) {
	page, err := p.client.Beta.Files.List(ctx, anthropic.BetaFileListParams{
		ScopeID: param.NewOpt(h.ID),
		Betas:   []anthropic.AnthropicBeta{anthropic.AnthropicBetaManagedAgents2026_04_01},
	})
	if err != nil {
		return nil, fmt.Errorf("list files for %s: %w", h.ID, err)
	}
	arts := make([]provider.Artifact, 0, len(page.Data))
	for _, entry := range page.Data {
		arts = append(arts, provider.Artifact{ID: entry.ID, Path: entry.Filename})
	}
	return arts, nil
}

// FetchArtifact downloads one artifact.
func (p *Provider) FetchArtifact(ctx context.Context, h provider.RunHandle, a provider.Artifact) ([]byte, error) {
	resp, err := p.client.Beta.Files.Download(ctx, a.ID, anthropic.BetaFileDownloadParams{
		Betas: []anthropic.AnthropicBeta{anthropic.AnthropicBetaManagedAgents2026_04_01},
	})
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", a.Path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", a.Path, err)
	}
	return body, nil
}

// CloseRun releases a finished run. Releasing is best-effort: a run that is
// already released is not an error worth propagating.
func (p *Provider) CloseRun(ctx context.Context, h provider.RunHandle) error {
	_, _ = p.client.Beta.Sessions.Archive(ctx, h.ID, anthropic.BetaSessionArchiveParams{})
	return nil
}
