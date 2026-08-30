// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/provider/memory"
	"github.com/latere-ai/replichai/internal/publish"
	"github.com/latere-ai/replichai/internal/registry"
)

// A server-side run waits on the backend session, not on a clock: an audit that
// takes longer than any wall-clock bound is still a live session, and cutting it
// marks a session failed while its agent goes on committing the reproduction.
func TestServerRunsWaitWithoutADeadline(t *testing.T) {
	a := audit.New(memory.New(), method.Method{}, auditOptions(&job.Job{}, func(provider.RunStatus) {})...)
	if !a.Unbounded() {
		t.Error("the server must wait on the session, not on a deadline")
	}
}

// The session runs on the tier its owner chose (031).
//
// It asserts auditOptions rather than either call site on purpose: that is the
// single wiring Execute and Resume both take, and it cannot be called without a
// job. The job is the only place the tier can come from — the submission is gone
// once Enqueue returns, and the worker that calls Execute may be a different
// process, so a tier that did not reach the row could not reach the run at all.
func TestServerRunsOnTheJobsChosenTier(t *testing.T) {
	optionsFor := func(j *job.Job) *audit.Auditor {
		return audit.New(memory.New(), method.Method{}, auditOptions(j, func(provider.RunStatus) {})...)
	}

	for _, tc := range []struct {
		name string
		job  job.Job
		want string
	}{
		{"a chosen tier", job.Job{Tier: credit.PrefFast}, "fast"},
		{"the middle tier", job.Job{Tier: credit.PrefBalanced}, "balanced"},
		// A session enqueued before the choice existed carries no tier, and must
		// keep running on exactly what it always ran on.
		{"no choice made", job.Job{}, "best"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := optionsFor(&tc.job).ModelPreference(); got != tc.want {
				t.Errorf("auditor resolves models from %q, want %q", got, tc.want)
			}
		})
	}
}

// toRepro must render the verdict as its name ("passed"), not the uint8 code
// point string(Grade) produces — the bug that showed a control character in the
// API before.
func TestToReproVerdictIsAName(t *testing.T) {
	m := publish.Manifest{
		Paper:          publish.ManifestPaper{Title: "A Paper"},
		Reproduction:   publish.Reproduction{Repo: "open-hai/x", Verdict: provider.GradePassed},
		Redistribution: publish.Redistribution{Review: publish.ReviewClear},
	}
	got := toRepro("x", m)
	if got.Verdict != "passed" {
		t.Errorf("verdict = %q, want \"passed\"", got.Verdict)
	}
	if !got.CanPublish || got.Review != "clear" || got.Title != "A Paper" {
		t.Errorf("repro = %+v", got)
	}
}

// A manifest barred because its transcript leaked a secret must not be publishable
// — the new review reason (009 commit 1) reads as not-clear at the CanPublish
// seam, so the public flip refuses it just as a tree leak does.
func TestToReproSecretInTranscriptCannotPublish(t *testing.T) {
	m := publish.Manifest{
		Reproduction:   publish.Reproduction{Repo: "open-hai/x", Verdict: provider.GradePassed},
		Redistribution: publish.Redistribution{Review: publish.ReviewSecretInTranscript},
	}
	if got := toRepro("x", m); got.CanPublish {
		t.Errorf("a secret_in_transcript manifest must not be publishable; repro = %+v", got)
	}
}

// errSource is a registry source whose Manifest fails with whatever the test
// hands it — the seam between "this repository has no manifest yet" and "the
// forge did not answer".
type errSource struct {
	registry.Source
	err error
}

func (s errSource) Manifest(context.Context, string) (publish.Manifest, error) {
	return publish.Manifest{}, s.err
}

// The reader must not disguise a read failure as a miss. It used to answer every
// error with ErrReproNotFound, so a GitHub timeout reached the publish gate as
// "this reproduction is not there" and reached the owner as "your reproduction
// did not pass the redistribution check". Only a genuinely absent manifest — a
// run that never finalized — is a miss.
func TestRegistryReaderSeparatesAMissFromAReadFailure(t *testing.T) {
	ctx := context.Background()

	absent := &registryReader{source: errSource{err: fmt.Errorf("x: %w", registry.ErrNoManifest)}}
	if _, err := absent.Get(ctx, "x"); !errors.Is(err, httpapi.ErrReproNotFound) {
		t.Errorf("no manifest: err = %v, want ErrReproNotFound", err)
	}

	down := &registryReader{source: errSource{err: errors.New("github timed out")}}
	_, err := down.Get(ctx, "x")
	if err == nil || errors.Is(err, httpapi.ErrReproNotFound) {
		t.Errorf("unreachable forge: err = %v, want the read failure itself", err)
	}
}

// A project's placeholder title is a paper *reference*, and since 012 a project
// holds several. The enrichment used to compare only against the running job's
// ref, which was right while every paper started at create — the first paper's
// session always ran, so it always did the naming. Now that starting is its own
// act, a project created around papers A and B and started at B compared B's ref
// to A's, declined, and kept a URL nobody chose as the project name forever.
func TestResolveNamesAProjectFromWhicheverPaperRunsFirst(t *testing.T) {
	ctx := context.Background()
	ps := project.NewMemStore()
	p, err := ps.Create(ctx, "owner", "doi:10.1234/first")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, ref := range []string{"doi:10.1234/first", "doi:10.1234/second"} {
		key, err := paper.Key(ref)
		if err != nil {
			t.Fatalf("Key(%q): %v", ref, err)
		}
		if err := ps.AddPaper(ctx, p.ID, project.Paper{Key: key, Ref: ref, AddedBy: "owner"}); err != nil {
			t.Fatalf("AddPaper: %v", err)
		}
	}

	// A fetcher that names any paper, so the resolve has a title to enrich with.
	titles := paper.FetcherFunc(func(context.Context, string) ([]byte, error) {
		return []byte(`{"message":{"title":["Point of no Undo"]}}`), nil
	})

	// The *second* paper is the one somebody presses Start on.
	e := &engineRunner{projects: ps, fetch: titles}
	j := &job.Job{ID: "j1", Kind: job.KindAudit, Ref: "doi:10.1234/second", ProjectID: p.ID}
	if err := e.Resolve(ctx, j); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got, _ := ps.Get(ctx, p.ID)
	if got.Title == "doi:10.1234/first" {
		t.Fatal("the project is still named after a reference nobody chose")
	}
	if got.Title != "Point of no Undo" {
		t.Fatalf("project title = %q, want the resolved title of the paper that ran", got.Title)
	}

	// A project somebody named is never renamed by a run.
	named, _ := ps.Create(ctx, "owner", "My study")
	key, _ := paper.Key("doi:10.1234/second")
	if err := ps.AddPaper(ctx, named.ID, project.Paper{Key: key, Ref: "doi:10.1234/second", AddedBy: "owner"}); err != nil {
		t.Fatalf("AddPaper: %v", err)
	}
	j2 := &job.Job{ID: "j2", Kind: job.KindAudit, Ref: "doi:10.1234/second", ProjectID: named.ID}
	if err := e.Resolve(ctx, j2); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got, _ := ps.Get(ctx, named.ID); got.Title != "My study" {
		t.Errorf("project title = %q, want the name the person chose", got.Title)
	}
}
