// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/profile"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/verdict"
)

// allowAll is a permissive gate for testing the handlers behind the gate.
var allowAll = GateFunc(func(*http.Request) (bool, string) { return true, "" })

func testDeps(gate Gate) Deps {
	jobs := job.NewMemStore()
	return Deps{
		Jobs: jobs,
		// Wired the way the composition root wires them, so a paper moved here
		// takes its sessions with it (035).
		Projects:      project.NewMemStore(project.WithSessions(jobs)),
		Members:       access.NewMemMembership(),
		Profiles:      profile.NewMemStore(),
		Reproductions: &fakeRepro{},
		Gate:          gate,
	}
}

// publishable gives a principal the public profile 006r requires before they
// may sign a project public. Every publish test needs it, so it is a shared
// helper.
func publishable(t *testing.T, d Deps, principal, handle string) {
	t.Helper()
	if _, err := d.Profiles.Save(context.Background(), profile.Profile{
		Principal:   principal,
		DisplayName: "Ada Lovelace",
		Contact:     "ada@example.org",
		Handle:      handle,
		Public:      true,
	}); err != nil {
		t.Fatalf("save publishable profile: %v", err)
	}
}

type fakeRepro struct {
	getErr, transcriptErr error
	findings              []byte // verdict.json, when the test supplies one (021a).
}

func (f *fakeRepro) Get(_ context.Context, id string) (Reproduction, error) {
	if f.getErr != nil {
		return Reproduction{}, f.getErr
	}
	switch id {
	case "r1":
		return Reproduction{ID: "r1", Repo: "open-hai/r1", Verdict: "passed", CanPublish: true}, nil
	case "flagged":
		// A reproduction whose tree flagged third-party data, so it may not be
		// made public.
		return Reproduction{ID: "flagged", Repo: "open-hai/flagged", CanPublish: false}, nil
	default:
		return Reproduction{}, ErrReproNotFound
	}
}
func (f *fakeRepro) Files(_ context.Context, id string) ([]string, error) {
	if id != "r1" {
		return nil, nil
	}
	return []string{"replication.json", "README.md", "src/main.py"}, nil
}
func (f *fakeRepro) Artifact(_ context.Context, id, path string) ([]byte, error) {
	if id != "r1" || path == "missing" {
		return nil, ErrReproNotFound
	}
	// verdict.json is served as whatever the test set, so the findings route can
	// be driven against a valid file, an invalid one, and an absent one (021a).
	if path == verdict.File {
		if f.findings == nil {
			return nil, ErrReproNotFound
		}
		return f.findings, nil
	}
	return []byte("content of " + path), nil
}
func (f *fakeRepro) Transcript(_ context.Context, id string) ([]byte, error) {
	if f.transcriptErr != nil {
		return nil, f.transcriptErr // A forge or transport failure must become a 500, not a 404.
	}
	switch id {
	case "r1":
		// A small valid transcript.jsonl: a message, a tool call, its result, and a
		// model-request event carrying per-request usage.
		return []byte(`{"role":"agent","kind":"message","content":"start","event":1}` + "\n" +
			`{"role":"agent","kind":"tool_use","tool":"bash","content":"ls"}` + "\n" +
			`{"role":"user","kind":"tool_result","content":"file.txt"}` + "\n" +
			`{"role":"system","kind":"model_request_end","usage":{"InputTokens":100,"OutputTokens":50,"CachedInputTokens":10}}` + "\n"), nil
	case "bare":
		// A transcript with no per-request usage anywhere, so usage cannot be
		// derived.
		return []byte(`{"role":"agent","kind":"message","content":"hi"}` + "\n"), nil
	case "multi":
		// Two charged requests either side of an uncharged event, so a per-event
		// figure can be told apart from the session total (006s).
		return []byte(`{"role":"system","kind":"model_request_end","usage":{"InputTokens":100,"OutputTokens":50,"CachedInputTokens":10}}` + "\n" +
			`{"role":"agent","kind":"message","content":"between"}` + "\n" +
			`{"role":"system","kind":"model_request_end","usage":{"InputTokens":7,"OutputTokens":3,"CachedInputTokens":1}}` + "\n"), nil
	default:
		return nil, ErrReproNotFound
	}
}

