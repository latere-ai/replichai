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

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
)

// allowAll is a permissive gate for testing the handlers behind the gate.
var allowAll = GateFunc(func(*http.Request) (bool, string) { return true, "" })

func testDeps(gate Gate) Deps {
	return Deps{Jobs: job.NewMemStore(), Projects: project.NewMemStore(), Reproductions: &fakeRepro{}, Gate: gate}
}

type fakeRepro struct{ err, getErr error }

func (f *fakeRepro) List(context.Context) ([]Reproduction, error) {
	return []Reproduction{{ID: "r1", Repo: "open-hai/x", Verdict: "passed"}}, f.err
}
func (f *fakeRepro) Get(_ context.Context, id string) (Reproduction, error) {
	if f.getErr != nil {
		return Reproduction{}, f.getErr
	}
	if id != "r1" {
		return Reproduction{}, ErrReproNotFound
	}
	return Reproduction{ID: "r1", Repo: "open-hai/x"}, nil
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

	// Another owner's project is refused, not disclosed.
	if w := do(t, h, "GET", "/projects/"+strangerP.ID, "", "me"); w.Code != http.StatusForbidden {
		t.Errorf("stranger project detail: status %d, want 403", w.Code)
	}
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
