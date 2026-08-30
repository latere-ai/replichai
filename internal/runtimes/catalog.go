// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package runtimes composes managed runtimes without package-global
// registration or adapter-specific dependencies.
package runtimes

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
)

// Factory constructs one runtime on demand. Construction is deferred rather
// than done at registration so a catalogue can be assembled, and its names
// resolved, before any credential is in hand: both composition roots build a
// zero-config catalogue to validate the operator's chosen name, then rebuild a
// configured one.
type Factory func(context.Context) (managed.Runtime, error)

// Entry is one runtime's registration in a Catalog: the canonical name it
// answers to, the other spellings that route to it, and how to construct it.
type Entry struct {
	// Name must already be its own canonical form; NewCatalog rejects an
	// entry rather than folding the name for the caller.
	Name managed.RuntimeName
	// Aliases are additional names that resolve to this entry. They are
	// canonicalised on registration and may not collide with any entry's
	// name or with another entry's alias.
	Aliases []string
	// Build is required: an entry without a factory is a name that could
	// never be honoured, so NewCatalog refuses it.
	Build Factory
}

// Catalog is the set of runtimes one process has installed, fixed at
// construction. It stands in for package-global registration, so what a binary
// can run is decided by the entries its composition root passes in and by
// nothing else.
type Catalog struct {
	entries map[managed.RuntimeName]Entry
	aliases map[managed.RuntimeName]managed.RuntimeName
}

// NewCatalog validates the entries and returns the catalogue they describe. A
// bad entry fails the whole call instead of being skipped: a name that is not
// canonical, or one claimed twice as a name or an alias, would make lookup
// ambiguous, and a persisted session that resolves to the wrong adapter cannot
// be detected later.
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

// Names lists the canonical names installed, sorted, so the choices an
// UnavailableError offers the operator come out in a stable order. Aliases are
// left out: they resolve, but they are not what a caller should be told to
// spell.
func (c *Catalog) Names() []managed.RuntimeName {
	names := make([]managed.RuntimeName, 0, len(c.entries))
	for name := range c.entries {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Canonical folds operator-supplied text and resolves it, through aliases, to
// an installed runtime's canonical name. It answers without constructing
// anything, which is what lets a caller reject an unknown name before it goes
// looking for credentials. An unmatched name yields an UnavailableError.
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

// Build resolves raw and constructs the runtime registered under it. It
// refuses an adapter that reports a name other than the one it was registered
// as, because routes are persisted by name: a mismatch would quietly bind
// later sessions to a different adapter than the one that started them.
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

// UnavailableError reports that nothing installed answers to the name asked
// for. It carries the installed set rather than only the miss, so the operator
// is told what they could have asked for instead of being left to guess.
type UnavailableError struct {
	// Runtime is the requested name in canonical form, not the raw text.
	Runtime managed.RuntimeName
	// Available holds the installed canonical names, sorted. It is empty
	// when no runtime is installed at all, which the message distinguishes
	// from a plain misspelling.
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
