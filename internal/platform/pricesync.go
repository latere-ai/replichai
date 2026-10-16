// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package platform

import (
	"context"
	"log/slog"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/managed"
)

// RateSyncer is a store whose card a price list can update.
type RateSyncer interface {
	SyncRates(ctx context.Context, listed []credit.Rates) (bool, error)
}

// SyncPrices keeps the card at the listed prices, so no admin types a vendor's
// price list: it applies the list every interval until ctx ends. The caller
// applies it once at boot with ApplyPriceList. A failed read or write is
// logged and the card keeps the prices it has, so an outage of the list never
// leaves a model unpriced.
func SyncPrices(ctx context.Context, store RateSyncer, fetch func(context.Context) ([]credit.Rates, error), every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if _, err := ApplyPriceList(ctx, store, fetch); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "prices: the card keeps its prices", "error", err)
		}
	}
}

// ApplyPriceList reads the list and applies it to the card, reporting whether
// the card changed. The list is read before the store opens its transaction,
// so the transaction never waits on the network.
func ApplyPriceList(ctx context.Context, store RateSyncer, fetch func(context.Context) ([]credit.Rates, error)) (bool, error) {
	listed, err := fetch(ctx)
	if err != nil {
		return false, err
	}
	changed, err := store.SyncRates(ctx, listed)
	if err != nil {
		return false, err
	}
	if changed {
		slog.InfoContext(ctx, "prices: the card follows the price list", "models", len(listed))
	}
	return changed, nil
}

// PriceList is what this server last applied from the price list: where it
// reads the list, when it last applied it, and the rates the list gave then.
// The admin console reads it to show which rows of the card follow the list,
// and an admin's save keeps those rows at the list's prices, since the next
// sync would put them back anyway.
//
// It is held in memory, not stored. Each replica reads the list on its own
// schedule, so the time is this replica's, and a server that has not read the
// list since it started has no time and no rates.
type PriceList struct {
	url   string
	clock func() time.Time

	mu     sync.Mutex
	at     time.Time
	listed []credit.Rates
}

// NewPriceList records the applications of the list read from url.
func NewPriceList(url string, clock func() time.Time) *PriceList {
	return &PriceList{url: url, clock: clock}
}

// Recording returns store with every list it accepts recorded on p. A list
// that left the card as it was is recorded too: the card matched the list at
// that moment, which is what the time says.
func (p *PriceList) Recording(store RateSyncer) RateSyncer {
	return recordingSyncer{store: store, list: p}
}

type recordingSyncer struct {
	store RateSyncer
	list  *PriceList
}

// SyncRates applies the list through the wrapped store and, once the store
// has accepted it, records it and the time.
func (r recordingSyncer) SyncRates(ctx context.Context, listed []credit.Rates) (bool, error) {
	changed, err := r.store.SyncRates(ctx, listed)
	if err != nil {
		return false, err
	}
	r.list.mu.Lock()
	defer r.list.mu.Unlock()
	r.list.at, r.list.listed = r.list.clock().UTC(), slices.Clone(listed)
	return changed, nil
}

// Listed returns the rates of the list as last applied, or nil before the
// first application.
func (p *PriceList) Listed() []credit.Rates {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.listed)
}

// PriceListView is the list as the admin console reads it: where it is read
// from, when this server last applied it, and the models it priced then. A
// row of the card for one of those models follows the list.
type PriceListView struct {
	URL       string             `json:"url"`
	AppliedAt time.Time          `json:"applied_at,omitzero"`
	Models    []managed.ModelRef `json:"models,omitempty"`
}

// View returns the list as the console reads it. The address loses any user
// information and query, where a deployment's own list might carry a key.
func (p *PriceList) View() PriceListView {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := PriceListView{URL: shownURL(p.url), AppliedAt: p.at}
	for _, r := range p.listed {
		v.Models = append(v.Models, r.ModelRef())
	}
	return v
}

// shownURL is u without user information, query or fragment, or "" when u is
// not an address at all.
func shownURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	parsed.User, parsed.RawQuery, parsed.ForceQuery, parsed.Fragment, parsed.RawFragment = nil, "", false, "", ""
	return parsed.String()
}
