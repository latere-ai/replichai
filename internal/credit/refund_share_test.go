// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"context"
	"errors"
	"testing"
)

// storeMakers builds the same ledger on both stores, so a reversal rule is
// asserted once for memory and for Postgres.
func storeMakers(opts ...Option) []struct {
	name string
	make func(t *testing.T) (Store, func())
} {
	return []struct {
		name string
		make func(t *testing.T) (Store, func())
	}{
		{"mem", func(t *testing.T) (Store, func()) {
			auth, members := testAuthority()
			return NewMemStore(auth, members, opts...), func() {}
		}},
		{"postgres", func(t *testing.T) (Store, func()) {
			pool := testPool(t)
			auth, members := testAuthority()
			return NewPGStore(pool, auth, members, opts...), pool.Close
		}},
	}
}

// TestRefundsTakeBackOnlyTheirShareOfTheCredit checks that each refund takes
// back the part of the credit it covers. Two partial refunds, of a quarter and
// then half of the charge, take back three quarters of what the purchase
// credited, where each used to take back all of it.
func TestRefundsTakeBackOnlyTheirShareOfTheCredit(t *testing.T) {
	for _, tc := range storeMakers() {
		t.Run(tc.name, func(t *testing.T) {
			s, done := tc.make(t)
			defer done()
			ctx := context.Background()
			w := Wallet(lead.Email)
			if err := s.TopupPaid(ctx, lead.Email, 18_700_000, "pi_1", "stripe:pi_1"); err != nil {
				t.Fatalf("TopupPaid: %v", err)
			}

			// The charge was $20.00, 2000 cents. A refund of $5.00 is a quarter.
			took, err := tookOf(s.RefundShare(ctx, "pi_1", "re_1", 500, 2000))
			if err != nil || took != 4_675_000 {
				t.Fatalf("first refund took %d, %v; want 4675000", took, err)
			}
			// The second refund of $10.00 arrives with $15.00 refunded in all.
			took, err = tookOf(s.RefundShare(ctx, "pi_1", "re_2", 1500, 2000))
			if err != nil || took != 9_350_000 {
				t.Fatalf("second refund took %d, %v; want 9350000", took, err)
			}
			if n, _ := s.Balance(ctx, w); n != 18_700_000-14_025_000 {
				t.Fatalf("balance = %d, want a quarter of the credit left (%d)", n, 18_700_000-14_025_000)
			}

			// A retried delivery of either refund takes back nothing more, and
			// neither does an older one arriving late.
			for _, ref := range []struct {
				id       string
				refunded int64
			}{{"re_2", 1500}, {"re_1", 500}} {
				if took, err := tookOf(s.RefundShare(ctx, "pi_1", ref.id, ref.refunded, 2000)); err != nil || took != 0 {
					t.Fatalf("replay of %s took %d, %v; want nothing", ref.id, took, err)
				}
			}
			if n, _ := s.Balance(ctx, w); n != 18_700_000-14_025_000 {
				t.Fatalf("balance after replays = %d, want %d", n, 18_700_000-14_025_000)
			}
		})
	}
}

// TestARefundNeverTakesBackMoreThanItsShareRoundedDown checks the rounding rule
// on a credit no round fraction divides: the share rounds down, so the person
// keeps the odd micro-dollar and the total never passes the credit.
func TestARefundNeverTakesBackMoreThanItsShareRoundedDown(t *testing.T) {
	for _, tc := range storeMakers() {
		t.Run(tc.name, func(t *testing.T) {
			s, done := tc.make(t)
			defer done()
			ctx := context.Background()
			if err := s.TopupPaid(ctx, lead.Email, 1_000_001, "pi_odd", "stripe:pi_odd"); err != nil {
				t.Fatalf("TopupPaid: %v", err)
			}
			var total int64
			// Thirds of a charge: 1/3 of 1000001 rounds down to 333333.
			for i, refunded := range []int64{1, 2, 3} {
				took, err := tookOf(s.RefundShare(ctx, "pi_odd", "re_"+string(rune('a'+i)), refunded, 3))
				if err != nil {
					t.Fatalf("refund %d: %v", i, err)
				}
				total += took
				if total > 1_000_001 {
					t.Fatalf("after refund %d the total taken back is %d, more than the credit", i, total)
				}
			}
			if total != 1_000_001 {
				t.Fatalf("a full refund in thirds took back %d, want the whole credit 1000001", total)
			}
			// More than the whole charge refunded still stops at the credit.
			if took, err := tookOf(s.RefundShare(ctx, "pi_odd", "re_over", 9, 3)); err != nil || took != 0 {
				t.Fatalf("an over-refund took %d, %v; want nothing past the credit", took, err)
			}
		})
	}
}

