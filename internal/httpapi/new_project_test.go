package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// fakeResolver is a stand-in paper resolver: it returns a canned preview or a
// canned error, so the resolve endpoint is tested with no network.
type fakeResolver struct {
	got ResolvedPaper
	err error
}

func (f fakeResolver) Resolve(context.Context, string) (ResolvedPaper, error) {
	return f.got, f.err
}

// AC5: resolve previews a reference; an unresolvable one is 400, an unreachable
// source is 502 — kept distinct.
func TestResolvePaperPreview(t *testing.T) {
	d := testDeps(allowAll)
	d.Resolver = fakeResolver{got: ResolvedPaper{Key: "arxiv:2603.01795", Title: "A Real Paper", Scheme: "arxiv", Slug: "a-real-paper"}}
	h := NewRouter(d).Handler()

	w := do(t, h, "POST", "/resolve", `{"url":"arxiv:2603.01795"}`, "me")
	if w.Code != http.StatusOK {
		t.Fatalf("resolve status = %d, want 200", w.Code)
	}
	var got ResolvedPaper
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Title != "A Real Paper" {
		t.Errorf("preview title = %q, want the resolved title", got.Title)
	}

	// A bad reference is a 400.
	d.Resolver = fakeResolver{err: ErrUnresolvable}
	if w := do(t, NewRouter(d).Handler(), "POST", "/resolve", `{"url":"nope"}`, "me"); w.Code != http.StatusBadRequest {
		t.Errorf("unresolvable status = %d, want 400", w.Code)
	}
	// An unreachable source is a 502, distinct from a bad reference.
	d.Resolver = fakeResolver{err: errors.New("arxiv timeout")}
	if w := do(t, NewRouter(d).Handler(), "POST", "/resolve", `{"url":"arxiv:1"}`, "me"); w.Code != http.StatusBadGateway {
		t.Errorf("unreachable status = %d, want 502", w.Code)
	}
}

type createResult struct {
	ProjectID string        `json:"project_id"`
	Title     string        `json:"title"`
	Papers    []PaperResult `json:"papers"`
	Members   []string      `json:"members"`
}

// AC1 (012): create with a title and several papers opens one project holding
// both, and runs neither. 006j's version started a session per paper here; the
// two acts are separate now, and this is the recording half.
func TestCreateProjectRecordsEveryPaper(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	w := do(t, h, "POST", "/projects", `{"title":"My study","papers":["doi:10.1234/aaa","doi:10.5678/bbb"]}`, "me")
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body %s", w.Code, w.Body)
	}
	var got createResult
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Title != "My study" {
		t.Errorf("title = %q, want the chosen name", got.Title)
	}
	if len(got.Papers) != 2 || got.Papers[0].Key != "doi:10.1234/aaa" || got.Papers[1].Key != "doi:10.5678/bbb" {
		t.Fatalf("papers = %+v, want both recorded", got.Papers)
	}
	p, err := d.Projects.Get(context.Background(), got.ProjectID)
	if err != nil || p.Title != "My study" || p.Owner != "me" {
		t.Errorf("stored project = %+v (err %v)", p, err)
	}
	// Nothing was enqueued and nothing was spent.
	sessions, _ := d.Jobs.ListByProject(context.Background(), got.ProjectID)
	if len(sessions) != 0 {
		t.Errorf("create started %d sessions, want 0", len(sessions))
	}
}

// AC3 (012): a reference that is not a DOI, arXiv ID, or URL is reported against
// itself, and the project — with the papers that were fine — survives. There is
// no all-failed case left to compensate for: nothing was spent, so a project
// holding no valid papers is just an empty project.
func TestCreateProjectReportsBadReferencesAndKeepsTheProject(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	w := do(t, h, "POST", "/projects", `{"papers":["doi:10.1234/ok","garbage"]}`, "me")
	if w.Code != http.StatusCreated {
		t.Fatalf("partial create status = %d, want 201; body %s", w.Code, w.Body)
	}
	var got createResult
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Papers[0].Key == "" || got.Papers[1].Error != "unresolvable_paper" {
		t.Errorf("papers = %+v, want one recorded + one unresolvable", got.Papers)
	}
	if _, err := d.Projects.Get(context.Background(), got.ProjectID); err != nil {
		t.Errorf("project should survive a partial failure: %v", err)
	}

	// Every reference bad: still a project, still 201, no 422 and no undo.
	w = do(t, h, "POST", "/projects", `{"papers":["garbage","also bad"]}`, "me2")
	if w.Code != http.StatusCreated {
		t.Fatalf("all-bad status = %d, want 201; body %s", w.Code, w.Body)
	}
	list, _ := d.Projects.ListByOwner(context.Background(), "me2")
	if len(list) != 1 {
		t.Errorf("an all-bad create left %d projects, want 1 — nothing was spent to undo", len(list))
	}
}

// Empty papers is a 400.
func TestCreateProjectNeedsAPaper(t *testing.T) {
	d := testDeps(allowAll)
	if w := do(t, NewRouter(d).Handler(), "POST", "/projects", `{"title":"x","papers":[]}`, "me"); w.Code != http.StatusBadRequest {
		t.Errorf("no-papers status = %d, want 400", w.Code)
	}
}

