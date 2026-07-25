// Package anthropic adapts the managed-agents backend currently in use.
//
// This is the only package that knows the backend's vocabulary, endpoints, or
// payload shapes. Everything above it speaks the neutral contract in
// internal/provider; porting to another backend means writing a sibling of
// this package and changing one setting.
//
// Configuration comes from the environment:
//
//	REPLICHAI_API_KEY   the backend's API key. Optional: when unset, the SDK's
//	                    own resolution applies, so an already-exported backend
//	                    variable or a logged-in CLI profile is picked up.
//	REPLICHAI_API_BASE  API root (optional; for tests and self-hosted routing)
package anthropic

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

// modelTier turns a neutral preference into a substring to look for in the
// backend's model identifiers. An unrecognized preference is used verbatim,
// so a caller can name a model family the backend has but this list does not.
func modelTier(preference string) string {
	switch preference {
	case "best":
		return "opus"
	case "balanced":
		return "sonnet"
	case "fast":
		return "haiku"
	default:
		return preference
	}
}

// translateGrade converts a backend grade name into a neutral grade.
// An unrecognized name is undecided rather than a pass: a grade this adapter
// does not understand must never read as success.
func translateGrade(name string) provider.Grade {
	switch name {
	case "satisfied":
		return provider.GradePassed
	case "needs_revision":
		return provider.GradeNeedsRevision
	case "max_iterations_reached":
		return provider.GradeExhausted
	case "failed", "interrupted":
		return provider.GradeFailed
	default:
		return provider.GradePending
	}
}

// translateState converts one backend observation into a neutral run state.
//
// The backend's status and the grader's record are read together, because
// neither answers the question alone: the backend rests in "idle" both before
// a run is picked up and after it finishes, and it reaches "terminated" both
// by completing and by dying. Deciding from the status alone gets one of
// those two cases wrong every time.
func translateState(status string, verdict *provider.Verdict) provider.RunState {
	graded := verdict != nil && verdict.Grade.Decided()

	switch status {
	case "running", "rescheduling":
		return provider.StateRunning

	case "idle":
		switch {
		case verdict == nil:
			// Resting with no grader record at all: not started yet.
			// Reporting this as finished would abort a healthy run whose
			// first poll lands between kickoff and pickup.
			return provider.StatePending
		case !graded:
			// A recorded but undecided grade means the grader is working.
			return provider.StateRunning
		default:
			return provider.StateDone
		}

	case "terminated":
		// Terminated is irreversible. If the run was graded first it simply
		// finished and was released; if it was not, no verdict is ever
		// coming, and waiting for one would poll until the deadline.
		if graded {
			return provider.StateDone
		}
		return provider.StateFailed

	default:
		// An unfamiliar status is treated as still working: the run may yet
		// reach a state this adapter recognizes, and the deadline bounds it.
		return provider.StateRunning
	}
}

func init() {
	provider.Register("anthropic", func() (provider.Provider, error) { return New() })
}

// Provider talks to the managed-agents backend through its official SDK.
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

// New builds a Provider.
//
// REPLICHAI_API_KEY is the documented way to supply the credential. It holds
// the backend's ordinary API key; the name is neutral so the variable does not
// advertise the supplier, not because the value is different.
//
// When it is unset the SDK's own credential resolution is left in place, so an
// operator who already has the backend's environment variable exported, or a
// logged-in CLI profile, does not have to copy the same secret under a second
// name. That path cannot be inspected from here, so a genuinely missing
// credential surfaces on the first request rather than at construction.
func New(opts ...Option) (*Provider, error) {
	cfg := config{
		apiKey:  os.Getenv("REPLICHAI_API_KEY"),
		apiBase: os.Getenv("REPLICHAI_API_BASE"),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	// The SDK sets the managed-agents beta header on every Beta.* call and
	// retries 429s and 5xx on its own, so neither belongs here.
	var requestOpts []option.RequestOption
	if cfg.apiKey != "" {
		requestOpts = append(requestOpts, option.WithAPIKey(cfg.apiKey))
	}
	if cfg.apiBase != "" {
		requestOpts = append(requestOpts, option.WithBaseURL(cfg.apiBase))
	}
	requestOpts = append(requestOpts, cfg.extra...)

	return &Provider{client: anthropic.NewClient(requestOpts...)}, nil
}

// Name identifies the adapter.
func (p *Provider) Name() string { return "anthropic" }

// OutputDir is the sandbox path a worker writes its deliverable to.
func (p *Provider) OutputDir() string { return "/mnt/session/outputs/" }

// ResolveModel picks a concrete model for a neutral preference.
func (p *Provider) ResolveModel(ctx context.Context, pref string) (string, error) {
	page, err := p.client.Models.List(ctx, anthropic.ModelListParams{})
	if err != nil {
		return "", fmt.Errorf("list models: %w", err)
	}
	tier := modelTier(pref)
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
		Model: anthropic.BetaManagedAgentsModelConfigParams{ID: spec.Model},
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
	params := anthropic.BetaSessionNewParams{
		Agent: anthropic.BetaSessionNewParamsAgentUnion{
			OfString: param.NewOpt(worker.ID),
		},
		EnvironmentID: ws.ID,
		Title:         param.NewOpt("reproducibility audit"),
	}
	// A neutral RepoMount becomes this backend's github_repository resource, so
	// the worker commits its reproduction into a real repository as it works.
	if spec.Mount.Set() {
		repo := anthropic.BetaManagedAgentsGitHubRepositoryResourceParams{
			Type:               anthropic.BetaManagedAgentsGitHubRepositoryResourceParamsTypeGitHubRepository,
			URL:                spec.Mount.URL,
			AuthorizationToken: spec.Mount.Token,
		}
		if spec.Mount.MountPath != "" {
			repo.MountPath = param.NewOpt(spec.Mount.MountPath)
		}
		params.Resources = []anthropic.BetaSessionNewParamsResourceUnion{
			{OfGitHubRepository: &repo},
		}
	}

	session, err := p.client.Beta.Sessions.New(ctx, params)
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

	var verdict *provider.Verdict
	if n := len(session.OutcomeEvaluations); n > 0 {
		last := session.OutcomeEvaluations[n-1]
		verdict = &provider.Verdict{
			Grade:       translateGrade(last.Result),
			Explanation: last.Explanation,
		}
	}
	state := translateState(string(session.Status), verdict)

	return provider.RunStatus{
		State:   state,
		Verdict: verdict,
		Usage: provider.Usage{
			InputTokens:       session.Usage.InputTokens,
			OutputTokens:      session.Usage.OutputTokens,
			CachedInputTokens: session.Usage.CacheReadInputTokens,
		},
	}, nil
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
