// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import "math/big"

// RefundReversal is how much more credit a refund takes back from a purchase.
// credited is what the purchase credited, charged is what the charge was and
// refunded is everything refunded on the charge so far, the last two in one
// unit. alreadyReversed is what earlier reversals of the purchase took back.
//
// The purchase owes back min(credited, floor(credited*refunded/charged)), and
// the result is that less alreadyReversed, or zero when nothing more is owed.
// So the reversals of one purchase never total more than credited, they reach
// it exactly when refunded reaches charged, and the order the refunds arrive
// in does not change the total.
//
// An input that cannot describe a refund (no credit, no charge, nothing
// refunded) owes nothing, and a negative alreadyReversed counts as zero. The
// product is formed with big integers, because credited*refunded overflows
// int64 for a purchase of a few thousand dollars in micro-USD. The function
// does no I/O.
func RefundReversal(credited, charged, refunded, alreadyReversed int64) int64 {
	if credited <= 0 || charged <= 0 || refunded <= 0 {
		return 0
	}
	owed := credited
	if refunded < charged {
		n := new(big.Int).Mul(big.NewInt(credited), big.NewInt(refunded))
		owed = n.Quo(n, big.NewInt(charged)).Int64()
	}
	return max(owed-max(alreadyReversed, 0), 0)
}
