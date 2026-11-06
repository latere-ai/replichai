// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
)

// reserve is the amount every credit test in this file gates on.
const reserve = 2 * credit.Dollar

func fixedReserve(n int64) credit.Reserve {
	return func(context.Context) (int64, error) { return n, nil }
}

// creditDeps wires a router over a real ledger with the credit gate inside
// Enqueue, so these tests exercise the refusal the server produces rather than
// a fake that returns the error directly.
func creditDeps(t *testing.T) (Deps, *Router, credit.Store) {
	t.Helper()
	d := testDeps(allowAll)
	members, ok := d.Members.(credit.Members)
	if !ok {
		t.Fatalf("the test membership store %T cannot answer roles", d.Members)
	}
	ledger := credit.NewMemStore(nil, members)
	d.Jobs = job.NewMemStore(job.WithCredits(ledger, fixedReserve(reserve)))
	d.Credits = ledger
	d.Reserve = fixedReserve(reserve)
	return d, NewRouter(d), ledger
}

// topUp funds a wallet. The ledger is built with no Authority, so the admin
// check is skipped. These tests cover the gate, not who may create credits,
// which the ledger's own contract covers.
func topUp(t *testing.T, ledger credit.Store, who Identity, amount int64) {
	t.Helper()
	if err := ledger.Topup(context.Background(), access.Principal{Sub: who.Sub, Email: who.Email}, who.Email, amount); err != nil {
		t.Fatalf("Topup: %v", err)
	}
}

// asPrincipal issues a request carrying an authenticated identity.
func asPrincipal(method, path, body, sub, email string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	return r.WithContext(WithIdentity(r.Context(), Identity{Sub: sub, Email: email}))
}

var payer = Identity{Sub: "u", Email: "u@example.com"}

// itoa writes an amount into a JSON body.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// createHolding opens a project holding the given papers and returns its id.
// Since 012 this spends nothing, so it works from an empty wallet.
func createHolding(t *testing.T, rt *Router, who Identity, papers ...string) string {
	t.Helper()
	quoted := make([]string, len(papers))
	for i, p := range papers {
		quoted[i] = `"` + p + `"`
	}
	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects",
		`{"papers":[`+strings.Join(quoted, ",")+`]}`, who.Sub, who.Email))
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.ProjectID == "" {
		t.Fatalf("create returned no project id: %s", w.Body.String())
	}
	return out.ProjectID
}

// allocate moves credits from a wallet into a project's pot. Before 012 the
// create request did this implicitly.
func allocate(t *testing.T, rt *Router, who Identity, projectID string, amount int64) {
	t.Helper()
	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects/"+projectID+"/credits",
		`{"amount":`+itoa(amount)+`}`, who.Sub, who.Email))
	if w.Code != http.StatusOK {
		t.Fatalf("allocate = %d %s", w.Code, w.Body.String())
	}
}

// start runs a paper the project holds and returns the raw response, so a test
// can assert on the refusal it checks.
func start(rt *Router, who Identity, projectID, key string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects/"+projectID+"/sessions",
		`{"paper_key":"`+key+`"}`, who.Sub, who.Email))
	return w
}

// TestStartRefusedWithoutCredits checks that a submit into a project that
// cannot commit the reserve answers 402 with a machine-readable code, not a 500
// or a generic failure the client would have to interpret before offering a
// top-up.
func TestStartRefusedWithoutCredits(t *testing.T) {
	_, rt, ledger := creditDeps(t)
	topUp(t, ledger, payer, reserve) // Enough for exactly one session.

	proj := createHolding(t, rt, payer, "https://arxiv.org/abs/2301.03711", "https://arxiv.org/abs/2302.03712")
	allocate(t, rt, payer, proj, reserve)

	if w := start(rt, payer, proj, "arxiv:2301.03711"); w.Code != http.StatusCreated {
		t.Fatalf("first start = %d %s", w.Code, w.Body.String())
	}
	w := start(rt, payer, proj, "arxiv:2302.03712")
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("second start = %d %s, want 402", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no_credits") {
		t.Errorf("body = %s, want no_credits", w.Body.String())
	}
}

