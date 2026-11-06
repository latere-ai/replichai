// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package anthropic adapts the managed-agents backend currently in use.
//
// This is the only package that knows the backend's vocabulary, endpoints, or
// payload shapes. Everything above it speaks the neutral contract in
// internal/provider; porting to another backend means writing a sibling of
// this package and changing one setting.
//
// The credential and the API root arrive as options. Nothing here reads the
// environment: internal/runtimes decides which variable belongs to which
// runtime, so a key or an API root meant for one backend can never reach
// another through a second, unscoped path.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/anthropics/anthropic-sdk-go/v2/packages/param"

	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/wirejson"
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
// by completing and by dying. A decision from the status alone gets one of
// those cases wrong.
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
		// An unknown status is treated as still running: the run may yet
		// reach a state this adapter recognizes. It is not declared dead,
		// even though the caller may be waiting without a deadline, because
		// a session that has finished reaches "terminated", which is handled
		// above.
		return provider.StateRunning
	}
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

// WithAPIKey sets the credential.
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
// The credential comes from WithAPIKey and nowhere else. When no key is given
// the SDK's own resolution is left in place, so a logged-in CLI profile still
// works; that path cannot be inspected from here, so a genuinely missing
// credential surfaces on the first request rather than at construction.
func New(opts ...Option) (*Provider, error) {
	var cfg config
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

// EnsureWorkspace creates the sandbox's environment, or for a shared one
// returns the environment already created under its name. Anthropic installs
// an environment's packages once and caches them for every session that uses
// it, so a shared environment is what makes that cache apply.
func (p *Provider) EnsureWorkspace(ctx context.Context, spec provider.WorkspaceSpec) (provider.WorkspaceRef, error) {
	if spec.Shared {
		id, found, err := p.findEnvironment(ctx, spec.Name)
		if err != nil {
			return provider.WorkspaceRef{}, err
		}
		if found {
			return provider.WorkspaceRef{ID: id, Shared: true}, nil
		}
	}
	cloud := anthropic.BetaCloudConfigParams{}
	if spec.Network == provider.NetworkLimited {
		limited := &anthropic.BetaLimitedNetworkParams{}
		// Anthropic refuses an environment that installs packages behind a
		// limited network unless the network lets it reach the registries.
		if len(spec.Packages["pip"]) > 0 || len(spec.Packages["apt"]) > 0 {
			limited.AllowPackageManagers = param.NewOpt(true)
		}
		cloud.Networking = anthropic.BetaCloudConfigParamsNetworkingUnion{OfLimited: limited}
	} else {
		cloud.Networking = anthropic.BetaCloudConfigParamsNetworkingUnion{
			OfUnrestricted: &anthropic.BetaUnrestrictedNetworkParam{},
		}
	}
	if pip := spec.Packages["pip"]; len(pip) > 0 {
		cloud.Packages.Pip = pip
	}
	if apt := spec.Packages["apt"]; len(apt) > 0 {
		cloud.Packages.Apt = apt
	}

	env, err := p.client.Beta.Environments.New(ctx, anthropic.BetaEnvironmentNewParams{
		Name:   spec.Name,
		Config: anthropic.BetaEnvironmentNewParamsConfigUnion{OfCloud: &cloud},
	})
	if err != nil {
		return provider.WorkspaceRef{}, fmt.Errorf("create environment: %w", err)
	}
	return provider.WorkspaceRef{ID: env.ID, Shared: spec.Shared}, nil
}

// findEnvironment returns the live environment named name, if there is one.
// Two runs that both find none create one each; the next run reuses either,
// which costs one package install and nothing else.
func (p *Provider) findEnvironment(ctx context.Context, name string) (string, bool, error) {
	pages := p.client.Beta.Environments.ListAutoPaging(ctx, anthropic.BetaEnvironmentListParams{})
	for pages.Next() {
		env := pages.Current()
		if env.Name == name && env.ArchivedAt == "" {
			return env.ID, true, nil
		}
	}
	if err := pages.Err(); err != nil {
		return "", false, fmt.Errorf("list environments: %w", err)
	}
	return "", false, nil
}

// EnsureWorker creates the worker.
func (p *Provider) EnsureWorker(ctx context.Context, spec provider.WorkerSpec) (provider.WorkerRef, error) {
	params := anthropic.BetaAgentNewParams{
		Name:  spec.Name,
		Model: anthropic.BetaManagedAgentsModelConfigParams{ID: spec.Model},
		Tools: []anthropic.BetaAgentNewParamsToolUnion{{
			OfAgentToolset20260401: &anthropic.BetaManagedAgentsAgentToolset20260401Params{},
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
			URL:                spec.Mount.URL,
			AuthorizationToken: spec.Mount.Token,
		}
		if spec.Mount.MountPath != "" {
			repo.MountPath = param.NewOpt(spec.Mount.MountPath)
		}
		// A read-only mount is expressed by the token's GitHub scope, not by this
		// resource; the SDK's github_repository has no read-only field.
		// spec.Mount.ReadOnly is enforced only when the token is
		// read-only-scoped (006f).
		params.Resources = []anthropic.BetaSessionNewParamsResourceUnion{
			{OfGitHubRepository: &repo},
		}
	}

	// Attachments become uploaded file resources, mounted outside the repository
	// so an experiment's dataset (007b) cannot be committed into the reproduction.
	uploaded := make([]string, 0, len(spec.Attachments))
	rollback := func(cause error, sessionID string) error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		failures := []error{cause}
		if sessionID != "" {
			if _, err := p.client.Beta.Sessions.Delete(cleanupCtx, sessionID, anthropic.BetaSessionDeleteParams{}); err != nil {
				failures = append(failures, fmt.Errorf("rollback session %s: %w", sessionID, err))
			}
		}
		for _, u := range slices.Backward(uploaded) {
			if _, err := p.client.Beta.Files.Delete(cleanupCtx, u, anthropic.BetaFileDeleteParams{}); err != nil {
				failures = append(failures, fmt.Errorf("rollback file %s: %w", u, err))
			}
		}
		return errors.Join(failures...)
	}
	for _, att := range spec.Attachments {
		meta, err := p.client.Beta.Files.Upload(ctx, anthropic.BetaFileUploadParams{
			File: bytes.NewReader(att.Content),
		})
		if err != nil {
			return provider.RunHandle{}, rollback(fmt.Errorf("upload attachment: %w", err), "")
		}
		uploaded = append(uploaded, meta.ID)
		file := anthropic.BetaManagedAgentsFileResourceParams{
			FileID: meta.ID,
		}
		if att.Path != "" {
			file.MountPath = param.NewOpt(att.Path)
		}
		params.Resources = append(params.Resources, anthropic.BetaSessionNewParamsResourceUnion{OfFile: &file})
	}

	session, err := p.client.Beta.Sessions.New(ctx, params)
	if err != nil {
		return provider.RunHandle{}, rollback(fmt.Errorf("create session: %w", err), "")
	}

	// A graded audit starts with an outcome definition. An ungraded experiment
	// starts with an ordinary user message: sending an empty outcome silently
	// created a grader for work that explicitly asked only to run and report.
	var kickoff anthropic.BetaManagedAgentsEventParamsUnion
	if strings.TrimSpace(spec.Rubric) == "" && spec.MaxRevisions == 0 {
		kickoff = anthropic.BetaManagedAgentsEventParamsUnion{OfUserMessage: &anthropic.BetaManagedAgentsUserMessageEventParams{
			Content: []anthropic.BetaManagedAgentsUserMessageEventParamsContentUnion{{
				OfText: &anthropic.BetaManagedAgentsTextBlockParam{Text: spec.Task},
			}},
		}}
	} else {
		// The SDK fills in each "type" discriminator itself; the tests check that
		// it reaches the wire payload.
		outcome := &anthropic.BetaManagedAgentsUserDefineOutcomeEventParams{
			Description: spec.Task,
			Rubric: anthropic.BetaManagedAgentsUserDefineOutcomeEventParamsRubricUnion{
				OfText: &anthropic.BetaManagedAgentsTextRubricParams{
					Content: spec.Rubric,
				},
			},
		}
		if spec.MaxRevisions > 0 {
			outcome.MaxIterations = param.NewOpt(int64(spec.MaxRevisions))
		}
		kickoff = anthropic.BetaManagedAgentsEventParamsUnion{OfUserDefineOutcome: outcome}
	}

	if _, err := p.client.Beta.Sessions.Events.Send(ctx, session.ID, anthropic.BetaSessionEventSendParams{
		Events: []anthropic.BetaManagedAgentsEventParamsUnion{kickoff},
	}); err != nil {
		return provider.RunHandle{}, rollback(fmt.Errorf("send kickoff: %w", err), session.ID)
	}

	return provider.RunHandle{ID: session.ID, Worker: worker, Workspace: ws, Files: uploaded}, nil
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
		RunID:   h.ID, // persist the session id so a restart can close this run
		// The model the backend resolved the preference to, so a session is
		// priced at the rates of the model that ran it (011).
		Model: session.Agent.Model.ID, ModelProvider: "anthropic",
		Usage: provider.Usage{
			InputTokens:       session.Usage.InputTokens,
			OutputTokens:      session.Usage.OutputTokens,
			CachedInputTokens: session.Usage.CacheReadInputTokens,
			// Cache writes arrive split by lifetime and are carried split:
			// the charge is computed from the session total (011), and the
			// two lifetimes are priced differently.
			CacheWrite5mTokens: session.Usage.CacheCreation.Ephemeral5mInputTokens,
			CacheWrite1hTokens: session.Usage.CacheCreation.Ephemeral1hInputTokens,
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
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", a.Path, err)
	}
	return body, nil
}

// Transcript returns the run's full event stream, translated to neutral events.
// Each event is translated generically from its raw JSON into a type, a
// timestamp and best-effort text, with the original preserved in Raw, so the
// many event shapes need no per-variant mapping and nothing is lost.
func (p *Provider) Transcript(ctx context.Context, h provider.RunHandle) ([]provider.Event, error) {
	iter := p.client.Beta.Sessions.Events.ListAutoPaging(ctx, h.ID, anthropic.BetaSessionEventListParams{
		Betas: []anthropic.AnthropicBeta{anthropic.AnthropicBetaManagedAgents2026_04_01},
	})
	var events []provider.Event
	for iter.Next() {
		events = append(events, translateEvent([]byte(iter.Current().RawJSON())))
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("list session events for %s: %w", h.ID, err)
	}
	return events, nil
}

// translateEvent maps one backend event's raw JSON to a neutral Event. It reads
// the fields that carry meaning across the event types, namely text content, a
// tool name and input, a tool result, and a grade, and fills the Event from
// whichever are present, so no per-variant type switch is needed and Raw
// preserves everything.
func translateEvent(raw []byte) provider.Event {
	var head struct {
		Type          string         `json:"type"`
		ProcessedAt   time.Time      `json:"processed_at"`
		OccurredAt    time.Time      `json:"occurred_at"`
		CreatedAt     time.Time      `json:"created_at"`
		Content       jsontext.Value `json:"content"`
		Name          string         `json:"name"`
		MCPServerName string         `json:"mcp_server_name"`
		Input         jsontext.Value `json:"input"`
		IsError       bool           `json:"is_error"`
		Result        string         `json:"result"`
		Explanation   string         `json:"explanation"`
	}
	_ = json.Unmarshal(raw, &head, wirejson.Foreign)

	role, kind := splitEventType(head.Type)
	ev := provider.Event{
		Role: role,
		Kind: kind,
		Time: firstNonZeroTime(head.ProcessedAt, head.OccurredAt, head.CreatedAt),
		Raw:  append(jsontext.Value(nil), raw...),
	}
	if usage, ok := eventUsage(raw); ok {
		ev.Usage = &usage
	}

	switch {
	case head.Name != "": // a tool use: the tool and its input
		ev.Tool = head.Name
		if head.MCPServerName != "" {
			ev.Tool = head.MCPServerName + "/" + head.Name
		}
		ev.Content = compactJSON(head.Input)
	case len(head.Content) > 0: // a message or a tool result: text blocks
		ev.Content = blocksText(head.Content)
		if head.IsError && ev.Content != "" {
			ev.Content = "error: " + ev.Content
		}
	case head.Result != "" || head.Explanation != "": // a grade / outcome
		ev.Content = strings.TrimSpace(head.Result + " " + head.Explanation)
	}
	return ev
}

func eventUsage(raw []byte) (provider.Usage, bool) {
	var wrap struct {
		ModelUsage *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"model_usage"`
	}
	if json.Unmarshal(raw, &wrap, wirejson.Foreign) != nil || wrap.ModelUsage == nil {
		return provider.Usage{}, false
	}
	return provider.Usage{
		InputTokens: wrap.ModelUsage.InputTokens, OutputTokens: wrap.ModelUsage.OutputTokens,
		CachedInputTokens: wrap.ModelUsage.CacheReadInputTokens,
		CacheWriteTokens:  wrap.ModelUsage.CacheCreationInputTokens,
	}, true
}

// blocksText concatenates the text of an event's content blocks, or returns the
// content when it is a plain string. It is best-effort.
func blocksText(raw jsontext.Value) string {
	if len(raw) == 0 {
		return ""
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks, wirejson.Foreign) == nil {
		var b strings.Builder
		for _, bl := range blocks {
			if bl.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(bl.Text)
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	var s string
	if json.Unmarshal(raw, &s, wirejson.Foreign) == nil {
		return s
	}
	return ""
}

// compactJSON renders a tool input as compact JSON, or "" when empty.
func compactJSON(raw jsontext.Value) string {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return ""
	}
	compact := jsontext.Value(bytes.Clone(raw))
	if compact.Compact(wirejson.Foreign) != nil {
		return string(raw)
	}
	return string(compact)
}

// splitEventType turns a namespaced type like "agent.message" into a neutral
// role and kind. Anything not agent- or user-scoped is the system's own record.
func splitEventType(t string) (role provider.Role, kind provider.EventKind) {
	prefix, rest, ok := strings.Cut(t, ".")
	if !ok {
		return provider.RoleSystem, provider.EventKind(t)
	}
	switch prefix {
	case "agent":
		return provider.RoleAgent, provider.EventKind(rest)
	case "user":
		return provider.RoleUser, provider.EventKind(rest)
	default:
		return provider.RoleSystem, provider.EventKind(rest)
	}
}

func firstNonZeroTime(ts ...time.Time) time.Time {
	for _, t := range ts {
		if !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// CloseRun stops any live work before releasing the session. Archiving alone
// only hides a managed-agent session; user.interrupt pauses every active
// thread. Both operations are best-effort so closing an already terminal or
// archived session remains idempotent.
func (p *Provider) CloseRun(ctx context.Context, h provider.RunHandle) error {
	_, interruptErr := p.client.Beta.Sessions.Events.Send(ctx, h.ID, anthropic.BetaSessionEventSendParams{
		Events: []anthropic.BetaManagedAgentsEventParamsUnion{
			{OfUserInterrupt: &anthropic.BetaManagedAgentsUserInterruptEventParams{}},
		},
	})
	_, archiveErr := p.client.Beta.Sessions.Archive(ctx, h.ID, anthropic.BetaSessionArchiveParams{})
	failures := []error{closeRunError("interrupt", interruptErr), closeRunError("archive", archiveErr)}
	for _, v := range slices.Backward(h.Files) {
		_, err := p.client.Beta.Files.Delete(ctx, v, anthropic.BetaFileDeleteParams{})
		failures = append(failures, releaseError("delete file", err))
	}
	return errors.Join(failures...)
}

// ReleaseResources archives the created agent and deletes its environment.
// It is separate from CloseRun so evidence remains readable until retention.
func (p *Provider) ReleaseResources(ctx context.Context, worker provider.WorkerRef, workspace provider.WorkspaceRef) error {
	var failures []error
	if worker.ID != "" {
		_, err := p.client.Beta.Agents.Archive(ctx, worker.ID, anthropic.BetaAgentArchiveParams{})
		failures = append(failures, releaseError("archive agent", err))
	}
	// A shared environment outlives the run; other runs reuse it.
	if workspace.ID != "" && !workspace.Shared {
		_, err := p.client.Beta.Environments.Delete(ctx, workspace.ID, anthropic.BetaEnvironmentDeleteParams{})
		failures = append(failures, releaseError("delete environment", err))
	}
	return errors.Join(failures...)
}

func releaseError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusConflict) {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// closeRunError keeps CloseRun idempotent without hiding infrastructure
// failures. The managed-agents API uses not-found/conflict when work is already
// terminal or archived, and rejects an interrupt sent to an already archived
// session with 400; all of those are the requested end state. Other 400s are
// real failures, since a malformed event would stop live sessions being
// interrupted, so only the archived message is tolerated.
func closeRunError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if apiErr, ok := errors.AsType[*anthropic.Error](err); ok {
		switch apiErr.StatusCode {
		case http.StatusNotFound, http.StatusConflict:
			return nil
		case http.StatusBadRequest:
			if strings.Contains(strings.ToLower(apiErr.Error()), "archived session") {
				return nil
			}
		}
	}
	return fmt.Errorf("%s managed session: %w", operation, err)
}