func do(t *testing.T, h http.Handler, method, path, body, principalSub string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if principalSub != "" {
		r = r.WithContext(WithPrincipal(r.Context(), principalSub))
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// submitResult is the shape a started session returns.
type submitResult struct {
	JobID     string `json:"job_id"`
	ProjectID string `json:"project_id"`
}

// createWithPaper opens a project holding one paper and returns the project id.
// Since 012 this starts nothing; it is the first half of what POST /papers used
// to do in one call.
func createWithPaper(t *testing.T, h http.Handler, url, sub string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	w := do(t, h, "POST", "/projects", `{"papers":["`+url+`"]}`, sub)
	var got struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	return w, got.ProjectID
}

// startPaper runs a paper a project already holds. It is the second half.
func startPaper(t *testing.T, h http.Handler, projectID, key, sub string) (*httptest.ResponseRecorder, submitResult) {
	t.Helper()
	w := do(t, h, "POST", "/projects/"+projectID+"/sessions", `{"paper_key":"`+key+`"}`, sub)
	var got submitResult
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	return w, got
}

// submit is the whole old flow in one call: create a project holding the
// paper, then start it. Many tests only need a running session and do not
// depend on which half produced it.
func submit(t *testing.T, h http.Handler, url, sub string) (*httptest.ResponseRecorder, submitResult) {
	t.Helper()
	w, projectID := createWithPaper(t, h, url, sub)
	if projectID == "" {
		return w, submitResult{}
	}
	key, err := paper.Key(url)
	if err != nil {
		t.Fatalf("paper.Key(%q): %v", url, err)
	}
	w, got := startPaper(t, h, projectID, key, sub)
	got.ProjectID = projectID
	return w, got
}

// TestCreateRecordsPapersAndStartsNothing covers 012: creating a project
// records the papers it is about and starts nothing. Starting a session is a
// separate second step, which lets somebody name a project and record its
// papers before they have the credits to run them.
func TestCreateRecordsPapersAndStartsNothing(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	w, projectID := createWithPaper(t, h, "https://arxiv.org/abs/2302.03714", "sub-1")
	if w.Code != http.StatusCreated || projectID == "" {
		t.Fatalf("create: status %d, body %s", w.Code, w.Body)
	}
	p, err := d.Projects.Get(context.Background(), projectID)
	if err != nil || p.Owner != "sub-1" {
		t.Fatalf("project = %+v, err %v", p, err)
	}
	// Nothing ran.
	sessions, _ := d.Jobs.ListByProject(context.Background(), projectID)
	if len(sessions) != 0 {
		t.Fatalf("create started %d sessions, want 0", len(sessions))
	}
	// The paper is on the project, under its offline identity.
	papers, err := d.Projects.ListPapers(context.Background(), projectID)
	if err != nil || len(papers) != 1 || papers[0].Key != "arxiv:2302.03714" {
		t.Fatalf("papers = %+v, err %v", papers, err)
	}
	if papers[0].Ref != "https://arxiv.org/abs/2302.03714" || papers[0].AddedBy != "sub-1" {
		t.Errorf("paper = %+v", papers[0])
	}

	// Starting the paper is the second step, and the one that creates a session.
	sw, got := startPaper(t, h, projectID, "arxiv:2302.03714", "sub-1")
	if sw.Code != http.StatusCreated || got.JobID == "" {
		t.Fatalf("start: status %d, body %s", sw.Code, sw.Body)
	}
	j, err := d.Jobs.Get(context.Background(), got.JobID)
	if err != nil || j.Phase != job.PhaseSubmitted || j.Principal != "sub-1" || j.ProjectID != projectID {
		t.Errorf("session = %+v, err %v", j, err)
	}
	if j.PaperKey != "arxiv:2302.03714" {
		t.Errorf("session paper_key = %q, want arxiv:2302.03714", j.PaperKey)
	}
	// The session carries the ref the paper was recorded with, not one the
	// caller typed again. A start request names a paper by key and carries no
	// reference of its own.
	if j.Ref != "https://arxiv.org/abs/2302.03714" {
		t.Errorf("session ref = %q", j.Ref)
	}
}

// TestAddPaperToOwnedProjectThenStart covers 012: a paper added to an
// existing project, then run. This is what the old project-scoped submit did
// in one call, split into its two steps.
func TestAddPaperToOwnedProjectThenStart(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "sub-1", "My project")

	w := do(t, h, "POST", "/projects/"+p.ID+"/papers", `{"url":"https://arxiv.org/abs/2302.03714"}`, "sub-1")
	if w.Code != http.StatusCreated {
		t.Fatalf("add paper: status %d, body %s", w.Code, w.Body)
	}
	if sessions, _ := d.Jobs.ListByProject(context.Background(), p.ID); len(sessions) != 0 {
		t.Fatalf("adding a paper started %d sessions, want 0", len(sessions))
	}
	// Adding the same paper twice is refused: a project holds each paper once.
	if w := do(t, h, "POST", "/projects/"+p.ID+"/papers", `{"url":"https://arxiv.org/abs/2302.03714"}`, "sub-1"); w.Code != http.StatusConflict {
		t.Errorf("duplicate paper: status %d, want 409", w.Code)
	}

	sw, got := startPaper(t, h, p.ID, "arxiv:2302.03714", "sub-1")
	if sw.Code != http.StatusCreated || got.ProjectID != p.ID {
		t.Fatalf("start: status %d, project %q", sw.Code, got.ProjectID)
	}
	sessions, _ := d.Jobs.ListByProject(context.Background(), p.ID)
	if len(sessions) != 1 {
		t.Fatalf("project has %d sessions, want 1", len(sessions))
	}
}

// TestStartDoubleFireIsRefused covers AC4: the double-trigger guard answers on
// the one route that starts a run, so the refusal names the act that was
// refused. Nothing is created that would need undoing, because starting a
// session creates nothing but the session.
func TestStartDoubleFireIsRefused(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	_, projectID := createWithPaper(t, h, "https://arxiv.org/abs/2302.03714", "sub-1")
	if w, _ := startPaper(t, h, projectID, "arxiv:2302.03714", "sub-1"); w.Code != http.StatusCreated {
		t.Fatalf("first start: %d", w.Code)
	}
	w, _ := startPaper(t, h, projectID, "arxiv:2302.03714", "sub-1")
	if w.Code != http.StatusConflict {
		t.Fatalf("double-fire status = %d, want 409", w.Code)
	}
	// One project and one session: the refusal left nothing behind, and no
	// project was created speculatively that would need deleting.
	ps, _ := d.Projects.ListByOwner(context.Background(), "sub-1")
	if len(ps) != 1 {
		t.Errorf("owner has %d projects, want 1", len(ps))
	}
	if sessions, _ := d.Jobs.ListByProject(context.Background(), projectID); len(sessions) != 1 {
		t.Errorf("project has %d sessions, want 1", len(sessions))
	}
}

// TestStartRefusesAPaperTheProjectDoesNotHold covers AC4: a session may only
// run a paper the project holds. Otherwise a caller could spend a project's
// credits on any paper they named.
func TestStartRefusesAPaperTheProjectDoesNotHold(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	_, projectID := createWithPaper(t, h, "https://arxiv.org/abs/2302.03714", "sub-1")

	if w, _ := startPaper(t, h, projectID, "arxiv:9999.99999", "sub-1"); w.Code != http.StatusNotFound {
		t.Errorf("unheld paper: status %d, want 404", w.Code)
	}
	if w := do(t, h, "POST", "/projects/"+projectID+"/sessions", `{"paper_key":"  "}`, "sub-1"); w.Code != http.StatusBadRequest {
		t.Errorf("blank key: status %d, want 400", w.Code)
	}
	if w := do(t, h, "POST", "/projects/nope/sessions", `{"paper_key":"arxiv:2302.03714"}`, "sub-1"); w.Code != http.StatusNotFound {
		t.Errorf("missing project: status %d, want 404", w.Code)
	}
}

// TestAPaperKeyWithSlashesRoundTrips checks that a paper key spanning several
// path segments, such as "doi:10.1145/3544548.3581433", round-trips. The key
// travels in a body or an encoded query, never in a path. Moving it into the
// URL would make this case 404 without any other error.
func TestAPaperKeyWithSlashesRoundTrips(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	const ref = "https://doi.org/10.1145/3544548.3581433"
	const key = "doi:10.1145/3544548.3581433"

	_, projectID := createWithPaper(t, h, ref, "sub-1")
	if w, got := startPaper(t, h, projectID, key, "sub-1"); w.Code != http.StatusCreated || got.JobID == "" {
		t.Fatalf("start with a slashed key: status %d, body %s", w.Code, w.Body)
	}
	// The same key goes through the delete route's query, which is the other
	// place it is carried in a URL.
	w := do(t, h, "DELETE", "/projects/"+projectID+"/papers?key="+url.QueryEscape(key), "", "sub-1")
	if w.Code != http.StatusConflict {
		t.Errorf("delete a started paper: status %d, want 409 (it has a session)", w.Code)
	}
}

// TestRemovePaperOnlyBeforeItHasRun covers AC7: a paper with no sessions can
// be removed; one that has been run cannot. The record of what was reproduced
// is not editable.
func TestRemovePaperOnlyBeforeItHasRun(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	_, projectID := createWithPaper(t, h, "https://arxiv.org/abs/2302.03714", "owner")
	if w := do(t, h, "POST", "/projects/"+projectID+"/papers", `{"url":"https://arxiv.org/abs/2401.00001"}`, "owner"); w.Code != http.StatusCreated {
		t.Fatalf("add second paper: %d", w.Code)
	}

	// A paper that was never run is removed.
	if w := do(t, h, "DELETE", "/projects/"+projectID+"/papers?key=arxiv:2401.00001", "", "owner"); w.Code != http.StatusNoContent {
		t.Fatalf("remove an unrun paper: status %d, body %s", w.Code, w.Body)
	}
	papers, _ := d.Projects.ListPapers(context.Background(), projectID)
	if len(papers) != 1 {
		t.Fatalf("after remove: %d papers, want 1", len(papers))
	}

	// A paper that has been run once stays, whatever the outcome of the run.
	if w, _ := startPaper(t, h, projectID, "arxiv:2302.03714", "owner"); w.Code != http.StatusCreated {
		t.Fatalf("start: %d", w.Code)
	}
	if w := do(t, h, "DELETE", "/projects/"+projectID+"/papers?key=arxiv:2302.03714", "", "owner"); w.Code != http.StatusConflict {
		t.Errorf("remove a run paper: status %d, want 409", w.Code)
	}
	if w := do(t, h, "DELETE", "/projects/"+projectID+"/papers", "", "owner"); w.Code != http.StatusBadRequest {
		t.Errorf("remove with no key: status %d, want 400", w.Code)
	}
}

// TestListPapersReportsSessions covers AC6: the papers list reports what has
// happened to each paper, so the project screen can show a paper nobody has
// started beside one that failed.
func TestListPapersReportsSessions(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	_, projectID := createWithPaper(t, h, "https://arxiv.org/abs/2302.03714", "owner")
	if w := do(t, h, "POST", "/projects/"+projectID+"/papers", `{"url":"https://arxiv.org/abs/2401.00001"}`, "owner"); w.Code != http.StatusCreated {
		t.Fatalf("add: %d", w.Code)
	}
	if w, _ := startPaper(t, h, projectID, "arxiv:2302.03714", "owner"); w.Code != http.StatusCreated {
		t.Fatalf("start: %d", w.Code)
	}

	w := do(t, h, "GET", "/projects/"+projectID+"/papers", "", "owner")
	if w.Code != http.StatusOK {
		t.Fatalf("list papers: status %d", w.Code)
	}
	var got []PaperView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 2 {
		t.Fatalf("papers = %s, err %v", w.Body, err)
	}
	if got[0].Key != "arxiv:2302.03714" || got[0].Sessions != 1 || got[0].LatestPhase != string(job.PhaseSubmitted) {
		t.Errorf("started paper = %+v", got[0])
	}
	if got[1].Key != "arxiv:2401.00001" || got[1].Sessions != 0 || got[1].LatestPhase != "" {
		t.Errorf("unstarted paper = %+v", got[1])
	}
}

func TestPaperRouteValidation(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	_, projectID := createWithPaper(t, h, "https://arxiv.org/abs/2302.03714", "sub")

	if w := do(t, h, "POST", "/projects/"+projectID+"/papers", `{"url":"  "}`, "sub"); w.Code != http.StatusBadRequest {
		t.Errorf("empty url: status %d, want 400", w.Code)
	}
	if w := do(t, h, "POST", "/projects/"+projectID+"/papers", `not json`, "sub"); w.Code != http.StatusBadRequest {
		t.Errorf("bad json: status %d, want 400", w.Code)
	}
	// A reference that is not a DOI, arXiv ID, or URL is rejected at the edge.
	if w := do(t, h, "POST", "/projects/"+projectID+"/papers", `{"url":"just some words"}`, "sub"); w.Code != http.StatusBadRequest {
		t.Errorf("garbage url: status %d, want 400", w.Code)
	}
	// A create whose only paper is unresolvable still opens the project and
	// reports the reference. There is no enqueue left to fail, so nothing is
	// undone.
	w := do(t, h, "POST", "/projects", `{"papers":["just some words"]}`, "sub")
	if w.Code != http.StatusCreated {
		t.Fatalf("create with an unresolvable paper: status %d, body %s", w.Code, w.Body)
	}
	var created struct {
		ProjectID string        `json:"project_id"`
		Papers    []PaperResult `json:"papers"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ProjectID == "" || len(created.Papers) != 1 || created.Papers[0].Error != "unresolvable_paper" {
		t.Errorf("create result = %+v", created)
	}
	if _, err := d.Projects.Get(context.Background(), created.ProjectID); err != nil {
		t.Errorf("the project must survive: %v", err)
	}

	// Working in a project the caller is neither owner nor collaborator of.
	other, _ := d.Projects.Create(context.Background(), "someone-else", "Theirs")
	if w := do(t, h, "POST", "/projects/"+other.ID+"/papers", `{"url":"https://arxiv.org/abs/2302.03714"}`, "me"); w.Code != http.StatusNotFound {
		t.Errorf("stranger add: status %d, want 404", w.Code)
	}
	if w := do(t, h, "POST", "/projects/nope/papers", `{"url":"https://arxiv.org/abs/2302.03714"}`, "me"); w.Code != http.StatusNotFound {
		t.Errorf("missing-project add: status %d, want 404", w.Code)
	}
}

// TestProjectListAndDetail covers AC1: GET /projects lists the caller's own
// projects with a session count, and GET /projects/{id} returns a project with
// its sessions and refuses other owners' projects.
func TestProjectListAndDetail(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	// Two create-and-start flows for the same owner open two projects.
	_, a := submit(t, h, "https://arxiv.org/abs/2302.03714", "me")
	_, _ = submit(t, h, "https://doi.org/10.1145/3313831", "me")
	// Another principal's project must not appear in the caller's list or be
	// readable by the caller.
	strangerP, _ := d.Projects.Create(context.Background(), "stranger", "Theirs")

	w := do(t, h, "GET", "/projects", "", "me")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}
	var list []ProjectView
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("listed %d projects, want 2", len(list))
	}
	for _, p := range list {
		if p.SessionCount != 1 {
			t.Errorf("project %s count = %d, want 1", p.ID, p.SessionCount)
		}
	}

	// Detail returns the project and its one session.
	w = do(t, h, "GET", "/projects/"+a.ProjectID, "", "me")
	if w.Code != http.StatusOK {
		t.Fatalf("detail status = %d", w.Code)
	}
	var detail ProjectDetail
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if detail.Project.ID != a.ProjectID || len(detail.Sessions) != 1 {
		t.Errorf("detail = %+v", detail)
	}

	// Another owner's private project is not disclosed. It is hidden as a 404,
	// not a 403 that would confirm it exists.
	if w := do(t, h, "GET", "/projects/"+strangerP.ID, "", "me"); w.Code != http.StatusNotFound {
		t.Errorf("stranger project detail: status %d, want 404", w.Code)
	}
}

// signOff is a well-formed sign-off body: an attestation version and the signer
// snapshot 006n requires, of which only the contact is mandatory. signOffNoRepo
// is the same body with the forge repository opted out of the flip.
const (
	signOff       = `{"attestation_version":"v2","signed_off_name":"Ada Lovelace","signed_off_institution":"LMU Munich","signed_off_contact":"ada@example.org"}`
	signOffNoRepo = `{"attestation_version":"v2","signed_off_contact":"ada@example.org","flip_repo":false}`
)

// TestMakeProjectPublic covers 006f AC6: the public flip is owner-only, needs
// a recorded sign-off, and refuses a project that is not clear with a 409. The
// one-way rule in 006f is superseded by 006o.
func TestMakeProjectPublic(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	// A project whose one session cleared the gate: the fake reproduction r1
	// has CanPublish set.
	p, _ := d.Projects.Create(context.Background(), "me", "Owned")
	_ = publishInto(t, d.Jobs, "me", p.ID, "r1")
	publishable(t, d, "me", "ada")

	// A stranger is answered as if the project did not exist, and nothing
	// changes.
	if w := do(t, h, "POST", "/projects/"+p.ID+"/public", signOff, "stranger"); w.Code != http.StatusNotFound {
		t.Fatalf("stranger flip: status %d, want 404", w.Code)
	}
	// An un-signed flip changes nothing.
	if w := do(t, h, "POST", "/projects/"+p.ID+"/public", `{}`, "me"); w.Code != http.StatusBadRequest {
		t.Fatalf("unsigned flip: status %d, want 400", w.Code)
	}
	if got, _ := d.Projects.Get(context.Background(), p.ID); got.Visibility != project.VisibilityPrivate {
		t.Fatalf("project went public without a valid request")
	}

	// The owner, with a sign-off, flips it public and the attestation is recorded.
	w := do(t, h, "POST", "/projects/"+p.ID+"/public", signOff, "me")
	if w.Code != http.StatusOK {
		t.Fatalf("owner flip: status %d, want 200", w.Code)
	}
	got, _ := d.Projects.Get(context.Background(), p.ID)
	if got.Visibility != project.VisibilityPublic || got.SignedOffBy != "me" || got.AttestationVersion != "v2" || got.SignedOffAt == nil {
		t.Fatalf("after flip: %+v", got)
	}
}

// TestMakeProjectPublicRefusesNotClear covers 006f AC6: a project whose
// reproduction did not clear the gate is refused with a 409.
func TestMakeProjectPublicRefusesNotClear(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "me", "Owned")
	_ = publishInto(t, d.Jobs, "me", p.ID, "flagged") // CanPublish is false.

	if w := do(t, h, "POST", "/projects/"+p.ID+"/public", signOff, "me"); w.Code != http.StatusConflict {
		t.Fatalf("not-clear flip: status %d, want 409", w.Code)
	}
	if got, _ := d.Projects.Get(context.Background(), p.ID); got.Visibility != project.VisibilityPrivate {
		t.Errorf("a non-clear project must stay private")
	}
}

// refusedPublish posts a sign-off and reads back the refusal: its code and the
// sessions it named. Every publish refusal below is asserted through this, so
// a code that stops carrying its blockers fails a test rather than reaching a
// reader as an unexplained refusal.
func refusedPublish(t *testing.T, h http.Handler, projectID string) (int, string, []PublishBlocker) {
	t.Helper()
	w := do(t, h, "POST", "/projects/"+projectID+"/public", signOff, "me")
	var got struct {
		Error    string           `json:"error"`
		Blockers []PublishBlocker `json:"blockers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("refusal body %q: %v", w.Body.String(), err)
	}
	return w.Code, got.Error, got.Blockers
}

// TestMakeProjectPublicNamesEveryBlocker checks that a refusal names every
// session in the way, not only the first. An owner who fixes one blocker must
// not be refused again for another that was not reported, so a project with
// two different blockers reports both, each with the session to open and the
// reason it is blocked.
func TestMakeProjectPublicNamesEveryBlocker(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	publishable(t, d, "me", "ada")
	p, _ := d.Projects.Create(context.Background(), "me", "Owned")
	clear := publishInto(t, d.Jobs, "me", p.ID, "r1")        // Cleared, so not a blocker.
	flagged := publishInto(t, d.Jobs, "me", p.ID, "flagged") // The review is not clear.
	unfinished := publishInto(t, d.Jobs, "me", p.ID, "gone") // A repository with no manifest.

	status, code, blockers := refusedPublish(t, h, p.ID)
	if status != http.StatusConflict {
		t.Fatalf("status %d, want 409", status)
	}
	// The flagged tree takes precedence over the unfinished run: one needs a
	// decision, the other only needs time.
	if code != "not_clear" {
		t.Errorf("code = %q, want not_clear", code)
	}
	reasons := map[string]string{}
	for _, b := range blockers {
		reasons[b.SessionID] = b.Reason
	}
	if len(blockers) != 2 {
		t.Fatalf("blockers = %+v, want two", blockers)
	}
	if reasons[flagged.ID] != string(BlockNotClear) {
		t.Errorf("flagged session reason = %q, want not_clear", reasons[flagged.ID])
	}
	if reasons[unfinished.ID] != string(BlockUnfinished) {
		t.Errorf("unfinished session reason = %q, want unfinished", reasons[unfinished.ID])
	}
	if _, named := reasons[clear.ID]; named {
		t.Errorf("the cleared session %s was reported as a blocker", clear.ID)
	}
	if got, _ := d.Projects.Get(context.Background(), p.ID); got.Visibility != project.VisibilityPrivate {
		t.Errorf("a blocked project must stay private")
	}
}

// TestMakeProjectPublicReportsUnfinishedSessions checks that a run that never
// finalized is not reported as a redistribution verdict. It has a repository
// and no manifest, which used to be reported as a flagged tree and sent the
// owner to look for third-party data that was never there.
func TestMakeProjectPublicReportsUnfinishedSessions(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	publishable(t, d, "me", "ada")
	p, _ := d.Projects.Create(context.Background(), "me", "Owned")
	j := publishInto(t, d.Jobs, "me", p.ID, "gone")

	status, code, blockers := refusedPublish(t, h, p.ID)
	if status != http.StatusConflict || code != "sessions_unfinished" {
		t.Fatalf("status %d code %q, want 409 sessions_unfinished", status, code)
	}
	if len(blockers) != 1 || blockers[0].SessionID != j.ID || blockers[0].Reason != string(BlockUnfinished) {
		t.Fatalf("blockers = %+v", blockers)
	}
}

// TestMakeProjectPublicReportsUnreadableManifest checks that a forge that does
// not answer is reported as a temporary failure on this side, not as a
// reproduction that failed the redistribution check.
func TestMakeProjectPublicReportsUnreadableManifest(t *testing.T) {
	d := testDeps(allowAll)
	d.Reproductions = &fakeRepro{getErr: errors.New("github timed out")}
	h := NewRouter(d).Handler()
	publishable(t, d, "me", "ada")
	p, _ := d.Projects.Create(context.Background(), "me", "Owned")
	_ = publishInto(t, d.Jobs, "me", p.ID, "r1")

	status, code, blockers := refusedPublish(t, h, p.ID)
	if status != http.StatusConflict || code != "manifest_unreadable" {
		t.Fatalf("status %d code %q, want 409 manifest_unreadable", status, code)
	}
	if len(blockers) != 1 || blockers[0].Reason != string(BlockUnreadable) {
		t.Fatalf("blockers = %+v", blockers)
	}
}

// TestMakeProjectPublicReportsNothingPublished checks that a project that has
// never published anything reports that it has nothing to make public. It is
// not blocked by a session, since no session has a repository, so the refusal
// names no session.
func TestMakeProjectPublicReportsNothingPublished(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	publishable(t, d, "me", "ada")
	p, _ := d.Projects.Create(context.Background(), "me", "Owned")

	status, code, blockers := refusedPublish(t, h, p.ID)
	if status != http.StatusConflict || code != "nothing_published" {
		t.Fatalf("status %d code %q, want 409 nothing_published", status, code)
	}
	if len(blockers) != 0 {
		t.Fatalf("blockers = %+v, want none", blockers)
	}
}

// fakeRepoPub records which repositories were flipped public and which were
// closed again. errOn names a repository whose flip fails, so a test can assert
// that a forge failure does not fail the request.
type fakeRepoPub struct {
	flipped []string
	closed  []string // Attempted, not confirmed: errOn is recorded here too.
	errOn   string
}

func (f *fakeRepoPub) SetPublic(_ context.Context, repo string) error {
	f.flipped = append(f.flipped, repo)
	return nil
}

func (f *fakeRepoPub) SetPrivate(_ context.Context, repo string) error {
	f.closed = append(f.closed, repo)
	if repo == f.errOn {
		return errors.New("github refused the visibility change")
	}
	return nil
}

// TestPublicFlipRepoOptOut covers 006f: the owner may opt the forge repository
// out of the public flip. The project still goes public in the app, but
// SetPublic is not called.
func TestPublicFlipRepoOptOut(t *testing.T) {
	pub := &fakeRepoPub{}
	d := testDeps(allowAll)
	d.RepoPublisher = pub
	h := NewRouter(d).Handler()

	publishable(t, d, "me", "ada")

	// With flip_repo omitted, the default, the repository is flipped.
	p1, _ := d.Projects.Create(context.Background(), "me", "One")
	_ = publishInto(t, d.Jobs, "me", p1.ID, "r1")
	if w := do(t, h, "POST", "/projects/"+p1.ID+"/public", signOff, "me"); w.Code != http.StatusOK {
		t.Fatalf("default flip: status %d", w.Code)
	}
	if len(pub.flipped) != 1 || pub.flipped[0] != "r1" {
		t.Fatalf("default should flip the repo, flipped=%v", pub.flipped)
	}

	// With flip_repo false, the project goes public but the repository is not
	// flipped.
	p2, _ := d.Projects.Create(context.Background(), "me", "Two")
	_ = publishInto(t, d.Jobs, "me", p2.ID, "r1")
	if w := do(t, h, "POST", "/projects/"+p2.ID+"/public", signOffNoRepo, "me"); w.Code != http.StatusOK {
		t.Fatalf("opt-out flip: status %d", w.Code)
	}
	if len(pub.flipped) != 1 {
		t.Errorf("opt-out must not flip the repo, flipped=%v", pub.flipped)
	}
	if got, _ := d.Projects.Get(context.Background(), p2.ID); got.Visibility != project.VisibilityPublic {
		t.Errorf("opt-out project should still be public in-app")
	}
}

// TestWithdrawProject covers 006o AC1, AC2 and AC3: the owner withdraws a
// publication. The project leaves the public zone and closes to strangers,
// the retraction is recorded, and the sign-off it retracts is left unchanged.
func TestWithdrawProject(t *testing.T) {
	pub := &fakeRepoPub{}
	d := testDeps(allowAll)
	d.RepoPublisher = pub
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "me", "Owned")
	sess := publishInto(t, d.Jobs, "me", p.ID, "r1")
	publishable(t, d, "me", "ada")
	if w := do(t, h, "POST", "/projects/"+p.ID+"/public", signOff, "me"); w.Code != http.StatusOK {
		t.Fatalf("publish: status %d", w.Code)
	}
	// While public, a stranger can read the session under it. These are the
	// reads the withdrawal has to close.
	sessionReads := []string{"/jobs/" + sess.ID, "/jobs/" + sess.ID + "/transcript", "/jobs/" + sess.ID + "/reproduction", "/jobs/" + sess.ID + "/artifacts"}
	for _, path := range sessionReads {
		if w := do(t, h, "GET", path, "", "someone-else"); w.Code != http.StatusOK {
			t.Fatalf("while public, %s: status %d, want 200", path, w.Code)
		}
	}

	// A non-owner cannot withdraw, and nothing changes.
	if w := do(t, h, "POST", "/projects/"+p.ID+"/private", `{}`, "stranger"); w.Code != http.StatusForbidden {
		t.Fatalf("non-owner withdrawal: status %d, want 403", w.Code)
	}
	if got, _ := d.Projects.Get(context.Background(), p.ID); got.Visibility != project.VisibilityPublic {
		t.Fatalf("a stranger withdrew someone else's publication")
	}
	if w := do(t, h, "POST", "/projects/nope/private", `{}`, "me"); w.Code != http.StatusNotFound {
		t.Errorf("withdrawing a missing project: status %d, want 404", w.Code)
	}

	// The owner withdraws it.
	w := do(t, h, "POST", "/projects/"+p.ID+"/private", `{"reason":"an unreleased figure"}`, "me")
	if w.Code != http.StatusOK {
		t.Fatalf("owner withdrawal: status %d, want 200", w.Code)
	}
	got, _ := d.Projects.Get(context.Background(), p.ID)
	if got.Visibility != project.VisibilityPrivate {
		t.Fatalf("after withdrawal: %+v", got)
	}
	if got.WithdrawnAt == nil || got.WithdrawnBy != "me" || got.WithdrawnReason != "an unreleased figure" {
		t.Errorf("retraction not recorded: %+v", got)
	}
	// The retraction ends the publication; it does not remove the attestation.
	if got.SignedOffBy != "me" || got.AttestationVersion != "v2" || got.SignedOffAt == nil ||
		got.SignedOffName != "Ada Lovelace" || got.SignedOffContact != "ada@example.org" {
		t.Errorf("withdrawal erased the sign-off: %+v", got)
	}
	// The forge repository is closed as well.
	if len(pub.closed) != 1 || pub.closed[0] != "r1" {
		t.Errorf("the repository should be closed, closed=%v", pub.closed)
	}

	// The public zone drops it, and the stranger who could read it while public
	// can no longer open it.
	zw := do(t, h, "GET", "/public", "", "someone-else")
	var zone []ProjectView
	_ = json.Unmarshal(zw.Body.Bytes(), &zone)
	if len(zone) != 0 {
		t.Errorf("public zone still lists a withdrawn project: %+v", zone)
	}
	if w := do(t, h, "GET", "/projects/"+p.ID, "", "someone-else"); w.Code != http.StatusNotFound {
		t.Errorf("stranger opening a withdrawn project: status %d, want 404", w.Code)
	}
	// The artifacts close with it. Closing the project page while a transcript
	// or an artifact stayed readable by URL would defeat the withdrawal, so
	// every session-scoped read is asserted, not only the project read.
	for _, path := range sessionReads {
		if w := do(t, h, "GET", path, "", "someone-else"); w.Code != http.StatusNotFound {
			t.Errorf("after withdrawal, %s: status %d, want 404", path, w.Code)
		}
	}
	// The owner still reads everything. A withdrawal takes the work out of
	// public view; it does not remove the owner's access to it.
	for _, path := range sessionReads {
		if w := do(t, h, "GET", path, "", "me"); w.Code != http.StatusOK {
			t.Errorf("after withdrawal, the owner's own %s: status %d, want 200", path, w.Code)
		}
	}
}

