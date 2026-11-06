// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/fetch"
	"github.com/latere-ai/replichai/internal/github"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/publish"
	"github.com/latere-ai/replichai/internal/registry"
	"github.com/latere-ai/replichai/internal/runtimes"
	"github.com/latere-ai/replichai/internal/runtimes/builtin"
	"github.com/latere-ai/replichai/internal/wirejson"
)

// engineRunner drives a job's phases with the real engine. It belongs to the
// composition root, so it may import internal/audit and its method, which
// internal/job and internal/httpapi must not import. It dispatches on Kind; it
// runs audits now, and the experiment path arrives with 007b's server wiring.
//
// Every session, however old its row, goes through the managed controller. A
// row that carries only a backend run id is addressed as a legacy reference,
// which the anthropic-managed runtime can poll, cancel, and read. There is no
// second engine behind the controller, so there is no path on which a nil one
// could be reached. Build one with newEngineRunner, which refuses to construct
// a runner without its dependencies.
type engineRunner struct {
	controller *managed.Controller
	// sessions finds the runtime behind a session reference, for the one read
	// the controller does not expose: a settled legacy row's transcript, at
	// publish time.
	sessions managed.RuntimeRouter
	method   method.Method
	fetch    paper.Fetcher
	projects project.Store
	jobs     jobStatusReader
	workDir  string
	// The forge (006i). When gh is nil, as in a local run with no token, the
	// runner neither creates a repository nor publishes, and the session's
	// transcript is readable live only. owner is the organization repositories
	// are created under; token is the mount credential, passed to the platform
	// egress and never to the sandbox.
	gh    forge
	owner string
	token string
	// delay pauses at the start of each phase. It exists for development: with
	// the instant memory provider a job finishes in about a second, so a small
	// delay makes the phase progression observable over SSE. It is zero in
	// production.
	delay time.Duration
	// router binds each new session to the runtime its job chose (048), and
	// newController builds the controller over that binding. Without a
	// router every session runs where the shared controller routes it.
	router        *runtimes.Router
	newController func(managed.RuntimeRouter) *managed.Controller
	// signIn gives, for a person, the source of the bearer a runtime the
	// person pays acts with on the Latere platform. Nil where the deployment
	// keeps no sign-ins.
	signIn func(principal string) func(context.Context) (string, error)
}

// jobStatusReader reads a job's latest observed status at publish time. The
// verdict and the backend run id are set on the Observe path, not carried on the
// phase transition, so the publishing phase reloads them from the store.
type jobStatusReader interface {
	Get(ctx context.Context, id string) (job.Job, error)
}

// engineDeps is what an engineRunner is built from. Every field but the forge
// is required; the forge is the only dependency a deployment may run without.
type engineDeps struct {
	controller *managed.Controller
	sessions   managed.RuntimeRouter
	method     method.Method
	fetch      paper.Fetcher
	projects   project.Store
	jobs       jobStatusReader
	workDir    string
	gh         forge
	owner      string
	token      string
	delay      time.Duration
	router     *runtimes.Router
	signIn     func(principal string) func(context.Context) (string, error)
}

// errEngineIncomplete is what newEngineRunner returns for a missing dependency.
var errEngineIncomplete = errors.New("engine runner is missing a dependency")

// newEngineRunner wires the runner and refuses a missing dependency at boot,
// where it is a one-line startup error, rather than at the first job that
// happens to take the path through it.
func newEngineRunner(d engineDeps) (*engineRunner, error) {
	switch {
	case d.controller == nil:
		return nil, fmt.Errorf("%w: controller", errEngineIncomplete)
	case d.sessions == nil:
		return nil, fmt.Errorf("%w: session runtimes", errEngineIncomplete)
	case d.fetch == nil:
		return nil, fmt.Errorf("%w: paper fetcher", errEngineIncomplete)
	case d.jobs == nil:
		return nil, fmt.Errorf("%w: job store", errEngineIncomplete)
	case d.workDir == "":
		return nil, fmt.Errorf("%w: work directory", errEngineIncomplete)
	}
	// Each session's evidence is written under the work directory when the
	// session ends. Proving it writable here makes a deployment that cannot
	// write it fail to start, rather than fail every session at its end after
	// the session's cost is spent.
	if err := writableDir(d.workDir); err != nil {
		return nil, fmt.Errorf("work directory: %w", err)
	}
	return &engineRunner{
		controller: d.controller, sessions: d.sessions, method: d.method, fetch: d.fetch,
		projects: d.projects, jobs: d.jobs, workDir: d.workDir,
		gh: d.gh, owner: d.owner, token: d.token, delay: d.delay,
		router: d.router, signIn: d.signIn,
		newController: func(router managed.RuntimeRouter) *managed.Controller { return managed.NewController(router) },
	}, nil
}

