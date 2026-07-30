package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
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
	rt.gatedHandle("POST", "/papers", rt.submitPaper)
	rt.gatedHandle("POST", "/resolve", rt.resolvePaper)
	rt.gatedHandle("POST", "/projects", rt.createProject)
	rt.gatedHandle("PATCH", "/projects/{id}", rt.renameProject)
	rt.gatedHandle("GET", "/projects", rt.listProjects)
	rt.gatedHandle("GET", "/shared", rt.listShared)
	rt.gatedHandle("GET", "/public", rt.listPublic)
	rt.gatedHandle("GET", "/projects/{id}", rt.getProject)
	rt.gatedHandle("POST", "/projects/{id}/public", rt.makeProjectPublic)
	rt.gatedHandle("GET", "/projects/{id}/members", rt.listMembers)
	rt.gatedHandle("POST", "/projects/{id}/members", rt.addMember)
	rt.gatedHandle("DELETE", "/projects/{id}/members/{email}", rt.removeMember)
	rt.gatedHandle("GET", "/jobs", rt.listJobs)
	rt.gatedHandle("GET", "/jobs/{id}", rt.getJob)
	rt.gatedHandle("POST", "/jobs/{id}/cancel", rt.cancelSession)
	rt.gatedHandle("GET", "/jobs/{id}/events", rt.jobEvents)
	rt.gatedHandle("GET", "/jobs/{id}/transcript", rt.getSessionTranscript)
	rt.gatedHandle("GET", "/jobs/{id}/reproduction", rt.getSessionReproduction)
	rt.gatedHandle("GET", "/jobs/{id}/artifacts", rt.listSessionArtifacts)
	rt.gatedHandle("GET", "/jobs/{id}/artifact", rt.getSessionArtifact)

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

// submitPaper opens a replication session in a project (006g): it resolves the
// paper's offline identity (rejecting garbage early, never touching the
// network), then either opens a fresh project for it or adds a session to a
// project the caller already owns. It returns the session and project ids
// without waiting on the run — no request handler ever blocks on an audit.
func (rt *Router) submitPaper(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		// PDF ingestion is 006d; the URL path works today.
		writeError(w, http.StatusNotImplemented, "pdf_ingestion_not_available")
		return
	}
	var body struct {
		URL     string `json:"url"`
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if strings.TrimSpace(body.URL) == "" {
		writeError(w, http.StatusBadRequest, "missing_url")
		return
	}
	// The offline paper key: it fails fast on a reference that is not a DOI,
	// arXiv ID, or URL, and keys the double-trigger guard. The worker's Resolve
	// phase does the enriching lookup; a submit never rides arXiv's availability.
	paperKey, err := paper.Key(body.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unresolvable_paper")
		return
	}
	owner := principal(r)

	if strings.TrimSpace(body.Project) == "" {
		rt.submitToNewProject(w, r, owner, body.URL, paperKey)
		return
	}
	rt.submitToExistingProject(w, r, owner, body.Project, body.URL, paperKey)
}

// submitToNewProject opens a fresh project and its first session. If the session
// fails to enqueue — including the per-principal guard catching the same paper
// already running under this owner — the just-created project is deleted, so a
// project is never left empty.
func (rt *Router) submitToNewProject(w http.ResponseWriter, r *http.Request, owner, url, paperKey string) {
	proj, err := rt.projects.Create(r.Context(), owner, defaultTitle(url))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_create_failed")
		return
	}
	j, err := rt.jobs.Enqueue(r.Context(), owner, url, job.KindAudit, proj.ID, paperKey)
	if err != nil {
		_ = rt.projects.Delete(r.Context(), proj.ID) // compensate: never leave an empty project
		rt.writeEnqueueError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": j.ID, "project_id": proj.ID})
}

// submitToExistingProject adds a session to a project the caller owns, refusing
// a project they do not own and a paper already running in it.
func (rt *Router) submitToExistingProject(w http.ResponseWriter, r *http.Request, owner, projectID, url, paperKey string) {
	proj, err := rt.projects.Get(r.Context(), projectID)
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project_not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	if proj.Owner != owner {
		writeError(w, http.StatusForbidden, "not_owner")
		return
	}
	j, err := rt.jobs.Enqueue(r.Context(), owner, url, job.KindAudit, proj.ID, paperKey)
	if err != nil {
		rt.writeEnqueueError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": j.ID, "project_id": proj.ID})
}

// writeEnqueueError maps an Enqueue failure to a status: the double-trigger
// guard is a 409, an empty reference a 400, anything else a 500.
func (rt *Router) writeEnqueueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, job.ErrActiveSession):
		writeError(w, http.StatusConflict, "active_session")
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

// SessionResult reports one paper's fate in a create: a job id when it started,
// or a machine-readable error when it did not (a bad reference, or the
// double-trigger guard catching a duplicate).
type SessionResult struct {
	URL   string `json:"url"`
	JobID string `json:"job_id,omitempty"`
	Error string `json:"error,omitempty"`
}

