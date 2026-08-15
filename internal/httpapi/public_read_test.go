package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
)

// 028: a reproduction is published so somebody can check it, and until now
// nobody could without an account. These tests drive the reads with **no
// identity at all** and behind a gate that refuses everyone, so passing them
// means the route is genuinely outside the gate rather than merely reachable by
// whoever the test happened to be.

// denyAll refuses every gated request, which is what makes "this route is open"
// a fact rather than an artefact of a permissive test gate.
var denyAll = GateFunc(func(*http.Request) (bool, string) { return false, "no_access" })

// closedTo returns a second router over the same stores whose gate refuses
// everyone. Publishing a project is a gated write, so the fixture is built
// through a permissive router and then read through this one — which is what
// makes a 200 here mean "outside the gate" and nothing else.
func closedTo(d Deps) http.Handler {
	closed := d
	closed.Gate = denyAll
	return NewRouter(closed).Handler()
}

// publicProject builds a published project with one finished session under it,
// and returns both. The publish flip needs a public profile, exactly as 006r
// requires of a real signer.
func publicProject(t *testing.T, d Deps, h http.Handler, owner string) (project.Project, job.Job) {
	t.Helper()
	p, err := d.Projects.Create(context.Background(), owner, "A published reproduction")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sess := publishInto(t, d.Jobs, owner, p.ID, "r1")
	publishable(t, d, owner, "ada")
	if w := do(t, h, "POST", "/projects/"+p.ID+"/public", signOff, owner); w.Code != http.StatusOK {
		t.Fatalf("publish: status %d (%s)", w.Code, w.Body.String())
	}
	got, _ := d.Projects.Get(context.Background(), p.ID)
	return got, sess
}

// AC6: the open set is exactly what 028 decided, asserted whole. A tenth open
// route is then a line somebody adds to this list on purpose, not one that slips
// past review inside a diff about something else.
func TestTheOpenSetIsExactlyWhatWasDecided(t *testing.T) {
	rt := NewRouter(testDeps(denyAll))
	want := map[string]bool{
		"GET /healthz":                true,
		"GET /profiles/{handle}":      true,
		"POST /stripe/webhook":        true,
		"GET /public":                 true,
		"GET /projects/{id}":          true,
		"GET /projects/{id}/papers":   true,
		"GET /jobs/{id}":              true,
		"GET /jobs/{id}/transcript":   true,
		"GET /jobs/{id}/reproduction": true,
		"GET /jobs/{id}/verdict":      true,
		"GET /jobs/{id}/artifacts":    true,
		"GET /jobs/{id}/artifact":     true,
	}
	got := map[string]bool{}
	for _, r := range rt.OpenRoutes() {
		got[r.Method+" "+r.Pattern] = true
	}
	for k := range want {
		if !got[k] {
			t.Errorf("%q was decided open and is not", k)
		}
	}
	for k := range got {
		// The SPA fallback is registered open only when a frontend is embedded;
		// testDeps embeds none, so it never appears here.
		if !want[k] {
			t.Errorf("%q is open and was not decided to be", k)
		}
	}
}

