package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/project"
	"github.com/latere-ai/replichai/internal/provider"
)

// Identity is the authenticated caller as the auth middleware (006c) resolved
// it: the stable subject and the IdP-authenticated email. The gate consumes
// these and nothing else. This package only reads the identity off the request;
// who a principal *is* and what they may do stays 006c's concern.
type Identity struct {
	Sub   string
	Email string
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
	rt.gatedHandle("GET", "/projects", rt.listProjects)
	rt.gatedHandle("GET", "/projects/{id}", rt.getProject)
	rt.gatedHandle("GET", "/jobs", rt.listJobs)
	rt.gatedHandle("GET", "/jobs/{id}", rt.getJob)
	rt.gatedHandle("GET", "/jobs/{id}/events", rt.jobEvents)
	rt.gatedHandle("GET", "/jobs/{id}/transcript", rt.getSessionTranscript)
	rt.gatedHandle("GET", "/reproductions", rt.listReproductions)
	rt.gatedHandle("GET", "/reproductions/{id}", rt.getReproduction)
	rt.gatedHandle("GET", "/reproductions/{id}/transcript", rt.getTranscript)

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

// ProjectView is a project in the owner's list, carrying how many sessions it
// holds so the list needs no per-project read on the client.
type ProjectView struct {
	project.Project
	SessionCount int `json:"session_count"`
}

// listProjects reports the caller's own projects, newest first, each with a
// session count. The scope is the authenticated principal alone.
func (rt *Router) listProjects(w http.ResponseWriter, r *http.Request) {
	owner := principal(r)
	projects, err := rt.projects.ListByOwner(r.Context(), owner)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	counts, err := rt.jobs.CountByProject(r.Context(), owner)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_failed")
		return
	}
	out := make([]ProjectView, 0, len(projects))
	for _, p := range projects {
		out = append(out, ProjectView{Project: p, SessionCount: counts[p.ID]})
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
	if proj.Owner != principal(r) {
		writeError(w, http.StatusForbidden, "not_owner")
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
}

// getSessionTranscript serves a session's committed transcript as structured
// events plus its recorded usage (006h). It resolves the session, reads its
// repository's transcript.jsonl through the forge pass-through, parses the
// events, and pairs them with the session's usage. A session that has not
// published a transcript yet answers 409 not_ready, distinct from a 404 for a
// session that does not exist.
func (rt *Router) getSessionTranscript(w http.ResponseWriter, r *http.Request) {
	j, err := rt.jobs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if j.RepoName == "" {
		// The run has not published its transcript yet — not a miss, not ready.
		writeError(w, http.StatusConflict, "not_ready")
		return
	}
	raw, err := rt.repro.Transcript(r.Context(), repoName(j.RepoName))
	if err != nil {
		writeReadError(w, err) // 404 for a genuine miss, 500 for a transport failure
		return
	}
	events, err := parseTranscriptEvents(raw)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "transcript_malformed")
		return
	}
	out := Transcript{Events: events}
	// A run always spends tokens, so a zero total means usage was never
	// recorded — show "not recorded" (nil), never a fabricated zero.
	if j.Status.Usage.Total() > 0 {
		u := j.Status.Usage
		out.Usage = &u
	}
	writeJSON(w, http.StatusOK, out)
}

// repoName strips an "owner/" prefix from a repository path, so a session's
// recorded "open-hai/name" resolves to the "name" the forge source keys on.
func repoName(full string) string {
	if i := strings.LastIndex(full, "/"); i >= 0 {
		return full[i+1:]
	}
	return full
}

// parseTranscriptEvents parses the newline-delimited provider.Event stream
// committed as transcript.jsonl (005). A blank line is skipped; a malformed
// line fails the parse, so a corrupt transcript is a 500, not a silent gap.
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
		// Drop the backend's raw event: the viewer renders the neutral fields, and
		// raw is the bulk of the payload (a full transcript is megabytes of it).
		e.Raw = nil
		events = append(events, e)
	}
	return events, nil
}

// getJob reports a job's status and phase.
func (rt *Router) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := rt.jobs.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// listReproductions reads the derived registry (003) straight through.
func (rt *Router) listReproductions(w http.ResponseWriter, r *http.Request) {
	list, err := rt.repro.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// getReproduction reads one reproduction's manifest view.
func (rt *Router) getReproduction(w http.ResponseWriter, r *http.Request) {
	repro, err := rt.repro.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeReadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, repro)
}

// getTranscript reads a reproduction's transcript (005) through the reader.
func (rt *Router) getTranscript(w http.ResponseWriter, r *http.Request) {
	body, err := rt.repro.Transcript(r.Context(), r.PathValue("id"))
	if err != nil {
		writeReadError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
