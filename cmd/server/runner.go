// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/latere-ai/replichai/internal/audit"
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
)

// engineRunner drives a job's phases with the real engine. It is the
// composition root's, so it may import internal/audit and its method — the code
// internal/job and internal/httpapi are forbidden to touch. It dispatches on
// Kind; today it runs audits, the experiment path lands with 007b's server wiring.
type engineRunner struct {
	provider   provider.Provider
	controller *managed.Controller
	method     method.Method
	fetch      paper.Fetcher
	projects   project.Store
	jobs       jobStatusReader
	workDir    string
	// The forge (006i). When gh is nil (a local run with no token) the runner
	// neither creates a repository nor publishes — the session's transcript stays
	// readable live-only. owner is the org repositories are created under; token
	// is the mount credential, handed to the platform egress, never the sandbox.
	gh    forge
	owner string
	token string
	// delay pauses at the start of each phase. It is a dev affordance: with the
	// instant memory provider a job finishes in ~1s, so a small delay makes the
	// phase progression watchable over SSE. Zero in production.
	delay time.Duration
}

// jobStatusReader reads a job's latest observed status at publish time. The
// verdict and the backend run id are set on the Observe path, not carried on the
// phase transition, so the publishing phase reloads them from the store.
type jobStatusReader interface {
	Get(ctx context.Context, id string) (job.Job, error)
}

// forge is the repository host the runner creates into and finalizes against
// (006i). Prepare allocates a collision-free name and creates the private repo;
// the rest is publish.LiveForge, so the shared finalize drives it unchanged.
// github-backed in production, faked in tests — the runner needs no network to
// test its phase logic.
type forge interface {
	publish.LiveForge
	// Prepare allocates a collision-free repository name for the paper under the
	// owner and creates it private, returning the name. Idempotent: an existing
	// repository of that name is reused, not an error.
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

// pause waits out the configured per-phase delay, honouring cancellation.
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

// Resolve validates the paper reference against the resolver (the resolving
// phase). A reference that cannot resolve fails the job here, before a run.
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
	// title, but only while it is still a placeholder — so a project that already
	// carries a real title is never clobbered. Best-effort: a failure here must
	// not fail the run.
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
		// Best-effort: publish falls back to re-resolving if this did not land.
		_ = publish.RecordIdentity(ctx, e.gh, e.owner, name, id)
		j.RepoName = e.owner + "/" + name
	}
	return nil
}

// auditOptions is how the server waits on every run, fresh or resumed: with the
// job's observer, on the model tier the job carries, and without a wall-clock
// deadline.
//
// The backend session is the source of truth for the outcome. A deadline is a
// second, weaker opinion about it — one that reports a session still grading, or
// one that settled a minute late, as a failure, while the agent's finished work
// sits committed in the mounted repository. Only a failed session, an operator
// cancelling it, or a backend that stops answering altogether ends the wait.
//
// The tier comes from the job because the job row is all the worker ever sees:
// the submission is gone the moment Enqueue returns, and Execute may run in a
// different process minutes later. Applying it here rather than at either call
// site keeps one wiring for both paths, which cannot then disagree.
//
// On the resumed path it is inert, and deliberately passed anyway. Resume
// rejoins a backend session that already exists, and a session's model was
// fixed when it was created (the auditor resolves a model in Start, nowhere
// else) — so there is nothing left to choose. Passing it costs nothing and
// keeps the two paths one expression rather than two that must be kept in step.
func auditOptions(j *job.Job, observe func(provider.RunStatus)) []audit.Option {
	return []audit.Option{
		audit.WithObserver(observe),
		audit.WithoutTimeout(),
		audit.WithModelPreference(j.Tier.OrDefault().String()),
	}
}

// Execute runs the audit, reporting progress through observe. A fresh Auditor
// per job carries that job's observer.
func (e *engineRunner) Execute(ctx context.Context, j *job.Job, observe func(provider.RunStatus)) error {
	e.pause(ctx)
	if e.controller != nil {
		options := []audit.SessionOption{
			audit.WithSessionHooks(e.managedHooks(j, observe)),
			audit.WithoutSessionTimeout(),
			audit.WithSessionModel(j.Tier.OrDefault().String()),
		}
		if e.gh != nil && j.RepoName != "" {
			mount := e.mountFor(j)
			options = append(options, audit.WithSessionMount(audit.SessionMount{
				URL: mount.URL, Token: mount.Token, MountPath: mount.MountPath, ReadOnly: mount.ReadOnly,
			}))
		}
		_, err := audit.NewSessionAuditor(e.controller, e.method, options...).Run(ctx, j.Ref, filepath.Join(e.workDir, j.ID))
		return err
	}
	opts := auditOptions(j, observe)
	// With the repository created in resolving, mount it so the agent commits its
	// reproduction into it live (006i); the mount is also the redistribution
	// boundary — only what lands in it is published.
	if e.gh != nil && j.RepoName != "" {
		opts = append(opts, audit.WithMount(e.mountFor(j)))
	}
	auditor := audit.New(e.provider, e.method, opts...)
	res, err := auditor.Run(ctx, j.Ref, filepath.Join(e.workDir, j.ID))
	if err != nil {
		return err
	}
	observe(res.Status)
	return nil
}

