package httpapi

import (
	"context"
	"encoding/json"
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

// The reserve every credit test in this file gates on.
const reserve = 2 * credit.Dollar

func fixedReserve(n int64) credit.Reserve {
	return func(context.Context) (int64, error) { return n, nil }
}

// creditDeps wires a router over a real ledger, with the gate inside Enqueue —
// so these tests exercise the refusal the server actually produces rather than
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

// topUp funds somebody's wallet. The ledger is built with no Authority, so the
// admin check is skipped — these tests are about the gate, not about who may
// create credits, which the ledger's own contract covers.
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

// A submit into a project that cannot commit the reserve is a 402 with a
// machine-readable code — not a 500, and not a generic failure the client would
// have to guess at to offer a top-up.
func TestSubmitRefusedWithoutCredits(t *testing.T) {
	_, rt, ledger := creditDeps(t)
	// Enough for exactly one session: the project is funded at creation with the
	// reserve, and a second session in it has nothing left to commit.
	topUp(t, ledger, payer, reserve)

	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/papers", `{"url":"https://arxiv.org/abs/2301.03711"}`, payer.Sub, payer.Email))
	if w.Code != http.StatusAccepted {
		t.Fatalf("first submit = %d %s", w.Code, w.Body.String())
	}
	var first struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &first)

	w = httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/papers",
		`{"url":"https://arxiv.org/abs/2302.03712","project":"`+first.ProjectID+`"}`, payer.Sub, payer.Email))
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("second submit = %d %s, want 402", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no_credits") {
		t.Errorf("body = %s, want no_credits", w.Body.String())
	}
}

// Creating a project funds it from the creator's wallet, in the request that
// creates it — otherwise the project's own first session could never start.
func TestCreatingAProjectFundsItFromTheWallet(t *testing.T) {
	_, rt, ledger := creditDeps(t)
	topUp(t, ledger, payer, 10*credit.Dollar)

	w := httptest.NewRecorder()
	body := `{"title":"funded","papers":["https://arxiv.org/abs/2301.03711"],"credits":` + itoa(4*credit.Dollar) + `}`
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects", body, payer.Sub, payer.Email))
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	var out struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ctx := context.Background()
	if n, _ := ledger.Balance(ctx, credit.Pot(out.ProjectID)); n != 4*credit.Dollar {
		t.Errorf("pot = %d, want the %d that was allocated", n, 4*credit.Dollar)
	}
	if n, _ := ledger.Balance(ctx, credit.Wallet(payer.Email)); n != 6*credit.Dollar {
		t.Errorf("wallet = %d, want %d — the money moved rather than being copied", n, 6*credit.Dollar)
	}
	// One session started, so one reserve is committed and the rest is free.
	if n, _ := ledger.Available(ctx, credit.Pot(out.ProjectID)); n != 4*credit.Dollar-reserve {
		t.Errorf("available = %d, want %d", n, 4*credit.Dollar-reserve)
	}
}

// An empty wallet is refused before any paper is enqueued, so the refusal is the
// whole request rather than 006j's per-paper report — and no project survives.
func TestCreatingAProjectWithAnEmptyWalletIsRefused(t *testing.T) {
	d, rt, _ := creditDeps(t)

	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects",
		`{"title":"broke","papers":["https://arxiv.org/abs/2301.03711"]}`, payer.Sub, payer.Email))
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("create with an empty wallet = %d %s, want 402", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no_credits") {
		t.Errorf("body = %s, want no_credits", w.Body.String())
	}
	list, err := d.Projects.ListByOwner(context.Background(), payer.Sub)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("owner holds %d project(s); a refused create must leave none", len(list))
	}
}

// 006j's rule is untouched where it still applies: an allocation that succeeds
// and papers that then run out of credits keep the project and report per paper.
func TestBatchCreateStartsWhatTheProjectCanAfford(t *testing.T) {
	_, rt, ledger := creditDeps(t)
	topUp(t, ledger, payer, 10*credit.Dollar)

	w := httptest.NewRecorder()
	body := `{"title":"batch","credits":` + itoa(2*reserve) + `,"papers":[` +
		`"https://arxiv.org/abs/2301.03711","https://arxiv.org/abs/2302.03712",` +
		`"https://arxiv.org/abs/2303.03713","https://arxiv.org/abs/2304.03714"]}`
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects", body, payer.Sub, payer.Email))
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
		case s.Error == "no_credits":
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

// The project pays, not the person: a collaborator submitting into a funded
// project is admitted on the project's credits, with nothing of their own.
func TestACollaboratorSpendsTheProjectsCredits(t *testing.T) {
	d, rt, ledger := creditDeps(t)
	ctx := context.Background()
	owner := Identity{Sub: "owner-sub", Email: "owner@example.com"}
	collab := Identity{Sub: "collab-sub", Email: "collab@example.com"}
	topUp(t, ledger, owner, 10*credit.Dollar)

	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/projects",
		`{"title":"shared","credits":`+itoa(4*credit.Dollar)+`,"papers":["https://arxiv.org/abs/2301.03711"],"collaborators":["collab@example.com"]}`,
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

	w = httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/papers",
		`{"url":"https://arxiv.org/abs/2302.03712","project":"`+out.ProjectID+`"}`, collab.Sub, collab.Email))
	if w.Code != http.StatusAccepted {
		t.Fatalf("collaborator submit = %d %s", w.Code, w.Body.String())
	}
	// Their own wallet was never touched — there is no path from a run to a
	// person's money.
	if n, _ := ledger.Balance(ctx, credit.Wallet(collab.Email)); n != 0 {
		t.Errorf("collaborator wallet = %d, want 0", n)
	}
	if n, _ := ledger.Available(ctx, credit.Pot(out.ProjectID)); n != 4*credit.Dollar-2*reserve {
		t.Errorf("available = %d, want two reserves committed", n)
	}
}

// A stranger — neither owner nor collaborator — may not open a session, so a
// funded project never becomes a way in.
func TestStrangerCannotSubmitIntoAProject(t *testing.T) {
	d, rt, _ := creditDeps(t)
	proj, err := d.Projects.Create(context.Background(), "owner-sub", "private")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	body := `{"url":"https://arxiv.org/abs/2301.03711","project":"` + proj.ID + `"}`
	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/papers", body, "stranger", "stranger@example.com"))
	if w.Code != http.StatusForbidden {
		t.Fatalf("stranger submit = %d %s, want 403", w.Code, w.Body.String())
	}
}

// may_submit is the server's answer, because the client cannot work it out — a
// public project is readable by everyone, and only the server knows membership.
// Without it the "Replicate again" form renders for a stranger browsing the
// public zone and every submit is refused.
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
	// Public, so a stranger may *read* it — which must not imply may submit.
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