// TestCreatingAProjectWithAnEmptyWalletSucceeds checks that creating a project
// spends nothing (012), so an empty wallet creates one.
//
// This is the central change of the spec. Before 012 a project was opened by
// submitting a paper, so the create had to fund the pot before its first
// enqueue, and an account that had never been given credits was refused 402
// for creating a project. A new user could not record what they intended to
// reproduce, or invite anyone to help, until an admin had paid for it.
func TestCreatingAProjectWithAnEmptyWalletSucceeds(t *testing.T) {
	d, rt, ledger := creditDeps(t)
	ctx := context.Background()

	proj := createHolding(t, rt, payer, "https://arxiv.org/abs/2301.03711")

	list, err := d.Projects.ListByOwner(ctx, payer.Sub)
	if err != nil || len(list) != 1 {
		t.Fatalf("owner holds %d project(s), want 1 (err %v)", len(list), err)
	}
	// The project holds the paper, holds no credits, and has run nothing.
	papers, _ := d.Projects.ListPapers(ctx, proj)
	if len(papers) != 1 {
		t.Errorf("project holds %d papers, want 1", len(papers))
	}
	if n, _ := ledger.Balance(ctx, credit.Pot(proj)); n != 0 {
		t.Errorf("pot = %d, want 0 — creating a project moves no money", n)
	}
	if n, _ := ledger.Balance(ctx, credit.Wallet(payer.Email)); n != 0 {
		t.Errorf("wallet = %d, want 0 — and takes none", n)
	}
	if sessions, _ := d.Jobs.ListByProject(ctx, proj); len(sessions) != 0 {
		t.Errorf("create started %d sessions, want 0", len(sessions))
	}

	// The 402 refusal still applies, once, on the start, which is the action
	// that spends.
	if w := start(rt, payer, proj, "arxiv:2301.03711"); w.Code != http.StatusPaymentRequired {
		t.Errorf("start with an empty pot = %d %s, want 402", w.Code, w.Body.String())
	}
}

// TestAProjectStartsWhatItCanAfford checks that a project funded for two
// sessions starts two and refuses the rest. The per-paper partial reporting a
// batch create used to do now happens on the starts, which are the actions
// that spend.
func TestAProjectStartsWhatItCanAfford(t *testing.T) {
	_, rt, ledger := creditDeps(t)
	topUp(t, ledger, payer, 10*credit.Dollar)

	proj := createHolding(t, rt, payer,
		"https://arxiv.org/abs/2301.03711", "https://arxiv.org/abs/2302.03712",
		"https://arxiv.org/abs/2303.03713", "https://arxiv.org/abs/2304.03714")
	allocate(t, rt, payer, proj, 2*reserve)

	started, refused := 0, 0
	for _, key := range []string{"arxiv:2301.03711", "arxiv:2302.03712", "arxiv:2303.03713", "arxiv:2304.03714"} {
		switch w := start(rt, payer, proj, key); w.Code {
		case http.StatusCreated:
			started++
		case http.StatusPaymentRequired:
			refused++
		default:
			t.Errorf("start %s = %d %s", key, w.Code, w.Body.String())
		}
	}
	if started != 2 || refused != 2 {
		t.Errorf("%d started / %d refused, want 2 and 2", started, refused)
	}
}