// createProject opens a project with a chosen title, one session per paper, and
// seeded collaborators (006j). Papers are enqueued best-effort: each is reported
// in sessions[]. The project is kept if at least one session started; if every
// paper failed it is deleted (never leave an empty project) and the call is 422.
func (rt *Router) createProject(w http.ResponseWriter, r *http.Request) {
	id, ok := IdentityFrom(r)
	if !ok {
		writeError(w, http.StatusForbidden, "not_invited")
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
	title := strings.TrimSpace(body.Title)
	if title == "" {
		title = defaultTitle(papers[0]) // the worker enriches this placeholder
	}

	proj, err := rt.projects.Create(r.Context(), id.Sub, title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_create_failed")
		return
	}

	results := make([]SessionResult, 0, len(papers))
	started := 0
	for _, url := range papers {
		res := SessionResult{URL: url}
		if key, kErr := paper.Key(url); kErr != nil {
			res.Error = "unresolvable_paper"
		} else if j, eErr := rt.jobs.Enqueue(r.Context(), id.Sub, url, job.KindAudit, proj.ID, key); eErr != nil {
			res.Error = enqueueErrorCode(eErr)
		} else {
			res.JobID = j.ID
			started++
		}
		results = append(results, res)
	}

	// Never leave an empty project: if not one paper started, undo the create.
	if started == 0 {
		_ = rt.projects.Delete(r.Context(), proj.ID)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "no_sessions_started", "sessions": results})
		return
	}

	members := rt.seedCollaborators(r.Context(), proj.ID, id, body.Collaborators)

	writeJSON(w, http.StatusCreated, map[string]any{
		"project_id": proj.ID,
		"title":      title,
		"sessions":   results,
		"members":    members,
	})
}

