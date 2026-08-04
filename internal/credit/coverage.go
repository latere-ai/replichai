package credit

import "context"

// ModelResolver turns a model *preference* into the concrete model a backend
// will actually run. Satisfied by provider.Provider, declared here as one
// method so pricing does not depend on the whole provider surface.
type ModelResolver interface {
	ResolveModel(ctx context.Context, pref string) (string, error)
}

// Preferences are the model tiers the engine asks for. Pricing checks against
// these rather than against a vendor's catalogue: what a session costs depends
// on which id a *preference* resolves to, not on what the catalogue contains.
var Preferences = []string{"best", "balanced", "fast"}

// Coverage is one preference checked against the card: what it resolves to, how
// that would be billed, and whether the card names it.
//
// Named is the finding. When it is false the session is still priced — Card.For
// charges the dearest rates it holds, so a vendor's new model id is never free
// compute — but at another model's price. That is a thing to know before the
// first run, not an error: refusing to start because a card is a release behind
// would turn a billing imprecision into an outage.
type Coverage struct {
	Preference string `json:"preference"`
	Model      string `json:"model,omitempty"`
	Named      bool   `json:"named"`
	Rates      Rates  `json:"rates,omitzero"`
	Err        string `json:"error,omitempty"`
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
		model, err := r.ResolveModel(ctx, pref)
		if err != nil {
			cov.Err = err.Error()
			out = append(out, cov)
			continue
		}
		cov.Model = model
		rates, err := c.For(model)
		if err != nil {
			cov.Err = err.Error()
			out = append(out, cov)
			continue
		}
		cov.Rates = rates
		cov.Named = c.names(model)
		out = append(out, cov)
	}
	return out
}

// names reports whether the card can price this model on its own terms — an
// entry for it, or an entry for the model it is a dated snapshot of — as
// opposed to Card.For having fallen back to the dearest rates.
//
// It cannot compare names to find out. The fallback synthesises an entry
// carrying the *requested* name, so a fallback and a real match are identical
// by name, and a check comparing them would report every model as priced.
func (c Card) names(model string) bool {
	for _, r := range c.Models {
		if r.Zero() {
			continue
		}
		if r.Model == model || isSnapshotOf(r.Model, model) {
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