// TestACollaboratorSpendsTheProjectsCredits checks that the project pays
// rather than the person: a collaborator starting a session in a funded project
// is admitted on the project's credits and needs none of their own.
func TestACollaboratorSpendsTheProjectsCredits(t *testing.T) {
	d, rt, ledger := creditDeps(t)
	ctx := context.Background()
	owner := Identity{Sub: "owner-sub", Email: "owner@example.com"}
	collab := Identity{Sub: "collab-sub", Email: "collab@example.com"}
	topUp(t, ledger, owner, 10*credit.Dollar)

	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects",
		`{"title":"shared","papers":["https://arxiv.org/abs/2301.03711"],"collaborators":["collab@example.com"]}`,
		owner.Sub, owner.Email))
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	allocate(t, rt, owner, out.ProjectID, 4*credit.Dollar)
	if _, err := d.Members.(access.Membership).BindAll(ctx, access.Principal{Sub: collab.Sub, Email: collab.Email}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// The collaborator adds a paper and runs it, both on the project's money.
	w = httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects/"+out.ProjectID+"/papers",
		`{"url":"https://arxiv.org/abs/2302.03712"}`, collab.Sub, collab.Email))
	if w.Code != http.StatusCreated {
		t.Fatalf("collaborator add paper = %d %s", w.Code, w.Body.String())
	}
	if w := start(rt, collab, out.ProjectID, "arxiv:2302.03712"); w.Code != http.StatusCreated {
		t.Fatalf("collaborator start = %d %s", w.Code, w.Body.String())
	}
	if w := start(rt, owner, out.ProjectID, "arxiv:2301.03711"); w.Code != http.StatusCreated {
		t.Fatalf("owner start = %d %s", w.Code, w.Body.String())
	}
	// The collaborator's own wallet was never touched. No path leads from a run
	// to a person's wallet.
	if n, _ := ledger.Balance(ctx, credit.Wallet(collab.Email)); n != 0 {
		t.Errorf("collaborator wallet = %d, want 0", n)
	}
	if n, _ := ledger.Available(ctx, credit.Pot(out.ProjectID)); n != 4*credit.Dollar-2*reserve {
		t.Errorf("available = %d, want two reserves committed", n)
	}
}

// TestStrangerCannotWorkInAProject checks that a stranger, who is neither
// owner nor collaborator, may not add a paper or open a session, so a funded
// project does not admit non-members.
func TestStrangerCannotWorkInAProject(t *testing.T) {
	d, rt, _ := creditDeps(t)
	proj, err := d.Projects.Create(context.Background(), "owner-sub", "private")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stranger := Identity{Sub: "stranger", Email: "stranger@example.com"}
	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects/"+proj.ID+"/papers",
		`{"url":"https://arxiv.org/abs/2301.03711"}`, stranger.Sub, stranger.Email))
	// A stranger is answered 404, so the status code does not reveal whether
	// the project exists.
	if w.Code != http.StatusNotFound {
		t.Fatalf("stranger add paper = %d %s, want 404", w.Code, w.Body.String())
	}
	if w := start(rt, stranger, proj.ID, "arxiv:2301.03711"); w.Code != http.StatusNotFound {
		t.Fatalf("stranger start = %d %s, want 404", w.Code, w.Body.String())
	}
}