// TestRepublishAfterWithdrawal covers 006o AC5: a withdrawn project can be
// published again with a fresh sign-off that clears the retraction. The
// manifest gate is re-run rather than carried over, so a tree that is no
// longer clear stays private.
func TestRepublishAfterWithdrawal(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "me", "Owned")
	_ = publishInto(t, d.Jobs, "me", p.ID, "r1")
	publishable(t, d, "me", "ada")
	_ = do(t, h, "POST", "/projects/"+p.ID+"/public", signOff, "me")
	_ = do(t, h, "POST", "/projects/"+p.ID+"/private", `{"reason":"second thoughts"}`, "me")

	if w := do(t, h, "POST", "/projects/"+p.ID+"/public", signOff, "me"); w.Code != http.StatusOK {
		t.Fatalf("republish: status %d, want 200", w.Code)
	}
	got, _ := d.Projects.Get(context.Background(), p.ID)
	if got.Visibility != project.VisibilityPublic {
		t.Fatalf("republish did not take: %+v", got)
	}
	if got.WithdrawnAt != nil || got.WithdrawnBy != "" || got.WithdrawnReason != "" {
		t.Errorf("republish left the retraction in place: %+v", got)
	}

	// A project whose tree never cleared is still refused after a withdrawal:
	// the gate is re-run, not inherited.
	q, _ := d.Projects.Create(context.Background(), "me", "Flagged")
	_ = publishInto(t, d.Jobs, "me", q.ID, "flagged")
	_ = do(t, h, "POST", "/projects/"+q.ID+"/private", `{}`, "me")
	if w := do(t, h, "POST", "/projects/"+q.ID+"/public", signOff, "me"); w.Code != http.StatusConflict {
		t.Errorf("republishing a non-clear project: status %d, want 409", w.Code)
	}
}

