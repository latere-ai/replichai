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

// failingSyncer refuses every list, as a store whose write fails does.
type failingSyncer struct{ err error }

func (f failingSyncer) SyncRates(context.Context, []credit.Rates) (bool, error) { return false, f.err }

// TestPriceListRecordsWhatTheStoreAccepted checks the record the console
// reads: nothing before the first application, the time and the models once
// the store accepts a list, even a list that changes nothing, and nothing new
// when the store refuses one.
func TestPriceListRecordsWhatTheStoreAccepted(t *testing.T) {
	now := time.Date(2026, 10, 16, 14, 2, 0, 0, time.FixedZone("CEST", 2*60*60))
	list := NewPriceList("https://reader:secret@openrouter.ai/api/v1/models?key=k#top", func() time.Time { return now })
	if v := list.View(); !v.AppliedAt.IsZero() || len(v.Models) != 0 || list.Listed() != nil {
		t.Fatalf("before any application the record is %+v, want none", v)
	}
	if v := list.View(); v.URL != "https://openrouter.ai/api/v1/models" {
		t.Errorf("the record shows the address %q, want it without credentials, query or fragment", v.URL)
	}

	s := NewMemStore(nil)
	fetch := func(context.Context) ([]credit.Rates, error) { return listedOpus(), nil }
	if _, err := ApplyPriceList(ctx, list.Recording(s), fetch); err != nil {
		t.Fatalf("ApplyPriceList: %v", err)
	}
	v := list.View()
	want := managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5-5"}
	if !v.AppliedAt.Equal(now) || v.AppliedAt.Location() != time.UTC || len(v.Models) != 1 || v.Models[0] != want {
		t.Fatalf("after an application the record is %+v, want %v in UTC and %v", v, now, want)
	}

	// A second application changes nothing on the card and still moves the
	// time: the card matched the list then.
	now = now.Add(time.Hour)
	if changed, err := ApplyPriceList(ctx, list.Recording(s), fetch); err != nil || changed {
		t.Fatalf("a second application = (%v, %v), want no change", changed, err)
	}
	if v := list.View(); !v.AppliedAt.Equal(now) {
		t.Errorf("an application that changed nothing left the time at %v, want %v", v.AppliedAt, now)
	}

	// A store that refuses the list leaves the record as it was.
	applied := now
	now = now.Add(time.Hour)
	refused := errors.New("write refused")
	if _, err := ApplyPriceList(ctx, list.Recording(failingSyncer{refused}), fetch); !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the store's", err)
	}
	if v := list.View(); !v.AppliedAt.Equal(applied) {
		t.Errorf("a refused list moved the time to %v, want %v", v.AppliedAt, applied)
	}

	// What Listed returns is a copy: a caller that changes it changes no record.
	got := list.Listed()
	got[0].Input = 1
	if list.Listed()[0].Input != listedOpus()[0].Input {
		t.Error("changing what Listed returned changed the record")
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
