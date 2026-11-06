// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json/v2"
	"errors"
	"slices"
	"testing"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/provider/memory"
	"github.com/latere-ai/replichai/internal/publish"
)

var runnerMethod = method.Method{
	Instructions: "Audit papers. Never fabricate. Write to {{OUTPUT_DIR}}.",
	Rubric:       "1. Verdict",
	TaskTemplate: "Audit {{PAPER}}.",
}

// fakeForge is an in-memory forge: it records prepared names and holds committed
// files, so the runner's create-and-publish path is tested with no network.
type fakeForge struct {
	prepared []string
	files    map[string][]byte
	tree     []string
	getErr   error
	headGets int
}

func newRunnerForge(tree []string) *fakeForge {
	return &fakeForge{files: map[string][]byte{}, tree: tree}
}

func (f *fakeForge) Prepare(_ context.Context, _ string, id paper.Identity) (string, error) {
	name := id.Slug + "-xyz"
	f.prepared = append(f.prepared, name)
	return name, nil
}
func (f *fakeForge) HeadCommit(context.Context, string, string) (string, error) {
	f.headGets++
	return "deadbeef", nil
}
func (f *fakeForge) TreePaths(context.Context, string, string, string) ([]string, error) {
	return f.tree, nil
}
func (f *fakeForge) GetFileContent(_ context.Context, _, _, path string) ([]byte, bool, error) {
	if f.getErr != nil {
		return nil, false, f.getErr
	}
	b, ok := f.files[path]
	return b, ok, nil
}
func (f *fakeForge) PutFile(_ context.Context, _, _, path, _ string, content []byte) error {
	f.files[path] = content
	f.track(path)
	return nil
}
func (f *fakeForge) CommitFiles(_ context.Context, _, _, _ string, files map[string][]byte) (bool, error) {
	for p, c := range files {
		f.files[p] = c
		f.track(p)
	}
	return len(files) > 0, nil
}

// track adds a committed path to the tree, as a real forge does, so a finalize
// running after a harvest reads the tree the harvest contributed to.
func (f *fakeForge) track(path string) {
	if !slices.Contains(f.tree, path) {
		f.tree = append(f.tree, path)
	}
}

// fakeJobs returns a fixed job. The runner reloads the observed status, the
// verdict and run id, from the store at publish time.
type fakeJobs struct{ j job.Job }

func (f fakeJobs) Get(context.Context, string) (job.Job, error) { return f.j, nil }

// mountRecorder captures the mount handed to StartRun, so the test can assert
// Execute mounts the created repository.
type mountRecorder struct {
	*memory.Provider
	mount provider.RepoMount
}

func (r *mountRecorder) StartRun(ctx context.Context, w provider.WorkerRef, ws provider.WorkspaceRef, spec provider.RunSpec) (provider.RunHandle, error) {
	r.mount = spec.Mount
	return r.Provider.StartRun(ctx, w, ws, spec)
}

// transcriptProvider returns a fixed transcript for the publish path.
type transcriptProvider struct {
	*memory.Provider
	events []provider.Event
	err    error
}

func (p *transcriptProvider) Transcript(context.Context, provider.RunHandle) ([]provider.Event, error) {
	return p.events, p.err
}

// TestResolveCreatesAndNamesRepo checks AC7: Resolve creates and names the
// repository through the forge, sets RepoName on the job, which is persisted at
// the transition into running, and records the paper identity into the
// repository. A DOI resolves offline, so no fetcher is needed.
func TestResolveCreatesAndNamesRepo(t *testing.T) {
	f := newRunnerForge(nil)
	e := &engineRunner{gh: f, owner: "open-hai"}
	j := &job.Job{ID: "j1", Kind: job.KindAudit, Ref: "doi:10.1234/foo-bar"}

	if err := e.Resolve(context.Background(), j); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(f.prepared) != 1 {
		t.Fatalf("forge.Prepare called %d times, want 1", len(f.prepared))
	}
	if want := "open-hai/" + f.prepared[0]; j.RepoName != want {
		t.Errorf("RepoName = %q, want %q", j.RepoName, want)
	}
	if _, ok := f.files[publish.IdentityFile]; !ok {
		t.Errorf("resolve did not record the paper identity into the repo")
	}
}

