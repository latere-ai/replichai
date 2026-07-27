package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
)

// allowAll is a permissive gate for testing the handlers behind the gate.
var allowAll = GateFunc(func(*http.Request) (bool, string) { return true, "" })

func testDeps(gate Gate) Deps {
	return Deps{
		Jobs:          job.NewMemStore(),
		Projects:      project.NewMemStore(),
		Members:       access.NewMemMembership(),
		Reproductions: &fakeRepro{},
		Gate:          gate,
	}
}

type fakeRepro struct{ err, getErr error }

func (f *fakeRepro) List(context.Context) ([]Reproduction, error) {
	return []Reproduction{{ID: "r1", Repo: "open-hai/x", Verdict: "passed"}}, f.err
}
func (f *fakeRepro) Get(_ context.Context, id string) (Reproduction, error) {
	if f.getErr != nil {
		return Reproduction{}, f.getErr
	}
	switch id {
	case "r1":
		return Reproduction{ID: "r1", Repo: "open-hai/r1", Verdict: "passed", CanPublish: true}, nil
	case "flagged":
		// A reproduction whose tree flagged third-party data — barred from public.
		return Reproduction{ID: "flagged", Repo: "open-hai/flagged", CanPublish: false}, nil
	default:
		return Reproduction{}, ErrReproNotFound
	}
}
func (f *fakeRepro) Transcript(_ context.Context, id string) ([]byte, error) {
	switch id {
	case "r1":
		// A small valid transcript.jsonl: a message, a tool call, its result, and a
		// model-request event carrying per-request usage. The `"event":1` marker
		// keeps the raw /reproductions passthrough test meaningful.
		return []byte(`{"role":"agent","kind":"message","content":"start","event":1}` + "\n" +
			`{"role":"agent","kind":"tool_use","tool":"bash","content":"ls"}` + "\n" +
			`{"role":"user","kind":"tool_result","content":"file.txt"}` + "\n" +
			`{"role":"system","kind":"model_request_end","raw":{"model_usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":10}}}` + "\n"), nil
	case "bare":
		// A transcript with no per-request usage anywhere — usage is unknowable.
		return []byte(`{"role":"agent","kind":"message","content":"hi"}` + "\n"), nil
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

// submitResult is the shape POST /papers returns.
type submitResult struct {
	JobID     string `json:"job_id"`
	ProjectID string `json:"project_id"`
}

func submit(t *testing.T, h http.Handler, body, sub string) (*httptest.ResponseRecorder, submitResult) {
	t.Helper()
	w := do(t, h, "POST", "/papers", body, sub)
	var got submitResult
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	return w, got
}

// AC2: a bare submit (no project) opens a fresh project and its first session,
// returning both ids without blocking on the run.
func TestSubmitBareOpensAProjectAndSession(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	w, got := submit(t, h, `{"url":"https://arxiv.org/abs/2302.03714"}`, "sub-1")
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	if got.JobID == "" || got.ProjectID == "" {
		t.Fatalf("missing ids: %+v", got)
	}
	// The project exists, owned by the caller; the session persisted, submitted,
	// under that project — the run has not started blocking anything.
	p, err := d.Projects.Get(context.Background(), got.ProjectID)
	if err != nil || p.Owner != "sub-1" {
		t.Fatalf("project = %+v, err %v", p, err)
	}
	j, err := d.Jobs.Get(context.Background(), got.JobID)
	if err != nil || j.Phase != job.PhaseSubmitted || j.Principal != "sub-1" || j.ProjectID != got.ProjectID {
		t.Errorf("session = %+v, err %v", j, err)
	}
	if j.PaperKey != "arxiv:2302.03714" {
		t.Errorf("session paper_key = %q, want arxiv:2302.03714", j.PaperKey)
	}
}

// AC2: submitting with an owned project adds a second session to it.
func TestSubmitToOwnedProjectAddsASession(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "sub-1", "My project")

	w, got := submit(t, h, `{"url":"https://arxiv.org/abs/2302.03714","project":"`+p.ID+`"}`, "sub-1")
	if w.Code != http.StatusAccepted || got.ProjectID != p.ID {
		t.Fatalf("status %d, project %q; want 202 into %s", w.Code, got.ProjectID, p.ID)
	}
	sessions, _ := d.Jobs.ListByProject(context.Background(), p.ID)
	if len(sessions) != 1 {
		t.Fatalf("project has %d sessions, want 1", len(sessions))
	}
}

// AC3: a second concurrent submit of the same paper is refused 409, and creates
// no orphan project — the double-trigger guard, including its bare-submit hole.
func TestSubmitDoubleFireIsRefused(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	if w, _ := submit(t, h, `{"url":"https://arxiv.org/abs/2302.03714"}`, "sub-1"); w.Code != http.StatusAccepted {
		t.Fatalf("first submit: %d", w.Code)
	}
	// Same paper again, still running: refused, even though it is a bare submit
	// that would otherwise open a brand-new project.
	w, _ := submit(t, h, `{"url":"https://arxiv.org/abs/2302.03714"}`, "sub-1")
	if w.Code != http.StatusConflict {
		t.Fatalf("double-fire status = %d, want 409", w.Code)
	}
	// Exactly one project exists — the compensating delete removed the second.
	ps, _ := d.Projects.ListByOwner(context.Background(), "sub-1")
	if len(ps) != 1 {
		t.Errorf("owner has %d projects, want 1 (no orphan)", len(ps))
	}
}

func TestSubmitValidation(t *testing.T) {
	h := NewRouter(testDeps(allowAll)).Handler()
	if w := do(t, h, "POST", "/papers", `{"url":"  "}`, "sub"); w.Code != http.StatusBadRequest {
		t.Errorf("empty url: status %d, want 400", w.Code)
	}
	if w := do(t, h, "POST", "/papers", `not json`, "sub"); w.Code != http.StatusBadRequest {
		t.Errorf("bad json: status %d, want 400", w.Code)
	}
	// A reference that is not a DOI, arXiv ID, or URL is rejected at the edge.
	if w := do(t, h, "POST", "/papers", `{"url":"just some words"}`, "sub"); w.Code != http.StatusBadRequest {
		t.Errorf("garbage url: status %d, want 400", w.Code)
	}
	// Submitting into a project the caller does not own is refused.
	d := testDeps(allowAll)
	other, _ := d.Projects.Create(context.Background(), "someone-else", "Theirs")
	h2 := NewRouter(d).Handler()
	if w := do(t, h2, "POST", "/papers", `{"url":"https://arxiv.org/abs/2302.03714","project":"`+other.ID+`"}`, "me"); w.Code != http.StatusForbidden {
		t.Errorf("not-owner submit: status %d, want 403", w.Code)
	}
	// Submitting into a project that does not exist is a 404.
	if w := do(t, h2, "POST", "/papers", `{"url":"https://arxiv.org/abs/2302.03714","project":"nope"}`, "me"); w.Code != http.StatusNotFound {
		t.Errorf("missing-project submit: status %d, want 404", w.Code)
	}
}

// AC1: GET /projects lists the caller's own projects with a session count, and
// GET /projects/{id} returns a project with its sessions, refusing others'.
func TestProjectListAndDetail(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	// Two bare submits for the same owner open two projects.
	_, a := submit(t, h, `{"url":"https://arxiv.org/abs/2302.03714"}`, "me")
	_, _ = submit(t, h, `{"url":"https://doi.org/10.1145/3313831"}`, "me")
	// A stranger's project must never appear in my list or be readable by me.
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

	// Another owner's private project is refused, not disclosed.
	if w := do(t, h, "GET", "/projects/"+strangerP.ID, "", "me"); w.Code != http.StatusForbidden {
		t.Errorf("stranger project detail: status %d, want 403", w.Code)
	}
}

// 006f AC6: the public flip is owner-only, needs a recorded sign-off, refuses a
// non-clear project (409), and is one-way.
func TestMakeProjectPublic(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	// A project whose one session cleared the gate (fake repro r1, CanPublish).
	p, _ := d.Projects.Create(context.Background(), "me", "Owned")
	_ = publishInto(t, d.Jobs, "me", p.ID, "r1")

	// A non-owner is refused, and nothing changes.
	if w := do(t, h, "POST", "/projects/"+p.ID+"/public", `{"attestation_version":"v1"}`, "stranger"); w.Code != http.StatusForbidden {
		t.Fatalf("non-owner flip: status %d, want 403", w.Code)
	}
	// An un-signed flip changes nothing.
	if w := do(t, h, "POST", "/projects/"+p.ID+"/public", `{}`, "me"); w.Code != http.StatusBadRequest {
		t.Fatalf("unsigned flip: status %d, want 400", w.Code)
	}
	if got, _ := d.Projects.Get(context.Background(), p.ID); got.Visibility != project.VisibilityPrivate {
		t.Fatalf("project went public without a valid request")
	}

	// The owner, with a sign-off, flips it public and the attestation is recorded.
	w := do(t, h, "POST", "/projects/"+p.ID+"/public", `{"attestation_version":"v1"}`, "me")
	if w.Code != http.StatusOK {
		t.Fatalf("owner flip: status %d, want 200", w.Code)
	}
	got, _ := d.Projects.Get(context.Background(), p.ID)
	if got.Visibility != project.VisibilityPublic || got.SignedOffBy != "me" || got.AttestationVersion != "v1" || got.SignedOffAt == nil {
		t.Fatalf("after flip: %+v", got)
	}
}

// 006f AC6: a project whose reproduction did not clear the gate is barred (409).
func TestMakeProjectPublicRefusesNotClear(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := d.Projects.Create(context.Background(), "me", "Owned")
	_ = publishInto(t, d.Jobs, "me", p.ID, "flagged") // CanPublish == false

	if w := do(t, h, "POST", "/projects/"+p.ID+"/public", `{"attestation_version":"v1"}`, "me"); w.Code != http.StatusConflict {
		t.Fatalf("not-clear flip: status %d, want 409", w.Code)
	}
	if got, _ := d.Projects.Get(context.Background(), p.ID); got.Visibility != project.VisibilityPrivate {
		t.Errorf("a non-clear project must stay private")
	}
}

// fakeRepoPub records which repositories were flipped public.
type fakeRepoPub struct{ flipped []string }

func (f *fakeRepoPub) SetPublic(_ context.Context, repo string) error {
	f.flipped = append(f.flipped, repo)
	return nil
}

// 006f: the owner may opt the forge repo out of the public flip — the project
// still goes public in-app, but SetPublic is not called.
func TestPublicFlipRepoOptOut(t *testing.T) {
	pub := &fakeRepoPub{}
	d := testDeps(allowAll)
	d.RepoPublisher = pub
	h := NewRouter(d).Handler()

	// Default (flip_repo omitted): the repository is flipped.
	p1, _ := d.Projects.Create(context.Background(), "me", "One")
	_ = publishInto(t, d.Jobs, "me", p1.ID, "r1")
	if w := do(t, h, "POST", "/projects/"+p1.ID+"/public", `{"attestation_version":"v1"}`, "me"); w.Code != http.StatusOK {
		t.Fatalf("default flip: status %d", w.Code)
	}
	if len(pub.flipped) != 1 || pub.flipped[0] != "r1" {
		t.Fatalf("default should flip the repo, flipped=%v", pub.flipped)
	}

	// Opt-out (flip_repo:false): the project goes public but the repo is not flipped.
	p2, _ := d.Projects.Create(context.Background(), "me", "Two")
	_ = publishInto(t, d.Jobs, "me", p2.ID, "r1")
	if w := do(t, h, "POST", "/projects/"+p2.ID+"/public", `{"attestation_version":"v1","flip_repo":false}`, "me"); w.Code != http.StatusOK {
		t.Fatalf("opt-out flip: status %d", w.Code)
	}
	if len(pub.flipped) != 1 {
		t.Errorf("opt-out must not flip the repo, flipped=%v", pub.flipped)
	}
	if got, _ := d.Projects.Get(context.Background(), p2.ID); got.Visibility != project.VisibilityPublic {
		t.Errorf("opt-out project should still be public in-app")
	}
}

// 006f AC8: a public project appears in the public zone to another signed-in
// user and opens; a private one never does.
func TestPublicZone(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	pub, _ := d.Projects.Create(context.Background(), "owner", "Public one")
	_ = publishInto(t, d.Jobs, "owner", pub.ID, "r1")
	_ = do(t, h, "POST", "/projects/"+pub.ID+"/public", `{"attestation_version":"v1"}`, "owner")
	_, _ = d.Projects.Create(context.Background(), "owner", "Private one")

	// The public zone lists only the public project, to a different user.
	w := do(t, h, "GET", "/public", "", "someone-else")
	var zone []ProjectView
	_ = json.Unmarshal(w.Body.Bytes(), &zone)
	if w.Code != http.StatusOK || len(zone) != 1 || zone[0].ID != pub.ID || zone[0].Owner != "owner" {
		t.Fatalf("public zone = %+v (status %d); want only the public project showing its owner", zone, w.Code)
	}
	// A different user can open the public project…
	if w := do(t, h, "GET", "/projects/"+pub.ID, "", "someone-else"); w.Code != http.StatusOK {
		t.Errorf("open public project: status %d, want 200", w.Code)
	}
}

// doAs sends a request carrying a full identity (sub + email), which the member
// routes need (the owner's email seeds the lead bridge).
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

// 006f AC7: the members API is owner-only — the owner invites and removes by
// email; a non-owner is refused.
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

	// A non-owner may neither list, add, nor remove.
	stranger := Identity{Sub: "stranger", Email: "s@x.com"}
	if w := doAs(t, h, "GET", "/projects/"+p.ID+"/members", "", stranger); w.Code != http.StatusForbidden {
		t.Errorf("non-owner list: status %d, want 403", w.Code)
	}
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"x@x.com"}`, stranger); w.Code != http.StatusForbidden {
		t.Errorf("non-owner add: status %d, want 403", w.Code)
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

// publishInto adds a session to a project and points it at a published repo.
func publishInto(t *testing.T, s JobStore, sub, projectID, repo string) job.Job {
	t.Helper()
	ctx := context.Background()
	j, err := s.Enqueue(ctx, sub, "ref", job.KindAudit, projectID, "arxiv:"+repo)
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
	j, _ := d.Jobs.Enqueue(context.Background(), "sub", "ref", job.KindAudit, "", "")
	h := NewRouter(d).Handler()

	if w := do(t, h, "GET", "/jobs/"+j.ID, "", "sub"); w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w := do(t, h, "GET", "/jobs/missing", "", "sub"); w.Code != http.StatusNotFound {
		t.Errorf("missing job: status %d, want 404", w.Code)
	}
}

// publishedSession drives a job to publishing under a repo, so its transcript
// resolves through the fake forge.
func publishedSession(t *testing.T, s JobStore, sub, repo string, usage *provider.Usage) job.Job {
	t.Helper()
	ctx := context.Background()
	j, err := s.Enqueue(ctx, sub, "ref", job.KindAudit, "p", "arxiv:1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	adv := s.(*job.MemStore)
	for _, p := range []job.Phase{job.PhaseResolving, job.PhaseRunning, job.PhasePublishing} {
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
	got, _ := s.Get(ctx, j.ID)
	return got
}

// 006h AC1-4: a session's transcript is served as parsed events plus usage, with
// not_ready and not_found kept distinct. Recorded usage wins over derived.
func TestSessionTranscript(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d.Jobs, "me", "r1", &provider.Usage{InputTokens: 100, OutputTokens: 50})

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

// 006h AC3: a backfilled session with no recorded usage derives it from the
// transcript's per-request usage, so the feature is not blank on real data.
func TestSessionTranscriptUsageDerived(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d.Jobs, "me", "r1", nil) // no usage observed

	w := do(t, h, "GET", "/jobs/"+j.ID+"/transcript", "", "me")
	var tr Transcript
	_ = json.Unmarshal(w.Body.Bytes(), &tr)
	if w.Code != http.StatusOK || tr.Usage == nil || tr.Usage.Total() != 160 {
		t.Fatalf("status %d usage %+v; want derived total 160 (100+50+10)", w.Code, tr.Usage)
	}
}

// A session whose usage is neither recorded nor in the transcript shows "not
// recorded" (nil), never a fabricated zero.
func TestSessionTranscriptUsageNotRecorded(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	j := publishedSession(t, d.Jobs, "me", "bare", nil) // no usage anywhere

	w := do(t, h, "GET", "/jobs/"+j.ID+"/transcript", "", "me")
	var tr Transcript
	_ = json.Unmarshal(w.Body.Bytes(), &tr)
	if w.Code != http.StatusOK || tr.Usage != nil {
		t.Fatalf("status %d usage %+v; want 200 and nil usage", w.Code, tr.Usage)
	}
}

// A session that has not published a transcript is 409 not_ready, distinct from
// a 404 for a session that does not exist — 006h AC4.
func TestSessionTranscriptNotReadyVsMissing(t *testing.T) {
	d := testDeps(allowAll)
	// A submitted session with no repository yet.
	j, _ := d.Jobs.Enqueue(context.Background(), "me", "ref", job.KindAudit, "p", "arxiv:1")
	h := NewRouter(d).Handler()

	if w := do(t, h, "GET", "/jobs/"+j.ID+"/transcript", "", "me"); w.Code != http.StatusConflict {
		t.Errorf("not-ready: status %d, want 409", w.Code)
	}
	if w := do(t, h, "GET", "/jobs/missing/transcript", "", "me"); w.Code != http.StatusNotFound {
		t.Errorf("missing session: status %d, want 404", w.Code)
	}
}

// GET /jobs lists only the caller's own jobs — the projects they submitted.
// Another principal's jobs never appear, even through the same handler.
func TestListMyJobs(t *testing.T) {
	d := testDeps(allowAll)
	mine, _ := d.Jobs.Enqueue(context.Background(), "me", "ref-mine", job.KindAudit, "", "")
	_, _ = d.Jobs.Enqueue(context.Background(), "other", "ref-other", job.KindAudit, "", "")
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
	// A caller with jobs-of-others but none of their own gets an empty JSON array,
	// never null.
	w = do(t, h, "GET", "/jobs", "", "nobody")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("empty list = %d %q, want 200 []", w.Code, w.Body.String())
	}
}

// AC3: read surfaces return what 003/005 hold, through their interfaces.
func TestReadSurfaces(t *testing.T) {
	h := NewRouter(testDeps(allowAll)).Handler()
	if w := do(t, h, "GET", "/reproductions", "", "sub"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "open-hai/x") {
		t.Errorf("list: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, h, "GET", "/reproductions/r1", "", "sub"); w.Code != http.StatusOK {
		t.Errorf("get: status %d, want 200", w.Code)
	}
	if w := do(t, h, "GET", "/reproductions/r1/transcript", "", "sub"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"event":1`) {
		t.Errorf("transcript: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, h, "GET", "/reproductions/nope", "", "sub"); w.Code != http.StatusNotFound {
		t.Errorf("missing repro: status %d, want 404", w.Code)
	}

	// A backend failure (not a miss) is a 500, never disguised as not-found.
	backendDown := NewRouter(Deps{
		Jobs: job.NewMemStore(), Gate: allowAll,
		Reproductions: &fakeRepro{getErr: errors.New("forge timeout")},
	}).Handler()
	if w := do(t, backendDown, "GET", "/reproductions/r1", "", "sub"); w.Code != http.StatusInternalServerError {
		t.Errorf("a backend error should be 500, got %d", w.Code)
	}
}