// TestMaySubmitDistinguishesWhoMayOpenASession checks the may_submit field.
// The server answers it because the client cannot: a public project is
// readable by everyone, and only the server knows membership. Without it the
// "Replicate again" form renders for a stranger browsing the public zone and
// every submit is refused.
func TestMaySubmitDistinguishesWhoMayOpenASession(t *testing.T) {
	d, rt, _ := creditDeps(t)
	ctx := context.Background()

	owner := Identity{Sub: "owner-sub", Email: "owner@example.com"}
	collab := Identity{Sub: "collab-sub", Email: "collab@example.com"}
	stranger := Identity{Sub: "stranger-sub", Email: "stranger@example.com"}

	proj, err := d.Projects.Create(ctx, owner.Sub, "open work")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := d.Members.SeedLead(ctx, proj.ID, access.Principal{Sub: owner.Sub, Email: owner.Email}); err != nil {
		t.Fatalf("seed lead: %v", err)
	}
	if err := d.Members.Add(ctx, access.Principal{Sub: owner.Sub, Email: owner.Email}, proj.ID, collab.Email, access.RoleCollaborator); err != nil {
		t.Fatalf("add member: %v", err)
	}
	// The project is public, so a stranger may read it. Reading must not imply
	// submitting.
	if err := d.Projects.SetPublic(ctx, proj.ID, project.SignOff{By: owner.Sub, AttestationVersion: "v1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	read := func(id Identity) ProjectDetail {
		t.Helper()
		w := httptest.NewRecorder()
		rt.Handler().ServeHTTP(w, asPrincipal("GET", "/projects/"+proj.ID, "", id.Sub, id.Email))
		if w.Code != http.StatusOK {
			t.Fatalf("read as %s = %d %s", id.Email, w.Code, w.Body.String())
		}
		var out ProjectDetail
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}

	for _, tc := range []struct {
		id   Identity
		want bool
	}{
		{owner, true},
		{collab, true},
		{stranger, false},
	} {
		if got := read(tc.id).MaySubmit; got != tc.want {
			t.Errorf("may_submit for %s = %v, want %v", tc.id.Email, got, tc.want)
		}
	}
}

// TestARefusedStartStrandsNoCredits checks that a refused start strands
// nothing. The credit hold is taken inside the enqueue transaction, so a
// session the double-trigger guard refuses never took one, and there is no
// longer a speculatively created project holding credits that a refusal has to
// unwind. That unwind, undoProject, was removed with the create-time funding
// that needed it.
func TestARefusedStartStrandsNoCredits(t *testing.T) {
	_, rt, ledger := creditDeps(t)
	ctx := context.Background()
	topUp(t, ledger, payer, 10*credit.Dollar)

	proj := createHolding(t, rt, payer, "https://arxiv.org/abs/2301.03711")
	allocate(t, rt, payer, proj, 4*credit.Dollar)
	if w := start(rt, payer, proj, "arxiv:2301.03711"); w.Code != http.StatusCreated {
		t.Fatalf("first start = %d %s", w.Code, w.Body.String())
	}
	wallet, _ := ledger.Balance(ctx, credit.Wallet(payer.Email))
	pot, _ := ledger.Balance(ctx, credit.Pot(proj))
	available, _ := ledger.Available(ctx, credit.Pot(proj))

	// The same paper is still running, so the guard refuses a second start.
	if w := start(rt, payer, proj, "arxiv:2301.03711"); w.Code != http.StatusConflict {
		t.Fatalf("second start = %d %s, want 409", w.Code, w.Body.String())
	}
	if n, _ := ledger.Balance(ctx, credit.Wallet(payer.Email)); n != wallet {
		t.Errorf("wallet = %d after a refused start, want %d", n, wallet)
	}
	if n, _ := ledger.Balance(ctx, credit.Pot(proj)); n != pot {
		t.Errorf("pot = %d after a refused start, want %d", n, pot)
	}
	if n, _ := ledger.Available(ctx, credit.Pot(proj)); n != available {
		t.Errorf("available = %d after a refused start, want %d — the refusal held a reserve", n, available)
	}
}

// TestCreateSeedsMembershipEvenWithNothingRunning checks that collaborators
// are seeded on every create, because no create is undone any more. The old
// rule delayed seeding until a session had started, since a membership row
// naming a deleted project would appear in the collaborator's "shared with me"
// list and nothing removes it. With nothing spent there is nothing to
// compensate for, so the row is written immediately.
func TestCreateSeedsMembershipEvenWithNothingRunning(t *testing.T) {
	d, rt, _ := creditDeps(t)
	ctx := context.Background()

	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects",
		`{"title":"planned","papers":["https://arxiv.org/abs/2301.03711"],"collaborators":["friend@example.com"]}`,
		payer.Sub, payer.Email))
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)

	friend := access.Principal{Sub: "friend-sub", Email: "friend@example.com"}
	if _, err := d.Members.(access.Membership).BindAll(ctx, friend); err != nil {
		t.Fatalf("bind: %v", err)
	}
	projects, err := d.Members.(access.Membership).Projects(ctx, friend.Sub)
	if err != nil {
		t.Fatalf("projects: %v", err)
	}
	// The project the collaborator was invited to exists, whether or not anybody
	// has started a session in it.
	if len(projects) != 1 || projects[0] != out.ProjectID {
		t.Fatalf("collaborator holds %v, want [%s]", projects, out.ProjectID)
	}
	if _, err := d.Projects.Get(ctx, out.ProjectID); err != nil {
		t.Errorf("the project the membership names must exist: %v", err)
	}
}

