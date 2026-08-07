package main

import (
	"context"
	"testing"

	"github.com/latere-ai/replichai/internal/audit"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/method"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/provider/memory"
	"github.com/latere-ai/replichai/internal/publish"
)

// A server-side run waits on the backend session, not on a clock: an audit that
// takes longer than any wall-clock bound is still a live session, and cutting it
// marks a session failed while its agent goes on committing the reproduction.
func TestServerRunsWaitWithoutADeadline(t *testing.T) {
	a := audit.New(memory.New(), method.Method{}, auditOptions(func(provider.RunStatus) {})...)
	if !a.Unbounded() {
		t.Error("the server must wait on the session, not on a deadline")
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
