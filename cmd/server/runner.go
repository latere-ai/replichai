package main

import (
	"context"
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
	workDir  string
	// delay pauses at the start of each phase. It is a dev affordance: with the
	// instant memory provider a job finishes in ~1s, so a small delay makes the
	// phase progression watchable over SSE. Zero in production.
	delay time.Duration
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
	return nil
}

// Execute runs the audit, reporting progress through observe. A fresh Auditor
// per job carries that job's observer.
func (e *engineRunner) Execute(ctx context.Context, j *job.Job, observe func(provider.RunStatus)) error {
	e.pause(ctx)
	auditor := audit.New(e.provider, e.method, audit.WithObserver(observe))
	res, err := auditor.Run(ctx, j.Ref, filepath.Join(e.workDir, j.ID))
	if err != nil {
		return err
	}
	observe(res.Status)
	return nil
}

// Publish is a pass-through in this build: the GitHub publish (006f) is not yet
// packaged for the server, so a reproduction runs and is graded but is not
// pushed to a repository. Wiring the mount publish here is the deployable
// layer's next step.
func (e *engineRunner) Publish(ctx context.Context, _ *job.Job) error {
	e.pause(ctx)
	return nil
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