// writableDir creates dir if it is missing and proves it accepts a file.
func writableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	probe, err := os.CreateTemp(dir, ".writable-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		return errors.Join(err, os.Remove(name))
	}
	return os.Remove(name)
}

// actAs attaches the person a session belongs to, so a runtime the person
// pays acts as them on the Latere platform. It changes nothing for a runtime
// the deployment pays.
func (e *engineRunner) actAs(ctx context.Context, principal string) context.Context {
	if e.signIn == nil || principal == "" {
		return ctx
	}
	return builtin.WithLatereSignIn(ctx, e.signIn(principal))
}

// sessionFor is the controller a new session of j runs through, bound to the
// runtime the job chose, and whether that runtime takes a writable
// repository: one that does not is not mounted, and its work is harvested
// into the repository when it ends (044b).
func (e *engineRunner) sessionFor(ctx context.Context, j *job.Job) (*managed.Controller, bool, error) {
	if e.router == nil {
		return e.controller, true, nil
	}
	route := e.router.Route()
	if j.Runtime != "" {
		route = runtimes.Route{Version: runtimes.RouteVersion, Runtime: managed.RuntimeName(j.Runtime)}
	}
	bound, err := e.router.Bind(route)
	if err != nil {
		return nil, false, err
	}
	runtime, err := bound.ForNew(managed.SessionSpec{})
	if err != nil {
		return nil, false, err
	}
	capabilities, err := runtime.Capabilities(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("read the %s runtime's capabilities: %w", runtime.Name(), err)
	}
	return e.newController(bound), capabilities.RepositoryModes[managed.AccessReadWrite], nil
}

// modelOf is what the session runs on: the exact model the person chose, or
// the tier.
func modelOf(j *job.Job) string {
	if j.Model != "" {
		return j.Model
	}
	return j.Tier.OrDefault().String()
}

// forge is the repository host the runner creates repositories in and
// finalizes against (006i). Prepare allocates a collision-free name and creates
// the private repository; the rest is publish.LiveForge, so the shared finalize
// drives it unchanged. It is backed by GitHub in production and by a fake in
// tests, so the runner needs no network to test its phase logic.
type forge interface {
	publish.LiveForge
	// Prepare allocates a collision-free repository name for the paper under the
	// owner and creates it private, returning the name. It is idempotent: an
	// existing repository of that name is reused rather than reported as an
	// error.
	Prepare(ctx context.Context, owner string, id paper.Identity) (name string, err error)
}

// githubForge is the production forge: a github client that also allocates names
// through the derived registry (003). It embeds *github.Client, so it already
// satisfies publish.LiveForge.
type githubForge struct{ *github.Client }

// Prepare allocates a collision-free name and creates the private repository.
func (f githubForge) Prepare(ctx context.Context, owner string, id paper.Identity) (string, error) {
	name, err := registry.New(registry.NewGitHubSource(f.Client, owner)).AllocateName(ctx, id)
	if err != nil {
		return "", fmt.Errorf("allocate repo name: %w", err)
	}
	_, err = f.CreateRepo(ctx, owner, name, true)
	if errors.Is(err, github.ErrRepoExists) {
		_, err = f.GetRepo(ctx, owner, name)
	}
	if err != nil {
		return "", fmt.Errorf("prepare repository: %w", err)
	}
	return name, nil
}

// mountFor builds the repository mount for a job whose repo was created in the
// resolving phase. The token authenticates the clone at the platform's egress;
// per the managed-agents vaults model it is never readable inside the sandbox.
func (e *engineRunner) mountFor(j *job.Job) provider.RepoMount {
	return provider.RepoMount{
		URL:       "https://github.com/" + j.RepoName,
		Token:     e.token,
		MountPath: audit.DefaultMountPath,
	}
}

