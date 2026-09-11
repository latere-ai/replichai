// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"context"
	"testing"
)

// TestATopupAnnouncesSolvencyOnlyWhenTheDebtIsPaid checks that money entering
// a wallet reports solvency once the wallet is at or above zero, and reports
// nothing while it is still in debt. The freeze override is retired on that
// report, so a read never has to write.
func TestATopupAnnouncesSolvencyOnlyWhenTheDebtIsPaid(t *testing.T) {
	ctx := context.Background()
	var told []string
	s := NewMemStore(nil, nil, WithOnSolvent(func(_ context.Context, email string) { told = append(told, email) }))
	const who = "debtor@example.com"

	if err := s.TopupPaid(ctx, who, 5*Dollar, "pi_1", "a"); err != nil {
		t.Fatal(err)
	}
	if len(told) != 1 { // a first top-up from zero is solvent
		t.Fatalf("told %v after a first top-up, want once", told)
	}
	_ = s.RefundPaid(ctx, who, 12*Dollar, "re_1", "a") // -$7
	told = nil
	if err := s.TopupPaid(ctx, who, 3*Dollar, "pi_2", "a"); err != nil { // -$4: still in debt
		t.Fatal(err)
	}
	if len(told) != 0 {
		t.Fatalf("told %v while still in debt", told)
	}
	if err := s.TopupPaid(ctx, who, 4*Dollar, "pi_3", "a"); err != nil { // $0: paid
		t.Fatal(err)
	}
	if len(told) != 1 || told[0] != who {
		t.Fatalf("told %v once the debt was paid, want [%s]", told, who)
	}
}