// TestProjectListDoesNotRepeatYourOwn checks that the merged project list
// (018) lists an owned project once. An owner is their own project's lead, so
// the membership store returns it like any other membership, and every project
// seeds one, including those opened by a bare submit. The merged list walks
// both the owned projects and the memberships, so it must produce one row, not
// two.
func TestProjectListDoesNotRepeatYourOwn(t *testing.T) {
	_, rt, ledger := creditDeps(t)
	topUp(t, ledger, payer, 10*credit.Dollar)

	createHolding(t, rt, payer, "https://arxiv.org/abs/2301.03711")

	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("GET", "/projects", "", payer.Sub, payer.Email))
	var list []ProjectView
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("list = %+v, want the one project exactly once", list)
	}
}

// TestCreditsMoveBetweenWalletAndProject checks that credits move both ways
// and that the rules differ by direction: any member may fund a project, and
// only a lead may take from it. The pot is fungible and does not record who
// filled it, so reclaiming is authority over the project's credits rather than
// a refund of one's own.
func TestCreditsMoveBetweenWalletAndProject(t *testing.T) {
	d, rt, ledger := creditDeps(t)
	ctx := context.Background()
	owner := Identity{Sub: "owner-sub", Email: "owner@example.com"}
	collab := Identity{Sub: "collab-sub", Email: "collab@example.com"}
	topUp(t, ledger, owner, 20*credit.Dollar)
	topUp(t, ledger, collab, 5*credit.Dollar)

	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects",
		`{"title":"shared","papers":["https://arxiv.org/abs/2301.03711"],"collaborators":["collab@example.com"]}`,
		owner.Sub, owner.Email))
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	allocate(t, rt, owner, out.ProjectID, 4*credit.Dollar)
	if _, err := d.Members.(access.Membership).BindAll(ctx, access.Principal{Sub: collab.Sub, Email: collab.Email}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	post := func(id Identity, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		rt.Handler().ServeHTTP(w, asPrincipal("POST", path, body, id.Sub, id.Email))
		return w
	}

	// A collaborator funds the project out of their own wallet.
	if w := post(collab, "/projects/"+out.ProjectID+"/credits", `{"amount":`+itoa(3*credit.Dollar)+`}`); w.Code != http.StatusOK {
		t.Fatalf("collaborator allocate = %d %s", w.Code, w.Body.String())
	}
	if n, _ := ledger.Balance(ctx, credit.Wallet(collab.Email)); n != 2*credit.Dollar {
		t.Errorf("collaborator wallet = %d, want %d", n, 2*credit.Dollar)
	}
	if n, _ := ledger.Balance(ctx, credit.Pot(out.ProjectID)); n != 7*credit.Dollar {
		t.Errorf("pot = %d, want %d", n, 7*credit.Dollar)
	}

	// The collaborator cannot take it back. Funding is not a claim on the pot.
	w = post(collab, "/projects/"+out.ProjectID+"/credits/reclaim", `{"amount":`+itoa(1*credit.Dollar)+`}`)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "not_lead") {
		t.Fatalf("collaborator reclaim = %d %s, want 403 not_lead", w.Code, w.Body.String())
	}

	// The lead can reclaim, including credits the collaborator put in.
	if w := post(owner, "/projects/"+out.ProjectID+"/credits/reclaim", `{"amount":`+itoa(5*credit.Dollar)+`}`); w.Code != http.StatusOK {
		t.Fatalf("lead reclaim = %d %s", w.Code, w.Body.String())
	}
	if n, _ := ledger.Balance(ctx, credit.Wallet(owner.Email)); n != 21*credit.Dollar {
		t.Errorf("owner wallet = %d, want %d — a lead reclaimed a collaborator's funding", n, 21*credit.Dollar)
	}

	// Credits already committed to a running session cannot be reclaimed.
	avail, _ := ledger.Available(ctx, credit.Pot(out.ProjectID))
	if w := post(owner, "/projects/"+out.ProjectID+"/credits/reclaim", `{"amount":`+itoa(avail+1)+`}`); w.Code != http.StatusPaymentRequired {
		t.Errorf("reclaiming into a hold = %d %s, want 402", w.Code, w.Body.String())
	}
	// A zero amount is a bad request rather than a silent no-op.
	if w := post(owner, "/projects/"+out.ProjectID+"/credits", `{"amount":0}`); w.Code != http.StatusBadRequest {
		t.Errorf("zero allocation = %d, want 400", w.Code)
	}
}

