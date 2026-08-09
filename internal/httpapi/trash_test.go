package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
)

// 019 AC1/AC2/AC5: a trashed project leaves every list it was in, is answered by
// the trash screen, and comes back whole. Only its owner may put it there.
func TestTrashAndRestore(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	ctx := context.Background()

	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	collab := Identity{Sub: "collab-sub", Email: "collab@x.com"}
	stranger := Identity{Sub: "stranger-sub", Email: "s@x.com"}

	p, _ := d.Projects.Create(ctx, owner.Sub, "Thrown away")
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"collab@x.com"}`, owner); w.Code != http.StatusNoContent {
		t.Fatalf("invite: %d", w.Code)
	}
	if _, err := d.Members.(access.Membership).BindAll(ctx, access.Principal{Sub: collab.Sub, Email: collab.Email}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Neither a collaborator nor a stranger may remove somebody else's project,
	// and a stranger is not even told it exists.
	if w := doAs(t, h, "DELETE", "/projects/"+p.ID, "", collab); w.Code != http.StatusForbidden {
		t.Errorf("collaborator delete = %d, want 403", w.Code)
	}
	if w := doAs(t, h, "DELETE", "/projects/"+p.ID, "", stranger); w.Code != http.StatusNotFound {
		t.Errorf("stranger delete = %d, want 404", w.Code)
	}

	if w := doAs(t, h, "DELETE", "/projects/"+p.ID, "", owner); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", w.Code)
	}

	// Gone from the owner's list and from the collaborator's.
	if n := len(listOf(t, h, "/projects", owner)); n != 0 {
		t.Errorf("the owner's list holds %d projects, want none", n)
	}
	if n := len(listOf(t, h, "/projects", collab)); n != 0 {
		t.Errorf("the collaborator's list holds %d projects, want none — a trashed project is nobody's shared project", n)
	}
	// And in the trash, which is the owner's alone.
	trash := listOf(t, h, "/projects/trash", owner)
	if len(trash) != 1 || trash[0].ID != p.ID {
		t.Fatalf("trash = %+v, want the one project", trash)
	}
	if trash[0].DeletedAt == nil {
		t.Error("the trashed project carries no deleted_at")
	}
	if n := len(listOf(t, h, "/projects/trash", collab)); n != 0 {
		t.Errorf("the collaborator's trash holds %d projects, want none", n)
	}

	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/restore", "", owner); w.Code != http.StatusNoContent {
		t.Fatalf("restore = %d, want 204", w.Code)
	}
	if n := len(listOf(t, h, "/projects", owner)); n != 1 {
		t.Errorf("after restore the owner's list holds %d projects, want 1", n)
	}
	if n := len(listOf(t, h, "/projects/trash", owner)); n != 0 {
		t.Errorf("after restore the trash holds %d projects, want none", n)
	}
}

// The trash shows the deleted time, so it sorts on the deleted time. A project
// created long ago and thrown away today belongs above one created recently and
// thrown away last month — the ordinary list's modified-time sort would put them
// the other way round, which is a column that does not order what it shows.
func TestTrashIsSortedByWhenItWasDeleted(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	ctx := context.Background()
	me := Identity{Sub: "me", Email: "me@x.com"}

	old, _ := d.Projects.Create(ctx, me.Sub, "Old but just deleted")
	recent, _ := d.Projects.Create(ctx, me.Sub, "New but deleted earlier")
	// The newer project is worked in (so it has the later modified time) and
	// thrown away first; the older one is thrown away last.
	if err := d.Projects.AddPaper(ctx, recent.ID, project.Paper{Key: "arxiv:1", Ref: "r", AddedBy: me.Sub}); err != nil {
		t.Fatalf("add paper: %v", err)
	}
	if w := doAs(t, h, "DELETE", "/projects/"+recent.ID, "", me); w.Code != http.StatusNoContent {
		t.Fatalf("delete recent: %d", w.Code)
	}
	if w := doAs(t, h, "DELETE", "/projects/"+old.ID, "", me); w.Code != http.StatusNoContent {
		t.Fatalf("delete old: %d", w.Code)
	}

	trash := listOf(t, h, "/projects/trash", me)
	if len(trash) != 2 {
		t.Fatalf("trash holds %d projects, want 2", len(trash))
	}
	if trash[0].ID != old.ID {
		t.Errorf("the trash leads with %q, want the most recently deleted %q", trash[0].Title, old.Title)
	}
}

// 019 AC6: a trashed project's address still resolves for its owner, read-only,
// and 404s everybody else — including the sessions and transcripts underneath
// it, which inherit the decision through mayViewProject.
func TestTrashedProjectIsOwnerOnlyAndReadOnly(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	ctx := context.Background()

	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	collab := Identity{Sub: "collab-sub", Email: "collab@x.com"}

	p, _ := d.Projects.Create(ctx, owner.Sub, "Thrown away")
	sess := publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"collab@x.com"}`, owner); w.Code != http.StatusNoContent {
		t.Fatalf("invite: %d", w.Code)
	}
	if _, err := d.Members.(access.Membership).BindAll(ctx, access.Principal{Sub: collab.Sub, Email: collab.Email}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if w := doAs(t, h, "DELETE", "/projects/"+p.ID, "", owner); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}

	// The owner can still read it — 013's rule is that the address is the state,
	// and a link that 404s the moment somebody tidies up lies about why.
	w := doAs(t, h, "GET", "/projects/"+p.ID, "", owner)
	if w.Code != http.StatusOK {
		t.Fatalf("the owner reads their trashed project = %d, want 200", w.Code)
	}
	var detail ProjectDetail
	_ = json.Unmarshal(w.Body.Bytes(), &detail)
	if detail.Project.DeletedAt == nil {
		t.Error("the detail does not report the project as trashed")
	}
	if detail.MaySubmit {
		t.Error("may_submit is true on a trashed project; nothing may be started in one")
	}

	// The collaborator loses the project and everything under it.
	for _, path := range []string{"/projects/" + p.ID, "/jobs/" + sess.ID, "/jobs/" + sess.ID + "/transcript", "/jobs/" + sess.ID + "/artifacts"} {
		if code := doAs(t, h, "GET", path, "", collab).Code; code != http.StatusNotFound {
			t.Errorf("collaborator GET %s = %d, want 404 on a trashed project", path, code)
		}
	}

	// Every write is refused, at the one gate they all pass.
	writes := []struct{ method, path, body string }{
		{"POST", "/projects/" + p.ID + "/papers", `{"url":"https://arxiv.org/abs/2301.03711"}`},
		{"POST", "/projects/" + p.ID + "/sessions", `{"paper_key":"arxiv:2301.03711"}`},
		{"PATCH", "/projects/" + p.ID, `{"title":"Renamed"}`},
	}
	for _, wr := range writes {
		got := doAs(t, h, wr.method, wr.path, wr.body, owner)
		if got.Code != http.StatusConflict {
			t.Errorf("%s %s on a trashed project = %d, want 409", wr.method, wr.path, got.Code)
		}
		if code := errorCode(got.Body.Bytes()); code != "project_trashed" {
			t.Errorf("%s %s refused with %q, want project_trashed", wr.method, wr.path, code)
		}
	}
}

