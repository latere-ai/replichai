// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"math"
	"math/big"
	"math/rand/v2"
	"testing"
)

// refundReversalOracle is the rule written the long way with math/big, with no
// early exits: max(0, min(credited, floor(credited*refunded/charged)) - already).
func refundReversalOracle(credited, charged, refunded, already int64) int64 {
	n := new(big.Int).Mul(big.NewInt(credited), big.NewInt(refunded))
	owed := new(big.Int).Quo(n, big.NewInt(charged))
	if owed.Cmp(big.NewInt(credited)) > 0 {
		owed = big.NewInt(credited)
	}
	owed.Sub(owed, big.NewInt(already))
	if owed.Sign() < 0 {
		return 0
	}
	return owed.Int64()
}

// TestRefundReversalMatchesTheBigIntegerRule checks RefundReversal against the
// same formula computed with math/big, over a fixed set of cases and many
// seeded random ones, including a refund larger than the charge and amounts
// already reversed beyond what is owed. The Lean theorem in
// formal/Proofs/Refund.lean states this rule as `reversal` and proves its
// bound, so this test is what ties the Go function to that proof.
func TestRefundReversalMatchesTheBigIntegerRule(t *testing.T) {
	cases := []struct{ credited, charged, refunded, already, want int64 }{
		{1000, 2000, 500, 0, 250},
		{1000, 2000, 1500, 250, 500}, // the second of two partial refunds
		{1000, 2000, 2000, 750, 250}, // a full refund lands exactly on credited
		{1000, 2000, 3000, 0, 1000},  // a refund past the charge is capped
		{1000, 2000, 500, 400, 0},    // already past what is owed
		{1000, 2000, 1500, 750, 0},   // a replay of the same total
		{1000, 2000, 500, -5, 250},   // a negative already counts as zero
		{1000, 0, 500, 0, 0},         // no charge
		{0, 2000, 500, 0, 0},         // no credit
		{1000, 2000, 0, 0, 0},        // nothing refunded
		{1000, 3, 1, 0, 333},         // rounds down
		{1000, 3, 2, 333, 333},       // out of order: the larger total first
		{math.MaxInt64, math.MaxInt64, math.MaxInt64, 0, math.MaxInt64},
		{math.MaxInt64, math.MaxInt64, math.MaxInt64 / 2, 0, math.MaxInt64 / 2},
	}
	for _, c := range cases {
		if got := RefundReversal(c.credited, c.charged, c.refunded, c.already); got != c.want {
			t.Errorf("RefundReversal(%d, %d, %d, %d) = %d, want %d", c.credited, c.charged, c.refunded, c.already, got, c.want)
		}
	}

	rng := rand.New(rand.NewPCG(52, 1))
	pick := func(limit int64) int64 { return rng.Int64N(limit) + 1 }
	for range 200000 {
		limit := []int64{10, 1_000_000, 5_000_000_000_000, math.MaxInt64}[rng.IntN(4)]
		credited, charged := pick(limit), pick(limit)
		refunded := pick(limit)
		if rng.IntN(3) == 0 {
			refunded = charged // a full refund
		}
		already := rng.Int64N(credited + 1)
		if rng.IntN(4) == 0 {
			already = pick(limit)
		}
		got := RefundReversal(credited, charged, refunded, already)
		if want := refundReversalOracle(credited, charged, refunded, already); got != want {
			t.Fatalf("RefundReversal(%d, %d, %d, %d) = %d, want %d", credited, charged, refunded, already, got, want)
		}
		if got < 0 || already+got > max(credited, already) {
			t.Fatalf("RefundReversal(%d, %d, %d, %d) = %d breaks the bound", credited, charged, refunded, already, got)
		}
	}
}

// TestRefundReversalOrderDoesNotChangeTheTotal checks that partial refunds
// delivered in any order take back the same total, and exactly the credit once
// the refunded total reaches the charge.
func TestRefundReversalOrderDoesNotChangeTheTotal(t *testing.T) {
	const credited, charged = 18_700_000, 20_000_000
	totals := []int64{3_000_000, 7_500_001, 12_345_678, 20_000_000}
	orders := [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 0, 3, 1}, {1, 3, 0, 2}}
	for _, order := range orders {
		var reversed int64
		for _, i := range order {
			reversed += RefundReversal(credited, charged, totals[i], reversed)
		}
		if reversed != credited {
			t.Errorf("order %v reversed %d, want exactly %d", order, reversed, int64(credited))
		}
	}
}