// splitRepo splits a stored "owner/name" into its parts.
func splitRepo(full string) (owner, name string) {
	if i := lastSlash(full); i >= 0 {
		return full[:i], full[i+1:]
	}
	return "", full
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

// pause waits out the configured per-phase delay, honoring cancellation.
func (e *engineRunner) pause(ctx context.Context) {
	if e.delay <= 0 {
		return
	}
	t := time.NewTimer(e.delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// Resolve validates the paper reference against the resolver; it is the
// resolving phase. A reference that cannot resolve fails the job here, before
// a run.
func (e *engineRunner) Resolve(ctx context.Context, j *job.Job) error {
	e.pause(ctx)
	if j.Kind != job.KindAudit {
		return fmt.Errorf("job kind %q is not yet wired in this build", j.Kind)
	}
	id, err := paper.Resolve(ctx, j.Ref, paper.WithFetcher(e.fetch))
	if err != nil {
		return fmt.Errorf("resolve %q: %w", j.Ref, err)
	}
	// Enrich the project's offline placeholder title with the resolved paper
	// title, but only while it is still a placeholder, so a project that already
	// carries a real title is never overwritten. This is best-effort: a failure
	// here must not fail the run.
	if id.Title != "" && e.projects != nil && j.ProjectID != "" {
		if p, err := e.projects.Get(ctx, j.ProjectID); err == nil && e.isPlaceholderTitle(ctx, p, j.Ref) {
			_ = e.projects.SetTitle(ctx, j.ProjectID, id.Title)
		}
	}

	// With a forge configured, create the reproduction's repository up front and
	// record the resolved identity into it, so the run can mount it and commit
	// live (006i). The name is allocated collision-free through the registry.
	// RepoName is set on the job here and persisted at the transition into
	// running, so it is durable before the mount and survives a restart.
	if e.gh != nil && j.RepoName == "" {
		name, err := e.gh.Prepare(ctx, e.owner, id)
		if err != nil {
			return err
		}
		// This is best-effort: GitHub refuses it on a repository with no commit
		// yet, and retaining and publishing both re-resolve when it is absent.
		if err := publish.RecordIdentity(ctx, e.gh, e.owner, name, id); err != nil {
			slog.WarnContext(ctx, "resolve: recording the paper identity", "session", j.ID, "error", err)
		}
		j.RepoName = e.owner + "/" + name
	}
	return nil
}

// Execute runs the audit, reporting progress through observe. A fresh Auditor
// per job carries that job's observer.
func (e *engineRunner) Execute(ctx context.Context, j *job.Job, observe func(provider.RunStatus)) error {
	e.pause(ctx)
	ctx = e.actAs(ctx, j.Principal)
	controller, mounts, err := e.sessionFor(ctx, j)
	if err != nil {
		return err
	}
	options := []audit.SessionOption{
		audit.WithSessionHooks(e.managedHooks(j, observe)),
		// The wait has no wall-clock deadline: the backend session is the
		// source of truth, and a deadline would mark a session failed while
		// its agent is still committing the reproduction.
		audit.WithoutSessionTimeout(),
		audit.WithSessionModel(modelOf(j)),
	}
	// With the repository created in resolving, mount it so the agent commits
	// its reproduction into it live (006i); the mount is also the
	// redistribution boundary, since only what is committed into it is
	// published. A runtime that takes no writable repository, such as the
	// Latere platform, is not mounted; its outputs are harvested into the
	// repository when it ends.
	if e.gh != nil && j.RepoName != "" && mounts {
		mount := e.mountFor(j)
		options = append(options, audit.WithSessionMount(audit.SessionMount{
			URL: mount.URL, Token: mount.Token, MountPath: mount.MountPath, ReadOnly: mount.ReadOnly,
		}))
	}
	_, err = audit.NewSessionAuditor(controller, e.method, options...).Run(ctx, j.Ref, filepath.Join(e.workDir, j.ID))
	return err
}

// Resume rejoins the managed-agents session a restart left in flight, using
// the reference observed onto the job, and drives it to completion, so the
// job's state tracks the real backend session rather than being assumed
// failed.
func (e *engineRunner) Resume(ctx context.Context, j *job.Job, observe func(provider.RunStatus)) error {
	ref, ok, err := j.ManagedSessionRef()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("running job has no managed session reference")
	}
	_, err = audit.NewSessionAuditor(e.controller, e.method,
		audit.WithSessionHooks(e.managedHooks(j, observe)),
		audit.WithoutSessionTimeout(),
		audit.WithSessionModel(modelOf(j)),
	).Resume(e.actAs(ctx, j.Principal), ref, j.Ref, filepath.Join(e.workDir, j.ID))
	return err
}

// Close releases a managed-agents run by its session reference, so a run left
// in flight by a restart is stopped and archived rather than orphaned. A legacy
// reference, which is a run id with no locator, is routed like any other: the
// runtime it names can address it. Close is idempotent.
func (e *engineRunner) Close(ctx context.Context, ref managed.SessionRef) error {
	if ref.ID == "" {
		return nil
	}
	_, err := e.controller.Cancel(e.actAs(ctx, job.SessionOwner(ctx)), ref, managed.Hooks{})
	return err
}

type jobObserver interface {
	Observe(context.Context, string, provider.RunStatus) error
}

func (e *engineRunner) managedHooks(j *job.Job, observe func(provider.RunStatus)) managed.Hooks {
	persistRef := func(ctx context.Context, ref managed.SessionRef) error {
		raw, err := json.Marshal(ref, wirejson.Stored, wirejson.Embedding)
		if err != nil {
			return err
		}
		status := j.Status
		status.RunID, status.Runtime, status.SessionRef = ref.ID, string(ref.Runtime), raw
		j.Status = status
		store, ok := e.jobs.(jobObserver)
		if !ok {
			return errors.New("job store cannot persist managed session references")
		}
		return store.Observe(ctx, j.ID, status)
	}
	return managed.Hooks{
		Started:    func(ctx context.Context, ref managed.SessionRef) error { return persistRef(ctx, ref) },
		Checkpoint: func(ctx context.Context, _, next managed.SessionRef) error { return persistRef(ctx, next) },
		Status: func(ctx context.Context, status managed.Status) error {
			converted := providerStatus(status)
			j.Status = converted
			store, ok := e.jobs.(jobObserver)
			if !ok {
				return errors.New("job store cannot persist managed session status")
			}
			if err := store.Observe(ctx, j.ID, converted); err != nil {
				return err
			}
			observe(converted)
			return nil
		},
		Retain: func(ctx context.Context, evidence managed.EvidenceReader) error {
			if e.gh == nil || j.RepoName == "" {
				return nil
			}
			owner, name := splitRepo(j.RepoName)
			id, ok, err := publish.RecordedIdentity(ctx, e.gh, owner, name)
			if err != nil {
				return fmt.Errorf("read recorded paper identity: %w", err)
			}
			if !ok {
				// Resolving records the identity before the session starts, but
				// GitHub refuses that commit on a repository with no commit yet.
				// The paper is resolved again here, and the identity recorded
				// now that the repository has the agent's commits.
				if id, err = paper.Resolve(ctx, j.Ref, paper.WithFetcher(e.fetch)); err != nil {
					return fmt.Errorf("resolve paper identity: %w", err)
				}
				if err := publish.RecordIdentity(ctx, e.gh, owner, name, id); err != nil {
					slog.WarnContext(ctx, "retain: recording the paper identity", "session", j.ID, "error", err)
				}
			}
			if id.Key == "" {
				return errors.New("managed retention requires a non-empty paper identity")
			}
			grade := provider.GradeUnknown
			if evidence.Status.Verdict != nil {
				grade = providerGrade(evidence.Status.Verdict.Grade)
			}
			// Harvest before finalizing (044b). The mount is not the only way out
			// of a session: an agent may write its reproduction, copy it to the
			// runtime's output directory, and never push. The sandbox is then
			// released and the output directory is the only copy left. Finalize
			// reads verdict.json from the committed tree, so a recovered one has to
			// be in the tree before the manifest is written.
			harvestOutputs(ctx, e.gh, owner, name, j.ID, evidence)
			// The session's own account of the run, with what the job adds:
			// the project it belongs to and the tier that was asked for.
			record := publish.RunOf(evidence.Status).WithModels(evidence.Events)
			record.Project, record.RequestedModel = j.ProjectID, string(j.Tier)
			// Only the process that specified the session knows its prompt,
			// and it is the one whose build ran it. A session adopted from
			// another replica has neither here, and the manifest keeps what
			// the starting process wrote.
			if digest := audit.PromptDigest(ctx); digest != "" {
				record.PromptDigest, record.Build = digest, version
			}
			_, err = publish.FinalizeLive(ctx, e.gh, owner, name, id, e.method.Digest(), grade, record, providerEvents(evidence.Events), time.Now)
			return err
		},
	}
}

func providerStatus(status managed.Status) provider.RunStatus {
	// The controller calls this only from its Status hook, and it validates the
	// observed reference immediately before that hook runs: SessionRef.Validate
	// unmarshals the locator, so the one field here that could refuse to encode
	// is already proven to be valid JSON.
	raw, _ := json.Marshal(status.Ref, wirejson.Stored, wirejson.Embedding)
	result := provider.RunStatus{
		State: providerState(status.State), RunID: status.Ref.ID, Runtime: string(status.Ref.Runtime), SessionRef: raw,
		Model: status.Model.ID, ModelProvider: status.Model.Provider, BillingVariant: status.Model.BillingVariant,
		Usage: provider.Usage{
			InputTokens: status.Usage.InputFresh, CachedInputTokens: status.Usage.InputCached,
			CacheWriteTokens: status.Usage.CacheWrite, CacheWrite5mTokens: status.Usage.CacheWrite5m,
			CacheWrite1hTokens: status.Usage.CacheWrite1h, OutputTokens: status.Usage.Output,
		},
		ChargedCost: status.Usage.Extra[managed.MeterChargedCost],
	}
	if status.Verdict != nil {
		result.Verdict = &provider.Verdict{Grade: providerGrade(status.Verdict.Grade), Explanation: status.Verdict.Explanation}
	}
	return result
}

func providerState(state managed.State) provider.RunState {
	switch state {
	case managed.StatePending:
		return provider.StatePending
	case managed.StateRunning:
		return provider.StateRunning
	case managed.StateDone:
		return provider.StateDone
	case managed.StateFailed:
		return provider.StateFailed
	default:
		return provider.StateUnknown
	}
}

func providerGrade(grade managed.Grade) provider.Grade {
	return provider.Grade(grade)
}

func providerEvents(events []managed.Event) []provider.Event {
	result := make([]provider.Event, 0, len(events))
	for _, event := range events {
		translated := provider.Event{
			Role: provider.Role(event.Role), Kind: provider.EventKind(event.Kind), Tool: event.Tool,
			Content: event.Content, Time: event.Time, Raw: event.Raw,
		}
		if event.Usage != nil {
			translated.Usage = &provider.Usage{
				InputTokens: event.Usage.InputFresh, CachedInputTokens: event.Usage.InputCached,
				CacheWriteTokens: event.Usage.CacheWrite, CacheWrite5mTokens: event.Usage.CacheWrite5m,
				CacheWrite1hTokens: event.Usage.CacheWrite1h, OutputTokens: event.Usage.Output,
			}
		}
		result = append(result, translated)
	}
	return result
}

// Publish finalizes the live-mount reproduction (006i): the agent has already
// committed its work into the mounted repository, so this verifies the committed
// tree, writes the manifest, and commits the transcript beside it, through the
// same publish.FinalizeLive the CLI drives. With no forge, or a job that never
// got a repository, it is a no-op and the session's transcript stays readable
// live.
func (e *engineRunner) Publish(ctx context.Context, j *job.Job) error {
	e.pause(ctx)
	if e.gh == nil || j.RepoName == "" {
		return nil
	}
	ref, ok, err := j.ManagedSessionRef()
	if err != nil {
		return err
	}
	if ok && len(j.Status.SessionRef) > 0 {
		// The controller's Retain hook committed transcript and manifest before
		// release; this phase remains as the durable job-state transition.
		return nil
	}
	// What remains is a legacy row: a run id observed before session references
	// were persisted. Its repository still needs a manifest and a transcript.
	owner, name := splitRepo(j.RepoName)

	// The verdict and the backend run id were observed onto the job during the
	// run; reload the latest, since the phase transition did not carry them. A
	// read failure fails the publish, which retries, rather than writing a
	// manifest that records "unknown" for a run that passed. A wrong manifest
	// would persist, since nothing re-finalizes once it is written.
	cur, err := e.jobs.Get(ctx, j.ID)
	if err != nil {
		return fmt.Errorf("reload job status for publish: %w", err)
	}
	verdict := provider.GradeUnknown
	if cur.Status.Verdict != nil {
		verdict = cur.Status.Verdict.Grade
	}
	runID := cur.Status.RunID
	if runID == "" {
		runID = j.Status.RunID
	}

	// Prefer the identity recorded in the repository, which resolving wrote;
	// fall back to re-resolving only if it is absent, to avoid the paper
	// source's rate limit.
	id, ok, err := publish.RecordedIdentity(ctx, e.gh, owner, name)
	if err != nil {
		return job.RetryablePublish(fmt.Errorf("read recorded paper identity: %w", err))
	}
	if !ok {
		id, err = paper.Resolve(ctx, j.Ref, paper.WithFetcher(e.fetch))
		if err != nil {
			return job.RetryablePublish(fmt.Errorf("resolve paper identity for publish: %w", err))
		}
	}
	if id.Key == "" {
		return errors.New("publish requires a non-empty paper identity")
	}

	// Commit the transcript alongside the manifest, read through the runtime
	// the reference names. A transient read failure is retried; a runtime this
	// deployment no longer installs is not, since no retry would install it,
	// and the manifest is published with the transcript noted as unavailable.
	var events []provider.Event
	if runID != "" {
		if !ok {
			ref = managed.SessionRef{Runtime: job.LegacyRuntime, ID: runID, Revision: 1, LocatorVersion: 1}
		}
		events, err = e.legacyTranscript(ctx, ref)
		if err != nil {
			return job.RetryablePublish(fmt.Errorf("read transcript for run %s: %w", runID, err))
		}
	}

	// This path finalizes without the session in hand, so it knows nothing
	// about the run and leaves whatever record an earlier finalize wrote.
	_, err = publish.FinalizeLive(ctx, e.gh, owner, name, id, e.method.Digest(), verdict, publish.Run{}, events, time.Now)
	return err
}

// legacyTranscript reads a settled legacy session's events through the runtime
// its reference names. A reference no installed runtime answers to yields no
// transcript and no error: there is nothing a retry could change.
func (e *engineRunner) legacyTranscript(ctx context.Context, ref managed.SessionRef) ([]provider.Event, error) {
	runtime, err := e.sessions.ForRef(ref)
	if err != nil {
		slog.WarnContext(ctx, "publish: no runtime answers to a legacy session; publishing without its transcript",
			"runtime", ref.Runtime, "run", ref.ID, "error", err)
		return nil, nil
	}
	events, err := runtime.Events(ctx, ref)
	if err != nil {
		return nil, err
	}
	return providerEvents(events), nil
}

// paperResolver previews a reference for the New-project screen (006j): it
// resolves the reference to its identity through the same HTTP fetcher the
// worker uses. An unresolvable reference is mapped to httpapi.ErrUnresolvable,
// which is a 400; any other failure is a source that could not be reached,
// which is a 502.
type paperResolver struct{ fetch paper.Fetcher }

// Resolve previews one reference's identity.
func (p paperResolver) Resolve(ctx context.Context, ref string) (httpapi.ResolvedPaper, error) {
	id, err := paper.Resolve(ctx, ref, paper.WithFetcher(p.fetch))
	if errors.Is(err, paper.ErrUnresolvable) {
		return httpapi.ResolvedPaper{}, httpapi.ErrUnresolvable
	}
	if err != nil {
		return httpapi.ResolvedPaper{}, err
	}
	return httpapi.ResolvedPaper{
		Key: id.String(), Title: id.Title, Scheme: id.Scheme.String(), Slug: id.Slug,
		Record: id.Record,
	}, nil
}

// liveTranscript routes a live evidence read by the durable session reference,
// so changing the default runtime cannot redirect an in-flight session.
type liveTranscript struct{ router managed.RuntimeRouter }

// Events returns the backend's live trajectory for a run.
func (l liveTranscript) Events(ctx context.Context, status provider.RunStatus) ([]provider.Event, error) {
	ref, ok, err := (job.Job{Status: status}).ManagedSessionRef()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("session has no runtime reference")
	}
	runtime, err := l.router.ForRef(ref)
	if err != nil {
		return nil, err
	}
	events, err := runtime.Events(ctx, ref)
	if err != nil {
		return nil, err
	}
	return providerEvents(events), nil
}

// forgePublisher changes a repository's visibility with the server token. It
// is the optional forge half of making a project public (006f) and of
// withdrawing it (006o), and never runs inside the sandbox. The owner is fixed
// to the organization the reproductions live under.
type forgePublisher struct {
	client *github.Client
	owner  string
}

// SetPublic makes one repository public.
func (p *forgePublisher) SetPublic(ctx context.Context, repo string) error {
	return p.client.SetPublic(ctx, p.owner, repo)
}

// SetPrivate makes one repository private again.
func (p *forgePublisher) SetPrivate(ctx context.Context, repo string) error {
	return p.client.SetPrivate(ctx, p.owner, repo)
}

// registryReader adapts the derived registry (003) to the read surface's
// internal checks: the publish gate's manifest-clear read and the session
// transcript reader. It is not a user-facing list: the project is the unit of
// visibility (006f), so there is no unscoped organization-wide listing of
// reproductions.
type registryReader struct{ source registry.Source }

// Get reads one reproduction by its repository id. A repository with no
// manifest is a miss, because the run has not finalized; any other failure,
// such as the forge being down or a malformed file, is returned as is and
// becomes a 500. Collapsing the two, as this once did, told an owner their
// reproduction did not exist whenever GitHub failed briefly, and told the
// publish gate the same thing.
func (r *registryReader) Get(ctx context.Context, id string) (httpapi.Reproduction, error) {
	m, err := r.source.Manifest(ctx, id)
	if errors.Is(err, registry.ErrNoManifest) {
		return httpapi.Reproduction{}, httpapi.ErrReproNotFound
	}
	if err != nil {
		return httpapi.Reproduction{}, err
	}
	return toRepro(id, m), nil
}

// Transcript reads a repository's committed transcript.jsonl (005) through the
// forge, which is the same read-only path the reproduction list uses. An
// absent file is a miss and a 404; a transport failure is returned as is and
// becomes a 500, and is never reported as not found.
func (r *registryReader) Transcript(ctx context.Context, repo string) ([]byte, error) {
	raw, ok, err := r.source.File(ctx, repo, publish.TranscriptJSONL)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httpapi.ErrReproNotFound
	}
	return raw, nil
}

