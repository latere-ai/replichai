// Package runtimes composes managed runtimes without package-global
// registration or adapter-specific dependencies.
package runtimes

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
)

type Factory func(context.Context) (managed.Runtime, error)

type Entry struct {
	Name    managed.RuntimeName
	Aliases []string
	Build   Factory
}

type Catalog struct {
	entries map[managed.RuntimeName]Entry
	aliases map[managed.RuntimeName]managed.RuntimeName
}

func NewCatalog(entries ...Entry) (*Catalog, error) {
	catalog := &Catalog{entries: make(map[managed.RuntimeName]Entry, len(entries)), aliases: make(map[managed.RuntimeName]managed.RuntimeName)}
	for _, entry := range entries {
		name := managed.CanonicalRuntimeName(string(entry.Name))
		if name == "" || name != entry.Name {
			return nil, fmt.Errorf("invalid canonical runtime name %q", entry.Name)
		}
		if entry.Build == nil {
			return nil, fmt.Errorf("runtime %q has no factory", name)
		}
		if _, exists := catalog.entries[name]; exists {
			return nil, fmt.Errorf("duplicate runtime %q", name)
		}
		if _, exists := catalog.aliases[name]; exists {
			return nil, fmt.Errorf("runtime name %q conflicts with an alias", name)
		}
		entry.Name = name
		catalog.entries[name] = entry
		for _, rawAlias := range entry.Aliases {
			alias := managed.CanonicalRuntimeName(rawAlias)
			if alias == "" {
				return nil, fmt.Errorf("runtime %q has an empty alias", name)
			}
			if _, exists := catalog.entries[alias]; exists {
				return nil, fmt.Errorf("runtime alias %q conflicts with a name", alias)
			}
			if previous, exists := catalog.aliases[alias]; exists && previous != name {
				return nil, fmt.Errorf("runtime alias %q is duplicated", alias)
			}
			catalog.aliases[alias] = name
		}
	}
	return catalog, nil
}

func (c *Catalog) Names() []managed.RuntimeName {
	names := make([]managed.RuntimeName, 0, len(c.entries))
	for name := range c.entries {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names
}

func (c *Catalog) Canonical(raw string) (managed.RuntimeName, error) {
	name := managed.CanonicalRuntimeName(raw)
	if canonical, ok := c.aliases[name]; ok {
		return canonical, nil
	}
	if _, ok := c.entries[name]; ok {
		return name, nil
	}
	return "", &UnavailableError{Runtime: name, Available: c.Names()}
}

func (c *Catalog) Build(ctx context.Context, raw string) (managed.Runtime, error) {
	name, err := c.Canonical(raw)
	if err != nil {
		return nil, err
	}
	runtime, err := c.entries[name].Build(ctx)
	if err != nil {
		return nil, fmt.Errorf("build runtime %q: %w", name, err)
	}
	if runtime == nil {
		return nil, fmt.Errorf("build runtime %q: factory returned nil", name)
	}
	if runtime.Name() != name {
		return nil, fmt.Errorf("build runtime %q: adapter identifies as %q", name, runtime.Name())
	}
	return runtime, nil
}

type UnavailableError struct {
	Runtime   managed.RuntimeName
	Available []managed.RuntimeName
}

func (e *UnavailableError) Error() string {
	choices := make([]string, len(e.Available))
	for index, name := range e.Available {
		choices[index] = string(name)
	}
	if len(choices) == 0 {
		return fmt.Sprintf("managed runtime %q is unavailable; no runtimes are installed", e.Runtime)
	}
	return fmt.Sprintf("managed runtime %q is unavailable; available: %s", e.Runtime, strings.Join(choices, ", "))
}