// TestWithdrawSurvivesForgeFailure covers 006o AC6: the forge is best-effort.
// A repository GitHub refuses to close, for example because of a fork network
// or an organization policy, must not fail the withdrawal. The project is
// already private in the app, and leaving the owner with a half-finished
// withdrawal would be worse than a repository left open.
func TestWithdrawSurvivesForgeFailure(t *testing.T) {
	pub := &fakeRepoPub{errOn: "r1"}
	d := testDeps(allowAll)
	d.RepoPublisher = pub
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "me", "Owned")
	_ = publishInto(t, d.Jobs, "me", p.ID, "r1")
	_ = do(t, h, "POST", "/projects/"+p.ID+"/public", signOff, "me")

	if w := do(t, h, "POST", "/projects/"+p.ID+"/private", `{}`, "me"); w.Code != http.StatusOK {
		t.Fatalf("withdrawal with a failing forge: status %d, want 200", w.Code)
	}
	if got, _ := d.Projects.Get(context.Background(), p.ID); got.Visibility != project.VisibilityPrivate {
		t.Errorf("the project must be private in-app even when the forge refuses")
	}
}

// TestWithdrawReasonIsCapped covers 006o: the reason is the owner's own record,
// not a published notice, so the cap only has to keep an oversized body out of
// the column. It truncates rather than refusing the withdrawal, which must
// never be blocked by its annotation.
func TestWithdrawReasonIsCapped(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "me", "Owned")
	_ = publishInto(t, d.Jobs, "me", p.ID, "r1")
	publishable(t, d, "me", "ada")
	_ = do(t, h, "POST", "/projects/"+p.ID+"/public", signOff, "me")

	long := strings.Repeat("x", maxWithdrawReason+500)
	if w := do(t, h, "POST", "/projects/"+p.ID+"/private", `{"reason":"`+long+`"}`, "me"); w.Code != http.StatusOK {
		t.Fatalf("withdrawal with an overlong reason: status %d, want 200", w.Code)
	}
	got, _ := d.Projects.Get(context.Background(), p.ID)
	if len(got.WithdrawnReason) != maxWithdrawReason {
		t.Errorf("reason length = %d, want it capped at %d", len(got.WithdrawnReason), maxWithdrawReason)
	}
	if got.Visibility != project.VisibilityPrivate {
		t.Errorf("an overlong reason must not block the withdrawal")
	}
}

// TestWithdrawClosesRepoOfAnUnclearProject covers 006o: the repository is
// closed even when the project's tree is no longer clear, which is often the
// reason it is being withdrawn. Sourcing the repositories from
// clearedForPublish would leave that repository open.
func TestWithdrawClosesRepoOfAnUnclearProject(t *testing.T) {
	pub := &fakeRepoPub{}
	d := testDeps(allowAll)
	d.RepoPublisher = pub
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "me", "Flagged")
	_ = publishInto(t, d.Jobs, "me", p.ID, "flagged") // CanPublish is false.

	if w := do(t, h, "POST", "/projects/"+p.ID+"/private", `{}`, "me"); w.Code != http.StatusOK {
		t.Fatalf("withdrawal: status %d, want 200", w.Code)
	}
	if len(pub.closed) != 1 || pub.closed[0] != "flagged" {
		t.Errorf("an unclear project's repository must still be closed, closed=%v", pub.closed)
	}
}

// TestPublicZone covers 006f AC8: a public project appears in the public zone
// to another signed-in user and opens; a private one never does.
func TestPublicZone(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	pub, _ := d.Projects.Create(context.Background(), "owner", "Public one")
	_ = publishInto(t, d.Jobs, "owner", pub.ID, "r1")
	publishable(t, d, "owner", "ada")
	_ = do(t, h, "POST", "/projects/"+pub.ID+"/public", signOff, "owner")
	_, _ = d.Projects.Create(context.Background(), "owner", "Private one")

	// The public zone lists only the public project, to a different user.
	w := do(t, h, "GET", "/public", "", "someone-else")
	var zone []ProjectView
	_ = json.Unmarshal(w.Body.Bytes(), &zone)
	if w.Code != http.StatusOK || len(zone) != 1 || zone[0].ID != pub.ID {
		t.Fatalf("public zone = %+v (status %d); want only the public project", zone, w.Code)
	}
	// The row names the publisher by the name they chose (006r) and withholds
	// the opaque subject behind that name (028). A stranger reads the work, not
	// the principal who owns it.
	if zone[0].OwnerName != "Ada Lovelace" || zone[0].OwnerHandle != "ada" {
		t.Errorf("public zone row does not name its publisher: %+v", zone[0])
	}
	if zone[0].Owner != "" {
		t.Errorf("public zone row carries the owning principal %q", zone[0].Owner)
	}
	// A different user can open the public project.
	if w := do(t, h, "GET", "/projects/"+pub.ID, "", "someone-else"); w.Code != http.StatusOK {
		t.Errorf("open public project: status %d, want 200", w.Code)
	}
}

// doAs sends a request carrying a full identity, sub and email, which the
// member routes need because the owner's email seeds the lead bridge.
func doAs(t *testing.T, h http.Handler, method, path, body string, id Identity) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req = req.WithContext(WithIdentity(req.Context(), id))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestProfileAPI covers 006n AC1 and AC2: the profile is the caller's own. It
// can be read before it exists, it is saved against the session's principal
// and never one named in the body, and it is refused field by field when an
// identifier is malformed.
func TestProfileAPI(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	// Before any save: the zero profile, not a 404.
	w := do(t, h, "GET", "/me/profile", "", "me")
	var got profile.Profile
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || got.DisplayName != "" || got.Contact != "" {
		t.Fatalf("profile before any save = %+v (status %d)", got, w.Code)
	}

	// A save is normalized and read back.
	body := `{"display_name":" Ada Lovelace ","institution":"LMU Munich","contact_email":"Ada@Example.ORG","orcid":"0000-0002-1825-0097","scholar_url":"https://scholar.google.com/citations?user=abc"}`
	if w := do(t, h, "PATCH", "/me/profile", body, "me"); w.Code != http.StatusOK {
		t.Fatalf("save: status %d (%s)", w.Code, w.Body.String())
	}
	w = do(t, h, "GET", "/me/profile", "", "me")
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.DisplayName != "Ada Lovelace" || got.Contact != "ada@example.org" || got.Institution != "LMU Munich" {
		t.Fatalf("saved profile = %+v", got)
	}

	// It is the caller's own profile: another principal sees their own, empty
	// one. A fresh value is used because every field is omitempty, and
	// unmarshaling `{}` over a populated struct would leave the previous
	// profile in place.
	got = profile.Profile{}
	w = do(t, h, "GET", "/me/profile", "", "someone-else")
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.DisplayName != "" {
		t.Errorf("another principal read this one's profile: %+v", got)
	}

	// Each malformed identifier is refused with its own code.
	for _, tc := range []struct{ body, code string }{
		{`{"contact_email":"not-an-email"}`, "invalid_contact"},
		{`{"orcid":"1234"}`, "invalid_orcid"},
		{`{"scholar_url":"https://example.com/me"}`, "invalid_scholar"},
	} {
		w := do(t, h, "PATCH", "/me/profile", tc.body, "me")
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.code) {
			t.Errorf("save %s: status %d body %s; want 400 %s", tc.body, w.Code, w.Body.String(), tc.code)
		}
	}
}

