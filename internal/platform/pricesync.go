// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package platform

import (
	"context"
	"log/slog"
	"time"

	"github.com/latere-ai/replichai/internal/credit"
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
