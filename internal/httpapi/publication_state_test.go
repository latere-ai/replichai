// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/seo"
)

const heldKey = "doi:10.1145/3654777.3676417"

// refusedProvisioner fails the test if a public project reaches the forge.
type refusedProvisioner struct{ t *testing.T }

func (p refusedProvisioner) Provision(context.Context, paper.Identity) (Provisioned, error) {
	p.t.Error("a repository was provisioned for a published project")
	return Provisioned{}, nil
}

// TestAPublishedProjectTakesNoPaperEditRemovalOrRepository checks decision 2
// for the three routes that change what a signed attestation covers beyond
// starting a session: editing a paper, removing a paper, and provisioning a
// repository for a local run. Each is refused with 409 project_public, and each
// works again after the owner withdraws.
func TestAPublishedProjectTakesNoPaperEditRemovalOrRepository(t *testing.T) {
	d := testDeps(allowAll)
	d.Repos = refusedProvisioner{t}
	h := NewRouter(d).Handler()
	ctx := context.Background()
	id := filed(t, d, "me", heldKey)
	if err := d.Projects.SetPublic(ctx, id, project.SignOff{By: "me", AttestationVersion: "v2"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	key := "?key=" + url.QueryEscape(heldKey)
	for _, tc := range []struct{ name, method, path, body string }{
		{"edit", http.MethodPatch, "/projects/" + id + "/papers" + key, `{"title":"Another title"}`},
		{"remove", http.MethodDelete, "/projects/" + id + "/papers" + key, ""},
		{"provision", http.MethodPost, "/projects/" + id + "/repository", `{"paper_key":"` + heldKey + `"}`},
	} {
		w := do(t, h, tc.method, tc.path, tc.body, "me")
		if w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != "project_public" {
			t.Errorf("%s on a published project = %d %s, want 409 project_public", tc.name, w.Code, w.Body)
		}
	}
	papers, _ := d.Projects.ListPapers(ctx, id)
	if len(papers) != 1 || papers[0].Title != "A paper" {
		t.Errorf("a refused route changed the papers: %+v", papers)
	}
	if err := d.Projects.SetPrivate(ctx, id, project.Withdrawal{By: "me"}); err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if w := do(t, h, http.MethodPatch, "/projects/"+id+"/papers"+key, `{"title":"Another title"}`, "me"); w.Code != http.StatusOK {
		t.Errorf("edit after the withdrawal = %d %s", w.Code, w.Body)
	}
}

// refusingJobs answers the two store calls that start a session with the error
// the job store gives for a project that changed after the handler read it.
type refusingJobs struct {
	JobStore
	err error
}

func (j refusingJobs) Enqueue(context.Context, job.Submission) (job.Job, error) {
	return job.Job{}, j.err
}

func (j refusingJobs) ImportLocal(context.Context, job.Import) (job.Job, error) {
	return job.Job{}, j.err
}

// TestTheStoresRefusalsOfANewSessionAreMapped checks a publication, a trashing
// or a finished row the store sees after the handler's own check comes back as
// the same 409 the handler gives.
func TestTheStoresRefusalsOfANewSessionAreMapped(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{job.ErrProjectPublic, "project_public"},
		{job.ErrProjectTrashed, "project_trashed"},
		{job.ErrSessionFinished, "session_finished"},
	} {
		d := testDeps(allowAll)
		id := filed(t, d, "me", heldKey)
		d.Jobs = refusingJobs{JobStore: d.Jobs, err: tc.err}
		h := NewRouter(d).Handler()

		w := do(t, h, http.MethodPost, "/projects/"+id+"/sessions/import", importedBody, "me")
		if w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != tc.code {
			t.Errorf("import refused with %v = %d %s, want 409 %s", tc.err, w.Code, w.Body, tc.code)
		}
		if errors.Is(tc.err, job.ErrSessionFinished) {
			continue // starting a session never rewrites a finished one
		}
		w = do(t, h, http.MethodPost, "/projects/"+id+"/sessions", `{"paper_key":"`+heldKey+`"}`, "me")
		if w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != tc.code {
			t.Errorf("start refused with %v = %d %s, want 409 %s", tc.err, w.Code, w.Body, tc.code)
		}
	}
}

// TestAnImportNamesARepositoryTheDeploymentCouldHavePublished checks the
// repository an imported session names: "owner/name" under the deployment's
// organization, with a plain repository name. A later publish or withdrawal
// changes the visibility of every repository a session names, so anything else
// is refused before a row is written.
func TestAnImportNamesARepositoryTheDeploymentCouldHavePublished(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	id := filed(t, d, "me", heldKey)
	for _, repo := range []string{
		"someone-else/a-paper-xyz", // another organization
		"a-paper-xyz",              // no owner
		"open-hai/",                // no name
		"open-hai/a/b",             // not one name
		"open-hai/..",              // not a repository name
		"open-hai/a paper",         // a character a repository name cannot hold
		"open-hai/" + strings.Repeat("x", 101),
	} {
		body := strings.Replace(importedBody, `"open-hai/a-paper-xyz"`, `"`+repo+`"`, 1)
		w := do(t, h, http.MethodPost, "/projects/"+id+"/sessions/import", body, "me")
		if w.Code != http.StatusBadRequest || errorCode(w.Body.Bytes()) != "bad_repo" {
			t.Errorf("import naming %q = %d %s, want 400 bad_repo", repo, w.Code, w.Body)
		}
	}
	if sessions, _ := d.Jobs.ListByProject(context.Background(), id); len(sessions) != 0 {
		t.Errorf("refused imports left %d sessions", len(sessions))
	}
	if w := do(t, h, http.MethodPost, "/projects/"+id+"/sessions/import", importedBody, "me"); w.Code != http.StatusOK {
		t.Errorf("import naming a repository under the organization = %d %s", w.Code, w.Body)
	}
}

// TestRestoreRefusesAProjectThatIsNotInTheTrash checks restore moves a project
// only out of the trash.
func TestRestoreRefusesAProjectThatIsNotInTheTrash(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	id := filed(t, d, "me", heldKey)
	w := do(t, h, http.MethodPost, "/projects/"+id+"/restore", "", "me")
	if w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != "not_trashed" {
		t.Errorf("restore of a project that is not in the trash = %d %s, want 409 not_trashed", w.Code, w.Body)
	}
}

// busyProjects is a project store that reports a session in flight on demand,
// the way the Postgres store does under the project's row lock.
func busyProjects(jobs project.SessionMover, busy *atomic.Bool) *project.MemStore {
	return project.NewMemStore(project.WithSessions(jobs),
		project.WithActiveSessions(func(context.Context, string) (bool, error) { return busy.Load(), nil }))
}

// TestTrashAndPublishAnswerASessionTheStoreSaw checks the store's refusal, made
// after the handler's own check passed, comes back as 409 sessions_running and
// changes nothing.
func TestTrashAndPublishAnswerASessionTheStoreSaw(t *testing.T) {
	d := testDeps(allowAll)
	var busy atomic.Bool
	d.Projects = busyProjects(d.Jobs.(project.SessionMover), &busy)
	h := NewRouter(d).Handler()
	ctx := context.Background()

	owner := "me"
	p, err := d.Projects.Create(ctx, owner, "A published reproduction")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	publishIntoWith(t, d.Jobs, owner, p.ID, "r1", spent())
	publishable(t, d, owner, "ada")

	busy.Store(true)
	if w := do(t, h, "POST", "/projects/"+p.ID+"/public", signOff, owner); w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != "sessions_running" {
		t.Errorf("publish with a session the store saw = %d %s, want 409 sessions_running", w.Code, w.Body)
	}
	if w := do(t, h, "DELETE", "/projects/"+p.ID, "", owner); w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != "sessions_running" {
		t.Errorf("trash with a session the store saw = %d %s, want 409 sessions_running", w.Code, w.Body)
	}
	if got, _ := d.Projects.Get(ctx, p.ID); got.Visibility != project.VisibilityPrivate || got.Trashed() {
		t.Errorf("a refused write changed the project: %+v", got)
	}
	busy.Store(false)
	if w := do(t, h, "POST", "/projects/"+p.ID+"/public", signOff, owner); w.Code != http.StatusOK {
		t.Errorf("publish once the session finished = %d %s", w.Code, w.Body)
	}
}

// TestPublishRefusesAProjectWithASessionRunning checks the handler's own check:
// a session in flight is unfinished work under a signed attestation.
func TestPublishRefusesAProjectWithASessionRunning(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	ctx := context.Background()
	id := filed(t, d, "me", heldKey)
	publishable(t, d, "me", "ada")
	if _, err := d.Jobs.Enqueue(ctx, job.Submission{
		Principal: "me", Ref: "10.1145/3654777.3676417", Kind: job.KindAudit, Project: id, PaperKey: heldKey,
	}); err != nil {
		t.Fatalf("start a session: %v", err)
	}
	w := do(t, h, "POST", "/projects/"+id+"/public", signOff, "me")
	if w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != "sessions_running" {
		t.Errorf("publish with a session running = %d %s, want 409 sessions_running", w.Code, w.Body)
	}
}

// TestAPublishedSignOffKeepsItsSignerLink checks the handle a sign-off was
// signed under stays the signer link when the signer later makes their profile
// private, and that the sitemap, which lists pages, stops naming the page.
func TestAPublishedSignOffKeepsItsSignerLink(t *testing.T) {
	d := pricedDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := publicProject(t, d, h, "owner")

	signer := func() string {
		w := do(t, h, "GET", "/projects/"+p.ID, "", "")
		var detail ProjectDetail
		if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != http.StatusOK {
			t.Fatalf("read the project = %d %v", w.Code, err)
		}
		return detail.SignerHandle
	}
	if got := signer(); got != "ada" {
		t.Fatalf("signer link before = %q, want ada", got)
	}
	if w := do(t, h, "PATCH", "/me/profile", `{"public":false}`, "owner"); w.Code != http.StatusOK {
		t.Fatalf("make the profile private = %d %s", w.Code, w.Body)
	}
	if got := signer(); got != "ada" {
		t.Errorf("signer link after the profile went private = %q, want it kept", got)
	}
	site := do(t, h, "GET", "/sitemap.xml", "", "")
	if strings.Contains(site.Body.String(), seo.ProfilePath("ada")) {
		t.Errorf("the sitemap names a researcher page that is no longer public:\n%s", site.Body)
	}
}

// TestEveryWriteThatChangesTheListingClearsItsCache checks the anonymous
// listing is rebuilt at once after a rename and after a profile goes private,
// not left for the cache to expire. Withdraw, trash and restore are covered by
// the tests of those routes.
func TestEveryWriteThatChangesTheListingClearsItsCache(t *testing.T) {
	d := pricedDeps(allowAll)
	h := NewRouter(d).Handler()
	p, _ := publicProject(t, d, h, "owner")

	listed := func() ProjectView {
		for _, v := range listOf(t, h, "/public", Identity{}) {
			if v.ID == p.ID {
				return v
			}
		}
		t.Fatalf("project %s is not in the public listing", p.ID)
		return ProjectView{}
	}
	if got := listed(); got.OwnerHandle != "ada" || got.Title != "A published reproduction" {
		t.Fatalf("listing before = %+v", got)
	}
	if w := do(t, h, "PATCH", "/projects/"+p.ID, `{"title":"Renamed"}`, "owner"); w.Code != http.StatusOK {
		t.Fatalf("rename = %d %s", w.Code, w.Body)
	}
	if got := listed(); got.Title != "Renamed" {
		t.Errorf("the listing shows %q after a rename, want Renamed", got.Title)
	}
	if w := do(t, h, "PATCH", "/me/profile", `{"public":false}`, "owner"); w.Code != http.StatusOK {
		t.Fatalf("make the profile private = %d %s", w.Code, w.Body)
	}
	if got := listed(); got.OwnerHandle != "" {
		t.Errorf("the listing links %q after the owner's profile went private", got.OwnerHandle)
	}
}

// TestTheSitemapIsHeldNoLongerThanTheServerHoldsIt checks the crawler's reuse
// time is the server's own cache lifetime.
func TestTheSitemapIsHeldNoLongerThanTheServerHoldsIt(t *testing.T) {
	h := NewRouter(testDeps(allowAll)).Handler()
	w := do(t, h, "GET", "/sitemap.xml", "", "")
	if got := w.Header().Get("Cache-Control"); got != "public, max-age=30" {
		t.Errorf("Cache-Control = %q, want public, max-age=30 (publicTTL)", got)
	}
}

// lookupCounter answers every lookup the same way and counts them.
type lookupCounter struct {
	calls atomic.Int32
	err   error
}

func (c *lookupCounter) Resolve(context.Context, string) (ResolvedPaper, error) {
	c.calls.Add(1)
	return ResolvedPaper{}, c.err
}

// TestTheSweepWaitsAfterALookupFoundNothing checks a paper no source knows is
// fetched once, not on every pass, and that a source that could not be reached
// is tried again.
func TestTheSweepWaitsAfterALookupFoundNothing(t *testing.T) {
	d := testDeps(allowAll)
	nothing := &lookupCounter{}
	d.Resolver = nothing
	rt := NewRouter(d)
	filed(t, d, "me", heldKey)

	for pass := 1; pass <= 2; pass++ {
		if _, _, err := rt.SweepPapers(context.Background()); err != nil {
			t.Fatalf("sweep %d: %v", pass, err)
		}
	}
	if n := nothing.calls.Load(); n != 1 {
		t.Errorf("a paper nothing knows was looked up %d times in two sweeps, want 1", n)
	}

	down := &lookupCounter{err: errors.New("source unreachable")}
	d2 := testDeps(allowAll)
	d2.Resolver = down
	rt2 := NewRouter(d2)
	filed(t, d2, "me", heldKey)
	for pass := 1; pass <= 2; pass++ {
		if _, _, err := rt2.SweepPapers(context.Background()); err != nil {
			t.Fatalf("sweep %d: %v", pass, err)
		}
	}
	if n := down.calls.Load(); n != 2 {
		t.Errorf("a paper whose source was down was looked up %d times in two sweeps, want 2", n)
	}
}

// TestTheOwnersMembershipRowIsNotRemovedOrDemoted checks the owner cannot drop
// or demote their own lead row through the member routes.
func TestTheOwnersMembershipRowIsNotRemovedOrDemoted(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	owner := Identity{Sub: "owner-sub", Email: "owner@x.com"}
	p, _ := d.Projects.Create(context.Background(), owner.Sub, "Mine")

	if w := doAs(t, h, "DELETE", "/projects/"+p.ID+"/members/owner@x.com", "", owner); w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != "owner_member" {
		t.Errorf("removing the owner = %d %s, want 409 owner_member", w.Code, w.Body)
	}
	if w := doAs(t, h, "POST", "/projects/"+p.ID+"/members", `{"email":"Owner@x.com","role":"collaborator"}`, owner); w.Code != http.StatusConflict || errorCode(w.Body.Bytes()) != "owner_member" {
		t.Errorf("demoting the owner = %d %s, want 409 owner_member", w.Code, w.Body)
	}
	if role, ok, _ := d.Members.Role(context.Background(), access.Principal{Sub: owner.Sub, Email: owner.Email}, p.ID); ok && role != access.RoleLead {
		t.Errorf("the owner's role is %s after a refused change", role)
	}
}
