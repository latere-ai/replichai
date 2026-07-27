package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/github"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
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
	provider provider.Provider
	method   method.Method
	fetch    paper.Fetcher
	projects project.Store
	jobs     jobStatusReader
	workDir  string
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
	// title, but only while it is still the placeholder (the submitted ref) — so
	// a project that already carries a real title, or gathers a second paper, is
	// never clobbered. Best-effort: a failure here must not fail the run.
	if id.Title != "" && e.projects != nil && j.ProjectID != "" {
		if p, err := e.projects.Get(ctx, j.ProjectID); err == nil && (p.Title == "" || p.Title == j.Ref) {
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

// Execute runs the audit, reporting progress through observe. A fresh Auditor
// per job carries that job's observer.
func (e *engineRunner) Execute(ctx context.Context, j *job.Job, observe func(provider.RunStatus)) error {
	e.pause(ctx)
	opts := []audit.Option{audit.WithObserver(observe)}
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
	auditor := audit.New(e.provider, e.method, audit.WithObserver(observe))
	res, err := auditor.Resume(ctx, provider.RunHandle{ID: j.Status.RunID}, j.Ref, filepath.Join(e.workDir, j.ID))
	if err != nil {
		return err
	}
	observe(res.Status)
	return nil
}

// Close releases a managed-agents run by its session id, so a run left in flight
// by a restart is archived (stopped) rather than orphaned. Idempotent.
func (e *engineRunner) Close(ctx context.Context, runID string) error {
	if runID == "" {
		return nil
	}
	return e.provider.CloseRun(ctx, provider.RunHandle{ID: runID})
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
	id, ok, _ := publish.RecordedIdentity(ctx, e.gh, owner, name)
	if !ok {
		if rid, err := paper.Resolve(ctx, j.Ref, paper.WithFetcher(e.fetch)); err == nil {
			id = rid
		}
	}

	// Commit the transcript alongside the manifest. A momentary transcript
	// failure must not block the manifest — publish it either way.
	var events []provider.Event
	if runID != "" {
		if evs, err := e.provider.Transcript(ctx, provider.RunHandle{ID: runID}); err == nil {
			events = evs
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
	return httpapi.ResolvedPaper{Key: id.String(), Title: id.Title, Scheme: id.Scheme.String(), Slug: id.Slug}, nil
}

// liveTranscript reads a run's live event stream from the backend by its run id,
// so a session still in flight is inspectable before it publishes (006h).
type liveTranscript struct{ prov provider.Provider }

// Events returns the backend's live trajectory for a run.
func (l liveTranscript) Events(ctx context.Context, runID string) ([]provider.Event, error) {
	return l.prov.Transcript(ctx, provider.RunHandle{ID: runID})
}

// forgePublisher flips a repository public with the server token (006f) — the
// optional half of the project public flip, never inside the sandbox. The
// owner is fixed to the org the reproductions live under.
type forgePublisher struct {
	client *github.Client
	owner  string
}

// SetPublic flips one repository public.
func (p *forgePublisher) SetPublic(ctx context.Context, repo string) error {
	return p.client.SetPublic(ctx, p.owner, repo)
}

// registryReader adapts the derived registry (003) to the read surface's
// internal checks — the publish gate's manifest-clear read and the session
// transcript reader. It is no longer a user-facing list: the project is the unit
// of visibility (006f), so there is no unscoped org-wide reproductions listing.
type registryReader struct{ source registry.Source }

// Get reads one reproduction by its repository id.
func (r *registryReader) Get(ctx context.Context, id string) (httpapi.Reproduction, error) {
	m, err := r.source.Manifest(ctx, id)
	if err != nil {
		return httpapi.Reproduction{}, httpapi.ErrReproNotFound
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
		Review:       m.Redistribution.Review,
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
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s returned %d", url, resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	})
}
