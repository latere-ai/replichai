// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/latere-ai/replichai/internal/httpapi"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/publish"
	"github.com/latere-ai/replichai/internal/registry"
)

// TestToReproVerdictIsAName checks that the verdict in a Reproduction is the
// grade's name and that a clear manifest is publishable.
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

// TestToReproSecretInTranscriptCannotPublish checks that a manifest barred
// because its transcript leaked a secret is not publishable. The review reason
// from 009 commit 1 reads as not clear at CanPublish, so the public flip
// refuses it as it does a tree leak.
func TestToReproSecretInTranscriptCannotPublish(t *testing.T) {
	m := publish.Manifest{
		Reproduction:   publish.Reproduction{Repo: "open-hai/x", Verdict: provider.GradePassed},
		Redistribution: publish.Redistribution{Review: publish.ReviewSecretInTranscript},
	}
	if got := toRepro("x", m); got.CanPublish {
		t.Errorf("a secret_in_transcript manifest must not be publishable; repro = %+v", got)
	}
}

// errSource is a registry source whose Manifest fails with the error the test
// supplies, to distinguish a repository with no manifest yet from a forge that
// did not answer.
type errSource struct {
	registry.Source
	err error
}

func (s errSource) Manifest(context.Context, string) (publish.Manifest, error) {
	return publish.Manifest{}, s.err
}

// TestRegistryReaderSeparatesAMissFromAReadFailure checks that the reader does
// not report a read failure as a miss. It once answered every error with
// ErrReproNotFound, so a GitHub timeout reached the publish gate as a missing
// reproduction and reached the owner as a failed redistribution check. Only an
// absent manifest, from a run that never finalized, is a miss.
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

// TestResolveNamesAProjectFromWhicheverPaperRunsFirst checks that the
// placeholder title is replaced by whichever paper runs first. A project's
// placeholder title is a paper reference, and since 012 a project holds
// several. The enrichment once compared only against the running job's ref,
// which was correct while every paper started at create, because the first
// paper's session always ran and so always did the naming. Once starting
// became a separate step, a project created around papers A and B and started
// at B compared B's ref to A's, declined, and kept a URL nobody chose as the
// project name.
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

	// The second paper is the one somebody starts.
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
