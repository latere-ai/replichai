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
	"github.com/latere-ai/replichai/internal/notify"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/profile"
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
	// A researcher's page is readable by anyone, signed in or not (006r) — the
	// same openness the public zone has, for the person behind it.
	rt.openHandle("GET", "/profiles/{handle}", rt.publicProfile)
	rt.gatedHandle("GET", "/me/profile", rt.getProfile)
	rt.gatedHandle("PATCH", "/me/profile", rt.saveProfile)
	rt.gatedHandle("POST", "/papers", rt.submitPaper)
	rt.gatedHandle("POST", "/resolve", rt.resolvePaper)
	rt.gatedHandle("POST", "/projects", rt.createProject)
	rt.gatedHandle("PATCH", "/projects/{id}", rt.renameProject)
	rt.gatedHandle("GET", "/projects", rt.listProjects)
	rt.gatedHandle("GET", "/shared", rt.listShared)
	rt.gatedHandle("GET", "/public", rt.listPublic)
	rt.gatedHandle("GET", "/projects/{id}", rt.getProject)
	rt.gatedHandle("POST", "/projects/{id}/public", rt.makeProjectPublic)
	rt.gatedHandle("POST", "/projects/{id}/private", rt.withdrawProject)
	rt.gatedHandle("POST", "/projects/{id}/sponsor", rt.sponsorProject)
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
		// ChargeTo picks whose quota this session spends (006l): "me" (the
		// default) or "project", which draws the owner's and is allowed only in
		// a project they have chosen to sponsor. The payer itself is always
		// resolved server-side; a client never names a principal.
		ChargeTo string `json:"charge_to"`
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
	rt.submitToExistingProject(w, r, owner, body.Project, body.URL, paperKey, body.ChargeTo)
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
	j, err := rt.jobs.Enqueue(r.Context(), job.Submission{Principal: owner, Ref: url, Kind: job.KindAudit, Project: proj.ID, PaperKey: paperKey})
	if err != nil {
		_ = rt.projects.Delete(r.Context(), proj.ID) // compensate: never leave an empty project
		rt.writeEnqueueError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": j.ID, "project_id": proj.ID})
}

// submitToExistingProject adds a session to a project the caller owns or
// collaborates on, refusing a project they are neither of and a paper already
// running in it. The payer is resolved here, never taken from the body.
func (rt *Router) submitToExistingProject(w http.ResponseWriter, r *http.Request, owner, projectID, url, paperKey, chargeTo string) {
	proj, err := rt.projects.Get(r.Context(), projectID)
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project_not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "project_read_failed")
		return
	}
	if !rt.mayWorkInProject(r, proj, owner) {
		writeError(w, http.StatusForbidden, "not_owner")
		return
	}
	payer, ok := rt.resolvePayer(w, proj, owner, chargeTo)
	if !ok {
		return
	}
	j, err := rt.jobs.Enqueue(r.Context(), job.Submission{
		Principal: owner, Ref: url, Kind: job.KindAudit, Project: proj.ID, PaperKey: paperKey, ChargedTo: payer,
	})
	if err != nil {
		rt.writeEnqueueError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": j.ID, "project_id": proj.ID})
}

