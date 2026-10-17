// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"latere.ai/x/pkg/pricelist"

	"github.com/latere-ai/replichai/internal/managed"
)

// FetchPriceList reads a model list in OpenRouter's format and returns the
// rates of every Anthropic model it prices, so the card follows the vendor's
// prices without an admin typing them. OpenRouter publishes Anthropic's list
// prices for each model, including both cache-write lifetimes, and Anthropic
// itself publishes no machine-readable price list.
//
// A model is returned under the id Anthropic's API takes: OpenRouter's
// "anthropic/claude-opus-5.5" is "claude-opus-5-5", and a dated snapshot such
// as "claude-opus-5-5-20260921" is matched to it by Card.PricedRef. A variant
// such as ":batch" is a billing mode, not a model, and is skipped, as is a
// model missing any of the five prices, since a price the list does not state
// would be charged as zero.
//
// A model priced in tiers by prompt length is returned at its base tier. A
// session's usage is a sum over many requests, so the tier each request fell
// in is not known when it is priced; the Latere platform bills the same models
// at the base tier.
//
// A list that prices no Anthropic model is an error, so an outage of the
// list is never read as a list with no prices in it.
func FetchPriceList(ctx context.Context, client *http.Client, url string) ([]Rates, error) {
	models, err := pricelist.Fetch(ctx, client, url)
	if err != nil {
		return nil, fmt.Errorf("read the price list: %w", err)
	}
	var out []Rates
	for _, m := range models {
		vendor, model, ok := strings.Cut(strings.ToLower(strings.TrimSpace(m.ID)), "/")
		if !ok || vendor != "anthropic" || model == "" || strings.Contains(model, ":") {
			continue
		}
		var prices [5]int64
		priced := true
		for i, p := range []pricelist.Price{m.Prices.Prompt, m.Prices.Completion, m.Prices.InputCacheRead, m.Prices.InputCacheWrite, m.Prices.InputCacheWrite1h} {
			if prices[i], ok = p.PerMillion(); !ok {
				priced = false
				break
			}
		}
		if !priced {
			continue
		}
		id := strings.ReplaceAll(model, ".", "-")
		out = append(out, Rates{
			Model: id, Ref: managed.ModelRef{Provider: "anthropic", ID: id},
			Input: prices[0], Output: prices[1], CacheRead: prices[2], CacheWrite5m: prices[3], CacheWrite1h: prices[4],
		})
	}
	if len(out) == 0 {
		return nil, errors.New("the price list prices no Anthropic model")
	}
	return out, nil
}

// WithListed returns the card with every listed model at its listed prices,
// and reports whether that changed the card. A row for a listed model takes
// the listed token prices and keeps its own meters; a listed model the card
// lacks is added after the card's rows, in id order; a row for a model the
// list does not price is kept as the admin wrote it.
func (c Card) WithListed(listed []Rates) (Card, bool) {
	byRef := make(map[managed.ModelRef]Rates, len(listed))
	for _, l := range listed {
		byRef[l.ModelRef()] = l
	}
	out := Card{Models: make([]Rates, 0, len(c.Models)+len(listed))}
	changed := false
	for _, r := range c.Models {
		ref := r.ModelRef()
		if l, ok := byRef[ref]; ok {
			delete(byRef, ref)
			next := r
			next.Model, next.Ref = ref.ID, ref
			next.Input, next.Output, next.CacheRead = l.Input, l.Output, l.CacheRead
			next.CacheWrite5m, next.CacheWrite1h, next.CacheWrite = l.CacheWrite5m, l.CacheWrite1h, 0
			next.Free = false
			if !sameTokenPrices(r, next) {
				changed = true
			}
			r = next
		}
		out.Models = append(out.Models, r)
	}
	added := make([]Rates, 0, len(byRef))
	for _, l := range byRef {
		added = append(added, l)
	}
	slices.SortFunc(added, func(a, b Rates) int { return cmp.Compare(a.Model, b.Model) })
	if len(added) > 0 {
		changed = true
	}
	out.Models = append(out.Models, added...)
	return out, changed
}

// sameTokenPrices reports whether two rows price tokens alike and carry the
// same model ref, so a sync that finds nothing new writes nothing.
func sameTokenPrices(a, b Rates) bool {
	return a.Ref == b.Ref && a.Model == b.Model && a.Free == b.Free &&
		a.Input == b.Input && a.Output == b.Output && a.CacheRead == b.CacheRead &&
		a.CacheWrite5m == b.CacheWrite5m && a.CacheWrite1h == b.CacheWrite1h && a.CacheWrite == b.CacheWrite
}