// seedCollaborators seeds the owner as lead (idempotent) and adds each
// collaborator email as a member, best-effort. It returns the emails that were
// added — a failure to seed one collaborator is reported by omission, never
// fatal to the create.
func (rt *Router) seedCollaborators(ctx context.Context, projectID string, owner Identity, emails []string) []string {
	if rt.members == nil {
		return nil
	}
	added := []string{}
	if err := rt.members.SeedLead(ctx, projectID, owner.Email); err != nil {
		return added
	}
	p := access.Principal{Sub: owner.Sub, Email: owner.Email}
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
type ProjectView struct {
	project.Project
	SessionCount int    `json:"session_count"`
	Running      int    `json:"running"`
	LatestPhase  string `json:"latest_phase,omitempty"`
}

// listProjects reports the caller's own projects, newest first, each with a
// session summary. The scope is the authenticated principal alone.
func (rt *Router) listProjects(w http.ResponseWriter, r *http.Request) {
	owner := principal(r)
	projects, err := rt.projects.ListByOwner(r.Context(), owner)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	status, err := rt.jobs.StatusByProject(r.Context(), owner)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	out := make([]ProjectView, 0, len(projects))
	for _, p := range projects {
		st := status[p.ID]
		out = append(out, ProjectView{Project: p, SessionCount: st.Total, Running: st.Running, LatestPhase: string(st.Latest)})
	}
	writeJSON(w, http.StatusOK, out)
}

// summarize builds a project's session summary from its sessions (for lists
// where the sessions are already loaded — shared and public projects).
func summarize(sessions []job.Job) ProjectView {
	v := ProjectView{SessionCount: len(sessions)}
	for i, s := range sessions {
		if s.Phase != job.PhaseDone && s.Phase != job.PhaseFailed {
			v.Running++
		}
		if i == 0 {
			v.LatestPhase = string(s.Phase) // sessions are newest-first
		}
	}
	return v
}

// listPublic is the public zone: every public project, newest first, each
// carrying its initiator (the owning principal), so any signed-in user can
// browse what others have published (006f).
func (rt *Router) listPublic(w http.ResponseWriter, r *http.Request) {
	projects, err := rt.projects.ListPublic(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	out := make([]ProjectView, 0, len(projects))
	for _, p := range projects {
		v := ProjectView{Project: p}
		if sessions, err := rt.jobs.ListByProject(r.Context(), p.ID); err == nil {
			v = summarize(sessions)
			v.Project = p
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// makeProjectPublic is the owner's one-way public flip, gated by a responsibility
// sign-off (006f). It refuses a non-owner (403), refuses a project none of whose
// published sessions cleared 002's redistribution gate (409 not_clear), records
// the sign-off, marks the project public, and — best-effort — flips the forge
// repository public when a publisher is wired.
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

	if err := rt.projects.SetPublic(r.Context(), proj.ID, principal(r), body.AttestationVersion); err != nil {
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
	return proj, id, true
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
	if err := rt.members.SeedLead(r.Context(), proj.ID, id.Email); err != nil {
		writeError(w, http.StatusInternalServerError, "member_add_failed")
		return
	}
	if err := rt.members.Add(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email}, proj.ID, body.Email, role); err != nil {
		writeError(w, http.StatusInternalServerError, "member_add_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// removeMember drops a collaborator by email, owner-only.
func (rt *Router) removeMember(w http.ResponseWriter, r *http.Request) {
	proj, id, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	if err := rt.members.SeedLead(r.Context(), proj.ID, id.Email); err != nil {
		writeError(w, http.StatusInternalServerError, "member_remove_failed")
		return
	}
	if err := rt.members.Remove(r.Context(), access.Principal{Sub: id.Sub, Email: id.Email}, proj.ID, r.PathValue("email")); err != nil {
		writeError(w, http.StatusInternalServerError, "member_remove_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listShared is the "shared with me" surface: projects the caller is a
// collaborator on (not their own), each with a session count.
func (rt *Router) listShared(w http.ResponseWriter, r *http.Request) {
	id, _ := IdentityFrom(r)
	ids, err := rt.members.Projects(r.Context(), id.Sub)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	out := make([]ProjectView, 0, len(ids))
	for _, pid := range ids {
		p, err := rt.projects.Get(r.Context(), pid)
		if err != nil {
			continue // a membership whose project is gone is skipped, not fatal
		}
		v := ProjectView{Project: p}
		if sessions, err := rt.jobs.ListByProject(r.Context(), pid); err == nil {
			v = summarize(sessions)
			v.Project = p
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// ProjectDetail is one project with its sessions, newest first.
type ProjectDetail struct {
	Project  project.Project `json:"project"`
	Sessions []job.Job       `json:"sessions"`
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
	if p.Visibility == project.VisibilityPublic || p.Owner == id.Sub {
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
	j, err := rt.jobs.Get(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return job.Job{}, false
	}
	p, err := rt.projects.Get(r.Context(), j.ProjectID)
	if err != nil || !rt.mayViewProject(r, p) {
		writeError(w, http.StatusNotFound, "not_found")
		return job.Job{}, false
	}
	return j, true
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
	writeJSON(w, http.StatusOK, ProjectDetail{Project: proj, Sessions: sessions})
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
	Events []provider.Event `json:"events"`
	Usage  *provider.Usage  `json:"usage"`
	// Live reports that these events came from an in-flight run's live stream, so
	// the client keeps polling for more.
	Live bool `json:"live"`
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
	// Prefer the session's recorded usage; when it was never recorded (a
	// historical backfill), derive it from the transcript's per-request usage so
	// the feature is not blank on real data. Only when neither exists is usage
	// nil ("not recorded") — never a fabricated zero. Derive before stripping
	// raw, since the per-request usage lives in raw.
	var usage *provider.Usage
	switch {
	case j.Status.Usage.Total() > 0:
		u := j.Status.Usage
		usage = &u
	default:
		if u, ok := deriveUsage(events); ok {
			usage = &u
		}
	}
	writeJSON(w, http.StatusOK, Transcript{Events: stripRaw(events), Usage: usage, Live: live})
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
// is retained here (usage derivation reads it) and dropped from the response by
// stripRaw before serving.
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

// stripRaw clears each event's backend-original payload: the viewer renders the
// neutral fields, and raw is the bulk of a transcript (megabytes). It returns
// the same slice for chaining.
func stripRaw(events []provider.Event) []provider.Event {
	for i := range events {
		events[i].Raw = nil
	}
	return events
}

// deriveUsage sums per-request token counts a managed-agents transcript records
// in each event's raw payload (a `model_usage` object), so a session whose usage
// was never recorded still shows real numbers. ok is false when no event carries
// usage — then the caller shows "not recorded" rather than a fabricated zero.
// The field selection matches the anthropic adapter's live-run mapping, so a
// derived total agrees with a recorded one.
func deriveUsage(events []provider.Event) (provider.Usage, bool) {
	var u provider.Usage
	found := false
	for _, e := range events {
		if len(e.Raw) == 0 {
			continue
		}
		var wrap struct {
			ModelUsage *struct {
				InputTokens          int64 `json:"input_tokens"`
				OutputTokens         int64 `json:"output_tokens"`
				CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
			} `json:"model_usage"`
		}
		if json.Unmarshal(e.Raw, &wrap) != nil || wrap.ModelUsage == nil {
			continue
		}
		found = true
		u.InputTokens += wrap.ModelUsage.InputTokens
		u.OutputTokens += wrap.ModelUsage.OutputTokens
		u.CachedInputTokens += wrap.ModelUsage.CacheReadInputTokens
	}
	return u, found
}

// getJob reports a session's status and phase, scoped to who may view its
// project.
func (rt *Router) getJob(w http.ResponseWriter, r *http.Request) {
	j, ok := rt.viewableSession(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, j)
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
