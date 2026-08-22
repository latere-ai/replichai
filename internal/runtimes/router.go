package runtimes

import (
	"fmt"

	"github.com/latere-ai/replichai/internal/managed"
)

// RouteVersion is the only route version this package understands. Validate
// refuses any other, so an unrecognised record is rejected rather than
// reinterpreted under today's meaning.
const RouteVersion = 1

// Route is a serialisable snapshot of the routing decision taken for new work.
// It names the runtime instead of leaving one to be derived later, so work
// bound to it still runs where it was routed after the deployment default has
// moved on.
type Route struct {
	Version int                 `json:"version"`
	Runtime managed.RuntimeName `json:"runtime"`
}

// Validate reports whether the route is one this package can honour: a version
// it recognises, and a runtime name already in canonical form.
func (route Route) Validate() error {
	if route.Version != RouteVersion {
		return fmt.Errorf("unsupported managed route version %d", route.Version)
	}
	if managed.CanonicalRuntimeName(string(route.Runtime)) == "" || managed.CanonicalRuntimeName(string(route.Runtime)) != route.Runtime {
		return fmt.Errorf("invalid managed route runtime %q", route.Runtime)
	}
	return nil
}

// Router picks a managed runtime for new work and for durable references. It
// holds only runtimes proven installed at construction, so a lookup can never
// resolve to an adapter this process lacks.
type Router struct {
	runtimes       map[managed.RuntimeName]managed.Runtime
	defaultRuntime managed.RuntimeName
}

// NewRouter installs the given runtimes and pins defaultRuntime as the choice
// for new work. Construction fails on a nil runtime, a non-canonical or
// duplicated name, or a default that is not installed, so every later lookup
// works against an already-verified set.
func NewRouter(defaultRuntime managed.RuntimeName, installed ...managed.Runtime) (*Router, error) {
	router := &Router{runtimes: make(map[managed.RuntimeName]managed.Runtime, len(installed)), defaultRuntime: defaultRuntime}
	for _, runtime := range installed {
		if runtime == nil {
			return nil, fmt.Errorf("installed managed runtime is nil")
		}
		name := runtime.Name()
		if name == "" || managed.CanonicalRuntimeName(string(name)) != name {
			return nil, fmt.Errorf("installed runtime has invalid name %q", name)
		}
		if _, exists := router.runtimes[name]; exists {
			return nil, fmt.Errorf("duplicate installed runtime %q", name)
		}
		router.runtimes[name] = runtime
	}
	if _, ok := router.runtimes[defaultRuntime]; !ok {
		return nil, router.unavailable(defaultRuntime)
	}
	return router, nil
}

// Route returns the routing decision this router would take for new work, in
// the form that can be snapshotted and handed back to Bind later.
func (r *Router) Route() Route { return Route{Version: RouteVersion, Runtime: r.defaultRuntime} }

// Bind pins new work to a route taken earlier, ignoring this router's current
// default. It is the seam that lets work routed under one default keep reaching
// that runtime once the default has changed. Binding to a route whose runtime
// is not installed fails here rather than at the first session.
func (r *Router) Bind(route Route) (managed.RuntimeRouter, error) {
	if err := route.Validate(); err != nil {
		return nil, err
	}
	if _, ok := r.runtimes[route.Runtime]; !ok {
		return nil, r.unavailable(route.Runtime)
	}
	return boundRouter{router: r, route: route}, nil
}

// WithDefault returns a copy that routes new work to name. The installed
// runtimes are shared with the original, so flipping the default strands
// neither existing references nor an already-bound route.
func (r *Router) WithDefault(name managed.RuntimeName) (*Router, error) {
	if _, ok := r.runtimes[name]; !ok {
		return nil, r.unavailable(name)
	}
	return &Router{runtimes: r.runtimes, defaultRuntime: name}, nil
}

// ForNew selects the default runtime. The specification is deliberately
// ignored: the route policy is deployment-wide, and the parameter is here only
// to satisfy managed.RuntimeRouter.
func (r *Router) ForNew(managed.SessionSpec) (managed.Runtime, error) {
	return r.runtime(r.defaultRuntime)
}

// ForRef dispatches on the reference's own runtime and never on the current
// default, so an existing session cannot be handed to a different adapter. A
// runtime that is not installed yields an actionable UnavailableError instead
// of a fallback.
func (r *Router) ForRef(ref managed.SessionRef) (managed.Runtime, error) {
	if err := ref.Validate(managed.DefaultMaxLocatorBytes); err != nil {
		return nil, err
	}
	return r.runtime(ref.Runtime)
}

func (r *Router) runtime(name managed.RuntimeName) (managed.Runtime, error) {
	runtime, ok := r.runtimes[name]
	if !ok {
		return nil, r.unavailable(name)
	}
	if runtime.Name() != name {
		return nil, &managed.RuntimeMismatchError{Expected: name, Actual: runtime.Name()}
	}
	return runtime, nil
}

func (r *Router) unavailable(name managed.RuntimeName) error {
	names := make([]managed.RuntimeName, 0, len(r.runtimes))
	for candidate := range r.runtimes {
		names = append(names, candidate)
	}
	// Catalog and router errors deliberately use the same actionable shape.
	return &UnavailableError{Runtime: name, Available: sortedNames(names)}
}

type boundRouter struct {
	router *Router
	route  Route
}

// ForNew selects the runtime the bound route named, so new work follows the
// snapshot rather than the underlying router's default.
func (r boundRouter) ForNew(managed.SessionSpec) (managed.Runtime, error) {
	return r.router.runtime(r.route.Runtime)
}

// ForRef defers to the unbound router: an existing reference already carries
// the runtime it must go to, and the bound route has no say over it.
func (r boundRouter) ForRef(ref managed.SessionRef) (managed.Runtime, error) {
	return r.router.ForRef(ref)
}

func sortedNames(names []managed.RuntimeName) []managed.RuntimeName {
	sorted := append([]managed.RuntimeName(nil), names...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	return sorted
}

var _ managed.RuntimeRouter = (*Router)(nil)
var _ managed.RuntimeRouter = boundRouter{}