// Files lists a repository's committed artifact paths through the forge.
func (r *registryReader) Files(ctx context.Context, repo string) ([]string, error) {
	return r.source.Tree(ctx, repo)
}

// Artifact reads one committed file's bytes through the forge. An absent file
// is a miss and a 404; a transport failure becomes a 500.
func (r *registryReader) Artifact(ctx context.Context, repo, path string) ([]byte, error) {
	raw, ok, err := r.source.File(ctx, repo, path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, httpapi.ErrReproNotFound
	}
	return raw, nil
}

func toRepro(repo string, m publish.Manifest) httpapi.Reproduction {
	// The manifest records the full "owner/name" repo path; prefer it so the
	// frontend's github.com/<repo> link resolves. Fall back to the source's id.
	full := repo
	if m.Reproduction.Repo != "" {
		full = m.Reproduction.Repo
	}
	created := ""
	if !m.Reproduction.Created.IsZero() {
		created = m.Reproduction.Created.Format(time.RFC3339)
	}
	// One rule decides the public flip, the same one the CLI and the manifest
	// verifier apply; the caller shows the reason rather than re-deriving it.
	eligible, _ := publish.EligibleForPublic(m)
	return httpapi.Reproduction{
		ID:           repo,
		Repo:         full,
		Title:        m.Paper.Title,
		Verdict:      m.Reproduction.Verdict.String(), // the name, such as "passed", rather than the uint8 code point
		Created:      created,
		MethodDigest: m.Reproduction.MethodDigest,
		Review:       string(m.Redistribution.Review),
		CanPublish:   eligible,
	}
}

