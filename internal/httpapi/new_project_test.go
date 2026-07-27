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
	ProjectID string          `json:"project_id"`
	Title     string          `json:"title"`
	Sessions  []SessionResult `json:"sessions"`
	Members   []string        `json:"members"`
}

// AC1: create with a title and several valid papers opens one project with one
// session per paper, each with a job id.
func TestCreateProjectAllSucceed(t *testing.T) {
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
	if len(got.Sessions) != 2 || got.Sessions[0].JobID == "" || got.Sessions[1].JobID == "" {
		t.Fatalf("sessions = %+v, want two started", got.Sessions)
	}
	// The project exists under the owner with the chosen title.
	p, err := d.Projects.Get(context.Background(), got.ProjectID)
	if err != nil || p.Title != "My study" || p.Owner != "me" {
		t.Errorf("stored project = %+v (err %v)", p, err)
	}
}

// AC2: among mixed papers, a bad one is reported while the valid one starts, and
// the project survives.
func TestCreateProjectPartial(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	w := do(t, h, "POST", "/projects", `{"papers":["doi:10.1234/ok","garbage"]}`, "me")
	if w.Code != http.StatusCreated {
		t.Fatalf("partial create status = %d, want 201; body %s", w.Code, w.Body)
	}
	var got createResult
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Sessions[0].JobID == "" || got.Sessions[1].Error != "unresolvable_paper" {
		t.Errorf("sessions = %+v, want one started + one unresolvable", got.Sessions)
	}
	if _, err := d.Projects.Get(context.Background(), got.ProjectID); err != nil {
		t.Errorf("project should survive a partial failure: %v", err)
	}
}

// AC3: when every paper fails, no project is left behind and the call is 422.
func TestCreateProjectAllFailDeletesProject(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	w := do(t, h, "POST", "/projects", `{"papers":["garbage","also bad"]}`, "me")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("all-fail status = %d, want 422", w.Code)
	}
	// No project was left behind for this owner.
	list, _ := d.Projects.ListByOwner(context.Background(), "me")
	if len(list) != 0 {
		t.Errorf("an all-failed create left %d project(s) behind", len(list))
	}
}

// Empty papers is a 400.
func TestCreateProjectNeedsAPaper(t *testing.T) {
	d := testDeps(allowAll)
	if w := do(t, NewRouter(d).Handler(), "POST", "/projects", `{"title":"x","papers":[]}`, "me"); w.Code != http.StatusBadRequest {
		t.Errorf("no-papers status = %d, want 400", w.Code)
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

// A create that starts at least one session but cannot enqueue a duplicate of
// the same paper reports the guard per-paper, not as a whole-request failure.
func TestCreateProjectDuplicatePaperGuarded(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	w := do(t, h, "POST", "/projects", `{"papers":["doi:10.1234/dup","doi:10.1234/dup"]}`, "me")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (one started); body %s", w.Code, w.Body)
	}
	var got createResult
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Sessions[0].JobID == "" || got.Sessions[1].Error != "active_session" {
		t.Errorf("sessions = %+v, want one started + the duplicate guarded", got.Sessions)
	}
}
