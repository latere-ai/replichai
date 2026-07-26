package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/latere-ai/replichai/internal/job"
)

// principalKey carries the authenticated principal (an OIDC sub) on the request
// context. 006c's auth middleware sets it; this package only reads it, so who a
// principal is stays 006c's concern.
type ctxKey int

const principalKey ctxKey = iota

// WithPrincipal returns a context carrying the authenticated principal.
func WithPrincipal(ctx context.Context, sub string) context.Context {
	return context.WithValue(ctx, principalKey, sub)
}

// principal reads the authenticated principal, empty when unauthenticated.
func principal(r *http.Request) string {
	s, _ := r.Context().Value(principalKey).(string)
	return s
}

// routes registers every endpoint through the two registration helpers, so the
// gated and open sets come from one place and cannot drift. The open routes
// (sign-in, /me) are 006c's to add; this spec owns the operator set.
func (rt *Router) routes() {
	// A health check sits outside the gate so a load balancer can reach it; the
	// sign-in and /me routes join the open set with 006c.
	rt.openHandle("GET", "/healthz", rt.health)
	rt.gatedHandle("POST", "/papers", rt.submitPaper)
	rt.gatedHandle("GET", "/jobs/{id}", rt.getJob)
	rt.gatedHandle("GET", "/reproductions", rt.listReproductions)
	rt.gatedHandle("GET", "/reproductions/{id}", rt.getReproduction)
	rt.gatedHandle("GET", "/reproductions/{id}/transcript", rt.getTranscript)
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
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, repro)
}

// getTranscript reads a reproduction's transcript (005) through the reader.
func (rt *Router) getTranscript(w http.ResponseWriter, r *http.Request) {
	body, err := rt.repro.Transcript(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
