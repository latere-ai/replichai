package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
)

// quotaDeps wires a router whose job store meters every payer at n.
func quotaDeps(t *testing.T, n int) (Deps, *Router) {
	t.Helper()
	d := testDeps(allowAll)
	d.Jobs = job.NewMemStore(job.WithAllowance(func(context.Context, string) (int, bool, error) {
		return n, false, nil
	}))
	return d, NewRouter(d)
}

// asPrincipal issues a request carrying an authenticated identity.
func asPrincipal(method, path, body, sub, email string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	return r.WithContext(WithIdentity(r.Context(), Identity{Sub: sub, Email: email}))
}

// 006l AC1: the quota refuses a submit past the limit, as a 402 with a
// machine-readable code — not a 500.
func TestSubmitRefusedPastQuota(t *testing.T) {
	_, rt := quotaDeps(t, 1)

	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/papers", `{"url":"https://arxiv.org/abs/2301.03711"}`, "u", "u@example.com"))
	if w.Code != http.StatusAccepted {
		t.Fatalf("first submit = %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/papers", `{"url":"https://arxiv.org/abs/2302.03712"}`, "u", "u@example.com"))
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("second submit = %d %s, want 402", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "quota_exceeded") {
		t.Errorf("body = %s, want quota_exceeded", w.Body.String())
	}
}

// 006l AC8: a batch create by someone with room for fewer papers starts what
// fits and reports quota_exceeded per paper for the rest, keeping the project.
func TestBatchCreateStopsAtTheQuota(t *testing.T) {
	_, rt := quotaDeps(t, 2)

	w := httptest.NewRecorder()
	body := `{"title":"batch","papers":["https://arxiv.org/abs/2301.03711","https://arxiv.org/abs/2302.03712","https://arxiv.org/abs/2303.03713","https://arxiv.org/abs/2304.03714"]}`
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects", body, "u", "u@example.com"))
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s, want 201 with partial results", w.Code, w.Body.String())
	}
	var out struct {
		ProjectID string          `json:"project_id"`
		Sessions  []SessionResult `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	started, refused := 0, 0
	for _, s := range out.Sessions {
		switch {
		case s.JobID != "":
			started++
		case s.Error == "quota_exceeded":
			refused++
		default:
			t.Errorf("unexpected session result %+v", s)
		}
	}
	if started != 2 || refused != 2 {
		t.Errorf("batch = %d started / %d refused, want 2 and 2", started, refused)
	}
	if out.ProjectID == "" {
		t.Error("the project should survive when at least one session started")
	}
}

// 006l AC5/AC6: sponsorship is the owner's to give, and only it makes
// charge_to:"project" legal.
func TestSponsorshipGovernsWhoPays(t *testing.T) {
	d, rt := quotaDeps(t, 5)
	ctx := context.Background()

	owner := Identity{Sub: "owner-sub", Email: "owner@example.com"}
	collab := Identity{Sub: "collab-sub", Email: "collab@example.com"}

	proj, err := d.Projects.Create(ctx, owner.Sub, "shared")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := d.Members.SeedLead(ctx, proj.ID, owner.Email); err != nil {
		t.Fatalf("seed lead: %v", err)
	}
	if err := d.Members.Add(ctx, access.Principal{Sub: owner.Sub, Email: owner.Email}, proj.ID, collab.Email, access.RoleCollaborator); err != nil {
		t.Fatalf("add member: %v", err)
	}

	submit := func(id Identity, url, chargeTo string) *httptest.ResponseRecorder {
		body := `{"url":"` + url + `","project":"` + proj.ID + `","charge_to":"` + chargeTo + `"}`
		w := httptest.NewRecorder()
		rt.Handler().ServeHTTP(w, asPrincipal("POST", "/papers", body, id.Sub, id.Email))
		return w
	}

	// A collaborator may open a session in a project shared with them.
	if w := submit(collab, "https://arxiv.org/abs/2301.03711", "me"); w.Code != http.StatusAccepted {
		t.Fatalf("collaborator submit = %d %s", w.Code, w.Body.String())
	}
	// Charging the project is refused while the owner has not sponsored it.
	if w := submit(collab, "https://arxiv.org/abs/2302.03712", "project"); w.Code != http.StatusForbidden ||
		!strings.Contains(w.Body.String(), "not_sponsored") {
		t.Fatalf("unsponsored charge = %d %s, want 403 not_sponsored", w.Code, w.Body.String())
	}
	// Only the owner may turn sponsorship on.
	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects/"+proj.ID+"/sponsor", `{"on":true}`, collab.Sub, collab.Email))
	if w.Code != http.StatusForbidden {
		t.Fatalf("collaborator sponsoring = %d, want 403", w.Code)
	}
	w = httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects/"+proj.ID+"/sponsor", `{"on":true}`, owner.Sub, owner.Email))
	if w.Code != http.StatusOK {
		t.Fatalf("owner sponsoring = %d %s", w.Code, w.Body.String())
	}

	// Now the sponsored charge lands on the owner, not the submitter.
	if w := submit(collab, "https://arxiv.org/abs/2302.03712", "project"); w.Code != http.StatusAccepted {
		t.Fatalf("sponsored submit = %d %s", w.Code, w.Body.String())
	}
	sessions, err := d.Jobs.ListByProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var sponsored, own int
	for _, j := range sessions {
		if j.Principal != collab.Sub {
			t.Errorf("session %s recorded principal %q, want the submitter", j.ID, j.Principal)
		}
		switch j.ChargedTo {
		case owner.Sub:
			sponsored++
		case collab.Sub:
			own++
		}
	}
	if sponsored != 1 || own != 1 {
		t.Errorf("charged: %d to the owner, %d to the submitter; want one each", sponsored, own)
	}
}

// A stranger — neither owner nor collaborator — may not open a session, so
// sponsorship never becomes a way in.
func TestStrangerCannotSubmitIntoAProject(t *testing.T) {
	d, rt := quotaDeps(t, 5)
	proj, err := d.Projects.Create(context.Background(), "owner-sub", "private")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.Projects.SetSponsored(context.Background(), proj.ID, true); err != nil {
		t.Fatalf("sponsor: %v", err)
	}
	body := `{"url":"https://arxiv.org/abs/2301.03711","project":"` + proj.ID + `","charge_to":"project"}`
	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/papers", body, "stranger", "stranger@example.com"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("stranger submit = %d %s, want 403", w.Code, w.Body.String())
	}
}

// The payer is resolved server-side: a body naming someone else's principal is
// ignored, because there is no field for it to arrive in.
func TestPayerIsNeverTakenFromTheBody(t *testing.T) {
	d, rt := quotaDeps(t, 5)
	body := `{"url":"https://arxiv.org/abs/2301.03711","charged_to":"someone-else","chargedTo":"someone-else","charge_to":"someone-else"}`
	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/papers", body, "u", "u@example.com"))
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit = %d %s", w.Code, w.Body.String())
	}
	jobs, err := d.Jobs.ListByPrincipal(context.Background(), "u")
	if err != nil || len(jobs) != 1 {
		t.Fatalf("list = %d (err %v)", len(jobs), err)
	}
	if jobs[0].ChargedTo != "u" {
		t.Errorf("charged_to = %q, want the submitter — a client may not name a payer", jobs[0].ChargedTo)
	}
}