// TestPublicFlipRecordsTheSigner covers 006n AC3 and AC4: the flip requires a
// contact, validates the signer snapshot with the profile's own rules, records
// what was signed, and derives the signer's identity from the session, so a
// body cannot name somebody else as the signer.
func TestPublicFlipRecordsTheSigner(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "ada.lovelace@example.org"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Owned")
	_ = publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")
	publishable(t, d, owner.Sub, "ada")

	// No contact is refused. The requirement exists to stop a result being
	// published with no way to ask about it.
	w := doAs(t, h, "POST", "/projects/"+p.ID+"/public", `{"attestation_version":"v2"}`, owner)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "missing_contact") {
		t.Fatalf("flip with no contact: status %d body %s; want 400 missing_contact", w.Code, w.Body.String())
	}
	// A malformed identifier is refused on this path too, so the publish route is
	// not a way around the settings route's validation.
	for _, tc := range []struct{ body, code string }{
		{`{"attestation_version":"v2","signed_off_contact":"nope"}`, "invalid_contact"},
		{`{"attestation_version":"v2","signed_off_contact":"a@x.org","signed_off_orcid":"1234"}`, "invalid_orcid"},
		{`{"attestation_version":"v2","signed_off_contact":"a@x.org","signed_off_scholar":"https://evil.test"}`, "invalid_scholar"},
	} {
		w := doAs(t, h, "POST", "/projects/"+p.ID+"/public", tc.body, owner)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.code) {
			t.Errorf("flip %s: status %d body %s; want 400 %s", tc.body, w.Code, w.Body.String(), tc.code)
		}
	}
	if got, _ := d.Projects.Get(context.Background(), p.ID); got.Visibility != project.VisibilityPrivate {
		t.Fatalf("a refused flip made the project public: %+v", got)
	}

	// A valid sign-off records the snapshot, and the signer's email is the
	// session's, redacted, whatever the body named.
	good := `{"attestation_version":"v2","signed_off_name":"Ada Lovelace","signed_off_institution":"LMU Munich",
		"signed_off_contact":"Inquiries@Example.ORG","signed_off_orcid":"0000-0002-1825-0097",
		"signed_off_scholar":"https://scholar.google.com/citations?user=abc",
		"signed_off_email":"impostor@evil.test","signed_off_by":"somebody-else"}`
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/public", good, owner); w.Code != http.StatusOK {
		t.Fatalf("owner flip: status %d (%s)", w.Code, w.Body.String())
	}
	got, _ := d.Projects.Get(context.Background(), p.ID)
	if got.SignedOffName != "Ada Lovelace" || got.SignedOffInstitution != "LMU Munich" ||
		got.SignedOffContact != "inquiries@example.org" || got.SignedOffORCID != "0000-0002-1825-0097" ||
		got.SignedOffScholar != "https://scholar.google.com/citations?user=abc" {
		t.Fatalf("signer snapshot = %+v", got)
	}
	if got.SignedOffEmail != "a**********e@example.org" {
		t.Errorf("signed_off_email = %q; want the session's email, redacted", got.SignedOffEmail)
	}
	if got.SignedOffBy != owner.Sub {
		t.Errorf("signed_off_by = %q; want the session's principal, not the body's", got.SignedOffBy)
	}
}

// TestProfileEditDoesNotRewriteAPublishedSignOff covers 006n AC5: the sign-off
// is a snapshot, not a view. Editing the profile after publishing must not
// change what the published project reports. The design depends on this
// property.
func TestProfileEditDoesNotRewriteAPublishedSignOff(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "ada@example.org"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Owned")
	_ = publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")
	publishable(t, d, owner.Sub, "ada")

	if w := doAs(t, h, "PATCH", "/me/profile", `{"display_name":"Ada Lovelace","institution":"LMU Munich","contact_email":"ada@example.org"}`, owner); w.Code != http.StatusOK {
		t.Fatalf("save profile: %d", w.Code)
	}
	flip := `{"attestation_version":"v2","signed_off_name":"Ada Lovelace","signed_off_institution":"LMU Munich","signed_off_contact":"ada@example.org"}`
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/public", flip, owner); w.Code != http.StatusOK {
		t.Fatalf("flip: %d (%s)", w.Code, w.Body.String())
	}

	// Later, the owner has a new institution and a new address.
	if w := doAs(t, h, "PATCH", "/me/profile", `{"display_name":"Ada L.","institution":"Somewhere Else","contact_email":"ada@elsewhere.org"}`, owner); w.Code != http.StatusOK {
		t.Fatalf("second save: %d", w.Code)
	}
	got, _ := d.Projects.Get(context.Background(), p.ID)
	if got.SignedOffInstitution != "LMU Munich" || got.SignedOffContact != "ada@example.org" || got.SignedOffName != "Ada Lovelace" {
		t.Fatalf("a profile edit rewrote a published attestation: %+v", got)
	}
}

// TestV1SignOffHasNoSnapshot covers 006n AC6: a project published before this
// spec, with a v1 sign-off and no snapshot, reads back with empty signer fields
// rather than failing. The read path must tolerate it.
func TestV1SignOffHasNoSnapshot(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "owner", "Signed before 006n")
	if err := d.Projects.SetPublic(context.Background(), p.ID, project.SignOff{By: "owner", AttestationVersion: "v1"}); err != nil {
		t.Fatalf("seed a v1 sign-off: %v", err)
	}
	w := do(t, h, "GET", "/projects/"+p.ID, "", "someone-else")
	var view ProjectDetail
	_ = json.Unmarshal(w.Body.Bytes(), &view)
	if w.Code != http.StatusOK || view.Project.Visibility != project.VisibilityPublic {
		t.Fatalf("v1 public project: status %d, %+v", w.Code, view)
	}
	if view.Project.SignedOffName != "" || view.Project.SignedOffContact != "" || view.Project.SignedOffEmail != "" {
		t.Errorf("a v1 sign-off invented a snapshot: %+v", view)
	}
}

// TestMembersAPI covers 006f AC7: the members API is owner-only. The owner
// invites and removes by email; a non-owner is refused.
func TestMembersAPI(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Proj")

	// The owner invites a collaborator by email.
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"collab@x.com"}`, owner); w.Code != http.StatusNoContent {
		t.Fatalf("owner add: status %d, want 204", w.Code)
	}
	// It appears in the members list.
	w := doAs(t, h, "GET", "/projects/"+p.ID+"/members", "", owner)
	var members []access.Member
	_ = json.Unmarshal(w.Body.Bytes(), &members)
	found := false
	for _, m := range members {
		if m.Email == "collab@x.com" {
			found = true
		}
	}
	if w.Code != http.StatusOK || !found {
		t.Fatalf("members = %+v (status %d); want collab@x.com listed", members, w.Code)
	}

	// A stranger may neither list, add, nor remove, and is not told the
	// project exists.
	stranger := Identity{Sub: "stranger", Email: "s@x.com"}
	if w := doAs(t, h, "GET", "/projects/"+p.ID+"/members", "", stranger); w.Code != http.StatusNotFound {
		t.Errorf("stranger list: status %d, want 404", w.Code)
	}
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"x@x.com"}`, stranger); w.Code != http.StatusNotFound {
		t.Errorf("stranger add: status %d, want 404", w.Code)
	}

	// The owner removes the collaborator.
	if w := doAs(t, h, "DELETE", "/projects/"+p.ID+"/members/collab@x.com", "", owner); w.Code != http.StatusNoContent {
		t.Errorf("owner remove: status %d, want 204", w.Code)
	}
	after, _ := d.Members.List(context.Background(), p.ID)
	for _, m := range after {
		if m.Email == "collab@x.com" {
			t.Errorf("collaborator still present after remove")
		}
	}
}

