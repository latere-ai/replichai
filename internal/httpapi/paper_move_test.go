// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
)

// moveBody is the one request shape this route takes. The key travels in the
// body for startSession's reason: paper.Key returns "doi:10.1145/…", which spans
// several path segments and would route nowhere.
func moveBody(key, to string) string {
	return `{"paper_key":"` + key + `","to_project":"` + to + `"}`
}

// finish drives a session to a terminal phase, which is the only state a paper
// may be moved in. Deps.Jobs is the read seam, so the store is reached through
// the concrete type the test built.
func finish(t *testing.T, d Deps, id string) {
	t.Helper()
	store, ok := d.Jobs.(*job.MemStore)
	if !ok {
		t.Fatalf("the test job store %T cannot advance a session", d.Jobs)
	}
	if err := store.Advance(context.Background(), id, job.PhaseFailed, func(j *job.Job) { j.Err = "over" }); err != nil {
		t.Fatalf("finish the session: %v", err)
	}
}

// twoProjects gives one owner a source holding a paper and an empty
// destination — the setup nearly every move test starts from.
func twoProjects(t *testing.T, d Deps, owner, key, ref string) (from, to project.Project) {
	t.Helper()
	ctx := context.Background()
	from, err := d.Projects.Create(ctx, owner, "Where it was filed")
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	to, err = d.Projects.Create(ctx, owner, "Where it belongs")
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}
	if err := d.Projects.AddPaper(ctx, from.ID, project.Paper{Key: key, Ref: ref, AddedBy: owner}); err != nil {
		t.Fatalf("add paper: %v", err)
	}
	return from, to
}

// 035 AC1: a paper filed under the wrong project is refiled under the right
// one, and the sessions that are runs of it go with it. A session under a
// project that is not about its paper is the orphan 015 exists to prevent.
func TestMovingAPaperTakesItsSessions(t *testing.T) {
	ctx := context.Background()
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	const key = "arxiv:2302.03714"
	from, to := twoProjects(t, d, "sub-1", key, "https://arxiv.org/abs/2302.03714")

	// A finished run of the paper, and a second paper in the source that must
	// not move with it.
	_, started := startPaper(t, h, from.ID, key, "sub-1")
	if started.JobID == "" {
		t.Fatal("start: no session")
	}
	finish(t, d, started.JobID)
	const staying = "arxiv:2401.00001"
	if err := d.Projects.AddPaper(ctx, from.ID, project.Paper{Key: staying, Ref: staying, AddedBy: "sub-1"}); err != nil {
		t.Fatalf("add the second paper: %v", err)
	}

	if w := do(t, h, "POST", "/projects/"+from.ID+"/papers/move", moveBody(key, to.ID), "sub-1"); w.Code != http.StatusNoContent {
		t.Fatalf("move: status %d, body %s", w.Code, w.Body)
	}

	papers, _ := d.Projects.ListPapers(ctx, to.ID)
	if len(papers) != 1 || papers[0].Key != key {
		t.Errorf("destination papers = %+v; want only %s", papers, key)
	}
	left, _ := d.Projects.ListPapers(ctx, from.ID)
	if len(left) != 1 || left[0].Key != staying {
		t.Errorf("source papers = %+v; want only %s", left, staying)
	}
	moved, _ := d.Jobs.ListByProject(ctx, to.ID)
	if len(moved) != 1 || moved[0].ID != started.JobID {
		t.Fatalf("destination sessions = %+v; want the run of the moved paper", moved)
	}
	if sessions, _ := d.Jobs.ListByProject(ctx, from.ID); len(sessions) != 0 {
		t.Errorf("source still holds %d sessions", len(sessions))
	}
	// And the destination reads it as a paper with a session, which is the join
	// the project screen makes: the paper row and its runs arrived together.
	w := do(t, h, "GET", "/projects/"+to.ID+"/papers", "", "sub-1")
	if w.Code != http.StatusOK {
		t.Fatalf("list papers: status %d", w.Code)
	}
	var views []PaperView
	if err := json.Unmarshal(w.Body.Bytes(), &views); err != nil {
		t.Fatalf("decode papers: %v", err)
	}
	if len(views) != 1 || views[0].Sessions != 1 {
		t.Errorf("destination paper view = %+v; want one paper with one session", views)
	}
}

