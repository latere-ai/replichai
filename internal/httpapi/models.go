package httpapi

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/credit"
)

// modelsTTL is how long a priced tier list is reused. Covers resolves each tier
// against the backend's catalogue — one round trip per tier — and a page that
// offers the choice would otherwise pay three of them on every load.
//
// Five minutes is chosen from what the two inputs actually do. A vendor's
// catalogue changes on release days; an admin's price edit is rare and is not
// something anyone is watching a screen for. Serving either a few minutes stale
// costs nothing, because neither is what a session is *billed* from — that is
// the card snapshotted onto the job at enqueue (011).
const modelsTTL = 5 * time.Minute

// tierPrice is one tier as the picker needs it: what it is, what it resolves to
// today, and what that costs. Rates are micro-USD per million tokens, the unit
// the card is kept in, formatted by the client.
//
// Named repeats Coverage.Named: false means the card does not price this tier's
// model on its own terms and Card.For fell back to the dearest rates it holds.
// The client shows the number either way — it is what a run would be charged —
// but an operator reading their own console should be able to tell a real price
// from a fallback.
type tierPrice struct {
	Tier   string `json:"tier"`
	Model  string `json:"model,omitempty"`
	Input  int64  `json:"input,omitempty"`
	Output int64  `json:"output,omitempty"`
	Named  bool   `json:"named"`
	Err    string `json:"error,omitempty"`
}

// modelCache memoises the priced tier list. It is one value, not a map: the
// answer does not vary by caller — the card is the platform's and the catalogue
// is the backend's.
type modelCache struct {
	mu     sync.Mutex
	at     time.Time
	tiers  []tierPrice
	loaded bool
}

// listModels answers what each tier would run on and what it would cost, so the
// control that offers the choice can price it.
//
// The prices come from here and not from the client, for the same reason
// settlement prices from a snapshot: the card is the admin's policy, and a
// second copy of it in TypeScript would be a second answer to what a run costs.
// A deployment with no card or no backend still lists the tiers — the choice is
// offerable without a price, and refusing to answer would take the control away
// rather than the number.
func (rt *Router) listModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tiers": rt.tiers(r.Context())})
}

// tiers returns the priced tier list, recomputing it at most once per modelsTTL.
func (rt *Router) tiers(ctx context.Context) []tierPrice {
	rt.models.mu.Lock()
	defer rt.models.mu.Unlock()
	if rt.models.loaded && time.Since(rt.models.at) < modelsTTL {
		return rt.models.tiers
	}
	tiers := rt.priceTiers(ctx)
	rt.models.tiers, rt.models.at, rt.models.loaded = tiers, time.Now(), true
	return tiers
}

// priceTiers resolves and prices every tier the engine offers. Held apart from
// the cache so the pricing is readable without the locking.
func (rt *Router) priceTiers(ctx context.Context) []tierPrice {
	// Bare names when this deployment cannot price them: an offline run with no
	// backend, or one with no policy store. The tiers are still real.
	if rt.policy == nil || rt.modelResolver == nil {
		out := make([]tierPrice, 0, len(credit.Preferences))
		for _, p := range credit.Preferences {
			out = append(out, tierPrice{Tier: p.String()})
		}
		return out
	}
	set, err := rt.policy.Settings(ctx)
	if err != nil {
		out := make([]tierPrice, 0, len(credit.Preferences))
		for _, p := range credit.Preferences {
			out = append(out, tierPrice{Tier: p.String(), Err: "rates_unavailable"})
		}
		return out
	}
	// A boot-time courtesy's deadline, for the same reason: a backend that will
	// not answer must not hold a page open.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cov := set.Rates.Covers(ctx, rt.modelResolver)
	out := make([]tierPrice, 0, len(cov))
	for _, c := range cov {
		out = append(out, tierPrice{
			Tier:   c.Preference.String(),
			Model:  c.Model,
			Input:  c.Rates.Input,
			Output: c.Rates.Output,
			Named:  c.Named,
			Err:    c.Err,
		})
	}
	return out
}
