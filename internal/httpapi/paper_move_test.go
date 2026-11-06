// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
)

// moveBody builds the request body this route takes. The key travels in the
// body for the same reason as in startSession: paper.Key returns
// "doi:10.1145/…", which spans several path segments and would not route.
func moveBody(key, to string) string {
	return `{"paper_key":"` + key + `","to_project":"` + to + `"}`
}

// finish advances a session to a terminal phase, which is the only state a
// paper may be moved in. Deps.Jobs is a read-only interface, so the store is
// reached through the concrete type the test built.
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

// twoProjects creates, for one owner, a source project holding a paper and an
// empty destination project. Nearly every move test starts from this setup.
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

// TestMovingAPaperTakesItsSessions covers 035 AC1: a paper filed under the
// wrong project is refiled under the right one, and the sessions that ran it
// move with it. A session under a project that does not hold its paper is the
// orphan that 015 exists to prevent.
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
	// The destination lists it as a paper with one session. This is the join
	// the project screen makes, so the paper row and its runs arrived together.
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

// TestMovingAPaperIsRefusedWhileItRuns covers 035 AC3: a paper is not moved
// while a session of it runs. The worker holds the old project id in memory,
// publishing may be in flight, and rewriting a live row into another project
// would move it inside jobs_active_paper_project, the partial unique index the
// paper table cannot see. This is the terminal-only rule from 032.
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

// TestMovingOntoAPaperTheDestinationHolds covers 035 AC4: two projects holding
// the same paper is the usual state when somebody reorganizes, so the
// collision happens in practice. Merging two papers' session histories is not
// a move, so the request is refused and both projects are left unchanged.
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

// TestMovingAPaperIsRefused covers 035 AC5 and AC6: every check the route
// applies before a move, one request each. They are asserted together because
// the bug this guards against is a route that applies some checks and omits
// one.
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

	// Another user's project is not a valid destination, and its existence is
	// not confirmed either. The id came from the caller, so a 403 would tell
	// them they had guessed a real one.
	stranger, _ := d.Projects.Create(ctx, "sub-2", "Not yours")
	w := do(t, h, "POST", path, moveBody(key, stranger.ID), "sub-1")
	if w.Code != http.StatusNotFound || errorCode(w.Body.Bytes()) != "target_not_found" {
		t.Errorf("a stranger's project as destination: status %d, body %s", w.Code, w.Body)
	}

	// A trashed project cannot be written to, at either end of the move.
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

	// A published project at either end refuses the move, because moving a
	// paper out of a published project changes what a signed attestation
	// covers. The owner must withdraw first, which is the purpose of 006o.
	if err := d.Projects.SetPublic(ctx, to.ID, project.SignOff{By: "sub-1", AttestationVersion: "v2"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if w := do(t, h, "POST", path, moveBody(key, to.ID), "sub-1"); w.Code != http.StatusConflict ||
		errorCode(w.Body.Bytes()) != "project_public" {
		t.Errorf("a published destination: status %d, body %s", w.Code, w.Body)
	}
	// A withdrawal makes it movable again. The sign-off is still on the row,
	// which is why the check reads Visibility and not the sign-off.
	if err := d.Projects.SetPrivate(ctx, to.ID, project.Withdrawal{By: "sub-1", Reason: "reorganizing"}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if w := do(t, h, "POST", path, moveBody(key, to.ID), "sub-1"); w.Code != http.StatusNoContent {
		t.Errorf("after withdrawal: status %d, body %s", w.Code, w.Body)
	}
}

// TestMovingAPaperIsTheLeads covers 035: a move is a removal from one project
// and an addition to the other, so it is authorized like a removal. Only a
// lead may do it, matching who may move credits out, and the check applies to
// both projects, not only the one in the path.
func TestMovingAPaperIsTheLeads(t *testing.T) {
	ctx := context.Background()
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	const key = "arxiv:2302.03714"
	owner := Identity{Sub: "sub-1", Email: "owner@example.org"}
	collab := Identity{Sub: "sub-2", Email: "collab@example.org"}
	from, to := twoProjects(t, d, owner.Sub, key, "https://arxiv.org/abs/2302.03714")

	// A collaborator in both projects may work in them, but may not move a
	// paper between them.
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

	// Authorization is checked before state. A destination the caller may not
	// move into must not first report that it is published. The caller may
	// name any project they are a member of, so the order of these two checks
	// decides whether the response is a refusal or a disclosure.
	if err := d.Projects.SetPublic(ctx, to.ID, project.SignOff{By: owner.Sub, AttestationVersion: "v2"}); err != nil {
		t.Fatalf("publish the destination: %v", err)
	}
	if w := doAs(t, h, "POST", path, moveBody(key, to.ID), collab); errorCode(w.Body.Bytes()) != "not_lead" {
		t.Fatalf("a published destination reported its state to a non-lead: status %d, body %s", w.Code, w.Body)
	}
	if err := d.Projects.SetPrivate(ctx, to.ID, project.Withdrawal{By: owner.Sub, Reason: "reorganizing"}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}

	// Promoted to lead of the source alone, they still cannot move the paper.
	// The destination is checked by the same rule, which a helper that checks
	// only one project would miss.
	if err := d.Members.Add(ctx, access.Principal{Sub: owner.Sub, Email: owner.Email},
		from.ID, collab.Email, access.RoleLead); err != nil {
		t.Fatalf("promote in the source: %v", err)
	}
	if w := doAs(t, h, "POST", path, moveBody(key, to.ID), collab); w.Code != http.StatusForbidden {
		t.Fatalf("a lead of one end only: status %d, body %s", w.Code, w.Body)
	}

	// A lead of both projects may move the paper.
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