// mayWorkInProject reports whether a caller may open a session in a project:
// its owner, or a collaborator it was shared with. Reading a public project
// does not confer this — spending someone's quota is not a read.
func (rt *Router) mayWorkInProject(r *http.Request, p project.Project, sub string) bool {
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

// resolvePayer turns the submitter's choice into a principal. "project" draws
// the owner's quota and is allowed only where the owner turned sponsorship on;
// anything else charges the submitter. The client picks between two options, it
// never names a payer.
func (rt *Router) resolvePayer(w http.ResponseWriter, p project.Project, submitter, chargeTo string) (string, bool) {
	if strings.TrimSpace(chargeTo) != "project" {
		return submitter, true
	}
	if !p.Sponsored {
		writeError(w, http.StatusForbidden, "not_sponsored")
		return "", false
	}
	return p.Owner, true
}

// writeEnqueueError maps an Enqueue failure to a status: the double-trigger
// guard is a 409, an empty reference a 400, anything else a 500.
func (rt *Router) writeEnqueueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, job.ErrActiveSession):
		writeError(w, http.StatusConflict, "active_session")
	case errors.Is(err, job.ErrQuotaExceeded):
		writeError(w, http.StatusPaymentRequired, "quota_exceeded")
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
		} else if j, eErr := rt.jobs.Enqueue(r.Context(), job.Submission{Principal: id.Sub, Ref: url, Kind: job.KindAudit, Project: proj.ID, PaperKey: key}); eErr != nil {
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

// sponsorProject records or withdraws the owner's standing consent to cover
// their collaborators' sessions (006l). Owner-only: a collaborator can never
// turn on an allowance that is not theirs to give.
func (rt *Router) sponsorProject(w http.ResponseWriter, r *http.Request) {
	proj, _, ok := rt.ownedProject(w, r)
	if !ok {
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if err := rt.projects.SetSponsored(r.Context(), proj.ID, body.On); err != nil {
		writeError(w, http.StatusInternalServerError, "sponsor_failed")
		return
	}
	proj.Sponsored = body.On
	writeJSON(w, http.StatusOK, proj)
}

// enqueueErrorCode maps an Enqueue failure to the machine-readable code the
// per-paper session result carries.
func enqueueErrorCode(err error) string {
	switch {
	case errors.Is(err, job.ErrActiveSession):
		return "active_session"
	case errors.Is(err, job.ErrQuotaExceeded):
		return "quota_exceeded"
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
		// The zone names a project by its signer, so it links to them too when
		// they have a page.
		v.SignerHandle = rt.signerHandle(r.Context(), p)
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
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
	// The forge follows, best-effort: the project is already private in-app, and a
	// forge outage must not strand an owner mid-withdrawal. Every repository the
	// project owns is closed, not only those the publish flipped — nothing records
	// which those were, and closing an already-closed repository costs nothing.
	// The repositories come from the sessions directly rather than from
	// clearedForPublish, because a project is often withdrawn precisely because
	// its tree stopped being clear, and that must not be what keeps the repository
	// open.
	if rt.repoPub != nil {
		sessions, err := rt.jobs.ListByProject(r.Context(), proj.ID)
		if err != nil {
			log.Printf("withdraw: project %s sessions unreadable, repositories left as they are: %v", proj.ID, err)
		}
		for _, s := range sessions {
			if s.RepoName == "" {
				continue
			}
			if err := rt.repoPub.SetPrivate(r.Context(), repoName(s.RepoName)); err != nil {
				log.Printf("withdraw: repo %s not closed: %v", s.RepoName, err)
			}
		}
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
		// Same rule as the public zone: a project named by its signer links to
		// them when they have a page. Usually empty here — a shared project is
		// typically private and unsigned — but the two lists must not answer the
		// same question differently.
		v.SignerHandle = rt.signerHandle(r.Context(), p)
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

// ProjectDetail is one project with its sessions, newest first.
type ProjectDetail struct {
	Project  project.Project `json:"project"`
	Sessions []job.Job       `json:"sessions"`
	// MaySubmit reports whether this caller may open a session here — its owner
	// or a collaborator, never a stranger who reached it through the public zone
	// (006l). The client cannot work this out: it does not know membership, and a
	// public project is readable by everyone.
	MaySubmit bool `json:"may_submit"`
	// SignerHandle is where the signer's name links; see ProjectView.
	SignerHandle string `json:"signer_handle,omitempty"`
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
	writeJSON(w, http.StatusOK, ProjectDetail{
		Project:      proj,
		Sessions:     sessions,
		MaySubmit:    rt.mayWorkInProject(r, proj, principal(r)),
		SignerHandle: rt.signerHandle(r.Context(), proj),
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