// AC1: signed out, every read a public project permits answers 200 — and the
// same reads of a private project answer 404, which hides it rather than
// confirming it exists.
func TestASignedOutReaderReadsAPublicProject(t *testing.T) {
	d := testDeps(allowAll)
	d.Reproductions.(*fakeRepro).findings = validFindings(t)
	pub, sess := publicProject(t, d, NewRouter(d).Handler(), "owner")
	priv, _ := d.Projects.Create(context.Background(), "owner", "Not published")
	privSess := publishInto(t, d.Jobs, "owner", priv.ID, "r1")
	h := closedTo(d)

	for _, path := range []string{
		"/public",
		"/projects/" + pub.ID,
		"/projects/" + pub.ID + "/papers",
		"/jobs/" + sess.ID,
		"/jobs/" + sess.ID + "/transcript",
		"/jobs/" + sess.ID + "/reproduction",
		"/jobs/" + sess.ID + "/verdict",
		"/jobs/" + sess.ID + "/artifacts",
		"/jobs/" + sess.ID + "/artifact?path=README.md",
	} {
		if w := do(t, h, "GET", path, "", ""); w.Code != http.StatusOK {
			t.Errorf("signed out GET %s = %d (%s), want 200", path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{
		"/projects/" + priv.ID,
		"/projects/" + priv.ID + "/papers",
		"/jobs/" + privSess.ID,
		"/jobs/" + privSess.ID + "/transcript",
		"/jobs/" + privSess.ID + "/verdict",
	} {
		if w := do(t, h, "GET", path, "", ""); w.Code != http.StatusNotFound {
			t.Errorf("signed out GET %s = %d, want 404", path, w.Code)
		}
	}
	// The zone lists the published project and not the private one.
	var zone []ProjectView
	w := do(t, h, "GET", "/public", "", "")
	_ = json.Unmarshal(w.Body.Bytes(), &zone)
	if len(zone) != 1 || zone[0].ID != pub.ID {
		t.Errorf("signed-out public zone = %+v", zone)
	}
}

// AC2: the four reads that stay behind the gate stay behind it — the caller's
// own job list, the live stream, the member roll, and the archive.
func TestTheGatedReadsStayGated(t *testing.T) {
	d := testDeps(allowAll)
	pub, sess := publicProject(t, d, NewRouter(d).Handler(), "owner")
	h := closedTo(d)

	for _, path := range []string{
		"/jobs",
		"/jobs/" + sess.ID + "/events",
		"/projects/" + pub.ID + "/members",
		"/projects/" + pub.ID + "/archive",
	} {
		if w := do(t, h, "GET", path, "", ""); w.Code != http.StatusForbidden {
			t.Errorf("signed out GET %s = %d, want 403", path, w.Code)
		}
	}
}

// AC3: an unauthenticated caller is nobody. The reads are open now, so the two
// ways an empty principal could accidentally match somebody are reachable from
// the internet: an owner column that is empty, and a membership row seeded
// against an email nobody has claimed.
func TestAnonymousIsNobody(t *testing.T) {
	d := testDeps(allowAll)
	h := closedTo(d)
	ctx := context.Background()

	// The store refuses an ownerless project outright, which is the first reason
	// `"" == ""` can never be ownership. The guard in mayViewProject is the
	// second, and it is the one that holds if a row ever arrives from anywhere
	// but Create — a migration, a restore, a hand-edited database.
	if _, err := d.Projects.Create(ctx, "", "Owned by nobody"); err == nil {
		t.Error("the store accepted a project with no owner")
	}

	// A membership seeded against an email, read by a caller with no sub and no
	// email. An empty identity must match no row.
	shared, _ := d.Projects.Create(ctx, "lead", "Shared, not published")
	lead := access.Principal{Sub: "lead", Email: "lead@example.org"}
	if err := d.Members.SeedLead(ctx, shared.ID, lead); err != nil {
		t.Fatalf("seed lead: %v", err)
	}
	if err := d.Members.Add(ctx, lead, shared.ID, "invitee@example.org", access.RoleCollaborator); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if w := do(t, h, "GET", "/projects/"+shared.ID, "", ""); w.Code != http.StatusNotFound {
		t.Errorf("anonymous read of a shared project = %d, want 404", w.Code)
	}
	// And an empty-email membership row, if one is ever seeded, does not become
	// everybody's.
	if err := d.Members.Add(ctx, lead, shared.ID, "", access.RoleCollaborator); err == nil {
		if w := do(t, h, "GET", "/projects/"+shared.ID, "", ""); w.Code != http.StatusNotFound {
			t.Errorf("anonymous read against an empty-email membership = %d, want 404", w.Code)
		}
	}
	// The invitee, once bound, still reads it — the guard refuses nobody, not
	// everybody.
	if w := doAs(t, h, "GET", "/projects/"+shared.ID, "", Identity{Sub: "invitee", Email: "invitee@example.org"}); w.Code != http.StatusOK {
		t.Errorf("the invited collaborator = %d, want 200", w.Code)
	}
}

// AC4/AC5: a reader with no stake gets the reproduction and not the wallet. The
// assertions are on the bytes, because the point is what leaves the process —
// a zeroed field that still serialises is still a field on the wire.
func TestAPublicReaderIsNotToldWhoOwnsItOrWhatItCost(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	pub, sess := publicProject(t, d, h, "owner-principal-sub")

	for _, path := range []string{"/public", "/projects/" + pub.ID, "/jobs/" + sess.ID} {
		body := do(t, h, "GET", path, "", "").Body.String()
		for _, leak := range []string{"owner-principal-sub", `"owner"`, `"principal"`, `"cost"`, `"spend_30d"`, `"signed_off_by"`} {
			if strings.Contains(body, leak) {
				t.Errorf("signed out GET %s carries %s: %s", path, leak, body)
			}
		}
	}
	// What was published is still published: the signer, by the name they chose.
	for _, path := range []string{"/public", "/projects/" + pub.ID} {
		body := do(t, h, "GET", path, "", "").Body.String()
		if !strings.Contains(body, "Ada Lovelace") || !strings.Contains(body, "ada@example.org") {
			t.Errorf("signed out GET %s does not name who signed it: %s", path, body)
		}
	}

	// A signed-in stranger reads exactly what a signed-out one does: browsing the
	// registry is not a stake in the project.
	stranger := do(t, h, "GET", "/projects/"+pub.ID, "", "somebody-else").Body.String()
	if strings.Contains(stranger, `"cost"`) || strings.Contains(stranger, `"owner"`) {
		t.Errorf("a signed-in stranger reads the owner or the cost: %s", stranger)
	}

	// The owner reads all of it, including a genuine zero cost — a session that
	// has spent nothing says so rather than saying nothing.
	owned := do(t, h, "GET", "/projects/"+pub.ID, "", "owner-principal-sub").Body.String()
	for _, want := range []string{`"owner"`, `"principal"`, `"cost"`, `"signed_off_by"`} {
		if !strings.Contains(owned, want) {
			t.Errorf("the owner's read is missing %s: %s", want, owned)
		}
	}
	// The spend lives on the list row, not the detail — so that is where the
	// owner's copy of it is asserted.
	if list := do(t, h, "GET", "/public", "", "owner-principal-sub").Body.String(); !strings.Contains(list, `"spend_30d"`) {
		t.Errorf("the owner's list row is missing the spend: %s", list)
	}
	var detail ProjectDetail
	if err := json.Unmarshal([]byte(owned), &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(detail.Sessions) != 1 || detail.Sessions[0].Cost == nil || detail.Sessions[0].Principal != "owner-principal-sub" {
		t.Errorf("the owner's session view = %+v", detail.Sessions)
	}
}