// Resume rejoins the managed-agents session a restart left in flight (by the id
// observed onto the job) and drives it to completion, so the job's state tracks
// the real backend session rather than being guessed as failed.
func (e *engineRunner) Resume(ctx context.Context, j *job.Job, observe func(provider.RunStatus)) error {
	if e.controller != nil {
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
			audit.WithSessionModel(j.Tier.OrDefault().String()),
		).Resume(ctx, ref, j.Ref, filepath.Join(e.workDir, j.ID))
		return err
	}
	auditor := audit.New(e.provider, e.method, auditOptions(j, observe)...)
	res, err := auditor.Resume(ctx, provider.RunHandle{ID: j.Status.RunID}, j.Ref, filepath.Join(e.workDir, j.ID))
	if err != nil {
		return err
	}
	observe(res.Status)
	return nil
}

// Close releases a managed-agents run by its session id, so a run left in flight
// by a restart is archived (stopped) rather than orphaned. Idempotent.
func (e *engineRunner) Close(ctx context.Context, ref managed.SessionRef) error {
	if ref.ID == "" {
		return nil
	}
	if e.controller != nil && len(ref.Locator) > 0 {
		_, err := e.controller.Cancel(ctx, ref, managed.Hooks{})
		return err
	}
	return e.provider.CloseRun(ctx, provider.RunHandle{ID: ref.ID})
}

type jobObserver interface {
	Observe(context.Context, string, provider.RunStatus) error
}

func (e *engineRunner) managedHooks(j *job.Job, observe func(provider.RunStatus)) managed.Hooks {
	persistRef := func(ctx context.Context, ref managed.SessionRef) error {
		raw, err := json.Marshal(ref)
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
				return errors.New("managed retention requires the recorded paper identity")
			}
			grade := provider.GradeUnknown
			if evidence.Status.Verdict != nil {
				grade = providerGrade(evidence.Status.Verdict.Grade)
			}
			// Harvest before finalizing (044b). The mount is not the only way out
			// of a session: an agent may write its reproduction, copy it to the
			// runtime's output directory, and never push — and then the sandbox is
			// released and the output directory is the only copy left. Finalize
			// reads verdict.json from the committed tree, so a recovered one has to
			// be in the tree before the manifest is written.
			harvestOutputs(ctx, e.gh, owner, name, j.ID, evidence)
			_, err = publish.FinalizeLive(ctx, e.gh, owner, name, id, e.method.Digest(), grade, providerEvents(evidence.Events), time.Now)
			return err
		},
	}
}