// 019 AC3/AC4: a running session blocks the removal, and a public project is
// withdrawn on the way out — a publication that vanishes with no retraction is
// what 006o exists to prevent.
func TestTrashRefusesARunningSessionAndWithdrawsAPublication(t *testing.T) {
	pub := &fakeRepoPub{}
	d := testDeps(allowAll)
	d.RepoPublisher = pub
	h := NewRouter(d).Handler()
	ctx := context.Background()
	owner := Identity{Sub: "me", Email: "me@x.com"}

	running, _ := d.Projects.Create(ctx, owner.Sub, "Running")
	if _, err := d.Jobs.Enqueue(ctx, job.Submission{Principal: owner.Sub, Ref: "r", Kind: job.KindAudit, Project: running.ID, PaperKey: "arxiv:1"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	got := doAs(t, h, "DELETE", "/projects/"+running.ID, "", owner)
	if got.Code != http.StatusConflict || errorCode(got.Body.Bytes()) != "sessions_running" {
		t.Errorf("delete with a running session = %d/%q, want 409/sessions_running", got.Code, errorCode(got.Body.Bytes()))
	}
	if p, _ := d.Projects.Get(ctx, running.ID); p.Trashed() {
		t.Error("the refused delete trashed the project anyway")
	}

	// A published project: withdrawn, its repository closed, and the retraction
	// recorded rather than skipped.
	published, _ := d.Projects.Create(ctx, owner.Sub, "Published")
	_ = publishInto(t, d.Jobs, owner.Sub, published.ID, "r1")
	publishable(t, d, owner.Sub, "ada")
	if w := doAs(t, h, "POST", "/projects/"+published.ID+"/public", signOff, owner); w.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", w.Code, w.Body.String())
	}
	if w := doAs(t, h, "DELETE", "/projects/"+published.ID, "", owner); w.Code != http.StatusNoContent {
		t.Fatalf("delete a published project = %d", w.Code)
	}
	after, _ := d.Projects.Get(ctx, published.ID)
	if after.Visibility != project.VisibilityPrivate {
		t.Errorf("visibility = %q, want private", after.Visibility)
	}
	if after.WithdrawnAt == nil || after.WithdrawnBy != owner.Sub {
		t.Errorf("no retraction recorded: %+v", after)
	}
	if after.SignedOffBy == "" {
		t.Error("the sign-off was erased; a withdrawal retracts a publication, it does not un-say the attestation")
	}
	if len(pub.closed) == 0 {
		t.Error("the forge repository was left open")
	}
	if n := len(listOf(t, h, "/public", owner)); n != 0 {
		t.Errorf("the public zone still lists %d projects, want none", n)
	}
}

// 019 AC7/AC8/AC9: a permanent delete is refused until the project is in the
// trash and its pot is empty; when it goes, it takes its sessions and papers,
// and leaves the repositories standing but closed.
func TestPurgeGuardsAndCascade(t *testing.T) {
	pub := &fakeRepoPub{}
	d := testDeps(allowAll)
	// A ledger beside an unmetered job store: this test is about what a purge
	// refuses to leave behind, not about the enqueue gate.
	members, ok := d.Members.(credit.Members)
	if !ok {
		t.Fatalf("the test membership store %T cannot answer roles", d.Members)
	}
	ledger := credit.NewMemStore(nil, members)
	d.Credits = ledger
	d.RepoPublisher = pub
	h := NewRouter(d).Handler()
	ctx := context.Background()

	p, _ := d.Projects.Create(ctx, payer.Sub, "Purged")
	if err := d.Members.SeedLead(ctx, p.ID, access.Principal{Sub: payer.Sub, Email: payer.Email}); err != nil {
		t.Fatalf("seed lead: %v", err)
	}
	sess := publishInto(t, d.Jobs, payer.Sub, p.ID, "r1")
	if err := d.Projects.AddPaper(ctx, p.ID, project.Paper{Key: "arxiv:1", Ref: "r", AddedBy: payer.Sub}); err != nil {
		t.Fatalf("add paper: %v", err)
	}

	// Not in the trash yet: a purge cannot skip the reversible step.
	got := doAs(t, h, "DELETE", "/projects/"+p.ID+"?purge=1", "", payer)
	if got.Code != http.StatusConflict || errorCode(got.Body.Bytes()) != "not_trashed" {
		t.Errorf("purge before trash = %d/%q, want 409/not_trashed", got.Code, errorCode(got.Body.Bytes()))
	}

	if w := doAs(t, h, "DELETE", "/projects/"+p.ID, "", payer); w.Code != http.StatusNoContent {
		t.Fatalf("trash: %d", w.Code)
	}

	// Funded: the money has to be accounted for before the container holding it
	// can be forgotten.
	topUp(t, ledger, payer, 10*credit.Dollar)
	if err := ledger.Allocate(ctx, access.Principal{Sub: payer.Sub, Email: payer.Email}, p.ID, p.Title, 5*credit.Dollar); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	got = doAs(t, h, "DELETE", "/projects/"+p.ID+"?purge=1", "", payer)
	if got.Code != http.StatusConflict || errorCode(got.Body.Bytes()) != "credits_remaining" {
		t.Errorf("purge with a funded pot = %d/%q, want 409/credits_remaining", got.Code, errorCode(got.Body.Bytes()))
	}
	if _, err := d.Projects.Get(ctx, p.ID); err != nil {
		t.Fatalf("the refused purge removed the project: %v", err)
	}

	if err := ledger.Reclaim(ctx, access.Principal{Sub: payer.Sub, Email: payer.Email}, p.ID, p.Title, 5*credit.Dollar); err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if w := doAs(t, h, "DELETE", "/projects/"+p.ID+"?purge=1", "", payer); w.Code != http.StatusNoContent {
		t.Fatalf("purge = %d, want 204", w.Code)
	}

	// Gone from every store it was in.
	if _, err := d.Projects.Get(ctx, p.ID); err == nil {
		t.Error("the project row survived the purge")
	}
	if sessions, _ := d.Jobs.ListByProject(ctx, p.ID); len(sessions) != 0 {
		t.Errorf("%d sessions survived the purge", len(sessions))
	}
	if _, err := d.Jobs.Get(ctx, sess.ID); err == nil {
		t.Error("the session row survived the purge")
	}
	// The repository is closed, not destroyed: a repository the agent authored is
	// evidence, and what the purge ends is the app's claim on it.
	if len(pub.closed) == 0 {
		t.Error("the forge repository was left open by the purge")
	}
}

// The trash screen's path also looks like a project id, and Go's mux resolves it
// by preferring the literal segment. Asserting it beats trusting it: a
// regression here turns the trash into a 404 on a project that does not exist.
func TestTrashPathBeatsTheProjectIdPattern(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	me := Identity{Sub: "me", Email: "me@x.com"}
	if _, err := d.Projects.Create(context.Background(), me.Sub, "One"); err != nil {
		t.Fatalf("create: %v", err)
	}
	w := doAs(t, h, "GET", "/projects/trash", "", me)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /projects/trash = %d, want 200", w.Code)
	}
	var out []ProjectView
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("the trash answered something that is not a project list: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("trash = %+v, want empty", out)
	}
}

// listOf reads a project list off one of the list endpoints.
func listOf(t *testing.T, h http.Handler, path string, id Identity) []ProjectView {
	t.Helper()
	w := doAs(t, h, "GET", path, "", id)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", path, w.Code)
	}
	var out []ProjectView
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}

// errorCode reads the machine-readable code out of a refusal body.
func errorCode(body []byte) string {
	var out struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &out)
	return out.Error
}
