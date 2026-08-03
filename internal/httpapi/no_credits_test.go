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
	for _, path := range []string{"/projects", "/shared", "/public", "/me/profile"} {
		if w := doAs(t, h, "GET", path, "", newcomer); w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 with no budget", path, w.Code)
		}
	}
	// Editing who they are.
	if w := doAs(t, h, "PATCH", "/me/profile", `{"display_name":"Ada"}`, newcomer); w.Code != http.StatusOK {
		t.Errorf("save profile = %d, want 200 with no budget", w.Code)
	}
	// Creating a project is *not* on this side of the line: a project is opened
	// by submitting a paper (006j), so creating one is starting a session, which
	// is spending. It is refused, the half-made project is undone, and each
	// paper reports why — which is what lets the client offer a top-up rather
	// than say "could not start".
	w := doAs(t, h, "POST", "/projects", `{"title":"Mine","papers":["https://arxiv.org/abs/2401.00003"]}`, newcomer)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create project = %d, want 422 with no budget", w.Code)
	}
	if code := firstSessionError(t, w.Body.Bytes()); code != "no_credits" {
		t.Errorf("per-paper error = %q, want no_credits", code)
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

	// And the one thing they cannot do: spend.
	if w := doAs(t, h, "POST", "/papers", `{"url":"https://arxiv.org/abs/2401.00001"}`, newcomer); w.Code != http.StatusPaymentRequired {
		t.Errorf("submit a paper = %d, want 402 with no budget", w.Code)
	}
	if w := doAs(t, h, "POST", "/papers", `{"url":"https://arxiv.org/abs/2401.00002","project":"`+proj+`"}`, newcomer); w.Code != http.StatusPaymentRequired {
		t.Errorf("submit into a project = %d, want 402 with no budget", w.Code)
	}
	// The project half-made for a paper that could not be enqueued is not left
	// behind — the compensating delete still runs on the credit path, so a
	// broke account does not accumulate empty projects by retrying.
	list := doAs(t, h, "GET", "/projects", "", newcomer)
	if n := countProjects(t, list.Body.Bytes()); n != 1 {
		t.Errorf("owner holds %d project(s), want only the one they already had", n)
	}
}

// firstSessionError reads the first per-paper error code out of a create
// response.
func firstSessionError(t *testing.T, body []byte) string {
	t.Helper()
	var got struct {
		Sessions []struct {
			Error string `json:"error"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode create response: %v (%s)", err, body)
	}
	if len(got.Sessions) == 0 {
		t.Fatalf("no per-paper results in %s", body)
	}
	return got.Sessions[0].Error
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
