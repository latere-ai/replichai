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
)

// allowAll is a permissive gate for testing the handlers behind the gate.
var allowAll = GateFunc(func(*http.Request) (bool, string) { return true, "" })

func testDeps(gate Gate) Deps {
	return Deps{Jobs: job.NewMemStore(), Reproductions: &fakeRepro{}, Gate: gate}
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
	if id != "r1" {
		return nil, ErrReproNotFound
	}
	return []byte(`{"event":1}` + "\n"), nil
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

// AC1: POST /papers returns a job id without blocking on the run.
func TestSubmitReturnsAJobWithoutBlocking(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()

	w := do(t, h, "POST", "/papers", `{"url":"https://arxiv.org/abs/2302.03714","project":"p1"}`, "sub-1")
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	var got struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.JobID == "" {
		t.Fatal("no job id returned")
	}
	// The job persisted, submitted, and carries the principal — the run has not
	// started blocking anything.
	j, err := d.Jobs.Get(context.Background(), got.JobID)
	if err != nil || j.Phase != job.PhaseSubmitted || j.Principal != "sub-1" {
		t.Errorf("job = %+v, err %v", j, err)
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