// TestAProjectReportsItsCreditsToMembersOnly checks that a project reports its
// credits to members only. A stranger reading a public project sees the
// research but not who paid for it.
func TestAProjectReportsItsCreditsToMembersOnly(t *testing.T) {
	d, rt, ledger := creditDeps(t)
	ctx := context.Background()
	owner := Identity{Sub: "owner-sub", Email: "owner@example.com"}
	stranger := Identity{Sub: "stranger-sub", Email: "stranger@example.com"}
	topUp(t, ledger, owner, 20*credit.Dollar)

	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects",
		`{"title":"open","papers":["https://arxiv.org/abs/2301.03711"]}`,
		owner.Sub, owner.Email))
	var out struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	allocate(t, rt, owner, out.ProjectID, 6*credit.Dollar)
	// One session is running, so available is one reserve below the balance,
	// which is why both numbers are reported.
	if w := start(rt, owner, out.ProjectID, "arxiv:2301.03711"); w.Code != http.StatusCreated {
		t.Fatalf("start = %d %s", w.Code, w.Body.String())
	}
	if err := d.Projects.SetPublic(ctx, out.ProjectID, project.SignOff{By: owner.Sub, AttestationVersion: "v1"}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	read := func(id Identity) ProjectDetail {
		t.Helper()
		w := httptest.NewRecorder()
		rt.Handler().ServeHTTP(w, asPrincipal("GET", "/projects/"+out.ProjectID, "", id.Sub, id.Email))
		var got ProjectDetail
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return got
	}

	mine := read(owner)
	if mine.Credits == nil {
		t.Fatal("the owner cannot see what their own project holds")
	}
	if mine.Credits.Balance != 6*credit.Dollar {
		t.Errorf("balance = %d, want %d", mine.Credits.Balance, 6*credit.Dollar)
	}
	// Available is lower by the running session's reserve, which is why both
	// numbers are reported.
	if mine.Credits.Available != 6*credit.Dollar-reserve {
		t.Errorf("available = %d, want %d", mine.Credits.Available, 6*credit.Dollar-reserve)
	}
	if mine.Credits.StartReserve != reserve {
		t.Errorf("start reserve = %d, want %d", mine.Credits.StartReserve, reserve)
	}
	if !mine.Credits.MayReclaim {
		t.Error("the owner is their project's lead and may reclaim")
	}

	if theirs := read(stranger); theirs.Credits != nil {
		t.Errorf("a stranger reading a public project sees its money: %+v", theirs.Credits)
	}
}

// TestTheWalletExplainsItself checks that the wallet endpoint shows a balance
// and the entries that produced it. The ledger exists to make every balance
// accountable, so the endpoint that shows a balance shows its statement too.
func TestTheWalletExplainsItself(t *testing.T) {
	_, rt, ledger := creditDeps(t)
	topUp(t, ledger, payer, 10*credit.Dollar)

	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("GET", "/me/credits", "", payer.Sub, payer.Email))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /me/credits = %d %s", w.Code, w.Body.String())
	}
	var got Wallet
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Balance != 10*credit.Dollar {
		t.Errorf("balance = %d, want %d", got.Balance, 10*credit.Dollar)
	}
	var sum int64
	for _, e := range got.Entries {
		sum += e.Amount
		if e.Actor == "" || e.CreatedAt.IsZero() {
			t.Errorf("entry %+v does not say who or when", e)
		}
	}
	if sum != got.Balance {
		t.Errorf("the statement sums to %d but the balance is %d", sum, got.Balance)
	}
}