// 035 AC3: a paper is not moved out from under a run. The worker holds the old
// project id in memory, publishing may be in flight, and a live row rewritten
// into another project moves inside jobs_active_paper_project — the partial
// unique index the paper table cannot see. It is 032's terminal-only rule.
func TestMovingAPaperIsRefusedWhileItRuns(t *testing.T) {
	ctx := context.Background()
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	const key = "arxiv:2302.03714"
	from, to := twoProjects(t, d, "sub-1", key, "https://arxiv.org/abs/2302.03714")

	if w, _ := startPaper(t, h, from.ID, key, "sub-1"); w.Code != http.StatusCreated {
		t.Fatalf("start: %d", w.Code)
	}
	w := do(t, h, "POST", "/projects/"+from.ID+"/papers/move", moveBody(key, to.ID), "sub-1")
	if w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != "paper_running" {
		t.Fatalf("move during a run: status %d, body %s", w.Code, w.Body)
	}
	if papers, _ := d.Projects.ListPapers(ctx, from.ID); len(papers) != 1 {
		t.Error("the refused move took the paper anyway")
	}
}

// 035 AC4: two projects about the same paper is exactly when somebody
// reorganises, so the collision is real. Merging two papers' session histories
// is not a move, and a refusal must leave both projects untouched.
func TestMovingOntoAPaperTheDestinationHolds(t *testing.T) {
	ctx := context.Background()
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	const key = "arxiv:2302.03714"
	from, to := twoProjects(t, d, "sub-1", key, "https://arxiv.org/abs/2302.03714")
	if err := d.Projects.AddPaper(ctx, to.ID, project.Paper{Key: key, Ref: key, AddedBy: "sub-1"}); err != nil {
		t.Fatalf("add the same paper to the destination: %v", err)
	}

	w := do(t, h, "POST", "/projects/"+from.ID+"/papers/move", moveBody(key, to.ID), "sub-1")
	if w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != "paper_exists" {
		t.Fatalf("move onto a held key: status %d, body %s", w.Code, w.Body)
	}
	if papers, _ := d.Projects.ListPapers(ctx, from.ID); len(papers) != 1 {
		t.Error("the source lost its paper to a refused move")
	}
}