// AC4: with the fail-closed gate, every gated route answers not_invited; the
// health check outside the gate does not. The gated set is enumerated from the
// router itself.
func TestGateIsFailClosedAndEnumerable(t *testing.T) {
	rt := NewRouter(Deps{Jobs: job.NewMemStore(), Reproductions: &fakeRepro{}}) // nil gate -> DenyAll
	h := rt.Handler()

	gated := rt.GatedRoutes()
	if len(gated) == 0 {
		t.Fatal("no gated routes enumerated")
	}
	for _, route := range gated {
		// A path with the {id} wildcard filled in.
		path := strings.ReplaceAll(route.Pattern, "{id}", "x")
		w := do(t, h, route.Method, path, `{"url":"u"}`, "authed-but-ungranted")
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "not_invited") {
			t.Errorf("%s %s: status %d body %s, want 403 not_invited", route.Method, path, w.Code, w.Body.String())
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

// AC5: no handler contains engine logic — the package imports neither
// internal/method nor a provider adapter. Asserted on the dependency graph.
func TestNoEngineImports(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/latere-ai/replichai/internal/httpapi").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	forbidden := []string{
		"github.com/latere-ai/replichai/internal/method",
		"github.com/latere-ai/replichai/internal/provider/anthropic",
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, bad := range forbidden {
			if dep == bad {
				t.Errorf("httpapi imports %s — a handler must contain no engine logic", bad)
			}
		}
	}
}