// TestASessionReportsWhatItCost checks that a session reports its cost,
// because the project that paid for it is entitled to know, and that the rates
// it was priced at are not reported, because they are the platform's price
// list and are not exposed to project owners.
func TestASessionReportsWhatItCost(t *testing.T) {
	_, rt, ledger := creditDeps(t)
	topUp(t, ledger, payer, 10*credit.Dollar)

	proj := createHolding(t, rt, payer, "https://arxiv.org/abs/2301.03711")
	allocate(t, rt, payer, proj, 4*credit.Dollar)
	w := start(rt, payer, proj, "arxiv:2301.03711")
	var started struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &started)

	w = httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("GET", "/jobs/"+started.JobID, "", payer.Sub, payer.Email))
	body := w.Body.String()
	if !strings.Contains(body, `"cost"`) {
		t.Errorf("the session payload does not say what it cost: %s", body)
	}
	for _, leak := range []string{"rate_card", "cache_write_5m", `"rates"`} {
		if strings.Contains(body, leak) {
			t.Errorf("the session payload carries %q — the price list is the platform's, not the owner's: %s", leak, body)
		}
	}
}

// TestTheCreditRoutesDoNotConfirmAPrivateProjectExists checks that a project
// the caller may not view answers 404 on the credit routes too.
//
// Answering not_member would confirm that a guessed private id exists, which
// every other read path withholds; getProject answers 404 for the same reason.
// A caller who can view the project still receives the specific refusal,
// because it tells them nothing they did not already know.
func TestTheCreditRoutesDoNotConfirmAPrivateProjectExists(t *testing.T) {
	d, rt, ledger := creditDeps(t)
	ctx := context.Background()
	owner := Identity{Sub: "owner-sub", Email: "owner@example.com"}
	collab := Identity{Sub: "collab-sub", Email: "collab@example.com"}
	stranger := Identity{Sub: "stranger-sub", Email: "stranger@example.com"}
	topUp(t, ledger, owner, 20*credit.Dollar)
	topUp(t, ledger, stranger, 20*credit.Dollar)

	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects",
		`{"title":"private","credits":`+itoa(6*credit.Dollar)+`,"papers":["https://arxiv.org/abs/2301.03711"],"collaborators":["collab@example.com"]}`,
		owner.Sub, owner.Email))
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if _, err := d.Members.(access.Membership).BindAll(ctx, access.Principal{Sub: collab.Sub, Email: collab.Email}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	post := func(id Identity, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		rt.Handler().ServeHTTP(w, asPrincipal("POST", path, body, id.Sub, id.Email))
		return w
	}
	amount := `{"amount":` + itoa(1*credit.Dollar) + `}`

	// A stranger who guessed the id learns nothing, including whether it exists.
	for _, path := range []string{"/projects/" + out.ProjectID + "/credits", "/projects/" + out.ProjectID + "/credits/reclaim"} {
		w := post(stranger, path, amount)
		if w.Code != http.StatusNotFound {
			t.Errorf("stranger POST %s = %d %s, want 404", path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "not_member") {
			t.Errorf("POST %s told a stranger they are not a member, which says the project exists", path)
		}
	}
	// A collaborator can view the project, so the refusal names their role.
	if w := post(collab, "/projects/"+out.ProjectID+"/credits/reclaim", amount); w.Code != http.StatusForbidden ||
		!strings.Contains(w.Body.String(), "not_lead") {
		t.Errorf("collaborator reclaim = %d %s, want 403 not_lead", w.Code, w.Body.String())
	}
}
