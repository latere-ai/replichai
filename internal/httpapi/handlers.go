package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/notify"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/profile"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/verdict"
)

// Identity is the authenticated caller as the auth middleware (006c) resolved
// it: the stable subject and the IdP-authenticated email. The gate consumes
// these and nothing else. This package only reads the identity off the request;
// who a principal *is* and what they may do stays 006c's concern.
//
// Name and Picture are display claims (006m), rendered and never consulted by
// any authorisation decision. Both may be empty.
type Identity struct {
	Sub     string
	Email   string
	Name    string
	Picture string
}

// identityKey carries the Identity on the request context.
type ctxKey int

const identityKey ctxKey = iota

// WithIdentity returns a context carrying the authenticated identity.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// IdentityFrom reads the authenticated identity, ok=false when unauthenticated.
func IdentityFrom(r *http.Request) (Identity, bool) {
	id, ok := r.Context().Value(identityKey).(Identity)
	return id, ok
}

// WithPrincipal carries a subject-only identity, for callers (the dev bypass,
// tests) that have just a sub.
func WithPrincipal(ctx context.Context, sub string) context.Context {
	return WithIdentity(ctx, Identity{Sub: sub})
}

// principal reads the authenticated subject, empty when unauthenticated.
func principal(r *http.Request) string {
	id, _ := IdentityFrom(r)
	return id.Sub
}

// routes registers every endpoint through the two registration helpers, so the
// gated and open sets come from one place and cannot drift. The open routes
// (sign-in, /me) are 006c's to add; this spec owns the operator set.
func (rt *Router) routes() {
	// A health check sits outside the gate so a load balancer can reach it; the
	// sign-in and /me routes join the open set with 006c.
	rt.openHandle("GET", "/healthz", rt.health)
	// A researcher's page is readable by anyone, signed in or not (006r) — the
	// same openness the public zone has, for the person behind it.
	rt.openHandle("GET", "/profiles/{handle}", rt.publicProfile)
	// The public zone, and everything a public project already permits anyone to
	// read (028). mayViewProject has admitted any caller to a public project
	// since 006f; until now the gate refused the request before it could. A
	// reader with no stake gets a Redacted copy — see redaction, below.
	//
	// Deliberately still gated: GET /jobs (caller-scoped by construction),
	// /jobs/{id}/events (a live stream nobody published needs), the member list
	// (people who did not publish themselves), and the archive (a whole
	// repository, streamed to an unauthenticated caller).
	rt.openHandle("GET", "/public", rt.listPublic)
	rt.openHandle("GET", "/projects/{id}", rt.getProject)
	rt.openHandle("GET", "/projects/{id}/papers", rt.listProjectPapers)
	rt.openHandle("GET", "/jobs/{id}", rt.getJob)
	rt.openHandle("GET", "/jobs/{id}/transcript", rt.getSessionTranscript)
	rt.openHandle("GET", "/jobs/{id}/reproduction", rt.getSessionReproduction)
	rt.openHandle("GET", "/jobs/{id}/verdict", rt.getSessionFindings)
	rt.openHandle("GET", "/jobs/{id}/artifacts", rt.listSessionArtifacts)
	rt.openHandle("GET", "/jobs/{id}/artifact", rt.getSessionArtifact)
	// The payment processor's webhook carries no session cookie; its signature is
	// its authentication (022b). It sits outside the operator gate deliberately —
	// still enumerated in OpenRoutes, so the choice is visible, not accidental.
	rt.openHandle("POST", "/stripe/webhook", rt.stripeWebhook)
	rt.gatedHandle("GET", "/me/profile", rt.getProfile)
	rt.gatedHandle("PATCH", "/me/profile", rt.saveProfile)
	rt.gatedHandle("POST", "/resolve", rt.resolvePaper)
	rt.gatedHandle("POST", "/projects", rt.createProject)
	rt.gatedHandle("POST", "/projects/{id}/papers", rt.addProjectPaper)
	rt.gatedHandle("DELETE", "/projects/{id}/papers", rt.removeProjectPaper)
	rt.gatedHandle("POST", "/projects/{id}/sessions", rt.startSession)
	rt.gatedHandle("PATCH", "/projects/{id}", rt.renameProject)
	rt.gatedHandle("GET", "/projects", rt.listProjects)
	// The literal segment wins over {id} in Go's mux, so the trash screen is
	// reachable at a path that also looks like a project id. router_test asserts
	// it rather than trusting the precedence.
	rt.gatedHandle("GET", "/projects/trash", rt.listTrash)
	rt.gatedHandle("DELETE", "/projects/{id}", rt.trashProject)
	rt.gatedHandle("POST", "/projects/{id}/restore", rt.restoreProject)
	rt.gatedHandle("GET", "/projects/{id}/archive", rt.archiveProject)
	rt.gatedHandle("POST", "/projects/{id}/public", rt.makeProjectPublic)
	rt.gatedHandle("POST", "/projects/{id}/private", rt.withdrawProject)
	rt.gatedHandle("GET", "/me/credits", rt.myCredits)
	rt.gatedHandle("POST", "/me/credits/checkout", rt.startCheckout)
	rt.gatedHandle("POST", "/projects/{id}/credits", rt.allocateCredits)
	rt.gatedHandle("POST", "/projects/{id}/credits/reclaim", rt.reclaimCredits)
	rt.gatedHandle("GET", "/projects/{id}/members", rt.listMembers)
	rt.gatedHandle("POST", "/projects/{id}/members", rt.addMember)
	rt.gatedHandle("DELETE", "/projects/{id}/members/{email}", rt.removeMember)
	rt.gatedHandle("GET", "/jobs", rt.listJobs)
	rt.gatedHandle("POST", "/jobs/{id}/cancel", rt.cancelSession)
	rt.gatedHandle("GET", "/jobs/{id}/events", rt.jobEvents)

	// The frontend shell is public and serves any GET path not matched above,
	// so a deep link loads the app and lets it route (006e). More specific API
	// patterns take precedence; the app's data calls are still gated.
	if rt.spa != nil {
		rt.openHandle("GET", "/", rt.spa.ServeHTTP)
	}
}

// writeReadError maps a ReproReader error to a status: a genuine miss is 404, a
// transport or backend failure is 500, so a canceled request is never disguised
// as not-found.
func writeReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrReproNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeError(w, http.StatusInternalServerError, "registry_unavailable")
}

// health is the unauthenticated liveness check.
func (rt *Router) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

/* ─── The papers a project holds, and starting a session on one (012) ─── */

// PaperView is a paper as the project screen reads it: the record, plus what has
// happened to it. The session count and latest phase are the join to the job
// store through the paper key both sides already carry — a paper with no
// sessions is the ordinary state of one nobody has started yet.
type PaperView struct {
	project.Paper
	Sessions    int    `json:"sessions"`
	LatestPhase string `json:"latest_phase,omitempty"`
}

