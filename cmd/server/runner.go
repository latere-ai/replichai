package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
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
	if _, err := paper.Resolve(ctx, j.Ref, paper.WithFetcher(e.fetch)); err != nil {
		return fmt.Errorf("resolve %q: %w", j.Ref, err)
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

// registryReader adapts the derived registry (003) to the HTTP read surface. It
// presents what the manifests hold and caches nothing of its own.
type registryReader struct{ source registry.Source }

// List reads every published reproduction's manifest.
func (r *registryReader) List(ctx context.Context) ([]httpapi.Reproduction, error) {
	repos, err := r.source.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]httpapi.Reproduction, 0, len(repos))
	for _, repo := range repos {
		m, err := r.source.Manifest(ctx, repo)
		if err != nil {
			continue // a repository without a readable manifest is not a reproduction
		}
		out = append(out, toRepro(repo, m))
	}
	return out, nil
}

// Get reads one reproduction by its repository id.
func (r *registryReader) Get(ctx context.Context, id string) (httpapi.Reproduction, error) {
	m, err := r.source.Manifest(ctx, id)
	if err != nil {
		return httpapi.Reproduction{}, httpapi.ErrReproNotFound
	}
	return toRepro(id, m), nil
}

// Transcript reading from the forge is not yet wired in this build.
func (r *registryReader) Transcript(_ context.Context, _ string) ([]byte, error) {
	return nil, httpapi.ErrReproNotFound
}

func toRepro(repo string, m publish.Manifest) httpapi.Reproduction {
	return httpapi.Reproduction{
		ID:      repo,
		Repo:    repo,
		Title:   m.Paper.Title,
		Verdict: string(m.Reproduction.Verdict),
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
