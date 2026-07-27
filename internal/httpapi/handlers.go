package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/latere-ai/replichai/internal/job"
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
	rt.gatedHandle("GET", "/jobs", rt.listJobs)
	rt.gatedHandle("GET", "/jobs/{id}", rt.getJob)
	rt.gatedHandle("GET", "/jobs/{id}/events", rt.jobEvents)
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

// submitPaper enqueues a job and returns its id without waiting on the run. No
// request handler ever blocks on an audit.
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
	j, err := rt.jobs.Enqueue(r.Context(), principal(r), body.URL, job.KindAudit, body.Project)
	if err != nil {
		if errors.Is(err, job.ErrEmptyRef) {
			writeError(w, http.StatusBadRequest, "missing_url")
			return
		}
		writeError(w, http.StatusInternalServerError, "enqueue_failed")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": j.ID})
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
