// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"context"
	"testing"
)

// TestReclaimingIntoANegativeWalletEndsTheWaiver checks that credit reclaimed
// from a project retires the freeze waiver once it lifts the wallet to zero,
// as a top-up does. Only a top-up did before, so a waiver outlived the debt and
// also excused a later chargeback.
func TestReclaimingIntoANegativeWalletEndsTheWaiver(t *testing.T) {
	var told []string
	onSolvent := WithOnSolvent(func(_ context.Context, email string) { told = append(told, email) })
	for _, tc := range storeMakers(onSolvent) {
		t.Run(tc.name, func(t *testing.T) {
			told = nil
			s, done := tc.make(t)
			defer done()
			ctx := context.Background()
			if err := s.TopupPaid(ctx, lead.Email, 10*Dollar, "pi_r", "a"); err != nil {
				t.Fatal(err)
			}
			if err := s.Allocate(ctx, lead, "p1", "Project One", 10*Dollar); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RefundRemainder(ctx, "pi_r", "dp_r"); err != nil { // wallet at -$10
				t.Fatal(err)
			}
			told = nil
			if err := s.Reclaim(ctx, lead, "p1", "Project One", 4*Dollar); err != nil { // -$6, still in debt
				t.Fatal(err)
			}
			if len(told) != 0 {
				t.Fatalf("told %v while the wallet was still in debt", told)
			}
			if err := s.Reclaim(ctx, lead, "p1", "Project One", 6*Dollar); err != nil { // $0
				t.Fatal(err)
			}
			if len(told) != 1 || told[0] != lead.Email {
				t.Fatalf("told %v once the reclaim cleared the debt, want [%s]", told, lead.Email)
			}
		})
	}
}