// newHTTPFetcher returns the fetcher paper resolution uses. It is the shared
// fetcher, so a reference typed into the app can only reach a public address,
// follow a bounded chain of redirects, and fill a bounded buffer; the timeout
// is per fetch.
func newHTTPFetcher(timeout time.Duration, opts ...fetch.Option) paper.Fetcher {
	return fetch.New(append([]fetch.Option{fetch.WithTimeout(timeout)}, opts...)...)
}

// isPlaceholderTitle reports whether a project is still carrying the offline
// stand-in it was created with, rather than a name somebody chose or a resolved
// paper title.
//
// The placeholder is a paper reference, and since 012 a project holds several.
// Comparing against the running job's ref alone was correct while every paper
// started at create, because the first paper's session always ran and so
// always did the naming. Now that starting is a separate step, a project
// created around papers A and B and started at B would compare B's ref to A's,
// decline, and keep a URL nobody chose as the project name.
func (e *engineRunner) isPlaceholderTitle(ctx context.Context, p project.Project, ref string) bool {
	if p.Title == "" || p.Title == ref {
		return true
	}
	papers, err := e.projects.ListPapers(ctx, p.ID)
	if err != nil {
		return false // unreadable: leave the title alone rather than guess
	}
	for _, held := range papers {
		if p.Title == held.Ref {
			return true
		}
	}
	return false
}