// 035 AC5/AC6: every gate the route puts in front of a move, one request each.
// They are asserted together because the bug this guards is a route that checks
// three of them and forgets the fourth.
func TestMovingAPaperIsRefused(t *testing.T) {
	ctx := context.Background()
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	const key = "arxiv:2302.03714"
	from, to := twoProjects(t, d, "sub-1", key, "https://arxiv.org/abs/2302.03714")
	path := "/projects/" + from.ID + "/papers/move"

	for _, c := range []struct {
		name, body, want string
		status           int
	}{
		{"no key", moveBody("  ", to.ID), "missing_key", http.StatusBadRequest},
		{"no destination", moveBody(key, "  "), "missing_target", http.StatusBadRequest},
		{"itself", moveBody(key, from.ID), "same_project", http.StatusBadRequest},
		{"not JSON", `{`, "bad_request", http.StatusBadRequest},
		{"a destination that is not there", moveBody(key, "nope"), "target_not_found", http.StatusNotFound},
		{"a key the source does not hold", moveBody("doi:10.0000/nope", to.ID), "paper_not_found", http.StatusNotFound},
	} {
		w := do(t, h, "POST", path, c.body, "sub-1")
		if w.Code != c.status || errorCode(w.Body.Bytes()) != c.want {
			t.Errorf("%s: status %d %s; want %d %s", c.name, w.Code, w.Body, c.status, c.want)
		}
	}

	// Somebody else's project is not a destination, and is not confirmed to
	// exist either: the id came from the caller, so a 403 would tell them they
	// had guessed a real one.
	stranger, _ := d.Projects.Create(ctx, "sub-2", "Not yours")
	w := do(t, h, "POST", path, moveBody(key, stranger.ID), "sub-1")
	if w.Code != http.StatusNotFound || errorCode(w.Body.Bytes()) != "target_not_found" {
		t.Errorf("a stranger's project as destination: status %d, body %s", w.Code, w.Body)
	}

	// A trashed project is writable by nobody, at either end.
	if err := d.Projects.Trash(ctx, to.ID); err != nil {
		t.Fatalf("trash: %v", err)
	}
	if w := do(t, h, "POST", path, moveBody(key, to.ID), "sub-1"); w.Code != http.StatusConflict ||
		errorCode(w.Body.Bytes()) != "project_trashed" {
		t.Errorf("a trashed destination: status %d, body %s", w.Code, w.Body)
	}
	if err := d.Projects.Restore(ctx, to.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// Published at either end and the move is refused: moving a paper out of a
	// published project changes what a signed attestation covers. The owner
	// withdraws first, which is 006o's whole point.
	if err := d.Projects.SetPublic(ctx, to.ID, project.SignOff{By: "sub-1", AttestationVersion: "v2"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if w := do(t, h, "POST", path, moveBody(key, to.ID), "sub-1"); w.Code != http.StatusConflict ||
		errorCode(w.Body.Bytes()) != "project_public" {
		t.Errorf("a published destination: status %d, body %s", w.Code, w.Body)
	}
	// And a withdrawal makes it movable again — the sign-off is still on the
	// row, which is exactly why the check reads Visibility and not the sign-off.
	if err := d.Projects.SetPrivate(ctx, to.ID, project.Withdrawal{By: "sub-1", Reason: "reorganising"}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if w := do(t, h, "POST", path, moveBody(key, to.ID), "sub-1"); w.Code != http.StatusNoContent {
		t.Errorf("after withdrawal: status %d, body %s", w.Code, w.Body)
	}
}

// 035: a move is a remove from one project and an add to the other, so it is
// authorised like removing — the lead's, matching who may move credits out —
// and on both ends, not just the one in the path.
func TestMovingAPaperIsTheLeads(t *testing.T) {
	ctx := context.Background()
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	const key = "arxiv:2302.03714"
	owner := Identity{Sub: "sub-1", Email: "owner@example.org"}
	collab := Identity{Sub: "sub-2", Email: "collab@example.org"}
	from, to := twoProjects(t, d, owner.Sub, key, "https://arxiv.org/abs/2302.03714")

	// A collaborator in both projects may work in them — and still may not move
	// a paper between them.
	for _, p := range []project.Project{from, to} {
		if err := d.Members.SeedLead(ctx, p.ID, access.Principal{Sub: owner.Sub, Email: owner.Email}); err != nil {
			t.Fatalf("seed lead: %v", err)
		}
		if err := d.Members.Add(ctx, access.Principal{Sub: owner.Sub, Email: owner.Email},
			p.ID, collab.Email, access.RoleCollaborator); err != nil {
			t.Fatalf("add collaborator: %v", err)
		}
	}
	if _, err := d.Members.(access.Membership).BindAll(ctx, access.Principal{Sub: collab.Sub, Email: collab.Email}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	path := "/projects/" + from.ID + "/papers/move"
	if w := doAs(t, h, "POST", path, moveBody(key, to.ID), collab); w.Code != http.StatusForbidden ||
		errorCode(w.Body.Bytes()) != "not_lead" {
		t.Fatalf("a collaborator moving a paper: status %d, body %s", w.Code, w.Body)
	}

	// Authorisation is answered before state. A destination they may not move
	// into must not first tell them it is published — the caller may name any
	// project they have a stake in, so the order of these two checks is the
	// difference between a refusal and a disclosure.
	if err := d.Projects.SetPublic(ctx, to.ID, project.SignOff{By: owner.Sub, AttestationVersion: "v2"}); err != nil {
		t.Fatalf("publish the destination: %v", err)
	}
	if w := doAs(t, h, "POST", path, moveBody(key, to.ID), collab); errorCode(w.Body.Bytes()) != "not_lead" {
		t.Fatalf("a published destination reported its state to a non-lead: status %d, body %s", w.Code, w.Body)
	}
	if err := d.Projects.SetPrivate(ctx, to.ID, project.Withdrawal{By: owner.Sub, Reason: "reorganising"}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}

	// Promoted to lead of the source alone, they still cannot: the destination
	// is checked by the same rule, which is the half a one-project helper misses.
	if err := d.Members.Add(ctx, access.Principal{Sub: owner.Sub, Email: owner.Email},
		from.ID, collab.Email, access.RoleLead); err != nil {
		t.Fatalf("promote in the source: %v", err)
	}
	if w := doAs(t, h, "POST", path, moveBody(key, to.ID), collab); w.Code != http.StatusForbidden {
		t.Fatalf("a lead of one end only: status %d, body %s", w.Code, w.Body)
	}

	// A lead of both may.
	if err := d.Members.Add(ctx, access.Principal{Sub: owner.Sub, Email: owner.Email},
		to.ID, collab.Email, access.RoleLead); err != nil {
		t.Fatalf("promote in the destination: %v", err)
	}
	if w := doAs(t, h, "POST", path, moveBody(key, to.ID), collab); w.Code != http.StatusNoContent {
		t.Fatalf("a lead of both: status %d, body %s", w.Code, w.Body)
	}
	if papers, _ := d.Projects.ListPapers(ctx, to.ID); len(papers) != 1 {
		t.Error("the paper did not arrive")
	}
}
