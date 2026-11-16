// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"context"
	"errors"
	"testing"
)

// TestAWonDisputeGivesBackWhatItTookOnce checks that a won dispute credits back
// exactly what that dispute's reversal took, once however many times the
// outcome is delivered, and that a later refund then counts the restored credit
// as not yet reversed.
func TestAWonDisputeGivesBackWhatItTookOnce(t *testing.T) {
	var told []string
	onSolvent := WithOnSolvent(func(_ context.Context, email string) { told = append(told, email) })
	for _, tc := range storeMakers(onSolvent) {
		t.Run(tc.name, func(t *testing.T) {
			told = nil
			s, done := tc.make(t)
			defer done()
			ctx := context.Background()
			w := Wallet(lead.Email)
			if err := s.TopupPaid(ctx, lead.Email, 20*Dollar, "pi_w", "a"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RefundShare(ctx, "pi_w", "re_w", 500, 2000); err != nil { // takes $5
				t.Fatal(err)
			}
			rev, err := s.RefundRemainder(ctx, "pi_w", "dp_w") // takes the other $15
			if err != nil || rev.Took != 15*Dollar {
				t.Fatalf("dispute took %d, %v; want $15", rev.Took, err)
			}
			told = nil

			back, err := s.RestoreDispute(ctx, "pi_w", "dp_w")
			if err != nil || back != 15*Dollar {
				t.Fatalf("restored %d, %v; want $15", back, err)
			}
			if bal, _ := s.Balance(ctx, w); bal != 15*Dollar {
				t.Fatalf("balance = %d, want $15: the refund's $5 stays reversed", bal)
			}
			if len(told) != 1 {
				t.Fatalf("solvency hook ran %d times, want once", len(told))
			}
			// The same outcome delivered again, as Stripe may.
			if back, err := s.RestoreDispute(ctx, "pi_w", "dp_w"); err != nil || back != 0 {
				t.Fatalf("second restore = %d, %v; want 0", back, err)
			}
			if bal, _ := s.Balance(ctx, w); bal != 15*Dollar {
				t.Fatalf("balance after a repeat = %d, want $15", bal)
			}
			// A refund of the whole charge now takes back only what is still
			// credited, $15 more.
			rev, err = s.RefundShare(ctx, "pi_w", "re_all", 2000, 2000)
			if err != nil || rev.Took != 15*Dollar {
				t.Fatalf("later refund took %d, %v; want $15", rev.Took, err)
			}
		})
	}
}

// TestRestoringADisputeThatTookNothingPostsNothing checks the restore of a
// dispute with no reversal, and of an unknown purchase.
func TestRestoringADisputeThatTookNothingPostsNothing(t *testing.T) {
	for _, tc := range storeMakers() {
		t.Run(tc.name, func(t *testing.T) {
			s, done := tc.make(t)
			defer done()
			ctx := context.Background()
			if err := s.TopupPaid(ctx, lead.Email, 5*Dollar, "pi_n", "a"); err != nil {
				t.Fatal(err)
			}
			if back, err := s.RestoreDispute(ctx, "pi_n", "dp_none"); err != nil || back != 0 {
				t.Fatalf("restore of an unreversed dispute = %d, %v; want 0", back, err)
			}
			if _, err := s.RestoreDispute(ctx, "pi_missing", "dp_x"); !errors.Is(err, ErrUnknownPurchase) {
				t.Fatalf("restore on an unknown purchase = %v, want ErrUnknownPurchase", err)
			}
		})
	}
}

// TestAWonDisputeBeforeItsDisputeKeepsTheCredit checks that a dispute whose win
// is delivered before the dispute itself takes nothing when it arrives, and
// that another dispute of the same purchase still takes what is left.
func TestAWonDisputeBeforeItsDisputeKeepsTheCredit(t *testing.T) {
	for _, tc := range storeMakers() {
		t.Run(tc.name, func(t *testing.T) {
			s, done := tc.make(t)
			defer done()
			ctx := context.Background()
			w := Wallet(lead.Email)
			if err := s.TopupPaid(ctx, lead.Email, 20*Dollar, "pi_o", "a"); err != nil {
				t.Fatal(err)
			}
			if back, err := s.RestoreDispute(ctx, "pi_o", "dp_o"); err != nil || back != 0 {
				t.Fatalf("win before its dispute restored %d, %v; want 0", back, err)
			}
			rev, err := s.RefundRemainder(ctx, "pi_o", "dp_o")
			if err != nil || rev.Took != 0 || rev.Froze {
				t.Fatalf("the won dispute's own delivery took %+v, %v; want nothing", rev, err)
			}
			if bal, _ := s.Balance(ctx, w); bal != 20*Dollar {
				t.Fatalf("balance = %d, want the $20 kept", bal)
			}
			rev, err = s.RefundRemainder(ctx, "pi_o", "dp_other")
			if err != nil || rev.Took != 20*Dollar {
				t.Fatalf("another dispute took %d, %v; want $20", rev.Took, err)
			}
		})
	}
}