// harvestOutputs commits what the session wrote to the runtime's output
// directory into the mounted repository, before the manifest is written (044b).
//
// It is best-effort and never fails the retention. The transcript and the
// manifest are the durable evidence a run always owes; a harvest is a recovery
// on top of them, and refusing to write the evidence because the recovery could
// not be fetched would lose more than it saves. Instead it logs the failure,
// which is the only place a failed recovery can be noticed.
func harvestOutputs(ctx context.Context, gh forge, owner, name, jobID string, evidence managed.EvidenceReader) {
	files := map[string][]byte{}
	for _, artifact := range evidence.Artifacts {
		if artifact.IsCompiled() {
			continue
		}
		body, err := evidence.Fetch(ctx, artifact)
		if err != nil {
			// One unreadable artifact must not prevent the rest from being
			// harvested.
			slog.ErrorContext(ctx, "harvest: fetching an artifact", "session", jobID, "path", artifact.Path, "error", err)
			continue
		}
		files[artifact.Path] = body
	}
	rep, err := publish.CommitOutputs(ctx, gh, owner, name, files)
	if err != nil {
		slog.ErrorContext(ctx, "harvest: committing outputs", "session", jobID, "error", err)
		return
	}
	if len(rep.Refused) > 0 {
		slog.WarnContext(ctx, "harvest: outputs refused", "session", jobID, "refused", rep.Refused)
	}
	if rep.Recovered() {
		slog.InfoContext(ctx, "harvest: recovered files the run did not push",
			"session", jobID, "count", len(rep.Committed), "files", rep.Committed)
	}
}

// forgeProvisioner allocates a repository for a session somebody is running on
// their own machine, and mints a credential that can write to that repository
// and to nothing else (045l).
//
// It calls githubForge.Prepare rather than allocating a name of its own: a
// second implementation would let a local session and a hosted session decide
// the same name for the same paper, and the collision would go unnoticed until
// one of them pushed.
type forgeProvisioner struct {
	forge githubForge
	owner string
}

func (p forgeProvisioner) Provision(ctx context.Context, id paper.Identity) (httpapi.Provisioned, error) {
	name, err := p.forge.Prepare(ctx, p.owner, id)
	if err != nil {
		return httpapi.Provisioned{}, err
	}
	minted, err := p.forge.MintRepoToken(ctx, name)
	if errors.Is(err, github.ErrMintingUnavailable) {
		return httpapi.Provisioned{}, httpapi.ErrMintingUnavailable
	}
	if err != nil {
		return httpapi.Provisioned{}, err
	}
	return httpapi.Provisioned{
		Repo:    p.owner + "/" + name,
		URL:     "https://github.com/" + p.owner + "/" + name + ".git",
		Token:   minted.Value,
		Expires: minted.Expires,
	}, nil
}