// TestARefundThenADisputeNeverPassesTheCredit checks that a dispute takes back
// what is left and no more, so a refund followed by a dispute on the same
// purchase reverses the credit once, and a dispute first leaves nothing for a
// refund to take.
func TestARefundThenADisputeNeverPassesTheCredit(t *testing.T) {
	for _, tc := range storeMakers() {
		t.Run(tc.name, func(t *testing.T) {
			s, done := tc.make(t)
			defer done()
			ctx := context.Background()
			w := Wallet(lead.Email)
			if err := s.TopupPaid(ctx, lead.Email, 10*Dollar, "pi_a", "stripe:pi_a"); err != nil {
				t.Fatalf("TopupPaid: %v", err)
			}
			if _, err := tookOf(s.RefundShare(ctx, "pi_a", "re_a", 1, 2)); err != nil { // half
				t.Fatalf("refund: %v", err)
			}
			took, err := tookOf(s.RefundRemainder(ctx, "pi_a", "dp_a"))
			if err != nil || took != 5*Dollar {
				t.Fatalf("dispute took %d, %v; want the other half %d", took, err, 5*Dollar)
			}
			if n, _ := s.Balance(ctx, w); n != 0 {
				t.Fatalf("balance = %d, want 0: the credit reversed once, not twice", n)
			}
			if took, err := tookOf(s.RefundRemainder(ctx, "pi_a", "dp_a")); err != nil || took != 0 {
				t.Fatalf("a retried dispute took %d, %v; want nothing", took, err)
			}

			// Dispute first, then a refund of the same charge.
			if err := s.TopupPaid(ctx, lead.Email, 10*Dollar, "pi_b", "stripe:pi_b"); err != nil {
				t.Fatalf("TopupPaid: %v", err)
			}
			if _, err := tookOf(s.RefundRemainder(ctx, "pi_b", "dp_b")); err != nil {
				t.Fatalf("dispute: %v", err)
			}
			if took, err := tookOf(s.RefundShare(ctx, "pi_b", "re_b", 1, 1)); err != nil || took != 0 {
				t.Fatalf("a refund after a dispute took %d, %v; want nothing", took, err)
			}
			if n, _ := s.Balance(ctx, w); n != 0 {
				t.Fatalf("balance = %d after dispute and refund, want 0", n)
			}
		})
	}
}

// TestReversingAnUnknownPurchaseIsRefused checks that a reversal names a wallet
// purchase this ledger recorded, so a stray reference cannot move money.
func TestReversingAnUnknownPurchaseIsRefused(t *testing.T) {
	for _, tc := range storeMakers() {
		t.Run(tc.name, func(t *testing.T) {
			s, done := tc.make(t)
			defer done()
			ctx := context.Background()
			if _, err := tookOf(s.RefundShare(ctx, "pi_missing", "re_x", 1, 2)); !errors.Is(err, ErrUnknownPurchase) {
				t.Fatalf("share of an unknown purchase: want ErrUnknownPurchase, got %v", err)
			}
			if _, err := tookOf(s.RefundRemainder(ctx, "pi_missing", "dp_x")); !errors.Is(err, ErrUnknownPurchase) {
				t.Fatalf("remainder of an unknown purchase: want ErrUnknownPurchase, got %v", err)
			}
			if err := s.TopupPaid(ctx, lead.Email, 5*Dollar, "pi_k", "a"); err != nil {
				t.Fatal(err)
			}
			if _, err := tookOf(s.RefundShare(ctx, "pi_k", "re_k", 1, 0)); !errors.Is(err, ErrNotPositive) {
				t.Fatalf("a charge of zero: want ErrNotPositive, got %v", err)
			}
			if _, err := tookOf(s.RefundShare(ctx, "pi_k", "", 1, 2)); !errors.Is(err, ErrNoRef) {
				t.Fatalf("no reversal reference: want ErrNoRef, got %v", err)
			}
		})
	}
}

// tookOf reduces a reversal to the credit it took back, for the tests that are
// about the amount.
func tookOf(r Reversal, err error) (int64, error) { return r.Took, err }
