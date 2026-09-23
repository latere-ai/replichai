// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"context"
	"slices"

	"github.com/latere-ai/replichai/internal/managed"
)

// ModelResolver turns a model preference into the concrete model a backend
// will run. provider.Provider satisfies it. It is declared here as one method
// so pricing does not depend on the whole provider interface.
type ModelResolver interface {
	ResolveModel(ctx context.Context, pref string) (string, error)
}

// QualifiedModelResolver is ModelResolver's provider-qualified form: it returns
// a ModelRef, so a card can distinguish two providers' identically named models
// instead of pricing whichever it names first.
//
// It is optional. A resolver that does not implement it is read through
// ModelResolver and its result attributed to Anthropic, which is what a bare
// model id meant before the managed contract existed.
type QualifiedModelResolver interface {
	ResolveModelRef(ctx context.Context, pref string) (managed.ModelRef, error)
}

// Preference is a model tier the engine asks for, and is the unit in which a
// session's cost is chosen. It is a named type over a closed set rather than a
// plain string because it crosses a trust boundary: a person picks one before
// a run (031), and the adapter passes anything it does not recognize through
// as a literal model id, which a card cannot price at its real rate.
//
// It is not called Tier because access.Tier already means what a principal may
// do, and this package reads that type. Preference is also the word the engine
// has used since 004, in modelPref, WithModelPreference, and
// ResolveModel(ctx, pref).
type Preference string

// The preferences the engine asks for. A backend maps each to one of its own
// model families; the card, not this list, determines what each costs.
const (
	PrefBest     Preference = "best"     // the most capable tier
	PrefBalanced Preference = "balanced" // capability against cost
	PrefFast     Preference = "fast"     // the cheapest and quickest
)

// DefaultPreference is the tier a session runs on when none was chosen. It is
// best because every session ran on best before the choice existed, and a
// default that silently moved existing work onto a cheaper model would change
// runs that 031 was only meant to make configurable.
const DefaultPreference = PrefBest

// Preferences are the model tiers the engine asks for. Pricing checks against
// these rather than against a vendor's catalog: what a session costs depends
// on which id a preference resolves to, not on what the catalog contains.
//
// It is also the closed set the HTTP boundary validates a submitted choice
// against, so the two cannot disagree about which tiers exist.
var Preferences = []Preference{PrefBest, PrefBalanced, PrefFast}

// Valid reports whether p is a preference the engine offers. The zero value is
// not valid: unset is a distinct state from any tier, and only the caller
// holding the field knows whether unset means the default applies, as for a
// job that predates the choice, or that the caller sent nothing.
func (p Preference) Valid() bool {
	return slices.Contains(Preferences, p)
}

// OrDefault resolves an unset preference to the default, so every reader of a
// stored choice resolves the model the same way and a row written before the
// choice existed needs no backfill.
func (p Preference) OrDefault() Preference {
	if p == "" {
		return DefaultPreference
	}
	return p
}

// String returns the preference as a string. At the boundaries that still use
// plain strings, the auditor's option and the adapter's resolver, it marks
// where the domain type is converted.
func (p Preference) String() string { return string(p) }

// Coverage is one preference checked against the card: what it resolves to, how
// that would be billed, and whether the card names it.
//
// Named is the result. When it is false the session is still priced, because
// Card.For charges the dearest rates it holds so a vendor's new model id is
// never free compute, but at another model's price. That is worth knowing
// before the first run, but it is not an error: refusing to start because a
// card is a release behind would turn a billing imprecision into an outage.
type Coverage struct {
	Preference Preference       `json:"preference"`
	Model      string           `json:"model,omitempty"`
	ModelRef   managed.ModelRef `json:"model_ref"`
	Named      bool             `json:"named"`
	Rates      Rates            `json:"rates,omitzero"`
	Err        string           `json:"error,omitempty"`
}

// Covers reports how this card would price each preference the engine asks for.
//
// It uses the caller's own resolver rather than reading a catalog, because
// that resolution decides the price: the adapter matches a preference against
// whatever the backend lists today, and a dated or suffixed id misses an exact
// match on a card that names the bare alias.
func (c Card) Covers(ctx context.Context, r ModelResolver) []Coverage {
	out := make([]Coverage, 0, len(Preferences))
	for _, pref := range Preferences {
		cov := Coverage{Preference: pref}
		// Converted to a string at the boundary: the resolver is the provider,
		// and the provider does not import pricing.
		modelRef, err := resolveModelRef(ctx, r, pref.String())
		if err != nil {
			cov.Err = err.Error()
			out = append(out, cov)
			continue
		}
		cov.Model, cov.ModelRef = modelRef.ID, modelRef
		rates, err := c.ForRef(modelRef)
		if err != nil {
			cov.Err = err.Error()
			out = append(out, cov)
			continue
		}
		cov.Rates = rates
		cov.Named = c.namesRef(modelRef)
		out = append(out, cov)
	}
	return out
}

func resolveModelRef(ctx context.Context, resolver ModelResolver, preference string) (managed.ModelRef, error) {
	if qualified, ok := resolver.(QualifiedModelResolver); ok {
		return qualified.ResolveModelRef(ctx, preference)
	}
	id, err := resolver.ResolveModel(ctx, preference)
	return managed.ModelRef{Provider: "anthropic", ID: id}, err
}

func (c Card) namesRef(model managed.ModelRef) bool {
	for _, r := range c.Models {
		if r.Zero() {
			continue
		}
		ref := r.modelRef()
		if ref.Provider == model.Provider && ref.BillingVariant == model.BillingVariant && (ref.ID == model.ID || isSnapshotOf(ref.ID, model.ID)) {
			return true
		}
	}
	return false
}

// Unnamed returns the preferences whose resolved model the card does not name,
// which are the ones billed at another model's price.
func Unnamed(cov []Coverage) []Coverage {
	var out []Coverage
	for _, c := range cov {
		if c.Err == "" && !c.Named {
			out = append(out, c)
		}
	}
	return out
}