// TestProjectAndSessionReadsAreScoped checks that a private project and every
// session under it are visible only to the owner and invited collaborators.
// A stranger gets a 404 everywhere, and a collaborator finds the project in
// their own project list.
func TestProjectAndSessionReadsAreScoped(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Private")
	sess := publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")

	view := func(id Identity, path string) int { return doAs(t, h, "GET", path, "", id).Code }

	// The owner sees the project, the session and the transcript.
	if view(owner, "/projects/"+p.ID) != http.StatusOK || view(owner, "/jobs/"+sess.ID) != http.StatusOK {
		t.Fatal("owner must see their own project and session")
	}

	// A stranger gets a 404 everywhere, including on the session-scoped reads
	// that used to be readable by any invited user.
	stranger := Identity{Sub: "stranger", Email: "s@x.com"}
	for _, path := range []string{"/projects/" + p.ID, "/jobs/" + sess.ID, "/jobs/" + sess.ID + "/transcript", "/jobs/" + sess.ID + "/events"} {
		if code := view(stranger, path); code != http.StatusNotFound {
			t.Errorf("stranger %s = %d, want 404 (hidden)", path, code)
		}
	}

	// A collaborator invited by the owner may view the private project and its
	// sessions, and finds it in their own project list (018).
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"collab@x.com"}`, owner); w.Code != http.StatusNoContent {
		t.Fatalf("invite: %d", w.Code)
	}
	collab := Identity{Sub: "collab-sub", Email: "collab@x.com"}
	if view(collab, "/projects/"+p.ID) != http.StatusOK || view(collab, "/jobs/"+sess.ID) != http.StatusOK {
		t.Error("a collaborator must see the private project and its sessions")
	}
	var shared []ProjectView
	_ = json.Unmarshal(doAs(t, h, "GET", "/projects", "", collab).Body.Bytes(), &shared)
	if len(shared) != 1 || shared[0].ID != p.ID {
		t.Errorf("collaborator /projects = %+v, want the one project", shared)
	}
	// A stranger's list is empty.
	var none []ProjectView
	_ = json.Unmarshal(doAs(t, h, "GET", "/projects", "", stranger).Body.Bytes(), &none)
	if len(none) != 0 {
		t.Errorf("stranger /projects = %+v, want empty", none)
	}
}

// TestProjectListMergesOwnedAndShared covers 018: the list is one list of the
// caller's own projects and the ones they were invited to, each named by its
// owner, newest-modified first. It counts a project's sessions the same way
// for both, whoever started them.
func TestProjectListMergesOwnedAndShared(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	ctx := context.Background()

	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	collab := Identity{Sub: "collab-sub", Email: "collab@x.com"}
	if _, err := d.Profiles.Save(ctx, profile.Profile{
		Principal: owner.Sub, DisplayName: "Ada Lovelace", Handle: "ada", Public: true,
	}); err != nil {
		t.Fatalf("save owner profile: %v", err)
	}

	theirs, _ := d.Projects.Create(ctx, owner.Sub, "Theirs")
	if w := doAs(t, h, "POST", "/projects/"+theirs.ID+"/members", `{"email":"collab@x.com"}`, owner); w.Code != http.StatusNoContent {
		t.Fatalf("invite: %d", w.Code)
	}
	// A membership counts once it is bound, which login does.
	if _, err := d.Members.(access.Membership).BindAll(ctx, access.Principal{Sub: collab.Sub, Email: collab.Email}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	mine, _ := d.Projects.Create(ctx, collab.Sub, "Mine")
	// A session the collaborator ran inside the owner's project: it belongs to the
	// project, so both of them must be told the same count.
	if _, err := d.Jobs.Enqueue(ctx, job.Submission{
		Principal: collab.Sub, Ref: "r1", Kind: job.KindAudit, Project: theirs.ID, PaperKey: "arxiv:1",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	list := func(id Identity) []ProjectView {
		var out []ProjectView
		w := doAs(t, h, "GET", "/projects", "", id)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /projects as %s = %d", id.Sub, w.Code)
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}

	got := list(collab)
	if len(got) != 2 {
		t.Fatalf("collaborator list = %d projects, want 2 (their own and the shared one)", len(got))
	}
	byID := map[string]ProjectView{}
	for _, v := range got {
		byID[v.ID] = v
	}
	// Newest-modified first: the shared project's session was enqueued last.
	if got[0].ID != theirs.ID {
		t.Errorf("first row = %q, want the just-worked-in project %q", got[0].Title, theirs.Title)
	}
	if byID[theirs.ID].SessionCount != 1 || byID[mine.ID].SessionCount != 0 {
		t.Errorf("session counts = %d/%d, want 1/0", byID[theirs.ID].SessionCount, byID[mine.ID].SessionCount)
	}
	if n := byID[theirs.ID].ModifiedAt; !n.After(theirs.CreatedAt) {
		t.Errorf("modified_at = %v, want later than created_at %v", n, theirs.CreatedAt)
	}
	// The owner is named through their profile, and never by their address.
	if v := byID[theirs.ID]; v.OwnerName != "Ada Lovelace" || v.OwnerHandle != "ada" {
		t.Errorf("owner = %q/%q, want Ada Lovelace/ada", v.OwnerName, v.OwnerHandle)
	}
	if body := doAs(t, h, "GET", "/projects", "", collab).Body.String(); strings.Contains(body, owner.Email) {
		t.Errorf("the list carries the owner's address: %s", body)
	}
	// The owner reads the same count for the session their collaborator ran.
	for _, v := range list(owner) {
		if v.ID == theirs.ID && v.SessionCount != 1 {
			t.Errorf("owner reads session_count %d, want the same 1 the collaborator reads", v.SessionCount)
		}
	}
	// The old split surface is gone rather than left beside the merged one.
	if w := doAs(t, h, "GET", "/shared", "", collab); w.Code != http.StatusNotFound {
		t.Errorf("GET /shared = %d, want 404", w.Code)
	}
}

// storeCanceler cancels through the mem store, as the real worker does.
type storeCanceler struct{ s *job.MemStore }

func (c storeCanceler) Cancel(ctx context.Context, id string) error {
	_, err := c.s.Cancel(ctx, id)
	return err
}

// TestCancelSession covers 006g: only the owner may stop a running session,
// and a stopped session ends failed/cancelled.
func TestCancelSession(t *testing.T) {
	d := testDeps(allowAll)
	jobs := d.Jobs.(*job.MemStore)
	d.Canceler = storeCanceler{jobs}
	ctx := context.Background()
	p, _ := d.Projects.Create(ctx, "me", "P")
	j, _ := jobs.Enqueue(ctx, job.Submission{Principal: "me", Ref: "ref", Kind: job.KindAudit, Project: p.ID, PaperKey: "arxiv:1"})
	_, _, _ = jobs.Claim(ctx, "test-worker") // Now resolving, which is non-terminal.
	h := NewRouter(d).Handler()

	// A stranger cannot stop it, and is answered as if it did not exist.
	if w := do(t, h, "POST", "/jobs/"+j.ID+"/cancel", "", "stranger"); w.Code != http.StatusNotFound {
		t.Fatalf("stranger cancel: %d, want 404", w.Code)
	}
	// The owner stops it; it ends failed/cancelled.
	if w := do(t, h, "POST", "/jobs/"+j.ID+"/cancel", "", "me"); w.Code != http.StatusOK {
		t.Fatalf("owner cancel: %d, want 200", w.Code)
	}
	if got, _ := jobs.Get(ctx, j.ID); got.Phase != job.PhaseFailed || got.Err != job.CancelCause {
		t.Errorf("cancelled session = %s/%q, want failed/cancelled", got.Phase, got.Err)
	}
}

// publishInto adds a session to a project and points it at a published repo.
func publishInto(t *testing.T, s JobStore, sub, projectID, repo string) job.Job {
	t.Helper()
	ctx := context.Background()
	j, err := s.Enqueue(ctx, job.Submission{Principal: sub, Ref: "ref", Kind: job.KindAudit, Project: projectID, PaperKey: "arxiv:" + repo})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	adv := s.(*job.MemStore)
	for _, p := range []job.Phase{job.PhaseResolving, job.PhaseRunning, job.PhasePublishing, job.PhaseDone} {
		phase := p
		if err := adv.Advance(ctx, j.ID, phase, func(jb *job.Job) {
			if phase == job.PhasePublishing {
				jb.RepoName = "open-hai/" + repo
			}
		}); err != nil {
			t.Fatalf("advance %s: %v", phase, err)
		}
	}
	got, _ := s.Get(ctx, j.ID)
	return got
}

func TestGetJob(t *testing.T) {
	d := testDeps(allowAll)
	p, _ := d.Projects.Create(context.Background(), "sub", "t")
	j, _ := d.Jobs.Enqueue(context.Background(), job.Submission{Principal: "sub", Ref: "ref", Kind: job.KindAudit, Project: p.ID})
	h := NewRouter(d).Handler()

	if w := do(t, h, "GET", "/jobs/"+j.ID, "", "sub"); w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	// Another user cannot read a session in a private project; it is hidden as
	// a 404.
	if w := do(t, h, "GET", "/jobs/"+j.ID, "", "stranger"); w.Code != http.StatusNotFound {
		t.Errorf("stranger job read: status %d, want 404", w.Code)
	}
	if w := do(t, h, "GET", "/jobs/missing", "", "sub"); w.Code != http.StatusNotFound {
		t.Errorf("missing job: status %d, want 404", w.Code)
	}
}

// publishedSession opens a real project owned by sub and drives a session in it
// to publishing under a repo, so the session's scoped reads resolve and its
// transcript comes back through the fake forge.
func publishedSession(t *testing.T, d Deps, sub, repo string, usage *provider.Usage) job.Job {
	t.Helper()
	ctx := context.Background()
	p, err := d.Projects.Create(ctx, sub, "t")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	j, err := d.Jobs.Enqueue(ctx, job.Submission{Principal: sub, Ref: "ref", Kind: job.KindAudit, Project: p.ID, PaperKey: "arxiv:1"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	adv := d.Jobs.(*job.MemStore)
	// A published session has finished: it reaches done with a committed repo. It
	// must be terminal, so the transcript reader takes the committed-repo path
	// rather than the live one (006i).
	for _, p := range []job.Phase{job.PhaseResolving, job.PhaseRunning, job.PhasePublishing, job.PhaseDone} {
		phase := p
		if err := adv.Advance(ctx, j.ID, phase, func(jb *job.Job) {
			if phase == job.PhasePublishing {
				jb.RepoName = "open-hai/" + repo
			}
		}); err != nil {
			t.Fatalf("advance %s: %v", phase, err)
		}
	}
	if usage != nil {
		if err := adv.Observe(ctx, j.ID, provider.RunStatus{Usage: *usage}); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}
	got, _ := d.Jobs.Get(ctx, j.ID)
	return got
}

// TestSessionTranscript covers 006h AC1 to AC4: a session's transcript is
// served as parsed events plus usage, with not_ready and not_found kept
// distinct. Recorded usage takes precedence over derived usage.
func TestSessionTranscript(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", &provider.Usage{InputTokens: 100, OutputTokens: 50})

	w := do(t, h, "GET", "/jobs/"+j.ID+"/transcript", "", "me")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var tr Transcript
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Four events served, raw stripped, tool call paired with its result.
	if len(tr.Events) != 4 || tr.Events[1].Kind != "tool_use" || tr.Events[1].Tool != "bash" || tr.Events[2].Kind != "tool_result" {
		t.Fatalf("events = %+v", tr.Events)
	}
	if len(tr.Events[3].Raw) != 0 {
		t.Errorf("raw was not stripped from the response")
	}
	// Recorded usage is used verbatim, not the transcript-derived total.
	if tr.Usage == nil || tr.Usage.Total() != 150 {
		t.Fatalf("usage = %+v, want recorded total 150", tr.Usage)
	}
}

// TestSessionTranscriptUsageDerived covers 006h AC3: a backfilled session with
// no recorded usage derives it from the transcript's per-request usage, so
// the figure is not blank on real data.
func TestSessionTranscriptUsageDerived(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", nil) // No usage observed.

	w := do(t, h, "GET", "/jobs/"+j.ID+"/transcript", "", "me")
	var tr Transcript
	_ = json.Unmarshal(w.Body.Bytes(), &tr)
	if w.Code != http.StatusOK || tr.Usage == nil || tr.Usage.Total() != 160 {
		t.Fatalf("status %d usage %+v; want derived total 160 (100+50+10)", w.Code, tr.Usage)
	}
}

// TestSessionTranscriptPerEventUsage covers 006s AC1 and AC2: every event
// carries the usage the backend charged against it, an event that was charged
// nothing carries none, and the per-event figures sum to exactly the session
// total the same response reports. The two are derived in one pass so they
// cannot drift apart.
func TestSessionTranscriptPerEventUsage(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "multi", nil) // Nothing recorded, so the total is derived.

	w := do(t, h, "GET", "/jobs/"+j.ID+"/transcript", "", "me")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var tr Transcript
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tr.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(tr.Events))
	}
	// The charged events carry their own figures, not the session's.
	if u := tr.Events[0].Usage; u == nil || u.InputTokens != 100 || u.OutputTokens != 50 || u.CachedInputTokens != 10 {
		t.Errorf("event 0 usage = %+v, want 100/50/10", u)
	}
	if u := tr.Events[2].Usage; u == nil || u.InputTokens != 7 || u.OutputTokens != 3 || u.CachedInputTokens != 1 {
		t.Errorf("event 2 usage = %+v, want 7/3/1", u)
	}
	// An uncharged event carries no usage, never a zero. An absent number
	// records that the event was not charged.
	if tr.Events[1].Usage != nil {
		t.Errorf("uncharged event carries usage %+v, want none", tr.Events[1].Usage)
	}
	// The per-event figures sum to the total the same response reports.
	var sum int64
	for _, e := range tr.Events {
		if e.Usage != nil {
			sum += e.Usage.Total()
		}
	}
	if tr.Usage == nil || sum != tr.Usage.Total() {
		t.Fatalf("per-event sum %d != session total %+v", sum, tr.Usage)
	}
	if sum != 171 {
		t.Errorf("sum = %d, want 171 (110 + 11)", sum)
	}
}

// TestSessionTranscriptPerEventUsageLive covers 006s AC1: per-event usage is
// not specific to the committed path. An in-flight session's live events
// carry it too, since both paths are built by the same provider translation.
func TestSessionTranscriptPerEventUsageLive(t *testing.T) {
	d := testDeps(allowAll)
	store := job.NewMemStore()
	d.Jobs = store
	d.Live = &fakeLive{events: []provider.Event{
		{Kind: "message", Content: "no charge"},
		{Kind: "model_request_end", Usage: &provider.Usage{InputTokens: 4, OutputTokens: 2}},
	}}
	p, _ := d.Projects.Create(context.Background(), "me", "t")
	j, _ := store.Enqueue(context.Background(), job.Submission{Principal: "me", Ref: "ref", Kind: job.KindAudit, Project: p.ID, PaperKey: "arxiv:1"})
	_, _, _ = store.Claim(context.Background(), "test-worker")
	_ = store.Advance(context.Background(), j.ID, job.PhaseRunning, nil)
	_ = store.Observe(context.Background(), j.ID, provider.RunStatus{RunID: "sesn_live"})

	w := do(t, NewRouter(d).Handler(), "GET", "/jobs/"+j.ID+"/transcript", "", "me")
	var tr Transcript
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tr.Events) != 2 || tr.Events[0].Usage != nil {
		t.Fatalf("events = %+v", tr.Events)
	}
	if u := tr.Events[1].Usage; u == nil || u.Total() != 6 {
		t.Fatalf("live event usage = %+v, want total 6", u)
	}
	// Raw is still dropped from the response, since it is the bulk of a
	// transcript.
	if len(tr.Events[1].Raw) != 0 {
		t.Error("raw was not dropped from a live event")
	}
}

// TestSessionArtifactsAndReproduction covers 006f: a published session serves
// its repository file tree, individual files, and its manifest verdict. These
// are forge pass-throughs, scoped like every session read, so a stranger gets
// a 404.
func TestSessionArtifactsAndReproduction(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", nil)

	// File tree, sorted.
	w := do(t, h, "GET", "/jobs/"+j.ID+"/artifacts", "", "me")
	var tree struct{ Files []string }
	_ = json.Unmarshal(w.Body.Bytes(), &tree)
	if w.Code != http.StatusOK || len(tree.Files) != 3 || tree.Files[0] != "README.md" {
		t.Fatalf("artifacts = %d %+v; want sorted 3 files", w.Code, tree.Files)
	}
	// One file's contents, as text.
	w = do(t, h, "GET", "/jobs/"+j.ID+"/artifact?path=README.md", "", "me")
	if w.Code != http.StatusOK || w.Body.String() != "content of README.md" {
		t.Errorf("artifact = %d %q", w.Code, w.Body.String())
	}
	// A missing file is 404; a missing path param is 400.
	if w := do(t, h, "GET", "/jobs/"+j.ID+"/artifact?path=missing", "", "me"); w.Code != http.StatusNotFound {
		t.Errorf("missing artifact: %d, want 404", w.Code)
	}
	if w := do(t, h, "GET", "/jobs/"+j.ID+"/artifact", "", "me"); w.Code != http.StatusBadRequest {
		t.Errorf("no path: %d, want 400", w.Code)
	}
	// The manifest verdict.
	w = do(t, h, "GET", "/jobs/"+j.ID+"/reproduction", "", "me")
	var repro Reproduction
	_ = json.Unmarshal(w.Body.Bytes(), &repro)
	if w.Code != http.StatusOK || repro.Verdict != "passed" {
		t.Errorf("reproduction = %d %+v", w.Code, repro)
	}
	// A stranger cannot reach any of it. The artifacts stay behind the same
	// project scope as everything else, which is the reason for the in-app
	// pass-through.
	for _, p := range []string{"/artifacts", "/artifact?path=README.md", "/reproduction"} {
		if w := do(t, h, "GET", "/jobs/"+j.ID+p, "", "stranger"); w.Code != http.StatusNotFound {
			t.Errorf("stranger %s = %d, want 404", p, w.Code)
		}
	}
}

// TestSessionTranscriptUsageNotRecorded checks that a session whose usage is
// neither recorded nor in the transcript reports nil usage, never a fabricated
// zero.
func TestSessionTranscriptUsageNotRecorded(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "bare", nil) // No usage anywhere.

	w := do(t, h, "GET", "/jobs/"+j.ID+"/transcript", "", "me")
	var tr Transcript
	_ = json.Unmarshal(w.Body.Bytes(), &tr)
	if w.Code != http.StatusOK || tr.Usage != nil {
		t.Fatalf("status %d usage %+v; want 200 and nil usage", w.Code, tr.Usage)
	}
}

// TestSessionTranscriptNotReadyVsMissing covers 006h AC4: a session that has
// not published a transcript is a 409 not_ready, distinct from the 404 for a
// session that does not exist.
func TestSessionTranscriptNotReadyVsMissing(t *testing.T) {
	d := testDeps(allowAll)
	// A submitted session in an owned project, with no repository yet.
	p, _ := d.Projects.Create(context.Background(), "me", "t")
	j, _ := d.Jobs.Enqueue(context.Background(), job.Submission{Principal: "me", Ref: "ref", Kind: job.KindAudit, Project: p.ID, PaperKey: "arxiv:1"})
	h := NewRouter(d).Handler()

	if w := do(t, h, "GET", "/jobs/"+j.ID+"/transcript", "", "me"); w.Code != http.StatusConflict {
		t.Errorf("not-ready: status %d, want 409", w.Code)
	}
	if w := do(t, h, "GET", "/jobs/missing/transcript", "", "me"); w.Code != http.StatusNotFound {
		t.Errorf("missing session: status %d, want 404", w.Code)
	}
}

// fakeLive is a stand-in backend live-event stream keyed by run id.
type fakeLive struct {
	events []provider.Event
	err    error
}

func (f *fakeLive) Events(context.Context, provider.RunStatus) ([]provider.Event, error) {
	return f.events, f.err
}

// TestSessionTranscriptLive covers 006h: a session still in flight, with a run
// id observed and no repository yet, serves the backend's live event stream,
// flagged live so the client keeps polling, rather than the 409 not_ready a
// session with no live run gets.
func TestSessionTranscriptLive(t *testing.T) {
	d := testDeps(allowAll)
	store := job.NewMemStore()
	d.Jobs = store
	d.Live = &fakeLive{events: []provider.Event{{Kind: "message", Content: "thinking"}}}
	p, _ := d.Projects.Create(context.Background(), "me", "t")
	j, _ := store.Enqueue(context.Background(), job.Submission{Principal: "me", Ref: "ref", Kind: job.KindAudit, Project: p.ID, PaperKey: "arxiv:1"})
	// Reach running with an observed backend run id, which is the in-flight
	// state.
	_, _, _ = store.Claim(context.Background(), "test-worker")
	_ = store.Advance(context.Background(), j.ID, job.PhaseRunning, nil)
	_ = store.Observe(context.Background(), j.ID, provider.RunStatus{RunID: "sesn_live"})
	h := NewRouter(d).Handler()

	w := do(t, h, "GET", "/jobs/"+j.ID+"/transcript", "", "me")
	if w.Code != http.StatusOK {
		t.Fatalf("live transcript status = %d, want 200", w.Code)
	}
	var tr Transcript
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !tr.Live {
		t.Error("an in-flight run's transcript must be flagged live")
	}
	if len(tr.Events) != 1 || tr.Events[0].Content != "thinking" {
		t.Errorf("live events = %+v, want the backend's stream", tr.Events)
	}
}

// TestSessionTranscriptLiveEvenWithRepo is a 006i regression test: a
// still-running session whose repository was created up front for the live
// mount, so repo_name is set, must still read the live stream. Its committed
// transcript does not exist yet, so reading the repository would 404. The
// non-terminal phase takes precedence over the presence of a repository.
func TestSessionTranscriptLiveEvenWithRepo(t *testing.T) {
	d := testDeps(allowAll)
	store := job.NewMemStore()
	d.Jobs = store
	// The committed-repository reader would answer, wrongly, if that branch
	// were taken.
	d.Reproductions = &fakeRepro{}
	d.Live = &fakeLive{events: []provider.Event{{Kind: "message", Content: "live-not-committed"}}}
	p, _ := d.Projects.Create(context.Background(), "me", "t")
	j, _ := store.Enqueue(context.Background(), job.Submission{Principal: "me", Ref: "ref", Kind: job.KindAudit, Project: p.ID, PaperKey: "arxiv:1"})
	_, _, _ = store.Claim(context.Background(), "test-worker")
	// Running, with a repository created in resolve and a run id observed,
	// which is the 006i state.
	_ = store.Advance(context.Background(), j.ID, job.PhaseRunning, func(jb *job.Job) { jb.RepoName = "open-hai/r1" })
	_ = store.Observe(context.Background(), j.ID, provider.RunStatus{RunID: "sesn_live"})
	h := NewRouter(d).Handler()

	w := do(t, h, "GET", "/jobs/"+j.ID+"/transcript", "", "me")
	if w.Code != http.StatusOK {
		t.Fatalf("running-with-repo transcript status = %d, want 200 (live)", w.Code)
	}
	var tr Transcript
	_ = json.Unmarshal(w.Body.Bytes(), &tr)
	if !tr.Live || len(tr.Events) != 1 || tr.Events[0].Content != "live-not-committed" {
		t.Errorf("running-with-repo served %+v, want the LIVE stream flagged live", tr)
	}
}

// TestListMyJobs checks that GET /jobs lists only the caller's own jobs, the
// ones they submitted. Another principal's jobs never appear, even through
// the same handler.
func TestListMyJobs(t *testing.T) {
	d := testDeps(allowAll)
	mine, _ := d.Jobs.Enqueue(context.Background(), job.Submission{Principal: "me", Ref: "ref-mine", Kind: job.KindAudit})
	_, _ = d.Jobs.Enqueue(context.Background(), job.Submission{Principal: "other", Ref: "ref-other", Kind: job.KindAudit})
	h := NewRouter(d).Handler()

	w := do(t, h, "GET", "/jobs", "", "me")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got []job.Job
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("listed %d jobs %+v, want only my job %s", len(got), got, mine.ID)
	}
	// A caller with no jobs of their own, while other principals' jobs exist,
	// gets an empty JSON array, never null.
	w = do(t, h, "GET", "/jobs", "", "nobody")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("empty list = %d %q, want 200 []", w.Code, w.Body.String())
	}
}

// TestSessionTranscriptForgeFailureIs500 covers 006h AC5 and the 003
// read-error rule: a forge or transport failure on the session transcript is
// a 500, never disguised as a 404.
func TestSessionTranscriptForgeFailureIs500(t *testing.T) {
	d := testDeps(allowAll)
	d.Reproductions = &fakeRepro{transcriptErr: errors.New("forge timeout")}
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", nil)

	if w := do(t, h, "GET", "/jobs/"+j.ID+"/transcript", "", "me"); w.Code != http.StatusInternalServerError {
		t.Errorf("a forge failure should be 500, got %d", w.Code)
	}
}

// TestGateIsFailClosedAndEnumerable covers AC4: with the fail-closed gate,
// every gated route answers not_granted, and the health check outside the
// gate does not. The gated set is enumerated from the router itself.
func TestGateIsFailClosedAndEnumerable(t *testing.T) {
	rt := NewRouter(Deps{Jobs: job.NewMemStore(), Reproductions: &fakeRepro{}}) // A nil gate means DenyAll.
	h := rt.Handler()

	gated := rt.GatedRoutes()
	if len(gated) == 0 {
		t.Fatal("no gated routes enumerated")
	}
	for _, route := range gated {
		// A path with the {id} wildcard filled in.
		path := strings.ReplaceAll(route.Pattern, "{id}", "x")
		w := do(t, h, route.Method, path, `{"url":"u"}`, "authed-but-ungranted")
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "not_granted") {
			t.Errorf("%s %s: status %d body %s, want 403 not_granted", route.Method, path, w.Code, w.Body.String())
		}
	}
	// The open health check is reachable without a grant.
	if w := do(t, h, "GET", "/healthz", "", ""); w.Code != http.StatusOK {
		t.Errorf("healthz should be open, got %d", w.Code)
	}
	// The gated and open sets are disjoint.
	for _, g := range rt.GatedRoutes() {
		for _, o := range rt.OpenRoutes() {
			if g == o {
				t.Errorf("route %v is in both the gated and open sets", g)
			}
		}
	}
}

// TestNoEngineImports covers AC5: no handler contains engine logic. The
// package imports neither internal/method nor a provider adapter, which is
// asserted on the dependency graph.
func TestNoEngineImports(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/latere-ai/replichai/internal/httpapi").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	forbidden := []string{
		"github.com/latere-ai/replichai/internal/method",
		"github.com/latere-ai/replichai/internal/provider/anthropic",
	}
	for dep := range strings.FieldsSeq(string(out)) {
		for _, bad := range forbidden {
			if dep == bad {
				t.Errorf("httpapi imports %s — a handler must contain no engine logic", bad)
			}
		}
	}
}

// TestReadingPapersFollowsTheProjectsVisibility checks that papers follow the
// project's visibility, like everything else under it. A public project's
// papers are readable by anyone signed in, a private project's are not, and a
// project the caller cannot see reads as missing rather than forbidden, which
// is the rule every other read path follows.
func TestReadingPapersFollowsTheProjectsVisibility(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	_, projectID := createWithPaper(t, h, "https://arxiv.org/abs/2302.03714", "owner")

	// While private, a stranger may not read it and may not work in it either.
	// It reads as missing rather than forbidden: since 028 the route is open,
	// and a 403 would confirm to anybody enumerating ids that the project
	// exists.
	if w := do(t, h, "GET", "/projects/"+projectID+"/papers", "", "stranger"); w.Code != http.StatusNotFound {
		t.Errorf("stranger reads a private project's papers: status %d, want 404", w.Code)
	}
	if w := do(t, h, "GET", "/projects/nope/papers", "", "owner"); w.Code != http.StatusNotFound {
		t.Errorf("papers of a missing project: status %d, want 404", w.Code)
	}

	// Once public, it is readable by anyone signed in, but reading is not
	// working in the project, so a stranger still cannot add a paper or spend
	// the project's credits.
	if err := d.Projects.(project.Store).SetPublic(context.Background(), projectID,
		project.SignOff{By: "owner", AttestationVersion: "v2"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if w := do(t, h, "GET", "/projects/"+projectID+"/papers", "", "stranger"); w.Code != http.StatusOK {
		t.Errorf("stranger reads a public project's papers: status %d, want 200", w.Code)
	}
	if w := do(t, h, "POST", "/projects/"+projectID+"/papers", `{"url":"https://arxiv.org/abs/2401.00001"}`, "stranger"); w.Code != http.StatusForbidden {
		t.Errorf("stranger adds to a public project: status %d, want 403", w.Code)
	}
	if w, _ := startPaper(t, h, projectID, "arxiv:2302.03714", "stranger"); w.Code != http.StatusForbidden {
		t.Errorf("stranger starts in a public project: status %d, want 403", w.Code)
	}
}

// The findings route (021a).

// validFindings is a verdict.json that passes the schema, so each case below
// changes exactly one thing about it.
func validFindings(t *testing.T) []byte {
	t.Helper()
	v := verdict.Verdict{
		Schema:     verdict.Schema,
		Verdict:    verdict.Partial,
		Summary:    "Retrains and matches within 1.8 pp.",
		GradedAt:   time.Date(2026, 7, 14, 10, 41, 0, 0, time.UTC),
		Components: verdict.Components{Verified: 1, Blocked: 1, Total: 2},
		Boundary: []verdict.Claim{
			{Claim: "Accuracy 91.4%", Loop: verdict.Inner, Outcome: verdict.Verified, Citation: "§6.1", Evidence: "89.6% retrained",
				Mismatches: []verdict.Mismatch{{Kind: verdict.Numeric, Reported: "91.4%", Observed: "89.6%", Note: "-1.8 pp"}}},
			{Claim: "Latency under 40 ms", Loop: verdict.Inner, Outcome: verdict.Blocked, Citation: "§6.3", Evidence: "needs the authors' tablet"},
			{Claim: "Preferred by participants", Loop: verdict.Outer, Outcome: verdict.NotScored, Citation: "§7", Evidence: "human study"},
		},
		Decisions: []verdict.Decision{{Question: "Window length?", Where: "§4", Assumed: "250 ms", Sensitivity: "±0.4 pp"}},
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestSessionFindings(t *testing.T) {
	d := testDeps(allowAll)
	d.Reproductions.(*fakeRepro).findings = validFindings(t)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", nil)

	w := do(t, h, "GET", "/jobs/"+j.ID+"/verdict", "", "me")
	if w.Code != http.StatusOK {
		t.Fatalf("findings = %d %s", w.Code, w.Body.String())
	}
	var got verdict.Verdict
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Verdict != verdict.Partial || len(got.Boundary) != 3 {
		t.Fatalf("findings = %+v", got)
	}
	// The mismatch survives the round trip: it is the one field a reader cannot
	// reconstruct from the outcome.
	if len(got.Boundary[0].Mismatches) != 1 || got.Boundary[0].Mismatches[0].Observed != "89.6%" {
		t.Errorf("mismatch lost: %+v", got.Boundary[0])
	}
	// Scoped like every other session read.
	if w := do(t, h, "GET", "/jobs/"+j.ID+"/verdict", "", "stranger"); w.Code != http.StatusNotFound {
		t.Errorf("stranger = %d, want 404", w.Code)
	}
}

// TestSessionFindingsAbsent checks that a run that never wrote the file is a
// 404 with a reason, not an empty object that a pane would render as a paper
// with no boundary.
func TestSessionFindingsAbsent(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", nil)

	w := do(t, h, "GET", "/jobs/"+j.ID+"/verdict", "", "me")
	if w.Code != http.StatusNotFound {
		t.Fatalf("absent findings = %d, want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "no_findings") {
		t.Errorf("body = %q, want a reason", w.Body.String())
	}
}

// TestSessionFindingsInvalid checks that an invalid file is refused on read
// exactly as finalize refused it, and that the specific problems are returned
// so the pane can say which rule it broke.
func TestSessionFindingsInvalid(t *testing.T) {
	d := testDeps(allowAll)
	var v verdict.Verdict
	_ = json.Unmarshal(validFindings(t), &v)
	v.Boundary[2].Outcome = verdict.Verified // An outer-loop claim that was scored.
	raw, _ := json.Marshal(v)
	d.Reproductions.(*fakeRepro).findings = raw
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", nil)

	w := do(t, h, "GET", "/jobs/"+j.ID+"/verdict", "", "me")
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid findings = %d, want 422", w.Code)
	}
	if !strings.Contains(w.Body.String(), "boundary[2].outcome") {
		t.Errorf("body does not name the offending field: %s", w.Body.String())
	}
}

// TestSessionFindingsNotJSON checks that prose in the file is a 404, not a 500
// and not a panic.
func TestSessionFindingsNotJSON(t *testing.T) {
	d := testDeps(allowAll)
	d.Reproductions.(*fakeRepro).findings = []byte("# Reproducibility\n")
	h := NewRouter(d).Handler()
	j := publishedSession(t, d, "me", "r1", nil)

	if w := do(t, h, "GET", "/jobs/"+j.ID+"/verdict", "", "me"); w.Code != http.StatusNotFound {
		t.Fatalf("markdown findings = %d, want 404", w.Code)
	}
}

// TestSessionFindingsNotReady checks that a session that has not begun
// publishing has no repository to read from.
func TestSessionFindingsNotReady(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	ctx := context.Background()
	p, err := d.Projects.Create(ctx, "me", "t")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	j, err := d.Jobs.Enqueue(ctx, job.Submission{Principal: "me", Ref: "ref", Kind: job.KindAudit, Project: p.ID, PaperKey: "arxiv:1"})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if w := do(t, h, "GET", "/jobs/"+j.ID+"/verdict", "", "me"); w.Code != http.StatusConflict {
		t.Fatalf("unpublished session = %d, want 409", w.Code)
	}
}
