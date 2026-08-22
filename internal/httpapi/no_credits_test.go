package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
)

// 006q AC6, in 011's terms: the wall is at spending, and only there.
//
// Everyone arrives with no credits, so this is what the product *is* for a new
// account until somebody tops it up. Asserted per route rather than in prose,
// so a route added later cannot quietly join the wrong side of the line.
func TestNoCreditsStillLetsSomebodyUseTheApp(t *testing.T) {
	d := testDeps(allowAll)
	// A store that refuses every enqueue for want of credits — what Enqueue
	// does for a project that cannot commit the start reserve (011).
	d.Jobs = brokeJobs{Store: d.Jobs.(job.Store)}
	h := NewRouter(d).Handler()

	newcomer := Identity{Sub: "newcomer", Email: "new@example.com"}

	// Reads, and their own workspace.
	for _, path := range []string{"/projects", "/public", "/me/profile"} {
		if w := doAs(t, h, "GET", path, "", newcomer); w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 with no budget", path, w.Code)
		}
	}
	// Editing who they are.
	if w := doAs(t, h, "PATCH", "/me/profile", `{"display_name":"Ada"}`, newcomer); w.Code != http.StatusOK {
		t.Errorf("save profile = %d, want 200 with no budget", w.Code)
	}
	// Creating a project moved onto this side of the line with 012. It used to
	// be spending — a project was opened *by submitting a paper*, so the create
	// funded a pot and was refused 402 without one — which meant an account that
	// had never been given credits could not so much as name what it intended to
	// reproduce. Now it records papers and runs nothing, so it costs nothing.
	w := doAs(t, h, "POST", "/projects", `{"title":"Mine","papers":["https://arxiv.org/abs/2401.00003"]}`, newcomer)
	if w.Code != http.StatusCreated {
		t.Fatalf("create project = %d %s, want 201 with no budget", w.Code, w.Body)
	}
	var planned struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &planned)
	// Adding another paper to it is free too: writing down what you mean to run
	// is not running it.
	if w := doAs(t, h, "POST", "/projects/"+planned.ProjectID+"/papers", `{"url":"https://arxiv.org/abs/2401.00004"}`, newcomer); w.Code != http.StatusCreated {
		t.Errorf("add a paper = %d, want 201 with no budget", w.Code)
	}

	// A project they already own — renaming it, inviting somebody, reading its
	// members. None of it spends anything.
	owned, _ := d.Projects.Create(context.Background(), newcomer.Sub, "Mine")
	proj := owned.ID

	if w := doAs(t, h, "PATCH", "/projects/"+proj, `{"title":"Renamed"}`, newcomer); w.Code != http.StatusOK {
		t.Errorf("rename = %d, want 200 with no budget", w.Code)
	}
	if w := doAs(t, h, "POST", "/projects/"+proj+"/members", `{"email":"c@x.com"}`, newcomer); w.Code != http.StatusNoContent {
		t.Errorf("invite = %d, want 204 with no budget", w.Code)
	}
	if w := doAs(t, h, "GET", "/projects/"+proj+"/members", "", newcomer); w.Code != http.StatusOK {
		t.Errorf("list members = %d, want 200 with no budget", w.Code)
	}
	// Being invited into somebody else's, and seeing it.
	other := Identity{Sub: "other-sub", Email: "other@example.com"}
	theirs, _ := d.Projects.Create(context.Background(), other.Sub, "Theirs")
	if err := d.Members.SeedLead(context.Background(), theirs.ID, access.Principal{Sub: other.Sub, Email: other.Email}); err != nil {
		t.Fatalf("seed lead: %v", err)
	}
	if err := d.Members.Add(context.Background(), access.Principal{Sub: other.Sub, Email: other.Email},
		theirs.ID, newcomer.Email, access.RoleCollaborator); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := d.Members.(access.Membership).BindAll(context.Background(),
		access.Principal{Sub: newcomer.Sub, Email: newcomer.Email}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if w := doAs(t, h, "GET", "/projects/"+theirs.ID, "", newcomer); w.Code != http.StatusOK {
		t.Errorf("read a shared project = %d, want 200 with no budget", w.Code)
	}

	// And the one thing they cannot do: spend. It is asked once, on the act that
	// actually starts a run, in a project whose pot is on screen to answer it.
	if w := doAs(t, h, "POST", "/projects/"+planned.ProjectID+"/sessions",
		`{"paper_key":"arxiv:2401.00003"}`, newcomer); w.Code != http.StatusPaymentRequired {
		t.Errorf("start a session = %d, want 402 with no budget", w.Code)
	}
	// The projects they hold are the ones they made on purpose, plus the one they
	// were invited into — one list now (018). A broke account no longer
	// accumulates half-made projects by retrying, because a retry creates nothing
	// to clean up.
	list := doAs(t, h, "GET", "/projects", "", newcomer)
	if n := countProjects(t, list.Body.Bytes()); n != 3 {
		t.Errorf("newcomer holds %d project(s), want the two they created and the one they were invited into", n)
	}
}

// countProjects counts the projects in a list response.
func countProjects(t *testing.T, body []byte) int {
	t.Helper()
	var list []json.RawMessage
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode project list: %v (%s)", err, body)
	}
	return len(list)
}

// brokeJobs is a job store whose projects can never afford a session: every
// enqueue is refused, the way 011's gate inside Enqueue refuses one.
type brokeJobs struct{ job.Store }

func (brokeJobs) Enqueue(context.Context, job.Submission) (job.Job, error) {
	return job.Job{}, job.ErrNoCredits
}