// TestResolveNoForgeLeavesNoRepo checks that with no forge, Resolve creates
// nothing and leaves RepoName empty, which is the transcript-only path.
func TestResolveNoForgeLeavesNoRepo(t *testing.T) {
	e := &engineRunner{owner: "open-hai"}
	j := &job.Job{ID: "j1", Kind: job.KindAudit, Ref: "doi:10.1234/foo-bar"}
	if err := e.Resolve(context.Background(), j); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.RepoName != "" {
		t.Errorf("RepoName = %q, want empty with no forge", j.RepoName)
	}
}

// TestExecuteMountsTheRepo checks AC7: Execute mounts the created repository,
// so the agent commits into it live. The mount carries the created
// repository's URL and the server token.
func TestExecuteMountsTheRepo(t *testing.T) {
	rec := &mountRecorder{Provider: memory.New()}
	e, j := engineOver(t, rec, "open-hai/a-paper-xyz")
	e.token = "tok-secret"

	if err := e.Execute(context.Background(), j, func(provider.RunStatus) {}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if rec.mount.URL != "https://github.com/open-hai/a-paper-xyz" || rec.mount.Token != "tok-secret" {
		t.Errorf("mount = %+v, want the repo URL + server token", rec.mount)
	}
}

// TestExecuteWithoutRepoDoesNotMount checks that a run with no repository
// mounts nothing, so the mount stays unset.
func TestExecuteWithoutRepoDoesNotMount(t *testing.T) {
	rec := &mountRecorder{Provider: memory.New()}
	e, j := engineOver(t, rec, "") // no RepoName

	if err := e.Execute(context.Background(), j, func(provider.RunStatus) {}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if rec.mount.Set() {
		t.Errorf("a run with no repository should mount nothing, got %+v", rec.mount)
	}
}

// engineOver builds a runner whose only runtime is the anthropic-managed
// adapter over backend, with a claimed job in the running phase the way the
// worker hands one over, so Execute can persist the session it starts.
func engineOver(t *testing.T, backend provider.Provider, repo string) (*engineRunner, *job.Job) {
	t.Helper()
	controller, router := managedStack(t, job.LegacyRuntime, legacyRuntime(t, job.LegacyRuntime, backend))
	store := job.NewMemStore()
	ctx := context.Background()
	j, _ := store.Enqueue(ctx, job.Submission{Principal: "p", Project: "project", Ref: "doi:10.1234/foo", PaperKey: "doi:10.1234/foo", Kind: job.KindAudit})
	_, _, _ = store.Claim(ctx, "worker")
	_ = store.Advance(ctx, j.ID, job.PhaseRunning, nil)
	j, _ = store.Get(ctx, j.ID)
	j.RepoName = repo
	forge := newRunnerForge(nil)
	if repo != "" {
		// The resolving phase records the identity before a run mounts the
		// repository; retention reads it back at the end of the session.
		_, name := splitRepo(repo)
		if err := publish.RecordIdentity(ctx, forge, "open-hai", name, paper.Identity{Key: "doi:10.1234/foo", Slug: "foo"}); err != nil {
			t.Fatal(err)
		}
	}
	e := &engineRunner{
		controller: controller, sessions: router, method: runnerMethod, jobs: store, workDir: t.TempDir(),
		gh: forge, owner: "open-hai", token: "tok",
	}
	return e, &j
}

// legacySessions is the router a runner reads a legacy row's transcript
// through: one anthropic-managed adapter over backend.
func legacySessions(t *testing.T, backend provider.Provider) managed.RuntimeRouter {
	t.Helper()
	_, router := managedStack(t, job.LegacyRuntime, legacyRuntime(t, job.LegacyRuntime, backend))
	return router
}

// TestPublishFinalizesToTheRepo checks AC7: Publish finalizes through the
// shared publish.FinalizeLive, so the manifest, carrying the observed verdict,
// and the transcript are committed to the repository.
func TestPublishFinalizesToTheRepo(t *testing.T) {
	f := newRunnerForge([]string{"README.md", "src/run.py"}) // clean tree
	prov := &transcriptProvider{Provider: memory.New(), events: []provider.Event{{Kind: "message", Content: "done"}}}
	e := &engineRunner{
		sessions: legacySessions(t, prov),
		method:   runnerMethod,
		gh:       f,
		owner:    "open-hai",
		jobs:     fakeJobs{j: job.Job{ID: "j1", Status: provider.RunStatus{RunID: "run-1", Verdict: &provider.Verdict{Grade: provider.GradePassed}}}},
	}
	j := &job.Job{ID: "j1", Ref: "doi:10.1234/a-paper", RepoName: "open-hai/a-paper-xyz"}

	if err := e.Publish(context.Background(), j); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	raw, ok := f.files[publish.ManifestFile]
	if !ok {
		t.Fatal("Publish did not commit the manifest")
	}
	var m publish.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if m.Reproduction.Verdict != provider.GradePassed {
		t.Errorf("manifest verdict = %v, want the observed passed", m.Reproduction.Verdict)
	}
	if m.Redistribution.Review != publish.ReviewClear {
		t.Errorf("clean tree recorded review %q, want clear", m.Redistribution.Review)
	}
	// The transcript was committed beside the manifest.
	if len(f.files) < 2 {
		t.Errorf("expected manifest + transcript files, got %d", len(f.files))
	}
}

func TestPublishStopsWhenRecordedIdentityCannotBeRead(t *testing.T) {
	readErr := errors.New("forge unavailable")
	f := newRunnerForge([]string{"README.md"})
	f.getErr = readErr
	e := &engineRunner{
		method: runnerMethod, gh: f, owner: "open-hai",
		jobs: fakeJobs{j: job.Job{ID: "j1"}},
	}

	err := e.Publish(context.Background(), &job.Job{ID: "j1", Ref: "doi:10.1234/a", RepoName: "open-hai/a-paper"})
	if !errors.Is(err, readErr) {
		t.Fatalf("Publish error = %v, want identity read error", err)
	}
	if f.headGets != 0 {
		t.Fatal("publish continued into finalization after identity read failed")
	}
	if _, ok := f.files[publish.ManifestFile]; ok {
		t.Fatal("manifest was committed without a readable paper identity")
	}
}

func TestPublishStopsWhenFallbackIdentityCannotResolve(t *testing.T) {
	f := newRunnerForge([]string{"README.md"})
	fetchErr := errors.New("paper source unavailable")
	e := &engineRunner{
		method: runnerMethod, gh: f, owner: "open-hai",
		fetch: paper.FetcherFunc(func(context.Context, string) ([]byte, error) { return nil, fetchErr }),
		jobs:  fakeJobs{j: job.Job{ID: "j1"}},
	}

	err := e.Publish(context.Background(), &job.Job{ID: "j1", Ref: "arxiv:2603.01795", RepoName: "open-hai/a-paper"})
	if err == nil {
		t.Fatal("Publish succeeded without resolving the fallback paper identity")
	}
	if _, ok := f.files[publish.ManifestFile]; ok {
		t.Fatal("manifest was committed with an empty paper identity")
	}
}

func TestPublishStopsWhenTranscriptCannotBeRead(t *testing.T) {
	f := newRunnerForge([]string{"README.md"})
	transcriptErr := errors.New("transcript unavailable")
	prov := &transcriptProvider{Provider: memory.New(), err: transcriptErr}
	e := &engineRunner{
		sessions: legacySessions(t, prov), method: runnerMethod, gh: f, owner: "open-hai",
		jobs: fakeJobs{j: job.Job{ID: "j1", Status: provider.RunStatus{RunID: "run-1"}}},
	}

	err := e.Publish(context.Background(), &job.Job{ID: "j1", Ref: "doi:10.1234/a", RepoName: "open-hai/a-paper"})
	if !errors.Is(err, transcriptErr) {
		t.Fatalf("Publish error = %v, want transcript error", err)
	}
	if _, ok := f.files[publish.ManifestFile]; ok {
		t.Fatal("manifest was committed and marked clear without its transcript")
	}
}

// TestPublishNoForgeIsNoOp checks that with no forge, or no repository,
// Publish is a no-op and touches nothing. This is the transcript-only path, and
// it must not dereference the job store.
func TestPublishNoForgeIsNoOp(t *testing.T) {
	e := &engineRunner{owner: "open-hai"} // gh nil, jobs nil
	if err := e.Publish(context.Background(), &job.Job{ID: "j1", RepoName: "open-hai/x"}); err != nil {
		t.Errorf("no-forge Publish should be a no-op, got %v", err)
	}
}
