package credit

import (
	"context"
	"slices"

	"github.com/latere-ai/replichai/internal/managed"
)

// ModelResolver turns a model *preference* into the concrete model a backend
// will actually run. Satisfied by provider.Provider, declared here as one
// method so pricing does not depend on the whole provider surface.
type ModelResolver interface {
	ResolveModel(ctx context.Context, pref string) (string, error)
}

// QualifiedModelResolver is ModelResolver's provider-qualified form: it answers
// with a ModelRef, so a card can tell two providers' identically-named models
// apart instead of pricing whichever it happens to name first.
//
// It is optional. A resolver that does not implement it is read through
// ModelResolver and its answer attributed to Anthropic, which is what a bare
// model id meant before the managed contract existed.
type QualifiedModelResolver interface {
	ResolveModelRef(ctx context.Context, pref string) (managed.ModelRef, error)
}

// Preference is a model tier the engine asks for — the unit a session's cost is
// chosen in. It is a named type over a closed set rather than a bare string
// because it crosses a trust boundary: a person picks one before a run (031),
// and the adapter passes anything it does not recognise straight through as a
// literal model id, which a card cannot price at its real rate.
//
// It is not called Tier: access.Tier already means what a principal may do, and
// this package reads that type. Preference is also the word the engine has used
// since 004 — modelPref, WithModelPreference, ResolveModel(ctx, pref).
type Preference string

// The preferences the engine asks for. A backend maps each to one of its own
// model families; what that costs is the card's business, not this list's.
const (
	PrefBest     Preference = "best"     // the most capable tier
	PrefBalanced Preference = "balanced" // capability against cost
	PrefFast     Preference = "fast"     // the cheapest and quickest
)

// DefaultPreference is what a session runs on when nobody chose. It is best
// because that is what every session ran on before the choice existed, and a
// default that silently moved existing work onto a cheaper model would be this
// spec changing runs it was only meant to give a say over.
const DefaultPreference = PrefBest

// Preferences are the model tiers the engine asks for. Pricing checks against
// these rather than against a vendor's catalogue: what a session costs depends
// on which id a *preference* resolves to, not on what the catalogue contains.
//
// It is also the closed set the HTTP boundary validates a submitted choice
// against, so the two cannot disagree about which tiers exist.
var Preferences = []Preference{PrefBest, PrefBalanced, PrefFast}

// Valid reports whether p is a preference the engine offers. The zero value is
// not valid: "unset" is a distinct state from any tier, and only the caller
// holding the field knows whether unset means take the default (a job that
// predates the choice) or means the caller sent nothing at all.
func (p Preference) Valid() bool {
	return slices.Contains(Preferences, p)
}

// OrDefault resolves an unset preference to the default, so every reader of a
// stored choice answers "which model" the same way and a row written before the
// choice existed needs no backfill to be readable.
func (p Preference) OrDefault() Preference {
	if p == "" {
		return DefaultPreference
	}
	return p
}

// String makes a Preference printable and, at the seams that still speak in
// bare strings (the auditor's option, the adapter's resolver), explicit about
// where the domain type is being projected out.
func (p Preference) String() string { return string(p) }

// Coverage is one preference checked against the card: what it resolves to, how
// that would be billed, and whether the card names it.
//
// Named is the finding. When it is false the session is still priced — Card.For
// charges the dearest rates it holds, so a vendor's new model id is never free
// compute — but at another model's price. That is a thing to know before the
// first run, not an error: refusing to start because a card is a release behind
// would turn a billing imprecision into an outage.
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
// It drives the caller's own resolution rather than reading a catalogue,
// because that resolution is what decides the price: the adapter matches a
// preference against whatever the backend lists today, and a dated or suffixed
// id misses an exact match on a card that names the bare alias.
func (c Card) Covers(ctx context.Context, r ModelResolver) []Coverage {
	out := make([]Coverage, 0, len(Preferences))
	for _, pref := range Preferences {
		cov := Coverage{Preference: pref}
		// Projected to a string at the seam: the resolver is the provider, and
		// the provider does not import pricing.
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

// names reports whether the card can price this model on its own terms — an
// entry for it, or an entry for the model it is a dated snapshot of — as
// opposed to Card.For having fallen back to the dearest rates.
//
// It cannot compare names to find out. The fallback synthesises an entry
// carrying the *requested* name, so a fallback and a real match are identical
// by name, and a check comparing them would report every model as priced.
func (c Card) names(model string) bool {
	return c.namesRef(managed.ModelRef{Provider: "anthropic", ID: model})
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

// Unnamed returns the preferences whose resolved model the card does not carry
// — the ones being billed at another model's price.
func Unnamed(cov []Coverage) []Coverage {
	var out []Coverage
	for _, c := range cov {
		if c.Err == "" && !c.Named {
			out = append(out, c)
		}
	}
	return out
}
