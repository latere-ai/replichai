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
	"net/http"

	"github.com/latere-ai/replichai/internal/job"
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
	Enqueue(ctx context.Context, principal, ref string, k job.Kind, project string) (job.Job, error)
	Get(ctx context.Context, id string) (job.Job, error)
}

// ReproReader is the read surface over the derived registry (003) and the
// transcript (005). It presents what the forge and the manifests already hold;
// no handler caches a reproduction into its own shape.
type ReproReader interface {
	List(ctx context.Context) ([]Reproduction, error)
	Get(ctx context.Context, id string) (Reproduction, error)
	Transcript(ctx context.Context, id string) ([]byte, error)
}

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
	Reproductions ReproReader
	Gate          Gate
}

// Router wires the routes, mounts the gate on the operator set, and keeps the
// gated and open sets enumerable so the two cannot drift — both come from one
// registration helper, never a second mux.Handle call.
type Router struct {
	mux   *http.ServeMux
	gate  Gate
	gated []Route
	open  []Route

	jobs  JobStore
	repro ReproReader
}

// NewRouter builds a fully wired router. A nil gate is fail-closed (DenyAll).
func NewRouter(d Deps) *Router {
	gate := d.Gate
	if gate == nil {
		gate = DenyAll
	}
	rt := &Router{mux: http.NewServeMux(), gate: gate, jobs: d.Jobs, repro: d.Reproductions}
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
