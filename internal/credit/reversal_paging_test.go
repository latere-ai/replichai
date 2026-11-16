// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"latere.ai/x/pay/ledger"
	"latere.ai/x/pay/money"
)

// fixedStatement is a ledger whose statement is a fixed list, paged the way the
// Postgres ledger pages it: newest first by time and then id, strictly before
// Page.Before.
type fixedStatement struct {
	ledger.Store
	rows []ledger.Entry
}

func (f fixedStatement) Entries(_ context.Context, _ ledger.Holder, p ledger.Page) ([]ledger.Entry, error) {
	var out []ledger.Entry
	for _, e := range f.rows {
		if p.Before.IsZero() || e.CreatedAt.Before(p.Before) {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b ledger.Entry) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(b.ID, a.ID)
	})
	return out[:min(len(out), p.Limit)], nil
}

// TestReversedReadsEveryEntryOfTheTimeAPageEndsOn checks that the sum of a
// purchase's reversals counts a reversal that shares its time with the last
// row of a full page and so falls just past it.
func TestReversedReadsEveryEntryOfTheTimeAPageEndsOn(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	var rows []ledger.Entry
	// reversalPage-1 older entries, each at its own time, newest last.
	for i := range reversalPage - 1 {
		rows = append(rows, ledger.Entry{ID: fmt.Sprintf("old-%04d", i), Amount: money.Micro(1), CreatedAt: t0.Add(time.Duration(i) * time.Second)})
	}
	// Two entries written together, older than all of the above: the unrelated
	// one sorts first and ends the first page, the reversal falls past it.
	tie := t0.Add(-time.Hour)
	rows = append(rows,
		ledger.Entry{ID: "b-unrelated", Amount: money.Micro(1), CreatedAt: tie},
		ledger.Entry{ID: "a-reversal", Amount: money.Micro(-7), CreatedAt: tie, Labels: map[string]string{labelReverses: "pi_t"}},
	)
	s := &store{led: fixedStatement{rows: rows}}
	got, err := s.reversed(context.Background(), Wallet(lead.Email), "pi_t")
	if err != nil || got != 7 {
		t.Fatalf("reversed = %d, %v; want 7", got, err)
	}
}

// TestAnAllocationWaitsForAReversalOfItsWallet checks that moving credit out of
// a wallet waits while a reversal of that wallet is in flight, so the reversal
// reads a balance no allocation changes under it.
func TestAnAllocationWaitsForAReversalOfItsWallet(t *testing.T) {
	s := NewMemStore(nil, nil)
	ctx := context.Background()
	if err := s.TopupPaid(ctx, lead.Email, 20*Dollar, "pi_a", "a"); err != nil {
		t.Fatal(err)
	}
	unlock, err := s.lockReversals(ctx, Wallet(lead.Email))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Allocate(ctx, lead, "p1", "P", 5*Dollar) }()
	select {
	case err := <-done:
		unlock()
		t.Fatalf("allocation finished during a reversal (%v), want it to wait", err)
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("allocation after the reversal: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("allocation did not finish after the reversal")
	}
}
