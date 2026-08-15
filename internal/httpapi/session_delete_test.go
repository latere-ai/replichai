package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
)

// 032 AC4, and the reason the spec exists: a run that began publishing and never
// finished blocked its project from going public forever, because nothing in the
// product could remove it. Deleting it clears the way.
//
// This is the regression test for the reported bug. It fails without the route.
func TestDeletingAnUnfinishedSessionUnblocksPublishing(t *testing.T) {
	pub := &fakeRepoPub{}
	d := testDeps(allowAll)
	d.RepoPublisher = pub
	h := NewRouter(d).Handler()
	ctx := context.Background()
	owner := Identity{Sub: "me", Email: "me@x.com"}

	publishable(t, d, owner.Sub, "ada")
	p, _ := d.Projects.Create(ctx, owner.Sub, "Blocked")
	publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")            // cleared
	stuck := publishInto(t, d.Jobs, owner.Sub, p.ID, "gone") // repository, no manifest

	// Before: the refusal names the stuck session and the project stays private.
	status, code, blockers := refusedPublish(t, h, p.ID)
	if status != http.StatusConflict || code != "sessions_unfinished" {
		t.Fatalf("before delete = %d/%q, want 409/sessions_unfinished", status, code)
	}
	if len(blockers) != 1 || blockers[0].SessionID != stuck.ID {
		t.Fatalf("blockers = %+v, want the stuck session", blockers)
	}

	if w := doAs(t, h, "DELETE", "/jobs/"+stuck.ID, "", owner); w.Code != http.StatusNoContent {
		t.Fatalf("delete the stuck session = %d %s", w.Code, w.Body.String())
	}

	// After: the same sign-off succeeds, and the cleared session's repository is
	// the one that gets flipped public.
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/public", signOff, owner); w.Code != http.StatusOK {
		t.Fatalf("publish after delete = %d %s", w.Code, w.Body.String())
	}
	after, _ := d.Projects.Get(ctx, p.ID)
	if after.Visibility != project.VisibilityPublic {
		t.Errorf("visibility = %q, want public", after.Visibility)
	}

	// 032 AC5: the deleted session's repository is closed, not destroyed — 019's
	// rule, because a repository the agent authored is evidence.
	if len(pub.closed) != 1 || pub.closed[0] != "gone" {
		t.Errorf("closed = %v, want the deleted session's repository alone", pub.closed)
	}
	// And it is not among the ones published: a session that left the app does
	// not get flipped open by the publish that followed it.
	for _, r := range pub.flipped {
		if r == "gone" {
			t.Errorf("the deleted session's repository %q was published anyway", r)
		}
	}
	if len(pub.flipped) != 1 || pub.flipped[0] != "r1" {
		t.Errorf("flipped = %v, want only the cleared session's repository", pub.flipped)
	}
}

// 032 AC1/AC2: the session leaves every read the app has for it, and a second
// delete is a 404 rather than a second close of the repository.
func TestDeletedSessionIsGoneFromEveryRead(t *testing.T) {
	pub := &fakeRepoPub{}
	d := testDeps(allowAll)
	d.RepoPublisher = pub
	h := NewRouter(d).Handler()
	ctx := context.Background()
	owner := Identity{Sub: "me", Email: "me@x.com"}

	p, _ := d.Projects.Create(ctx, owner.Sub, "Owned")
	kept := publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")
	gone := publishInto(t, d.Jobs, owner.Sub, p.ID, "r2")

	if w := doAs(t, h, "DELETE", "/jobs/"+gone.ID, "", owner); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}

	// The per-session reads all resolve through the store's Get, so closing that
	// one gate is what closes them; assert the ones a reader can reach.
	for _, path := range []string{
		"/jobs/" + gone.ID,
		"/jobs/" + gone.ID + "/transcript",
		"/jobs/" + gone.ID + "/reproduction",
		"/jobs/" + gone.ID + "/verdict",
		"/jobs/" + gone.ID + "/artifacts",
	} {
		if w := doAs(t, h, "GET", path, "", owner); w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, w.Code)
		}
	}
	// Cancel too: a deleted session is not something to stop.
	if w := doAs(t, h, "POST", "/jobs/"+gone.ID+"/cancel", "", owner); w.Code != http.StatusNotFound {
		t.Errorf("cancel a deleted session = %d, want 404", w.Code)
	}

	sessions, err := d.Jobs.ListByProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != kept.ID {
		t.Errorf("the project still lists %d sessions, want only %s", len(sessions), kept.ID)
	}

	// Idempotent at the edge the owner can see: a repeated delete is a 404, and
	// it must not close the repository a second time.
	before := len(pub.closed)
	if w := doAs(t, h, "DELETE", "/jobs/"+gone.ID, "", owner); w.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", w.Code)
	}
	if len(pub.closed) != before {
		t.Errorf("closed %v; the repeated delete closed the repository again", pub.closed)
	}
}

