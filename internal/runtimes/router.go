package runtimes

import (
	"fmt"

	"github.com/latere-ai/replichai/internal/managed"
)

const RouteVersion = 1

type Route struct {
	Version int                 `json:"version"`
	Runtime managed.RuntimeName `json:"runtime"`
}

func (route Route) Validate() error {
	if route.Version != RouteVersion {
		return fmt.Errorf("unsupported managed route version %d", route.Version)
	}
	if managed.CanonicalRuntimeName(string(route.Runtime)) == "" || managed.CanonicalRuntimeName(string(route.Runtime)) != route.Runtime {
		return fmt.Errorf("invalid managed route runtime %q", route.Runtime)
	}
	return nil
}

type Router struct {
	runtimes       map[managed.RuntimeName]managed.Runtime
	defaultRuntime managed.RuntimeName
}

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

func (r *Router) Route() Route { return Route{Version: RouteVersion, Runtime: r.defaultRuntime} }

func (r *Router) Bind(route Route) (managed.RuntimeRouter, error) {
	if err := route.Validate(); err != nil {
		return nil, err
	}
	if _, ok := r.runtimes[route.Runtime]; !ok {
		return nil, r.unavailable(route.Runtime)
	}
	return boundRouter{router: r, route: route}, nil
}

func (r *Router) WithDefault(name managed.RuntimeName) (*Router, error) {
	if _, ok := r.runtimes[name]; !ok {
		return nil, r.unavailable(name)
	}
	return &Router{runtimes: r.runtimes, defaultRuntime: name}, nil
}

func (r *Router) ForNew(managed.SessionSpec) (managed.Runtime, error) {
	return r.runtime(r.defaultRuntime)
}

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

func (r boundRouter) ForNew(managed.SessionSpec) (managed.Runtime, error) {
	return r.router.runtime(r.route.Runtime)
}

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