func providerStatus(status managed.Status) provider.RunStatus {
	// The controller calls this only from its Status hook, and it validates the
	// observed reference immediately before that hook runs: SessionRef.Validate
	// unmarshals the locator, so the one field here that could refuse to encode
	// is already proven to be valid JSON.
	//nolint:errchkjson // status.Ref.Locator is validated by managed.Controller before the Status hook fires, so Marshal cannot fail.
	raw, _ := json.Marshal(status.Ref)
	result := provider.RunStatus{
		State: providerState(status.State), RunID: status.Ref.ID, Runtime: string(status.Ref.Runtime), SessionRef: raw,
		Model: status.Model.ID, ModelProvider: status.Model.Provider, BillingVariant: status.Model.BillingVariant,
		Usage: provider.Usage{
			InputTokens: status.Usage.InputFresh, CachedInputTokens: status.Usage.InputCached,
			CacheWriteTokens: status.Usage.CacheWrite, CacheWrite5mTokens: status.Usage.CacheWrite5m,
			CacheWrite1hTokens: status.Usage.CacheWrite1h, OutputTokens: status.Usage.Output,
		},
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
// tree, writes the manifest, and commits the transcript beside it — the same
// publish.FinalizeLive the CLI drives. With no forge, or a job that never got a
// repository, it is a no-op and the session's transcript stays readable live.
func (e *engineRunner) Publish(ctx context.Context, j *job.Job) error {
	e.pause(ctx)
	if e.gh == nil || j.RepoName == "" {
		return nil
	}
	if _, ok, err := j.ManagedSessionRef(); err != nil {
		return err
	} else if ok && len(j.Status.SessionRef) > 0 {
		// The controller's Retain hook committed transcript and manifest before
		// release; this phase remains as the durable job-state transition.
		return nil
	}
	owner, name := splitRepo(j.RepoName)

	// The verdict and the backend run id were observed onto the job during the
	// run; reload the latest, since the phase transition did not carry them. A
	// read failure fails the publish (which retries) rather than writing a
	// manifest that records "unknown" for a run that actually passed — a wrong
	// manifest would stick, since nothing re-finalizes once it is written.
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

	// Prefer the identity recorded in the repo (resolving wrote it); fall back to
	// re-resolving only if it is absent, to avoid the paper source's rate limit.
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

	// Commit the transcript alongside the manifest. A momentary transcript
	// failure must not block the manifest — publish it either way.
	var events []provider.Event
	if runID != "" {
		events, err = e.provider.Transcript(ctx, provider.RunHandle{ID: runID})
		if err != nil {
			return job.RetryablePublish(fmt.Errorf("read transcript for run %s: %w", runID, err))
		}
	}

	_, err = publish.FinalizeLive(ctx, e.gh, owner, name, id, e.method.Digest(), verdict, events, time.Now)
	return err
}

// paperResolver previews a reference for the New-project screen (006j): it
// resolves the reference to its identity through the same HTTP fetcher the
// worker uses. An unresolvable reference is mapped to httpapi.ErrUnresolvable (a
// 400); any other failure is a source that could not be reached (a 502).
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

// forgePublisher flips a repository's visibility with the server token — the
// optional half of the project public flip (006f) and of withdrawing it (006o),
// never inside the sandbox. The owner is fixed to the org the reproductions live
// under.
type forgePublisher struct {
	client *github.Client
	owner  string
}

// SetPublic flips one repository public.
func (p *forgePublisher) SetPublic(ctx context.Context, repo string) error {
	return p.client.SetPublic(ctx, p.owner, repo)
}

// SetPrivate closes one repository again.
func (p *forgePublisher) SetPrivate(ctx context.Context, repo string) error {
	return p.client.SetPrivate(ctx, p.owner, repo)
}

// registryReader adapts the derived registry (003) to the read surface's
// internal checks — the publish gate's manifest-clear read and the session
// transcript reader. It is no longer a user-facing list: the project is the unit
// of visibility (006f), so there is no unscoped org-wide reproductions listing.
type registryReader struct{ source registry.Source }

// Get reads one reproduction by its repository id. A repository with no
// manifest is a miss (the run has not finalized); anything else — the forge
// down, a malformed file — is returned as itself and becomes a 500. Collapsing
// the two, as this used to, told an owner their reproduction did not exist
// whenever GitHub hiccuped, and told the publish gate the same thing.
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
// forge pass-through — the same read-only path the reproduction list uses. A
// genuinely absent file is a miss (404); a transport failure is returned as-is
// and becomes a 500, never disguised as not-found.
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

// Artifact reads one committed file's bytes through the forge. A genuinely
// absent file is a miss (404); a transport failure becomes a 500.
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
	return httpapi.Reproduction{
		ID:           repo,
		Repo:         full,
		Title:        m.Paper.Title,
		Verdict:      m.Reproduction.Verdict.String(), // the name ("passed"), not the uint8 code point
		Created:      created,
		MethodDigest: m.Reproduction.MethodDigest,
		Review:       string(m.Redistribution.Review),
		CanPublish:   m.Redistribution.Review == publish.ReviewClear,
	}
}

// newHTTPFetcher is the network seam paper resolution uses — a plain timed GET.
func newHTTPFetcher(timeout time.Duration) paper.Fetcher {
	return paper.FetcherFunc(func(ctx context.Context, url string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s returned %d", url, resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	})
}

// isPlaceholderTitle reports whether a project is still carrying the offline
// stand-in it was created with, rather than a name somebody chose or a resolved
// paper title.
//
// The placeholder is a paper *reference*, and since 012 a project holds several.
// Comparing against the running job's ref alone was right while every paper
// started at create — the first paper's session always ran, so it always did the
// naming. Now that starting is its own act, a project created around papers A
// and B and started at B compares B's ref to A's, declines, and keeps a URL
// nobody chose as the project name forever.
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
// not be fetched would lose more than it saves. What it does instead is say so
// in the log, which is the only place a failed recovery can be noticed.
func harvestOutputs(ctx context.Context, gh forge, owner, name, jobID string, evidence managed.EvidenceReader) {
	files := map[string][]byte{}
	for _, artifact := range evidence.Artifacts {
		if artifact.IsCompiled() {
			continue
		}
		body, err := evidence.Fetch(ctx, artifact)
		if err != nil {
			// One unreadable artifact must not cost the rest of them.
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
// the same name for the same paper, and the collision would be silent until one
// of them pushed.
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