// AC8 (012): titles are the server's to write, and they land after the answer —
// a create must not block on somebody else's web server.
func TestCreateProjectResolvesPaperTitles(t *testing.T) {
	d := testDeps(allowAll)
	d.Resolver = fakeResolver{got: ResolvedPaper{Key: "doi:10.1234/aaa", Title: "Point of no Undo"}}
	rt := NewRouter(d)

	w := do(t, rt.Handler(), "POST", "/projects", `{"papers":["doi:10.1234/aaa"]}`, "me")
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	var got createResult
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	rt.WaitBackground()

	papers, _ := d.Projects.ListPapers(context.Background(), got.ProjectID)
	if len(papers) != 1 || papers[0].Title != "Point of no Undo" {
		t.Errorf("stored papers = %+v", papers)
	}
	// A project nobody named takes the resolved title rather than keeping the
	// raw reference it was created with.
	p, _ := d.Projects.Get(context.Background(), got.ProjectID)
	if p.Title != "Point of no Undo" {
		t.Errorf("project title = %q, want the resolved paper title", p.Title)
	}

	// A source that cannot be reached is not a failure: the paper is recorded
	// without a title, and the create still succeeds.
	d2 := testDeps(allowAll)
	d2.Resolver = fakeResolver{err: errors.New("arxiv timeout")}
	rt2 := NewRouter(d2)
	w = do(t, rt2.Handler(), "POST", "/projects", `{"papers":["doi:10.1234/bbb"]}`, "me")
	if w.Code != http.StatusCreated {
		t.Fatalf("create with an unreachable source: status %d", w.Code)
	}
	var unreachable createResult
	_ = json.Unmarshal(w.Body.Bytes(), &unreachable)
	rt2.WaitBackground()
	if unreachable.Papers[0].Error != "" {
		t.Errorf("an unreachable source must not fail the paper: %+v", unreachable.Papers[0])
	}
	papers, _ = d2.Projects.ListPapers(context.Background(), unreachable.ProjectID)
	if len(papers) != 1 || papers[0].Title != "" {
		t.Errorf("papers with an unreachable source = %+v", papers)
	}
}

// A project somebody named keeps its name, whatever the papers resolve to.
func TestResolvedTitlesNeverClobberAChosenName(t *testing.T) {
	d := testDeps(allowAll)
	d.Resolver = fakeResolver{got: ResolvedPaper{Title: "Point of no Undo"}}
	rt := NewRouter(d)

	w := do(t, rt.Handler(), "POST", "/projects", `{"title":"My study","papers":["doi:10.1234/aaa"]}`, "me")
	var got createResult
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	rt.WaitBackground()

	p, _ := d.Projects.Get(context.Background(), got.ProjectID)
	if p.Title != "My study" {
		t.Errorf("project title = %q, want the name the person chose", p.Title)
	}
}

// AC4: collaborator emails are seeded as members; the owner is the lead.
func TestCreateProjectSeedsCollaborators(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	w := doAs(t, h, "POST", "/projects", `{"papers":["doi:10.1234/ok"],"collaborators":["friend@example.com"]}`, Identity{Sub: "me", Email: "owner@example.com"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body %s", w.Code, w.Body)
	}
	var got createResult
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if len(got.Members) != 1 || got.Members[0] != "friend@example.com" {
		t.Errorf("members = %v, want the collaborator", got.Members)
	}
	// The collaborator is visible through the members API.
	ms, _ := d.Members.List(context.Background(), got.ProjectID)
	var found bool
	for _, m := range ms {
		if m.Email == "friend@example.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("collaborator not seeded into the membership store: %+v", ms)
	}
}

// AC6: rename an owned project; a non-owner is refused.
func TestRenameProject(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "me", "old")

	w := do(t, h, "PATCH", "/projects/"+p.ID, `{"title":"new name"}`, "me")
	if w.Code != http.StatusOK {
		t.Fatalf("rename status = %d, want 200", w.Code)
	}
	if got, _ := d.Projects.Get(context.Background(), p.ID); got.Title != "new name" {
		t.Errorf("title after rename = %q, want %q", got.Title, "new name")
	}
	// A non-owner cannot rename.
	if w := do(t, h, "PATCH", "/projects/"+p.ID, `{"title":"hijack"}`, "someone-else"); w.Code != http.StatusForbidden {
		t.Errorf("non-owner rename status = %d, want 403", w.Code)
	}
}

// The same reference twice in one create is the same paper named twice, not a
// failure: the project is about it once, and says so without an error.
func TestCreateProjectDedupesARepeatedReference(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	w := do(t, h, "POST", "/projects", `{"papers":["doi:10.1234/dup","doi:10.1234/dup"]}`, "me")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", w.Code, w.Body)
	}
	var got createResult
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Papers[0].Error != "" || got.Papers[1].Error != "" {
		t.Errorf("papers = %+v, want no error on either", got.Papers)
	}
	papers, _ := d.Projects.ListPapers(context.Background(), got.ProjectID)
	if len(papers) != 1 {
		t.Errorf("the project holds %d papers, want 1", len(papers))
	}
}