// 032 AC2: only the owner removes a session. A collaborator is refused, and a
// stranger is not told the session is there — cancelSession answers 403 to both,
// and this route deliberately does not inherit that.
func TestDeleteSessionIsOwnerOnly(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	ctx := context.Background()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	collab := Identity{Sub: "collab-sub", Email: "collab@x.com"}
	stranger := Identity{Sub: "stranger-sub", Email: "s@x.com"}

	p, _ := d.Projects.Create(ctx, owner.Sub, "Shared")
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"collab@x.com"}`, owner); w.Code != http.StatusNoContent {
		t.Fatalf("invite: %d", w.Code)
	}
	if _, err := d.Members.(access.Membership).BindAll(ctx, access.Principal{Sub: collab.Sub, Email: collab.Email}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	j := publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")

	if w := doAs(t, h, "DELETE", "/jobs/"+j.ID, "", collab); w.Code != http.StatusForbidden {
		t.Errorf("collaborator delete = %d, want 403", w.Code)
	}
	if w := doAs(t, h, "DELETE", "/jobs/"+j.ID, "", stranger); w.Code != http.StatusNotFound {
		t.Errorf("stranger delete = %d, want 404 — whether it exists is not their business", w.Code)
	}
	// Both refusals changed nothing.
	if _, err := d.Jobs.Get(ctx, j.ID); err != nil {
		t.Errorf("the refused deletes removed the session anyway: %v", err)
	}
}

// 032 AC3: a session in flight is not a corpse. This is what keeps the delete
// button on a blocker row from killing a publish that is merely slow — a session
// legitimately publishing raises the identical unfinished blocker.
func TestDeleteSessionRefusesARunningOne(t *testing.T) {
	pub := &fakeRepoPub{}
	d := testDeps(allowAll)
	d.RepoPublisher = pub
	h := NewRouter(d).Handler()
	ctx := context.Background()
	owner := Identity{Sub: "me", Email: "me@x.com"}

	p, _ := d.Projects.Create(ctx, owner.Sub, "Running")
	j, err := d.Jobs.Enqueue(ctx, job.Submission{
		Principal: owner.Sub, Ref: "r", Kind: job.KindAudit, Project: p.ID, PaperKey: "arxiv:1",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// Walk it to publishing: the phase that produces the same blocker as a dead
	// run, and the one a delete must refuse.
	mem := d.Jobs.(*job.MemStore)
	for _, phase := range []job.Phase{job.PhaseResolving, job.PhaseRunning, job.PhasePublishing} {
		p := phase
		if err := mem.Advance(ctx, j.ID, p, func(jb *job.Job) {
			if p == job.PhasePublishing {
				jb.RepoName = "open-hai/slow"
			}
		}); err != nil {
			t.Fatalf("advance %s: %v", p, err)
		}
	}

	w := doAs(t, h, "DELETE", "/jobs/"+j.ID, "", owner)
	if w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != "session_running" {
		t.Fatalf("delete a publishing session = %d/%q, want 409/session_running", w.Code, errorCode(w.Body.Bytes()))
	}
	if _, err := d.Jobs.Get(ctx, j.ID); err != nil {
		t.Errorf("the refused delete removed the session anyway: %v", err)
	}
	if len(pub.closed) != 0 {
		t.Errorf("closed = %v; a refused delete must not touch the forge", pub.closed)
	}
}

// 032 AC6/AC7: what a delete must leave alone. The money is still spent, and the
// project's running count is about sessions that are running.
func TestDeletedSessionKeepsItsCostAndItsProjectSummary(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	ctx := context.Background()
	owner := Identity{Sub: "me", Email: "me@x.com"}

	p, _ := d.Projects.Create(ctx, owner.Sub, "Counted")
	gone := publishInto(t, d.Jobs, owner.Sub, p.ID, "r1")
	if _, err := d.Jobs.Enqueue(ctx, job.Submission{
		Principal: owner.Sub, Ref: "r2", Kind: job.KindAudit, Project: p.ID, PaperKey: "arxiv:2",
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	before, err := d.Jobs.SpendSince(ctx, []string{p.ID}, gone.CreatedAt.Add(-time.Hour))
	if err != nil {
		t.Fatalf("SpendSince before: %v", err)
	}

	if w := doAs(t, h, "DELETE", "/jobs/"+gone.ID, "", owner); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", w.Code, w.Body.String())
	}

	// Spend is the one read that keeps a deleted session, because the ledger
	// still holds the charge and two totals would disagree.
	after, err := d.Jobs.SpendSince(ctx, []string{p.ID}, gone.CreatedAt.Add(-time.Hour))
	if err != nil {
		t.Fatalf("SpendSince after: %v", err)
	}
	if after[p.ID] != before[p.ID] {
		t.Errorf("spend %d -> %d; deleting a session does not un-spend its money", before[p.ID], after[p.ID])
	}

	status, err := d.Jobs.StatusByProjects(ctx, []string{p.ID})
	if err != nil {
		t.Fatalf("StatusByProjects: %v", err)
	}
	if status[p.ID].Total != 1 || status[p.ID].Running != 1 {
		t.Errorf("summary = %+v; want the one session still running and nothing else", status[p.ID])
	}
}
