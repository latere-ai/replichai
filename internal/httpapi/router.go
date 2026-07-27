// Package httpapi is the HTTP server that wraps the audit engine: submit a
// paper, watch a job, read a result. It translates HTTP to the job model and
// back and adds no engine logic — every handler depends on an interface (the
// job store, a registry reader, the gate), never on internal/audit, its method,
// or a provider adapter, so the whole surface tests against fakes with no
// database and no network (006a).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/latere-ai/replichai/internal/job"
	"github.com/latere-ai/replichai/internal/project"
)

// Route is one registered endpoint, method and pattern, so the gated set is
// enumerable for 006c's test.
type Route struct {
	Method  string
	Pattern string
}

// Gate decides whether a request may reach an operator route. This package
// mounts it and decides which routes it covers; 006c supplies the policy (who
// passes). The default is fail-closed — an unconfigured server admits nobody.
type Gate interface {
	// Allow reports whether the request may proceed, and a machine-readable
	// reason to return when it may not (e.g. "not_invited").
	Allow(r *http.Request) (ok bool, reason string)
}

// GateFunc adapts a function to a Gate.
type GateFunc func(r *http.Request) (bool, string)

// Allow calls the function.
func (f GateFunc) Allow(r *http.Request) (bool, string) { return f(r) }

// DenyAll is the fail-closed default gate: it admits nobody, so a server wired
// without a real gate cannot accidentally ship its operator routes open.
var DenyAll Gate = GateFunc(func(*http.Request) (bool, string) { return false, "not_invited" })

// JobStore is the slice of the job store the handlers need — enqueue a
// submission, read one back. Depending on this interface rather than a concrete
// store keeps engine construction in the composition root and out of the
// handlers, which is what the no-engine-logic dependency test enforces.
type JobStore interface {
	Enqueue(ctx context.Context, principal, ref string, k job.Kind, project, paperKey string) (job.Job, error)
	Get(ctx context.Context, id string) (job.Job, error)
	ListByPrincipal(ctx context.Context, principal string) ([]job.Job, error)
	ListByProject(ctx context.Context, projectID string) ([]job.Job, error)
	CountByProject(ctx context.Context, owner string) (map[string]int, error)
}

// ProjectStore is the slice of the project store the handlers need — open a
// project, read one back, list an owner's, and the compensating delete when a
// project's first session fails to enqueue (006g).
type ProjectStore interface {
	Create(ctx context.Context, owner, title string) (project.Project, error)
	Get(ctx context.Context, id string) (project.Project, error)
	ListByOwner(ctx context.Context, owner string) ([]project.Project, error)
	Delete(ctx context.Context, id string) error
}

// ReproReader is the read surface over the derived registry (003) and the
// transcript (005). It presents what the forge and the manifests already hold;
// no handler caches a reproduction into its own shape.
//
// A genuine miss must be reported as ErrReproNotFound; any other error is a
// transport or backend failure and becomes a 500, so a canceled request or a
// forge outage is never disguised as "not found".
type ReproReader interface {
	List(ctx context.Context) ([]Reproduction, error)
	Get(ctx context.Context, id string) (Reproduction, error)
	Transcript(ctx context.Context, id string) ([]byte, error)
}

// ErrReproNotFound is the sentinel a ReproReader returns for a genuine miss.
var ErrReproNotFound = errors.New("reproduction not found")

// Reproduction is the presented shape of one published reproduction. Field
// details settle here; it is a view over 003's manifest, never a stored copy.
type Reproduction struct {
	ID      string `json:"id"`
	Repo    string `json:"repo"`
	Title   string `json:"title"`
	Verdict string `json:"verdict"`
}

// Deps are what a Router is built from — all interfaces, so tests supply fakes.
type Deps struct {
	Jobs          JobStore
	Projects      ProjectStore
	Reproductions ReproReader
	Gate          Gate
	// SSEInterval is how often the event stream re-reads a job's state. Zero
	// selects a sensible default.
	SSEInterval time.Duration
	// SPA, when set, serves the frontend for any GET path not matched by an API
	// route — the public app shell (006e). Nil in API-only tests.
	SPA http.Handler
}

// Router wires the routes, mounts the gate on the operator set, and keeps the
// gated and open sets enumerable so the two cannot drift — both come from one
// registration helper, never a second mux.Handle call.
type Router struct {
	mux   *http.ServeMux
	gate  Gate
	gated []Route
	open  []Route

	jobs        JobStore
	projects    ProjectStore
	repro       ReproReader
	sseInterval time.Duration
	spa         http.Handler
}

// defaultSSEInterval is how often the event stream re-reads a job when the
// caller does not choose.
const defaultSSEInterval = time.Second

// NewRouter builds a fully wired router. A nil gate is fail-closed (DenyAll).
func NewRouter(d Deps) *Router {
	gate := d.Gate
	if gate == nil {
		gate = DenyAll
	}
	interval := d.SSEInterval
	if interval <= 0 {
		interval = defaultSSEInterval
	}
	rt := &Router{mux: http.NewServeMux(), gate: gate, jobs: d.Jobs, projects: d.Projects, repro: d.Reproductions, sseInterval: interval, spa: d.SPA}
	rt.routes()
	return rt
}

// Handler returns the wired handler, gate mounted.
func (rt *Router) Handler() http.Handler { return rt.mux }

// GatedRoutes enumerates every operator route from the router itself, so 006c's
// test can assert each answers not_invited for an ungranted principal and that
// no route was registered outside the gate.
func (rt *Router) GatedRoutes() []Route { return append([]Route(nil), rt.gated...) }

// OpenRoutes enumerates the routes deliberately outside the gate.
func (rt *Router) OpenRoutes() []Route { return append([]Route(nil), rt.open...) }

// gatedHandle registers an operator route: it is wrapped in the gate and
// recorded in the gated set. This is the only path a gated route is added, so a
// route cannot be registered open by mistake.
func (rt *Router) gatedHandle(method, pattern string, h http.HandlerFunc) {
	rt.gated = append(rt.gated, Route{method, pattern})
	rt.mux.HandleFunc(method+" "+pattern, rt.guard(h))
}

// openHandle registers a route outside the gate (sign-in, /me, the not-invited
// path), recorded so the two sets are comparable.
func (rt *Router) openHandle(method, pattern string, h http.HandlerFunc) {
	rt.open = append(rt.open, Route{method, pattern})
	rt.mux.HandleFunc(method+" "+pattern, h)
}

// guard is the gate middleware: it refuses a request the gate does not allow
// with 403 and the gate's reason, before the handler runs.
func (rt *Router) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ok, reason := rt.gate.Allow(r); !ok {
			writeError(w, http.StatusForbidden, reason)
			return
		}
		h(w, r)
	}
}

// writeJSON writes v as JSON with a status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a machine-readable error code.
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

// WriteError writes a machine-readable error code, for middleware (006c's tier
// guard) that refuses a request before the handler runs.
func WriteError(w http.ResponseWriter, status int, code string) {
	writeError(w, status, code)
}

// mustJSON marshals v, returning an empty object on the impossible error so a
// stream write never panics.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