// listProjectPapers returns the papers a project is about, each with what has
// been run against it.
func (rt *Router) listProjectPapers(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForRead(w, r)
	if !ok {
		return
	}
	papers, err := rt.projects.ListPapers(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	out := make([]PaperView, 0, len(papers))
	for _, p := range papers {
		v := PaperView{Paper: p}
		for _, j := range sessions {
			if j.PaperKey != p.Key {
				continue
			}
			v.Sessions++
			// ListByProject is newest-first, so the first match is the latest.
			if v.LatestPhase == "" {
				v.LatestPhase = string(j.Phase)
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// addProjectPaper records another paper on a project. It starts nothing: adding
// a paper and running it are two acts since 012, and this is the first.
func (rt *Router) addProjectPaper(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForWork(w, r)
	if !ok {
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeError(w, http.StatusBadRequest, "missing_url")
		return
	}
	key, err := paper.Key(body.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unresolvable_paper")
		return
	}
	rec := project.Paper{Key: key, Ref: body.URL, AddedBy: principal(r)}
	switch err := rt.projects.AddPaper(r.Context(), proj.ID, rec); {
	case errors.Is(err, project.ErrPaperExists):
		writeError(w, http.StatusConflict, "paper_exists")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "add_paper_failed")
		return
	}
	added := PaperResult{Ref: body.URL, Key: key}
	// No placeholder: adding a paper to a project that already exists fills in
	// that paper's title and never touches the project's own name.
	rt.background(func(ctx context.Context) { rt.enrichTitles(ctx, proj.ID, "", []PaperResult{added}) })
	writeJSON(w, http.StatusCreated, added)
}

// removeProjectPaper drops a paper a project has never run.
//
// Once a paper has a session it stays: the record of what was actually
// reproduced is not editable, and every paper the 012 migration backfilled has a
// session by definition — so nothing that predates this endpoint can be removed
// through it. Removing is the lead's, matching who may move credits out.
func (rt *Router) removeProjectPaper(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForWork(w, r)
	if !ok {
		return
	}
	// An owner leads their own project by definition; isLead covers the case
	// where a collaborator was promoted to lead.
	if proj.Owner != principal(r) && !rt.isLead(r, proj.ID) {
		writeError(w, http.StatusForbidden, "not_lead")
		return
	}
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing_key")
		return
	}
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	for _, j := range sessions {
		if j.PaperKey == key {
			writeError(w, http.StatusConflict, "paper_has_sessions")
			return
		}
	}
	if err := rt.projects.RemovePaper(r.Context(), proj.ID, key); err != nil {
		writeError(w, http.StatusInternalServerError, "remove_paper_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// startSession runs one paper the project already holds.
//
// This is where every refusal a create used to report per paper now lives,
// singly: the double-trigger guard (409), the project pot (402), and a key the
// project does not hold (404). One act, one answer — and the caller is looking at
// the project whose credits are being spent, which is the one screen where "this
// project is out of credits" names something they can act on.
//
// The paper key travels in the body, never the path: paper.Key returns
// "doi:10.1145/3544548.3581433" and "url:https://…", so a key spans several path
// segments and would route nowhere.
func (rt *Router) startSession(w http.ResponseWriter, r *http.Request) {
	proj, ok := rt.projectForWork(w, r)
	if !ok {
		return
	}
	var body struct {
		PaperKey string `json:"paper_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	key := strings.TrimSpace(body.PaperKey)
	if key == "" {
		writeError(w, http.StatusBadRequest, "missing_key")
		return
	}
	papers, err := rt.projects.ListPapers(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	var held *project.Paper
	for i := range papers {
		if papers[i].Key == key {
			held = &papers[i]
			break
		}
	}
	// A session may only run a paper the project is about. Without this a caller
	// could spend a project's credits on anything at all.
	if held == nil {
		writeError(w, http.StatusNotFound, "paper_not_found")
		return
	}
	j, err := rt.jobs.Enqueue(r.Context(), job.Submission{
		Principal: principal(r), Ref: held.Ref, Kind: job.KindAudit, Project: proj.ID, PaperKey: held.Key,
	})
	if err != nil {
		rt.writeEnqueueError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"job_id": j.ID, "project_id": proj.ID})
}

// projectForWork loads the project named in the path and refuses a caller who
// may not work in it — the one gate every paper and session write shares, so no
// route can forget it.
func (rt *Router) projectForWork(w http.ResponseWriter, r *http.Request) (project.Project, bool) {
	proj, err := rt.projects.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project_not_found")
		return project.Project{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return project.Project{}, false
	}
	if !rt.mayWorkInProject(r, proj, principal(r)) {
		writeError(w, http.StatusForbidden, "not_owner")
		return project.Project{}, false
	}
	// A project in the trash is readable by its owner and writable by nobody
	// (019). It is refused here, at the one gate every working route passes, so
	// starting a session, adding a paper, moving credits and publishing are all
	// closed by one check rather than five that have to be remembered.
	if proj.Trashed() {
		writeError(w, http.StatusConflict, "project_trashed")
		return project.Project{}, false
	}
	return proj, true
}

// projectForRead is projectForWork's read counterpart: a public project is open
// to anyone signed in, so listing what a project is about follows the same rule
// as listing its sessions.
func (rt *Router) projectForRead(w http.ResponseWriter, r *http.Request) (project.Project, bool) {
	proj, err := rt.projects.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project_not_found")
		return project.Project{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return project.Project{}, false
	}
	// Hidden, not refused — the same rule getProject follows. It mattered less
	// while the route was gated; open (028), a 403 here would confirm to anybody
	// walking ids that a private project exists.
	if !rt.mayViewProject(r, proj) {
		writeError(w, http.StatusNotFound, "project_not_found")
		return project.Project{}, false
	}
	return proj, true
}

// stakeIn reports whether this caller has a stake in a project — its owner or a
// collaborator it was shared with. It is the one predicate every redaction keys
// on (028), and the same one that already withholds the credit balance, so
// "inside this project" has a single answer per request rather than one per
// field. Note what it is not: reading a public project confers no stake, so a
// signed-in stranger browsing the registry sees exactly what a signed-out one
// does.
func (rt *Router) stakeIn(r *http.Request, p project.Project) bool {
	return rt.mayWorkInProject(r, p, principal(r))
}

// mayWorkInProject reports whether a caller may open a session in a project:
// its owner, or a collaborator it was shared with. Reading a public project
// does not confer this — spending a project's credits is not a read.
func (rt *Router) mayWorkInProject(r *http.Request, p project.Project, sub string) bool {
	// Nobody is not an owner and not a member. The read routes are open since
	// 028, so this predicate now answers for unauthenticated callers too — and
	// it decides both what may be written and what may be read of the money.
	if sub == "" {
		return false
	}
	if p.Owner == sub {
		return true
	}
	if rt.members == nil {
		return false
	}
	id, _ := IdentityFrom(r)
	_, ok, _ := rt.members.Role(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email}, p.ID)
	return ok
}

// writeEnqueueError maps an Enqueue failure to a status: the double-trigger
// guard is a 409, an empty reference a 400, anything else a 500.
func (rt *Router) writeEnqueueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, job.ErrActiveSession):
		writeError(w, http.StatusConflict, "active_session")
	case errors.Is(err, job.ErrNoCredits):
		writeError(w, http.StatusPaymentRequired, "no_credits")
	case errors.Is(err, job.ErrEmptyRef):
		writeError(w, http.StatusBadRequest, "missing_url")
	default:
		writeError(w, http.StatusInternalServerError, "enqueue_failed")
	}
}

// defaultTitle is the offline placeholder a new project is born with — the paper
// reference itself. The worker enriches it with the resolved paper title once
// its Resolve phase has run.
func defaultTitle(url string) string { return strings.TrimSpace(url) }

// resolvePaper previews a reference against the source (006j): it returns the
// resolved identity so the New-project screen shows the real title before
// anything is created. A bad reference is a 400; an unreachable source is a 502,
// kept distinct so the UI can say "couldn't reach the source."
func (rt *Router) resolvePaper(w http.ResponseWriter, r *http.Request) {
	if rt.resolver == nil {
		writeError(w, http.StatusNotImplemented, "resolve_unavailable")
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeError(w, http.StatusBadRequest, "missing_url")
		return
	}
	got, err := rt.resolver.Resolve(r.Context(), body.URL)
	if errors.Is(err, ErrUnresolvable) {
		writeError(w, http.StatusBadRequest, "unresolvable_paper")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "resolve_failed") // the source could not be reached
		return
	}
	writeJSON(w, http.StatusOK, got)
}

// PaperResult reports one reference's fate in a create: the paper it was
// recorded as, or why it was not recorded at all. Since 012 a create records
// papers and starts nothing, so the only thing that can go wrong per reference
// is the reference itself — there is no enqueue here to fail, and no credits to
// run out of.
type PaperResult struct {
	Ref   string `json:"ref"`
	Key   string `json:"key,omitempty"`
	Title string `json:"title,omitempty"`
	Error string `json:"error,omitempty"`
}

// createProject opens a project with a chosen title, the papers it is about, and
// seeded collaborators (006j, reshaped by 012).
//
// It records; it does not run. Nothing is enqueued and nothing is spent, which
// is what lets an account with an empty wallet name a project, write down what it
// intends to reproduce, and invite people to it — all of which the funding-at-
// create rule used to refuse with a 402 at the door. The credit question is asked
// once, later, by startSession, where there is a project pot on screen to answer
// it with.
//
// Because nothing is spent there is nothing to compensate for: 006j's "never
// leave an empty project" rule, its 422, and the undo delete all go with the
// enqueue that needed them. A project with no papers is simply a project nobody
// has added a paper to yet.
func (rt *Router) createProject(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r)
	if !ok {
		writeError(w, http.StatusForbidden, "not_granted")
		return
	}
	var body struct {
		Title         string   `json:"title"`
		Papers        []string `json:"papers"`
		Collaborators []string `json:"collaborators"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	papers := nonEmpty(body.Papers)
	if len(papers) == 0 {
		writeError(w, http.StatusBadRequest, "missing_papers")
		return
	}
	// placeholder is the stand-in a project gets when nobody names it — and the
	// only title enrichment may replace. A project somebody named is theirs.
	title, placeholder := strings.TrimSpace(body.Title), ""
	if title == "" {
		placeholder = defaultTitle(papers[0])
		title = placeholder
	}

	proj, err := rt.projects.Create(r.Context(), id.Sub, title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_create_failed")
		return
	}

	results := make([]PaperResult, 0, len(papers))
	for _, ref := range papers {
		results = append(results, rt.recordPaper(r, proj.ID, id.Sub, ref))
	}
	// Titles land after the answer: a create must not wait on somebody else's
	// web server, and the papers are already recorded without them.
	rt.background(func(ctx context.Context) { rt.enrichTitles(ctx, proj.ID, placeholder, results) })

	members := rt.seedCollaborators(r.Context(), proj.ID, id, body.Collaborators)

	writeJSON(w, http.StatusCreated, map[string]any{
		"project_id": proj.ID,
		"title":      title,
		"papers":     results,
		"members":    members,
	})
}

// recordPaper writes one reference onto a project, reporting what became of it.
// paper.Key is offline, so a create costs no network and cannot half-fail on a
// slow source — a reference that is not a DOI, arXiv ID, or URL is the only
// thing that can be refused here.
func (rt *Router) recordPaper(r *http.Request, projectID, principal, ref string) PaperResult {
	res := PaperResult{Ref: ref}
	key, err := paper.Key(ref)
	if err != nil {
		res.Error = "unresolvable_paper"
		return res
	}
	res.Key = key
	switch err := rt.projects.AddPaper(r.Context(), projectID, project.Paper{Key: key, Ref: ref, AddedBy: principal}); {
	case err == nil, errors.Is(err, project.ErrPaperExists):
		// A repeated reference in one batch is the same paper named twice, not a
		// failure: the project is about it either way.
	default:
		res.Error = "add_paper_failed"
	}
	return res
}

// enrichTitles resolves each recorded paper against its source and writes back
// what it learns: the paper's own title, and the project's title while that is
// still the placeholder it was created with.
//
// It runs *after* the response, on a context of its own. Blocking a create on N
// network fetches is exactly the half-failure paper.Key was chosen to avoid — a
// four-paper create would wait on the slowest source, and a client that timed
// out would be left with the papers recorded and nothing to show for it.
// Nothing here is load-bearing: every title it writes, the first session's
// Resolve phase would write anyway.
//
// The client's own preview is never trusted onto the record. The New-project
// screen has already resolved these papers for its display, but a title stored
// against a project is a record, and records are the server's to write.
func (rt *Router) enrichTitles(ctx context.Context, projectID, placeholder string, papers []PaperResult) {
	if rt.resolver == nil {
		return
	}
	titles := make([]string, len(papers))
	var wg sync.WaitGroup
	for i := range papers {
		if papers[i].Error != "" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := rt.resolver.Resolve(ctx, papers[i].Ref)
			if err != nil || strings.TrimSpace(got.Title) == "" {
				return
			}
			titles[i] = got.Title
			_ = rt.projects.SetPaperTitle(ctx, projectID, papers[i].Key, got.Title)
		}(i)
	}
	wg.Wait()

	// The project takes the first title that resolved, not necessarily the first
	// paper's: a project whose lead paper sits on an unreachable source should
	// still end up named after something a person can read. It only ever
	// replaces the placeholder, so a project somebody named keeps its name.
	for _, title := range titles {
		if title == "" {
			continue
		}
		if placeholder == "" {
			return
		}
		if p, err := rt.projects.Get(ctx, projectID); err == nil && p.Title == placeholder {
			_ = rt.projects.SetTitle(ctx, projectID, title)
		}
		return
	}
}

// background runs work that outlives the request which scheduled it, on a
// context of its own so a client hanging up cannot cancel a write that is
// already half done. The WaitGroup is what lets a test wait for it.
func (rt *Router) background(f func(context.Context)) {
	rt.bg.Add(1)
	go func() {
		defer rt.bg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), backgroundWork)
		defer cancel()
		f(ctx)
	}()
}

// WaitBackground blocks until every background task scheduled so far has
// finished. Tests use it; nothing in the serving path does.
func (rt *Router) WaitBackground() { rt.bg.Wait() }

// backgroundWork bounds any after-the-response work. It is generous: the only
// thing running under it is best-effort enrichment nobody is waiting on.
const backgroundWork = 30 * time.Second

// seedCollaborators seeds the owner as lead (idempotent) and adds each
// collaborator email as a member, best-effort. It returns the emails that were
// added — a failure to seed one collaborator is reported by omission, never
// fatal to the create.
func (rt *Router) seedCollaborators(ctx context.Context, projectID string, owner Identity, emails []string) []string {
	if rt.members == nil {
		return nil
	}
	added := []string{}
	p := access.Principal{Sub: owner.Sub, Email: owner.Email}
	if err := rt.members.SeedLead(ctx, projectID, p); err != nil {
		return added
	}
	for _, email := range emails {
		email = strings.TrimSpace(email)
		if email == "" || strings.EqualFold(email, owner.Email) {
			continue // skip blanks and the owner (already the lead)
		}
		if err := rt.members.Add(ctx, p, projectID, email, access.RoleCollaborator); err == nil {
			added = append(added, email)
		}
	}
	return added
}

// Wallet is one person's own money and the statement that explains it (011).
// The statement is the point: a balance nobody can account for is the thing the
// ledger exists to prevent, so the surface that shows a number also shows the
// entries that produced it.
type Wallet struct {
	Balance int64         `json:"balance"`
	Entries []walletEntry `json:"entries"`
	// The purchase policy the top-up modal reads (022b): the spread it applies to
	// show "you'll get $Y", and the floor below which it refuses before the
	// processor is ever called. Zero when this deployment sells no credits.
	SpreadBps        int64 `json:"spread_bps"`
	SpreadFixedMicro int64 `json:"spread_fixed_micro"`
	MinTopupMicro    int64 `json:"min_topup_micro"`
}

// walletEntry is a ledger entry as the statement reads it: the durable row, plus
// whether the project it names still exists (024b). project_alive is the
// server's answer, not the ledger's — the ledger keeps no foreign key to a
// project, so only a surface that can see the projects table can say whether a
// trashed row still exists to link to or a purged one is gone. Absent (false)
// on every entry that names no project, and on one whose project was purged.
type walletEntry struct {
	credit.Entry
	ProjectAlive bool `json:"project_alive,omitempty"`
}

// myCredits reports the caller's own wallet. Always their own — there is no
// path here to read somebody else's, and no id to pass.
func (rt *Router) myCredits(w http.ResponseWriter, r *http.Request) {
	if rt.credits == nil {
		writeError(w, http.StatusNotImplemented, "credits_unavailable")
		return
	}
	id, _ := IdentityFrom(r)
	holder := credit.Wallet(id.Email)
	balance, err := rt.credits.Balance(r.Context(), holder)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credits_read_failed")
		return
	}
	entries, err := rt.credits.Entries(r.Context(), holder, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "credits_read_failed")
		return
	}
	// A row that names a project gets a liveness bit so the client can link a
	// project that still exists and show a purged one as plain text. "Alive"
	// means the caller can actually open it — a project they were removed from,
	// or one whose owner is not them and has been trashed, would 404 or 403 at
	// /p/{id}, so it reads as plain text rather than a link that dead-ends.
	// Resolved once per distinct project — a statement names few — and never
	// fatal: a read that fails leaves the row unlinked, not the wallet unread.
	alive := map[string]bool{}
	out := make([]walletEntry, len(entries))
	for i, e := range entries {
		we := walletEntry{Entry: e}
		if e.ProjectID != "" {
			live, seen := alive[e.ProjectID]
			if !seen {
				proj, gerr := rt.projects.Get(r.Context(), e.ProjectID)
				live = gerr == nil && rt.mayViewProject(r, proj)
				alive[e.ProjectID] = live
			}
			we.ProjectAlive = live
		}
		out[i] = we
	}
	wallet := Wallet{Balance: balance, Entries: out}
	// The purchase policy rides along so the top-up modal can quote a credit and
	// enforce the floor client-side. A failed read leaves it zero rather than
	// failing the wallet, which is about the balance, not the sale.
	if rt.policy != nil {
		if set, err := rt.policy.Settings(r.Context()); err == nil {
			wallet.SpreadBps, wallet.SpreadFixedMicro, wallet.MinTopupMicro = set.SpreadBps, set.SpreadFixedMicro, set.MinTopupMicro
		}
	}
	writeJSON(w, http.StatusOK, wallet)
}

// allocateCredits moves credits from the caller's wallet into a project's pot.
// Any member may fund a project they work in; the ledger re-checks that rather
// than trusting this route, so a route mounted without a guard still cannot
// move somebody's money.
func (rt *Router) allocateCredits(w http.ResponseWriter, r *http.Request) {
	rt.moveCredits(w, r, false)
}

// reclaimCredits moves credits from a project's pot back to the caller's
// wallet. Leads only, enforced in the ledger: the pot is fungible and does not
// remember who filled it, so taking from it is authority over the project's
// money rather than a refund of one's own.
func (rt *Router) reclaimCredits(w http.ResponseWriter, r *http.Request) {
	rt.moveCredits(w, r, true)
}

// moveCredits is the shared half of the two directions: decode, refuse a
// nonsense amount, move, and answer with the balances the client would
// otherwise have to re-read.
func (rt *Router) moveCredits(w http.ResponseWriter, r *http.Request, back bool) {
	if rt.credits == nil {
		writeError(w, http.StatusNotImplemented, "credits_unavailable")
		return
	}
	proj, err := rt.projects.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	// A project the caller may not see reads as not-found here exactly as it
	// does on every read path. Answering `not_member` instead would confirm
	// that a guessed private id exists — the one thing getProject's 404 is
	// there to withhold. Somebody who *can* see the project still gets the
	// honest 403 below: that refusal tells them nothing they did not know.
	if !rt.mayViewProject(r, proj) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	var body struct {
		Amount int64 `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	id, _ := IdentityFrom(r)
	p := access.Principal{Sub: id.Sub, Email: id.Email}
	if back {
		err = rt.credits.Reclaim(r.Context(), p, proj.ID, proj.Title, body.Amount)
	} else {
		err = rt.credits.Allocate(r.Context(), p, proj.ID, proj.Title, body.Amount)
	}
	switch {
	case err == nil:
	case errors.Is(err, credit.ErrNotPositive):
		writeError(w, http.StatusBadRequest, "bad_amount")
		return
	case errors.Is(err, credit.ErrInsufficient):
		writeError(w, http.StatusPaymentRequired, "no_credits")
		return
	case errors.Is(err, credit.ErrNotLead):
		writeError(w, http.StatusForbidden, "not_lead")
		return
	case errors.Is(err, credit.ErrNotMember):
		writeError(w, http.StatusForbidden, "not_member")
		return
	default:
		writeError(w, http.StatusInternalServerError, "credits_move_failed")
		return
	}
	// Both sides come back, because both moved and a client that re-read them
	// separately could show a moment where the money is in neither.
	wallet, _ := rt.credits.Balance(r.Context(), credit.Wallet(id.Email))
	writeJSON(w, http.StatusOK, map[string]any{
		"wallet":  wallet,
		"project": rt.projectCredits(r, proj),
	})
}

// renameProject renames an owned project (006j), owner-only.
func (rt *Router) renameProject(w http.ResponseWriter, r *http.Request) {
	proj, _, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		writeError(w, http.StatusBadRequest, "missing_title")
		return
	}
	if err := rt.projects.SetTitle(r.Context(), proj.ID, strings.TrimSpace(body.Title)); err != nil {
		writeError(w, http.StatusInternalServerError, "rename_failed")
		return
	}
	proj.Title = strings.TrimSpace(body.Title)
	writeJSON(w, http.StatusOK, proj)
}

// enqueueErrorCode maps an Enqueue failure to the machine-readable code the
// per-paper session result carries.
func enqueueErrorCode(err error) string {
	switch {
	case errors.Is(err, job.ErrActiveSession):
		return "active_session"
	case errors.Is(err, job.ErrNoCredits):
		return "no_credits"
	case errors.Is(err, job.ErrEmptyRef):
		return "missing_url"
	default:
		return "enqueue_failed"
	}
}

// nonEmpty trims and drops blank entries, preserving order.
func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ProjectView is a project in a list, carrying a session summary so the list
// shows status (how many sessions, how many running, the newest phase) at a
// glance without a per-project read on the client.
// SpendWindowDays is how far back the projects screen's spend tile looks. A
// window needs a stated edge or the number means nothing; thirty days is the
// one the tile is labelled with, so the two cannot drift.
const SpendWindowDays = 30

type ProjectView struct {
	project.Project
	SessionCount int `json:"session_count"`
	Running      int `json:"running"`
	// PublishedSessions is how many of them committed a repository — the exact
	// condition GET /projects/{id}/archive answers 409 no_artifacts on (020). The
	// client cannot work it out: a session count says nothing about whether any
	// of them published, so without this the download offers itself on a project
	// that has nothing to hand over.
	PublishedSessions int    `json:"published_sessions"`
	LatestPhase       string `json:"latest_phase,omitempty"`
	// Spend30d is what this project's sessions cost in the last thirty days, in
	// micro-USD (021e). The window is on when a session last changed, not when
	// it started, so a run that began five weeks ago and settled yesterday spent
	// yesterday's money; a session still running counts what it has burned so
	// far, because the wallet has already committed it.
	//
	// It is a pointer because a reader with no stake in the project does not get
	// it at all (028), and an absent figure must not read as a project that spent
	// nothing. Nil is "not yours to know"; a zero is a fact.
	Spend30d *int64 `json:"spend_30d,omitempty"`
	// SignerHandle is where the signer's name links (006r). It is resolved on
	// every read from the signer's profile as it is *now*, and deliberately not
	// snapshotted onto the project: a stored handle would be a second, staler
	// copy of the same person, and 006n's whole position is that a published
	// attestation reports what was signed, not what a profile says later.
	//
	// So the link comes and goes with the profile. Somebody who makes theirs
	// private after publishing keeps their attestation intact and loses the
	// link, which is correct — the record was never the profile.
	SignerHandle string `json:"signer_handle,omitempty"`
	// The owner, as a reader can read them (018). Project.Owner is an opaque OIDC
	// subject, which named nobody in a list that now has an Owner column.
	//
	// Both are resolved per read from the owner's profile, for the same reason
	// SignerHandle is: they are a view of who somebody is now, and a stored copy
	// would go stale against the profile that is the actual record. OwnerHandle
	// is set only when that profile is public, so it is safe to link.
	//
	// Neither is ever the owner's address. A project stores its signer's email
	// already redacted so a full one never reaches a client; putting the owner's
	// on every row a collaborator can read would undo that on a wider surface.
	OwnerName   string `json:"owner_name,omitempty"`
	OwnerHandle string `json:"owner_handle,omitempty"`
	// ModifiedAt is when this project last changed: the newest of its creation,
	// its papers' additions, and its sessions' updates (018). Derived on every
	// read — see project.Store.ModifiedAt for why it is not a column.
	ModifiedAt time.Time `json:"modified_at,omitempty"`
}

// listProjects reports every project the caller may work in — their own and the
// ones they were invited to — as one list, newest-modified first (018).
//
// It is one list because ownership is a column, not a place: a project used to
// move between two tabs when somebody else happened to create it, so finding
// work meant first remembering who had.
func (rt *Router) listProjects(w http.ResponseWriter, r *http.Request) {
	id, _ := IdentityFrom(r)
	projects, err := rt.projects.ListByOwner(r.Context(), id.Sub)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	seen := make(map[string]bool, len(projects))
	for _, p := range projects {
		seen[p.ID] = true
	}
	if rt.members != nil {
		ids, err := rt.members.Projects(r.Context(), id.Sub)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list_failed")
			return
		}
		for _, pid := range ids {
			if seen[pid] {
				// An owner is seeded as their own project's lead, so the membership
				// store answers with projects ListByOwner already returned.
				continue
			}
			p, err := rt.projects.Get(r.Context(), pid)
			if err != nil {
				continue // a membership whose project is gone is skipped, not fatal
			}
			seen[pid] = true
			if p.Trashed() {
				// Get answers with trashed projects on purpose (019), so this is the
				// one list read that has to filter them itself. The owner's own half
				// came from ListByOwner, which already has.
				continue
			}
			projects = append(projects, p)
		}
	}
	views, err := rt.projectViews(r, projects)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	writeJSON(w, http.StatusOK, views)
}

// projectViews decorates a set of projects for a list: the session summary, the
// modified time, and who owns them — three reads for the whole page rather than
// three per row.
//
// The result is newest-modified first, because that is the column the list is
// now sorted by and a displayed order nobody can see is one the reader has to
// sort by eye.
//
// A summary that could not be read fails the list rather than rendering a zero.
// "0 sessions" is a claim about the project, and a list quietly making a false
// one is worse than a list that says it could not load.
//
// It takes the request, not just its context, because the row a reader gets
// depends on whether they have a stake in the project (028): the public zone
// lists other people's work, and the owner's principal and the project's spend
// are not part of what was published.
func (rt *Router) projectViews(r *http.Request, projects []project.Project) ([]ProjectView, error) {
	ctx := r.Context()
	ids := make([]string, 0, len(projects))
	for _, p := range projects {
		ids = append(ids, p.ID)
	}
	status, err := rt.jobs.StatusByProjects(ctx, ids)
	if err != nil {
		log.Printf("projects: summarising sessions: %v", err)
		return nil, err
	}
	modified, err := rt.projects.ModifiedAt(ctx, ids)
	if err != nil {
		log.Printf("projects: reading modified times: %v", err)
		return nil, err
	}
	// Spend is the one summary that may fail softly. A missing session count is
	// a false claim about the project; a missing spend figure renders as a dash,
	// which claims nothing, and refusing the whole list because a cost could not
	// be summed would take the reader's projects away over a number.
	spend, err := rt.jobs.SpendSince(ctx, ids, time.Now().AddDate(0, 0, -SpendWindowDays))
	if err != nil {
		log.Printf("projects: summing spend: %v", err)
		spend = nil
	}
	out := make([]ProjectView, 0, len(projects))
	for _, p := range projects {
		st := status[p.ID]
		stake := rt.stakeIn(r, p)
		row := p
		if !stake {
			row = p.Redacted()
		}
		v := ProjectView{
			Project:           row,
			SessionCount:      st.Total,
			Running:           st.Running,
			PublishedSessions: st.Published,
			LatestPhase:       string(st.Latest),
			SignerHandle:      rt.signerHandle(ctx, p),
			ModifiedAt:        later(later(p.CreatedAt, modified[p.ID]), st.LatestAt),
		}
		// The spend is a pointer so absent and zero stay different things: a
		// reader with no stake is told nothing, and one who spent nothing is told
		// exactly that.
		if stake {
			n := spend[p.ID]
			v.Spend30d = &n
		}
		// The owner's name and handle are resolved from p, not from the redacted
		// row — a public reader still reads who published this, by the name they
		// chose to publish under. It is the opaque subject that is withheld.
		v.OwnerName, v.OwnerHandle = rt.owner(ctx, p.Owner)
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	return out, nil
}

// later is the newer of two times.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// owner names a project's owner for a list: their profile's display name, and
// their handle when that profile is public. Both empty for somebody who has
// saved no profile — the client renders that as a dash rather than an opaque
// subject, which named nobody anyway.
func (rt *Router) owner(ctx context.Context, principal string) (name, handle string) {
	if rt.profiles == nil || principal == "" {
		return "", ""
	}
	if p, err := rt.profiles.Get(ctx, principal); err == nil {
		name = p.DisplayName
	}
	if h, ok, err := rt.profiles.PrincipalHandle(ctx, principal); err == nil && ok {
		handle = h
		if name == "" {
			name = h
		}
	}
	return name, handle
}

// listPublic is the public zone: every public project, newest-modified first,
// each carrying its initiator, so any signed-in user can browse what others have
// published (006f). It decorates through the same path the caller's own list
// does — the zone lists other people's projects and had the identical problem of
// naming an owner nobody could read.
func (rt *Router) listPublic(w http.ResponseWriter, r *http.Request) {
	projects, err := rt.projects.ListPublic(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	views, err := rt.projectViews(r, projects)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	writeJSON(w, http.StatusOK, views)
}

// getProfile returns the caller's researcher profile (006n) — the zero profile
// before they have ever saved one, so the settings screen and the publish wizard
// render the same way on first use as on every other.
func (rt *Router) getProfile(w http.ResponseWriter, r *http.Request) {
	p, err := rt.profiles.Get(r.Context(), principal(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "profile_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// saveProfile writes the caller's researcher profile. The principal comes off
// the session, never the body, so a profile is always its owner's; a malformed
// identifier is refused by its own code, so the UI can point at the field.
// It is a real PATCH: a field absent from the body is left as it was. That was
// harmless while every field was one the settings form always sent, and stopped
// being harmless when 006r added `public` — a client that saved a display name
// without echoing it back would have silently taken somebody's page down, and
// with it every link a published attestation made to them.
func (rt *Router) saveProfile(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName *string `json:"display_name"`
		Institution *string `json:"institution"`
		Contact     *string `json:"contact_email"`
		ORCID       *string `json:"orcid"`
		Scholar     *string `json:"scholar_url"`
		Handle      *string `json:"handle"`
		Public      *bool   `json:"public"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	current, err := rt.profiles.Get(r.Context(), principal(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "profile_read_failed")
		return
	}
	setString(&current.DisplayName, body.DisplayName)
	setString(&current.Institution, body.Institution)
	setString(&current.Contact, body.Contact)
	setString(&current.ORCID, body.ORCID)
	setString(&current.Scholar, body.Scholar)
	setString(&current.Handle, body.Handle)
	if body.Public != nil {
		current.Public = *body.Public
	}
	current.Principal = principal(r)
	saved, err := rt.profiles.Save(r.Context(), current)
	// A handle somebody else holds is a conflict, not a malformed field: the
	// value is fine, the world disagrees. The UI points at the same input either
	// way, but the status says which kind of problem it is.
	if errors.Is(err, profile.ErrHandleTaken) {
		writeError(w, http.StatusConflict, "handle_taken")
		return
	}
	if code, ok := profileErrorCode(err); ok {
		writeError(w, http.StatusBadRequest, code)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "profile_save_failed")
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

// setString applies a PATCH field when the body carried one, and leaves the
// existing value alone when it did not.
func setString(dst *string, v *string) {
	if v != nil {
		*dst = *v
	}
}

// profileErrorCode maps a profile validation error to its wire code, reporting
// false for anything that is not one — so a storage failure is never disguised
// as a bad field.
func profileErrorCode(err error) (string, bool) {
	switch {
	case errors.Is(err, profile.ErrInvalidContact):
		return "invalid_contact", true
	case errors.Is(err, profile.ErrInvalidORCID):
		return "invalid_orcid", true
	case errors.Is(err, profile.ErrInvalidScholar):
		return "invalid_scholar", true
	case errors.Is(err, profile.ErrInvalidHandle):
		return "invalid_handle", true
	case errors.Is(err, profile.ErrHandleReserved):
		return "handle_reserved", true
	default:
		return "", false
	}
}

// publicProfile serves a researcher's page: who they say they are, and the
// public work signed under that name (006r).
//
// It is open — no sign-in — matching the public zone: a reproduction anyone can
// read, signed by somebody anyone can look up. An unknown handle, a private
// profile, and a profile with no handle are one 404, so the endpoint cannot be
// walked to learn who holds which name.
func (rt *Router) publicProfile(w http.ResponseWriter, r *http.Request) {
	if rt.profiles == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	p, err := rt.profiles.ByHandle(r.Context(), r.PathValue("handle"))
	if errors.Is(err, profile.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "profile_read_failed")
		return
	}
	out := profile.PublicProfile{
		Handle:      p.Handle,
		DisplayName: p.DisplayName,
		Institution: p.Institution,
		Contact:     p.Contact,
		ORCID:       p.ORCID,
		Scholar:     p.Scholar,
		Projects:    rt.signedPublicProjects(r.Context(), p.Principal),
	}
	writeJSON(w, http.StatusOK, out)
}

// signedPublicProjects lists the public projects this principal signed. It reads
// the public zone and filters by signer, so a withdrawn project leaves the page
// the moment it leaves the zone — one source of truth for what is public.
func (rt *Router) signedPublicProjects(ctx context.Context, principal string) []profile.PublicSigned {
	out := []profile.PublicSigned{}
	if rt.projects == nil || principal == "" {
		return out
	}
	projects, err := rt.projects.ListPublic(ctx)
	if err != nil {
		return out
	}
	for _, p := range projects {
		if p.SignedOffBy != principal {
			continue
		}
		out = append(out, profile.PublicSigned{ID: p.ID, Title: p.Title, SignedOffAt: p.SignedOffAt})
	}
	return out
}

// maxWithdrawReason caps the owner's stated reason. It is their own record, not
// a published notice, so the limit only has to keep a runaway body out of the
// column.
const maxWithdrawReason = 2000

// withdrawProject ends a publication (006o): the project returns to private, the
// retraction is recorded beside the sign-off it retracts rather than erasing it,
// and — best-effort — every repository the project owns is set private on the
// forge. Owner-only. Withdrawing an already-private project is a success that
// changes nothing, so a double-click cannot rewrite when the publication ended.
func (rt *Router) withdrawProject(w http.ResponseWriter, r *http.Request) {
	proj, _, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	reason := strings.TrimSpace(body.Reason)
	if len(reason) > maxWithdrawReason {
		reason = reason[:maxWithdrawReason]
	}
	if err := rt.projects.SetPrivate(r.Context(), proj.ID, project.Withdrawal{
		By:     principal(r),
		Reason: reason,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "withdraw_failed")
		return
	}
	// The forge follows, best-effort.
	if sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID); err != nil {
		log.Printf("withdraw: project %s sessions unreadable, repositories left as they are: %v", proj.ID, err)
	} else {
		rt.closeRepos(r.Context(), sessions)
	}
	updated, _ := rt.projects.Get(r.Context(), proj.ID)
	writeJSON(w, http.StatusOK, updated)
}

// makeProjectPublic is the owner's public flip, gated by a responsibility
// sign-off (006f). It refuses a non-owner (403), refuses a project none of whose
// published sessions cleared 002's redistribution gate (409 not_clear), records
// the sign-off with its signer snapshot (006n), marks the project public, and —
// best-effort — flips the forge repository public when a publisher is wired.
func (rt *Router) makeProjectPublic(w http.ResponseWriter, r *http.Request) {
	proj, err := rt.projects.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	if proj.Owner != principal(r) {
		writeError(w, http.StatusForbidden, "not_owner")
		return
	}
	var body struct {
		AttestationVersion string `json:"attestation_version"`
		// The signer, as the wizard confirmed them (006n). Prefilled from the
		// owner's profile in the UI, but recorded from this body: what is published
		// is what they signed, not what their profile says later.
		Name        string `json:"signed_off_name"`
		Institution string `json:"signed_off_institution"`
		Contact     string `json:"signed_off_contact"`
		ORCID       string `json:"signed_off_orcid"`
		Scholar     string `json:"signed_off_scholar"`
		// FlipRepo opts the forge repository into the public flip. Absent means
		// yes (the common intent — publish the reproduction), so a GitHub-connected
		// owner opts out by sending false: the in-app artifacts still go public,
		// but the repository stays private.
		FlipRepo *bool `json:"flip_repo"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if strings.TrimSpace(body.AttestationVersion) == "" {
		// The sign-off is the whole point — a flip with no recorded attestation
		// changes nothing.
		writeError(w, http.StatusBadRequest, "missing_attestation")
		return
	}
	// The signer snapshot is validated with the same rules the profile is, so a
	// malformed identifier cannot reach a public page by taking the publish path
	// instead of the settings one. Contact is required here and only here: a
	// result published with no way to ask about it is the case this spec exists
	// to close.
	signer := profile.Profile{
		DisplayName: body.Name,
		Institution: body.Institution,
		Contact:     body.Contact,
		ORCID:       body.ORCID,
		Scholar:     body.Scholar,
	}.Normalize()
	if signer.Contact == "" {
		writeError(w, http.StatusBadRequest, "missing_contact")
		return
	}
	if code, ok := profileErrorCode(signer.Validate()); ok {
		writeError(w, http.StatusBadRequest, code)
		return
	}

	// Publishing puts a name on a result, and 006r's position is that the name
	// must resolve to something: the signer needs a public profile with a
	// handle. Checked before anything is recorded, so a refusal leaves no
	// half-written sign-off behind.
	if !rt.signerIsPublic(r.Context(), proj.Owner) {
		writeError(w, http.StatusConflict, "profile_not_public")
		return
	}

	// The project must have a published session whose manifest cleared the gate,
	// and none that did not — a flagged tree bars the project from going public.
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	cleared, repos := rt.clearedForPublish(r.Context(), sessions)
	if !cleared {
		writeError(w, http.StatusConflict, "not_clear")
		return
	}

	// The signer's identity is derived from the session, never from the body: the
	// email the identity provider verified, redacted before it is stored, so a
	// published attestation cannot be attributed to somebody who did not sign it
	// and the full address is never put on the wire.
	id, _ := IdentityFrom(r)
	if err := rt.projects.SetPublic(r.Context(), proj.ID, project.SignOff{
		By:                 principal(r),
		AttestationVersion: body.AttestationVersion,
		Name:               signer.DisplayName,
		Institution:        signer.Institution,
		Contact:            signer.Contact,
		ORCID:              signer.ORCID,
		Scholar:            signer.Scholar,
		Email:              profile.RedactEmail(id.Email),
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "publish_failed")
		return
	}
	// Best-effort forge flip, unless the owner opted the repository out: the
	// project is already public in-app either way, and a repo flip failure must
	// not fail the request, only be reported.
	flipRepo := body.FlipRepo == nil || *body.FlipRepo
	if flipRepo && rt.repoPub != nil {
		for _, repo := range repos {
			if err := rt.repoPub.SetPublic(r.Context(), repo); err != nil {
				log.Printf("public flip: repo %s not flipped: %v", repo, err)
			}
		}
	}
	updated, _ := rt.projects.Get(r.Context(), proj.ID)
	writeJSON(w, http.StatusOK, updated)
}

// signerHandle resolves the public handle of whoever signed a project, empty
// when nobody signed it or their profile is not public. See ProjectView for why
// this is a view and never a stored field.
func (rt *Router) signerHandle(ctx context.Context, p project.Project) string {
	if rt.profiles == nil || p.SignedOffBy == "" {
		return ""
	}
	handle, ok, err := rt.profiles.PrincipalHandle(ctx, p.SignedOffBy)
	if err != nil || !ok {
		return ""
	}
	return handle
}

// signerIsPublic reports whether a principal has a public profile with a handle
// — the precondition for publishing (006r).
//
// A deployment with no profile store wired does not block publishing: the
// requirement is about attribution, and a missing store is not a reason to
// refuse an owner their own decision.
func (rt *Router) signerIsPublic(ctx context.Context, principal string) bool {
	if rt.profiles == nil {
		return true
	}
	_, ok, err := rt.profiles.PrincipalHandle(ctx, principal)
	return err == nil && ok
}

// clearedForPublish reports whether a project's published sessions may go public:
// at least one has a manifest, and every one that does cleared 002's gate. It
// returns the repositories (owner/name) to flip. A session with no repository
// yet is ignored; a session whose manifest flags third-party data bars the flip.
func (rt *Router) clearedForPublish(ctx context.Context, sessions []job.Job) (bool, []string) {
	any := false
	var repos []string
	for _, s := range sessions {
		if s.RepoName == "" {
			continue
		}
		repro, err := rt.repro.Get(ctx, repoName(s.RepoName))
		if err != nil {
			return false, nil // cannot confirm clearance → refuse, fail-closed
		}
		if !repro.CanPublish {
			return false, nil
		}
		any = true
		repos = append(repos, repoName(s.RepoName))
	}
	return any, repos
}

// ownedProject resolves a project and asserts the caller owns it, writing the
// error and returning ok=false otherwise — the authority behind every member
// route (006f): membership is owner-driven.
func (rt *Router) ownedProject(w http.ResponseWriter, r *http.Request) (project.Project, Identity, bool) {
	id, _ := IdentityFrom(r)
	proj, err := rt.projects.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return project.Project{}, id, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return project.Project{}, id, false
	}
	if proj.Owner != id.Sub {
		writeError(w, http.StatusForbidden, "not_owner")
		return project.Project{}, id, false
	}
	// Nothing about a project in the trash changes (019) — not its name, not its
	// members, not its visibility. The trash routes resolve through
	// trashableProject instead, which is what lets them act on one.
	if proj.Trashed() {
		writeError(w, http.StatusConflict, "project_trashed")
		return project.Project{}, id, false
	}
	return proj, id, true
}

// trashableProject resolves a project for the trash routes (019): the owner
// alone may remove one. A collaborator is refused; anybody who may not even view
// it is told nothing exists, the same rule getProject applies, because whether a
// stranger's project is there is not a stranger's business.
func (rt *Router) trashableProject(w http.ResponseWriter, r *http.Request) (project.Project, Identity, bool) {
	id, _ := IdentityFrom(r)
	proj, err := rt.projects.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return project.Project{}, id, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return project.Project{}, id, false
	}
	if proj.Owner != id.Sub {
		if !rt.mayViewProject(r, proj) {
			writeError(w, http.StatusNotFound, "not_found")
		} else {
			writeError(w, http.StatusForbidden, "not_owner")
		}
		return project.Project{}, id, false
	}
	return proj, id, true
}

// trashProject moves a project to the trash, or — with ?purge=1 — removes it for
// good (019).
//
// The two are one verb at different depths because that is what they are, and
// because the depth is what carries the order: a purge is refused on a project
// that is not in the trash, so the reversible step cannot be skipped.
func (rt *Router) trashProject(w http.ResponseWriter, r *http.Request) {
	proj, id, ok := rt.trashableProject(w, r)
	if !ok {
		return
	}
	if r.URL.Query().Get("purge") == "1" {
		rt.purgeProject(w, r, proj)
		return
	}
	// A running session spends this project's credits into a container its owner
	// is about to stop looking at. Stop it first.
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	for _, s := range sessions {
		if !s.Phase.Terminal() {
			writeError(w, http.StatusConflict, "sessions_running")
			return
		}
	}
	// A publication ends with a recorded retraction or it does not end (006o).
	// Deleting is not a licence to skip the record, so the withdrawal happens
	// first and through the same path an ordinary withdrawal takes.
	if proj.Visibility == project.VisibilityPublic {
		if err := rt.projects.SetPrivate(r.Context(), proj.ID, project.Withdrawal{By: id.Sub, Reason: withdrawnByDeletion}); err != nil {
			writeError(w, http.StatusInternalServerError, "withdraw_failed")
			return
		}
		rt.closeRepos(r.Context(), sessions)
	}
	if err := rt.projects.Trash(r.Context(), proj.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "trash_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// withdrawnByDeletion is the reason recorded against a publication that ended
// because its project was thrown away. It is the owner's record of why, and the
// one case where the reason is not in their own words.
const withdrawnByDeletion = "the project was deleted"

// purgeProject removes a trashed project for good: its sessions, its papers, and
// the row itself.
//
// What it does not remove is the repositories those sessions published. They are
// closed on the forge, exactly as a withdrawal closes them — a repository the
// agent authored is evidence, and this product does not destroy evidence on a
// click. What a purge ends is the app's claim on it.
func (rt *Router) purgeProject(w http.ResponseWriter, r *http.Request, proj project.Project) {
	if !proj.Trashed() {
		writeError(w, http.StatusConflict, "not_trashed")
		return
	}
	// Money outlives the project that held it unless somebody takes it back. A
	// purge that dropped a funded pot would leave a balance nobody can account
	// for, which is the one thing the ledger exists to prevent.
	if rt.credits != nil {
		balance, err := rt.credits.Balance(r.Context(), credit.Pot(proj.ID))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "credits_read_failed")
			return
		}
		if balance != 0 {
			writeError(w, http.StatusConflict, "credits_remaining")
			return
		}
	}
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	rt.closeRepos(r.Context(), sessions)
	// The sessions go first: jobs.project_id references the project row and the
	// foreign key does not cascade, so the other order fails at the database.
	if err := rt.jobs.DeleteByProject(r.Context(), proj.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "purge_failed")
		return
	}
	if err := rt.projects.Delete(r.Context(), proj.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "purge_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// closeRepos makes every repository a project's sessions published private.
//
// Best-effort on purpose: the project is already private in this app, and a
// forge outage must not strand an owner mid-withdrawal. Every repository the
// project owns is closed, not only those a publish flipped — nothing records
// which those were, and closing an already-closed repository costs nothing. The
// repositories come from the sessions directly rather than from
// clearedForPublish, because a project is often withdrawn precisely because its
// tree stopped being clear, and that must not be what keeps the repository open.
//
// One helper for both ways a publication ends — a withdrawal (006o) and a
// delete (019) — because they close exactly the same set for exactly the same
// reasons, and two copies would drift.
func (rt *Router) closeRepos(ctx context.Context, sessions []job.Job) {
	if rt.repoPub == nil {
		return
	}
	for _, s := range sessions {
		if s.RepoName == "" {
			continue
		}
		if err := rt.repoPub.SetPrivate(ctx, repoName(s.RepoName)); err != nil {
			log.Printf("delete: repo %s not closed: %v", s.RepoName, err)
		}
	}
}

// restoreProject takes a project back out of the trash. Owner-only, and a
// success on one that was never in it.
func (rt *Router) restoreProject(w http.ResponseWriter, r *http.Request) {
	proj, _, ok := rt.trashableProject(w, r)
	if !ok {
		return
	}
	if err := rt.projects.Restore(r.Context(), proj.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "restore_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listTrash is the trash screen: the caller's own trashed projects, newest-
// trashed first. Only an owner's own, because only an owner can put one here.
func (rt *Router) listTrash(w http.ResponseWriter, r *http.Request) {
	projects, err := rt.projects.ListTrashed(r.Context(), principal(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	views, err := rt.projectViews(r, projects)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	// projectViews sorts on the modified time, which is the column the projects
	// list shows. This screen shows the deleted time, so it sorts on that: a
	// project trashed today belongs at the top however long ago it was last
	// worked in.
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].DeletedAt == nil || views[j].DeletedAt == nil {
			return views[j].DeletedAt == nil
		}
		return views[i].DeletedAt.After(*views[j].DeletedAt)
	})
	writeJSON(w, http.StatusOK, views)
}

// listMembers returns a project's contributors, owner-only.
func (rt *Router) listMembers(w http.ResponseWriter, r *http.Request) {
	proj, _, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	members, err := rt.members.List(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	if members == nil {
		members = []access.Member{}
	}
	writeJSON(w, http.StatusOK, members)
}

// addMember invites a collaborator into a project by email, owner-only. The
// owner is seeded as the project's lead (idempotent) so the membership store's
// own lead-check is satisfied — one authority, project ownership. Inviting an
// email admits nobody to the platform: they still need a platform grant.
//
// It then tells them (006p). The mail is best-effort and deliberately so: the
// membership is the durable thing, created and authorised here, and an outage
// at a mail provider must not read to the owner as a failure to share their
// work. A message that did not go out is logged and can be sent again by
// inviting the same person once more.
func (rt *Router) addMember(w http.ResponseWriter, r *http.Request) {
	proj, id, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if strings.TrimSpace(body.Email) == "" {
		writeError(w, http.StatusBadRequest, "missing_email")
		return
	}
	role := access.RoleCollaborator
	if body.Role == "lead" {
		role = access.RoleLead
	}
	if err := rt.members.SeedLead(r.Context(), proj.ID, access.Principal{Sub: id.Sub, Email: id.Email}); err != nil {
		writeError(w, http.StatusInternalServerError, "member_add_failed")
		return
	}
	// Asked before the add, so "already there" is distinguishable from "just
	// added": re-inviting an existing member is a no-op the person should not
	// hear about twice, while re-inviting somebody who was removed is a real
	// invitation and gets one.
	already := rt.isMember(r.Context(), proj.ID, body.Email)
	if err := rt.members.Add(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email}, proj.ID, body.Email, role); err != nil {
		writeError(w, http.StatusInternalServerError, "member_add_failed")
		return
	}
	if !already && rt.notifier != nil {
		if err := rt.notifier.ProjectInvitation(r.Context(), body.Email, proj.ID, proj.Title, inviterName(id)); err != nil {
			notify.LogFailure("project invitation", body.Email, err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// isMember reports whether an email is already on a project. A store that
// cannot be read answers false: the cost of being wrong is one extra message,
// against a silent non-invitation the other way.
func (rt *Router) isMember(ctx context.Context, projectID, email string) bool {
	members, err := rt.members.List(ctx, projectID)
	if err != nil {
		return false
	}
	for _, m := range members {
		if strings.EqualFold(m.Email, strings.TrimSpace(email)) {
			return true
		}
	}
	return false
}

// inviterName is how the invitation names the person who sent it: their display
// name when the identity provider gave one, their address otherwise. Never the
// opaque subject, which would tell a reader nothing.
func inviterName(id Identity) string {
	if n := strings.TrimSpace(id.Name); n != "" {
		return n
	}
	return id.Email
}

// removeMember drops a collaborator by email, owner-only.
func (rt *Router) removeMember(w http.ResponseWriter, r *http.Request) {
	proj, id, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	if err := rt.members.SeedLead(r.Context(), proj.ID, access.Principal{Sub: id.Sub, Email: id.Email}); err != nil {
		writeError(w, http.StatusInternalServerError, "member_remove_failed")
		return
	}
	if err := rt.members.Remove(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email}, proj.ID, r.PathValue("email")); err != nil {
		writeError(w, http.StatusInternalServerError, "member_remove_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SessionView is a session as this reader may read it (028).
//
// The two fields shadow job.Job's own, at a shallower depth, so encoding/json
// takes these and the embedded ones never reach the wire. Both are empty for a
// reader with no stake in the session's project, and omitted rather than sent as
// zero: a public reader is entitled to the evidence a published reproduction
// rests on, and `"cost": 0` on a session that cost three dollars would be a
// number this codebase does not put on a wire. For the owner and a collaborator
// they carry what job.Job holds, including a genuine zero — a session that has
// spent nothing yet says so.
type SessionView struct {
	job.Job
	Principal string `json:"principal,omitempty"`
	Cost      *int64 `json:"cost,omitempty"`
}

// sessionView projects one session for a reader, keeping the withheld fields
// exactly when they have a stake in the project it belongs to.
func sessionView(j job.Job, stake bool) SessionView {
	v := SessionView{Job: j}
	if stake {
		cost := j.Cost
		v.Principal, v.Cost = j.Principal, &cost
	}
	return v
}

// sessionViews is sessionView over a list.
func sessionViews(js []job.Job, stake bool) []SessionView {
	out := make([]SessionView, 0, len(js))
	for _, j := range js {
		out = append(out, sessionView(j, stake))
	}
	return out
}

// ProjectDetail is one project with its sessions, newest first.
type ProjectDetail struct {
	Project  project.Project `json:"project"`
	Sessions []SessionView   `json:"sessions"`
	// MaySubmit reports whether this caller may open a session here — its owner
	// or a collaborator, never a stranger who reached it through the public zone
	// (006l). The client cannot work this out: it does not know membership, and a
	// public project is readable by everyone.
	MaySubmit bool `json:"may_submit"`
	// SignerHandle is where the signer's name links; see ProjectView.
	SignerHandle string `json:"signer_handle,omitempty"`
	// Credits is what this project holds and what it may still commit (011).
	// Reported only to somebody who may work here: a stranger reading a public
	// project is looking at the research, not at whose money paid for it.
	Credits *ProjectCredits `json:"credits,omitempty"`
}

// ProjectCredits is a project's money as a member reads it: what the pot holds,
// what is still free to commit, and how much a session needs to start.
//
// Balance and Available differ by the sessions running right now — each holds
// the reserve — which is exactly the thing a member needs to see before
// wondering why a submit was refused on a project that "has credits".
//
// The overdraft floor is not here, and that is the point: an allowance somebody
// can read is an allowance they plan around.
type ProjectCredits struct {
	Balance      int64 `json:"balance"`
	Available    int64 `json:"available"`
	StartReserve int64 `json:"start_reserve"`
	// MayReclaim reports whether this caller may take credits back out. Only a
	// lead may: the pot is fungible and does not remember who filled it, so
	// reclaiming is authority over the project's money rather than a refund of
	// one's own. The client cannot work this out — it does not know roles.
	MayReclaim bool `json:"may_reclaim"`
}

// projectCredits reads a project's money for a caller who may work in it, and
// reports nothing for anyone else. A read failure is not fatal: the project
// still renders, without its balance.
func (rt *Router) projectCredits(r *http.Request, proj project.Project) *ProjectCredits {
	if rt.credits == nil || !rt.mayWorkInProject(r, proj, principal(r)) {
		return nil
	}
	out := ProjectCredits{MayReclaim: rt.isLead(r, proj.ID)}
	balance, err := rt.credits.Balance(r.Context(), credit.Pot(proj.ID))
	if err != nil {
		log.Printf("credits: reading the balance of %s: %v", proj.ID, err)
		return nil
	}
	available, err := rt.credits.Available(r.Context(), credit.Pot(proj.ID))
	if err != nil {
		log.Printf("credits: reading the available balance of %s: %v", proj.ID, err)
		return nil
	}
	out.Balance, out.Available = balance, available
	if rt.reserve != nil {
		if n, err := rt.reserve(r.Context()); err == nil {
			out.StartReserve = n
		}
	}
	return &out
}

// isLead reports whether the caller leads this project — the role a reclaim
// needs. An owner is seeded as its lead, so this covers them too.
func (rt *Router) isLead(r *http.Request, projectID string) bool {
	if rt.members == nil {
		return false
	}
	id, _ := IdentityFrom(r)
	role, ok, err := rt.members.Role(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email}, projectID)
	return err == nil && ok && role >= access.RoleLead
}

// getProject returns a project and its sessions, refusing a project the caller
// does not own.
// mayViewProject is the one predicate every project- and session-scoped read
// consults (006f): a public project is open to anyone signed in, an owner sees
// their own, and a collaborator sees one they were invited to. Visibility lives
// on the project, so everything under it (sessions, transcripts, history)
// inherits this decision — there is no second, unscoped "published" surface.
func (rt *Router) mayViewProject(r *http.Request, p project.Project) bool {
	id, _ := IdentityFrom(r)
	// A trashed project is its owner's alone (019), whatever its visibility column
	// still says. Putting it here rather than in each read is what closes the
	// sessions, transcripts and artifacts underneath it in one edit — this
	// predicate is documented as the decision everything below a project
	// inherits, and a delete that left those readable would be a delete in name.
	if p.Trashed() {
		return id.Sub != "" && p.Owner == id.Sub
	}
	if p.Visibility == project.VisibilityPublic {
		return true
	}
	// An unauthenticated caller is nobody, and nobody owns nothing. Without this
	// an anonymous read of a project whose owner column is empty would match on
	// "" == "", and the reads this route set opened (028) are exactly where that
	// would first be reachable from the internet.
	if id.Sub == "" {
		return false
	}
	if p.Owner == id.Sub {
		return true
	}
	if rt.members == nil {
		return false
	}
	_, ok, _ := rt.members.Role(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email}, p.ID)
	return ok
}

// viewableSession resolves a session and the project it belongs to, and asserts
// the caller may view that project — so a session read is scoped exactly like
// its project. It writes 404 and returns ok=false otherwise, hiding a session
// the caller may not see rather than revealing that it exists.
func (rt *Router) viewableSession(w http.ResponseWriter, r *http.Request, id string) (job.Job, bool) {
	j, _, ok := rt.viewableSessionIn(w, r, id)
	return j, ok
}

// viewableSessionIn is viewableSession, handing back the project as well — for
// the one caller that has to decide what the reader may see of the session, not
// just whether they may see it (028).
func (rt *Router) viewableSessionIn(w http.ResponseWriter, r *http.Request, id string) (job.Job, project.Project, bool) {
	j, err := rt.jobs.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return job.Job{}, project.Project{}, false
	}
	p, err := rt.projects.Get(r.Context(), j.ProjectID)
	if err != nil || !rt.mayViewProject(r, p) {
		writeError(w, http.StatusNotFound, "not_found")
		return job.Job{}, project.Project{}, false
	}
	return j, p, true
}

func (rt *Router) getProject(w http.ResponseWriter, r *http.Request) {
	proj, err := rt.projects.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	// A project the caller may not view is hidden, not merely refused — a private
	// project reads as not-found to anyone but its owner and collaborators.
	if !rt.mayViewProject(r, proj) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sessions_read_failed")
		return
	}
	if sessions == nil {
		sessions = []job.Job{}
	}
	// A reader with no stake reads the reproduction, not the wallet behind it
	// (028): the project without its owning principal, and each session without
	// who submitted it or what it cost. The redacted copy is the payload alone —
	// every predicate below still runs against `proj`, which is the project as it
	// actually is.
	stake := rt.stakeIn(r, proj)
	shown := proj
	if !stake {
		shown = proj.Redacted()
	}
	writeJSON(w, http.StatusOK, ProjectDetail{
		Project:      shown,
		Sessions:     sessionViews(sessions, stake),
		MaySubmit:    !proj.Trashed() && stake,
		SignerHandle: rt.signerHandle(r.Context(), proj),
		Credits:      rt.projectCredits(r, proj),
	})
}

// listJobs reports the caller's own jobs, newest first — the projects they
// submitted. The scope is the authenticated principal alone; a caller never sees
// another principal's jobs. The gate guarantees the request is authenticated, so
// an empty principal (which cannot occur past the gate) simply lists nothing.
func (rt *Router) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := rt.jobs.ListByPrincipal(r.Context(), principal(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	if jobs == nil {
		jobs = []job.Job{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

// Transcript is a session's trajectory and token usage (006h): the parsed
// provider.Event sequence the agent produced, and what the run consumed. Usage
// is nil when it was never recorded (a historical backfill), never a fabricated
// zero.
type Transcript struct {
	Events []TranscriptEvent `json:"events"`
	Usage  *provider.Usage   `json:"usage"`
	// Live reports that these events came from an in-flight run's live stream, so
	// the client keeps polling for more.
	Live bool `json:"live"`
}

// TranscriptEvent is one event as the viewer reads it: the neutral event, plus
// the token usage the backend recorded against that one request (006s).
//
// Usage is derived on the read path from the event's backend-original payload
// and is nil for an event that carried none — most events carry none, and an
// absent number is a fact about the run rather than a gap to fill (the 006h
// honesty rule, one level down). It is deliberately *not* a field on
// provider.Event: that type is the schema of the committed transcript.jsonl
// (005), and a published artifact's shape does not change to serve a viewer.
type TranscriptEvent struct {
	provider.Event
	Usage *provider.Usage `json:"usage,omitempty"`
}

// getSessionTranscript serves a session's transcript as structured events plus
// its usage (006h). A finished session reads its committed transcript.jsonl
// through the forge; a session still in flight reads the backend's live event
// stream directly, so its trajectory is inspectable in real time — not only
// after it publishes. Only a session with neither a repository nor a live run
// answers 409 not_ready, and its `live` flag tells the client to keep polling.
func (rt *Router) getSessionTranscript(w http.ResponseWriter, r *http.Request) {
	j, ok := rt.viewableSession(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	var events []provider.Event
	live := false
	switch {
	case !j.Phase.Terminal():
		// The session is still in flight — its committed transcript does not exist
		// yet (it is written at publish), and a repository created up front for the
		// live mount (006i) must NOT be mistaken for one. Read the backend's live
		// event stream by the observed run id; until that id lands, it is not a
		// miss, just not ready (the client keeps polling).
		if j.Status.RunID == "" || rt.live == nil {
			writeError(w, http.StatusConflict, "not_ready")
			return
		}
		evs, err := rt.live.Events(r.Context(), j.Status.RunID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "live_transcript_unavailable")
			return
		}
		events = evs
		live = true
	case j.RepoName != "":
		// A finished session reads its committed transcript.jsonl through the forge.
		raw, err := rt.repro.Transcript(r.Context(), repoName(j.RepoName))
		if err != nil {
			writeReadError(w, err) // 404 for a genuine miss, 500 for a transport failure
			return
		}
		events, err = parseTranscriptEvents(raw)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "transcript_malformed")
			return
		}
	case j.Status.RunID != "" && rt.live != nil:
		// A finished session with no committed repository (a server run that never
		// published) still shows its trajectory via the backend session's events.
		evs, err := rt.live.Events(r.Context(), j.Status.RunID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "live_transcript_unavailable")
			return
		}
		events = evs
	default:
		// No live run, no committed transcript — not a miss, not ready.
		writeError(w, http.StatusConflict, "not_ready")
		return
	}
	// Pair each event with its own usage and take the session total from the same
	// pass (006s); this also drops the raw payloads, which are the bulk of a
	// transcript. The session's *recorded* usage still wins for the total when it
	// has one — it is what the run itself observed — and the derived sum stands in
	// for a historical backfill that never recorded any. Only when neither exists
	// is usage nil ("not recorded"), never a fabricated zero.
	shown, derived, ok := attachUsage(events)
	var usage *provider.Usage
	switch {
	case j.Status.Usage.Total() > 0:
		u := j.Status.Usage
		usage = &u
	case ok:
		usage = &derived
	}
	writeJSON(w, http.StatusOK, Transcript{Events: shown, Usage: usage, Live: live})
}

// repoName strips an "owner/" prefix from a repository path, so a session's
// recorded "open-hai/name" resolves to the "name" the forge source keys on.
func repoName(full string) string {
	if i := strings.LastIndex(full, "/"); i >= 0 {
		return full[i+1:]
	}
	return full
}

// getSessionReproduction serves a session's manifest summary (006f): the
// verdict, the redistribution review, and the reproduction metadata, read from
// the committed repository. Scoped exactly like the transcript.
func (rt *Router) getSessionReproduction(w http.ResponseWriter, r *http.Request) {
	j, ok := rt.viewableSession(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if j.RepoName == "" {
		writeError(w, http.StatusConflict, "not_ready")
		return
	}
	repro, err := rt.repro.Get(r.Context(), repoName(j.RepoName))
	if err != nil {
		writeReadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repro)
}

// getSessionFindings serves the session's verdict.json — the boundary table, the
// hidden decisions, the scorecard, and what the authors released (021a).
//
// It is deliberately not folded into /jobs/{id}/reproduction. That route is the
// manifest, the manifest is the system's word, and these are the agent's claims;
// mixing them would blur the line the attestation model rests on.
//
// A session with no findings is a 404 with a reason, not an empty body: a run
// that predates the file and a run whose file failed validation are different
// facts, and a reader is told which.
func (rt *Router) getSessionFindings(w http.ResponseWriter, r *http.Request) {
	j, ok := rt.viewableSession(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if j.RepoName == "" {
		writeError(w, http.StatusConflict, "not_ready")
		return
	}
	raw, err := rt.repro.Artifact(r.Context(), repoName(j.RepoName), verdict.File)
	if err != nil {
		if errors.Is(err, ErrReproNotFound) {
			writeError(w, http.StatusNotFound, "no_findings")
			return
		}
		writeReadError(w, err)
		return
	}
	problems, err := verdict.Validate(raw)
	if err != nil {
		writeError(w, http.StatusNotFound, "findings_unreadable")
		return
	}
	if len(problems) > 0 {
		// The same refusal finalize made, made again on read: a file that does
		// not validate is not served as if it did, and the reason travels so the
		// pane can say which rule it broke rather than "unavailable".
		msgs := make([]string, 0, len(problems))
		for _, p := range problems {
			msgs = append(msgs, p.String())
		}
		writeJSONError(w, http.StatusUnprocessableEntity, "findings_invalid", msgs)
		return
	}
	v, err := verdict.Parse(raw)
	if err != nil {
		writeError(w, http.StatusNotFound, "findings_unreadable")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// listSessionArtifacts serves the session's repository file tree — the in-app
// artifact browser (006f), an OSF-style pass-through that works even when the
// forge repository stays private.
func (rt *Router) listSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	j, ok := rt.viewableSession(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if j.RepoName == "" {
		writeError(w, http.StatusConflict, "not_ready")
		return
	}
	files, err := rt.repro.Files(r.Context(), repoName(j.RepoName))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry_unavailable")
		return
	}
	if files == nil {
		files = []string{}
	}
	sort.Strings(files)
	writeJSON(w, http.StatusOK, map[string][]string{"files": files})
}

// getSessionArtifact serves one committed file's bytes as text. The path is a
// query parameter; the forge scopes reads to the one repository, so a path can
// only reach that repository's committed tree.
func (rt *Router) getSessionArtifact(w http.ResponseWriter, r *http.Request) {
	j, ok := rt.viewableSession(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if j.RepoName == "" {
		writeError(w, http.StatusConflict, "not_ready")
		return
	}
	path := r.URL.Query().Get("path")
	if strings.TrimSpace(path) == "" {
		writeError(w, http.StatusBadRequest, "missing_path")
		return
	}
	raw, err := rt.repro.Artifact(r.Context(), repoName(j.RepoName), path)
	if err != nil {
		writeReadError(w, err)
		return
	}
	// Serve images with a real image type so the browser renders them; serve
	// everything else as plain text (never text/html — a committed .html must not
	// execute on our origin). nosniff stops the browser from re-sniffing it.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", artifactContentType(path))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

// artifactContentType maps a committed file's extension to a safe content type:
// a real image type for images, plain text for everything else.
func artifactContentType(path string) string {
	switch strings.ToLower(path[strings.LastIndex(path, ".")+1:]) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "svg":
		return "image/svg+xml"
	default:
		return "text/plain; charset=utf-8"
	}
}

// parseTranscriptEvents parses the newline-delimited provider.Event stream
// committed as transcript.jsonl (005). A blank line is skipped; a malformed
// line fails the parse, so a corrupt transcript is a 500, not a silent gap. Raw
// is retained here — usage derivation reads it — and dropped by attachUsage
// before serving, since it is the bulk of a transcript (megabytes).
func parseTranscriptEvents(raw []byte) ([]provider.Event, error) {
	events := []provider.Event{}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e provider.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}

// eventUsage reads the token counts a managed-agents transcript records against
// one request, in that event's raw payload (a `model_usage` object). ok is false
// for an event that carries none, which is most of them — only an event that
// closes a model request is charged for one. The field selection matches the
// anthropic adapter's live-run mapping, so a derived figure agrees with a
// recorded one.
func eventUsage(e provider.Event) (provider.Usage, bool) {
	if len(e.Raw) == 0 {
		return provider.Usage{}, false
	}
	var wrap struct {
		ModelUsage *struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"model_usage"`
	}
	if json.Unmarshal(e.Raw, &wrap) != nil || wrap.ModelUsage == nil {
		return provider.Usage{}, false
	}
	return provider.Usage{
		InputTokens:       wrap.ModelUsage.InputTokens,
		OutputTokens:      wrap.ModelUsage.OutputTokens,
		CachedInputTokens: wrap.ModelUsage.CacheReadInputTokens,
		// A span reports cache creation as one number with no lifetime, unlike
		// the session total. It goes to the unattributed field rather than into
		// a bucket it might not belong to (011).
		CacheWriteTokens: wrap.ModelUsage.CacheCreationInputTokens,
	}, true
}

// attachUsage pairs every event with its own usage and returns the session total
// beside it, so a session whose usage was never recorded still shows real
// numbers. ok is false when no event carries usage — then the caller shows "not
// recorded" rather than a fabricated zero.
//
// Both come out of one pass on purpose: the total is the sum of exactly the
// per-event numbers the viewer displays, so the two can never disagree (006s).
// It also drops each event's raw payload, which is the bulk of a transcript and
// which nothing downstream of this function reads.
func attachUsage(events []provider.Event) ([]TranscriptEvent, provider.Usage, bool) {
	out := make([]TranscriptEvent, len(events))
	var total provider.Usage
	found := false
	for i, e := range events {
		te := TranscriptEvent{}
		if u, ok := eventUsage(e); ok {
			found = true
			total.Add(u)
			te.Usage = &u
		}
		e.Raw = nil
		te.Event = e
		out[i] = te
	}
	return out, total, found
}

// getJob reports a session's status and phase, scoped to who may view its
// project.
func (rt *Router) getJob(w http.ResponseWriter, r *http.Request) {
	j, proj, ok := rt.viewableSessionIn(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, sessionView(j, rt.stakeIn(r, proj)))
}

// cancelSession stops a running session. Only the owner of the session's project
// may stop it (a write action, so 403 explains rather than hides). It returns
// the resulting job; stopping an already-finished session is a no-op.
func (rt *Router) cancelSession(w http.ResponseWriter, r *http.Request) {
	j, err := rt.jobs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	proj, err := rt.projects.Get(r.Context(), j.ProjectID)
	if err != nil || proj.Owner != principal(r) {
		writeError(w, http.StatusForbidden, "not_owner")
		return
	}
	if rt.canceler == nil {
		writeError(w, http.StatusNotImplemented, "cancel_unavailable")
		return
	}
	if err := rt.canceler.Cancel(r.Context(), j.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "cancel_failed")
		return
	}
	updated, _ := rt.jobs.Get(r.Context(), j.ID)
	writeJSON(w, http.StatusOK, updated)
}
