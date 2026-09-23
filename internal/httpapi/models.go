// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/credit"
)

// modelsTTL is how long a priced tier list is reused. Covers resolves each tier
// against the backend's catalog, one round trip per tier, and a page that
// offers the choice would otherwise pay three round trips on every load.
//
// Five minutes follows from how the two inputs change. A vendor's catalog
// changes on release days, and an admin's price edit is rare and not something
// anyone waits at a screen for. Serving either a few minutes stale costs
// nothing, because neither is what a session is billed from; that is the card
// snapshotted onto the job at enqueue (011).
const modelsTTL = 5 * time.Minute

// tierPrice is one tier as the model picker needs it: its name, the model it
// resolves to today, and what that costs. Rates are micro-USD per million
// tokens, the unit the card is kept in, and the client formats them.
//
// Named repeats Coverage.Named: false means the card does not price this tier's
// model by name and Card.For fell back to the dearest rates it holds. The
// client shows the number either way, because it is what a run would be
// charged, but an operator reading their own console should be able to tell a
// listed price from a fallback.
type tierPrice struct {
	Tier     string `json:"tier"`
	Model    string `json:"model,omitempty"`
	Provider string `json:"provider,omitempty"`
	Input    int64  `json:"input,omitempty"`
	Output   int64  `json:"output,omitempty"`
	Named    bool   `json:"named"`
	Err      string `json:"error,omitempty"`
}

// modelCache memoises the priced tier list. It is one value rather than a map,
// because the answer does not vary by caller: the card is the platform's and
// the catalog is the backend's.
type modelCache struct {
	mu     sync.Mutex
	at     time.Time
	tiers  []tierPrice
	loaded bool
}

// listModels reports what each tier would run on and what it would cost, so the
// control that offers the choice can price it.
//
// The prices come from the server and not from the client, for the same reason
// settlement prices from a snapshot: the card is the admin's policy, and a
// second copy of it in TypeScript would be a second answer to what a run costs.
// A deployment with no card or no backend still lists the tiers. The choice can
// be offered without a price, and refusing to answer would remove the control
// rather than only the number.
func (rt *Router) listModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tiers": rt.tiers(r.Context()),
		// drafting reports whether this deployment can draft a description
		// (039). The app asks so it can decide whether to offer the control,
		// rather than offering one that answers 503. It is reported here with
		// the other model policy because it is model policy: which model does
		// what, and at what price.
		"drafting": rt.canDraft(r.Context()),
	})
}

// canDraft reports whether a draft would work: a model is configured, and the
// rate card prices it. Both are required, because a configured model that
// cannot be priced would give a control that always refuses.
func (rt *Router) canDraft(ctx context.Context) bool {
	if rt.drafter == nil {
		return false
	}
	_, err := rt.draftRates(ctx)
	return err == nil
}

// tiers returns the priced tier list, recomputing it at most once per modelsTTL.
//
// Only a fully priced answer is kept. A transient policy store failure or a
// backend timeout would otherwise cache blank prices for every caller for five
// minutes, turning one slow read into a five-minute outage of the price. An
// unpriced answer is served and not cached, so the next caller tries again.
func (rt *Router) tiers(ctx context.Context) []tierPrice {
	rt.models.mu.Lock()
	defer rt.models.mu.Unlock()
	if rt.models.loaded && time.Since(rt.models.at) < modelsTTL {
		return rt.models.tiers
	}
	tiers := rt.priceTiers(ctx)
	if priced(tiers) {
		rt.models.tiers, rt.models.at, rt.models.loaded = tiers, time.Now(), true
	}
	return tiers
}

// priced reports whether every tier came back with rates and no error, which
// is the only answer worth caching.
func priced(tiers []tierPrice) bool {
	for _, p := range tiers {
		if p.Err != "" || p.Model == "" {
			return false
		}
	}
	return len(tiers) > 0
}

// priceTiers resolves and prices every tier the engine offers. It is separate
// from the cache so the pricing can be read without the locking.
func (rt *Router) priceTiers(ctx context.Context) []tierPrice {
	// Only the names are returned when this deployment cannot price them, as
	// in an offline run with no backend or one with no policy store. The tiers
	// are still valid choices.
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
	// The deadline matches the server's boot-time rates report, for the same
	// reason: a backend that does not answer must not hold a page open.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cov := set.Rates.Covers(ctx, rt.modelResolver)
	out := make([]tierPrice, 0, len(cov))
	for _, c := range cov {
		out = append(out, tierPrice{
			Tier:     c.Preference.String(),
			Model:    c.Model,
			Provider: c.ModelRef.Provider,
			Input:    c.Rates.Input,
			Output:   c.Rates.Output,
			Named:    c.Named,
			Err:      c.Err,
		})
	}
	return out
}
