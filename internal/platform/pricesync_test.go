// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package platform

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/managed"
)

func listedOpus() []credit.Rates {
	return []credit.Rates{{
		Model: "claude-opus-5-5", Ref: managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5-5"},
		Input: 4_000_000, Output: 20_000_000, CacheRead: 200_000, CacheWrite5m: 5_000_000, CacheWrite1h: 8_000_000,
	}}
}

// TestApplyPriceListPricesAModelTheCardLacked checks the in-memory store: a
// listed model the card did not name is priced at the listed rates, and a
// second application changes nothing.
func TestApplyPriceListPricesAModelTheCardLacked(t *testing.T) {
	s := NewMemStore(nil)
	fetch := func(context.Context) ([]credit.Rates, error) { return listedOpus(), nil }
	changed, err := ApplyPriceList(ctx, s, fetch)
	if err != nil || !changed {
		t.Fatalf("ApplyPriceList = (%v, %v), want a change", changed, err)
	}
	set, _ := s.Settings(ctx)
	r, ok := set.Rates.PricedRef(managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5-5-20260921"})
	if !ok || r.Input != 4_000_000 || r.Output != 20_000_000 {
		t.Fatalf("a dated opus-5-5 prices as (%+v, %v), want the listed rates", r, ok)
	}
	if changed, err := ApplyPriceList(ctx, s, fetch); err != nil || changed {
		t.Errorf("a second application = (%v, %v), want no change", changed, err)
	}
}

// TestApplyPriceListKeepsTheCardOnAFailedRead checks that a list that cannot
// be read leaves the card as it was.
func TestApplyPriceListKeepsTheCardOnAFailedRead(t *testing.T) {
	s := NewMemStore(nil)
	before, _ := s.Settings(ctx)
	readErr := errors.New("list unavailable")
	if _, err := ApplyPriceList(ctx, s, func(context.Context) ([]credit.Rates, error) { return nil, readErr }); !errors.Is(err, readErr) {
		t.Fatalf("err = %v, want the read error", err)
	}
	after, _ := s.Settings(ctx)
	if len(after.Rates.Models) != len(before.Rates.Models) {
		t.Errorf("a failed read changed the card: %d rows, was %d", len(after.Rates.Models), len(before.Rates.Models))
	}
}

// TestSyncPricesStopsWithItsContext checks that the background loop applies
// the list on its interval and returns when its context ends.
func TestSyncPricesStopsWithItsContext(t *testing.T) {
	s := NewMemStore(nil)
	loop, cancel := context.WithCancel(ctx)
	applied := make(chan struct{}, 1)
	fetch := func(context.Context) ([]credit.Rates, error) {
		select {
		case applied <- struct{}{}:
		default:
		}
		return listedOpus(), nil
	}
	done := make(chan struct{})
	go func() { SyncPrices(loop, s, fetch, time.Millisecond); close(done) }()
	select {
	case <-applied:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop never applied the list")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not stop with its context")
	}
}

// TestPGSyncRatesOnOneConnection checks the Postgres store against the
// production pooler, which holds one server connection: the sync's
// transaction must not need a second one. It also checks that the sync writes
// only the card and leaves the admin's last save on record.
func TestPGSyncRatesOnOneConnection(t *testing.T) {
	setup := testPool(t)
	defer setup.Close()
	cfg, err := pgxpool.ParseConfig(setup.Config().ConnString())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	s := NewPGStore(pool, nil)
	before, err := s.Settings(ctx)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}

	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	changed, err := s.SyncRates(bounded, listedOpus())
	if err != nil || !changed {
		t.Fatalf("SyncRates = (%v, %v), want a change on one connection", changed, err)
	}
	after, err := s.Settings(ctx)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if r, ok := after.Rates.PricedRef(managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5-5"}); !ok || r.Output != 20_000_000 {
		t.Errorf("opus-5-5 = (%+v, %v), want the listed rates", r, ok)
	}
	if after.UpdatedBy != before.UpdatedBy || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("the sync rewrote the admin's record: %q %v, was %q %v", after.UpdatedBy, after.UpdatedAt, before.UpdatedBy, before.UpdatedAt)
	}
	if changed, err := s.SyncRates(bounded, listedOpus()); err != nil || changed {
		t.Errorf("a second sync = (%v, %v), want no change", changed, err)
	}
}
