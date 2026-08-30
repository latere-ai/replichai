// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"testing"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/provider"
)

// Choosing a cheaper tier is only worth anything if the cheaper model is what
// the session is actually billed at. Nothing in 031 adds a pricing path: the
// tier decides which model the backend serves, the resolved model is observed
// onto the job, and the card snapshotted at enqueue prices it. This asserts that
// chain end to end on the number settlement charges — the same expression the
// client is shown, since both come from Job.cost.
func TestATierIsBilledAtItsOwnModelsRates(t *testing.T) {
	// One session's worth of usage, held constant, so the only thing moving
	// between the cases is which model served it.
	usage := provider.Usage{InputTokens: 2_000_000, OutputTokens: 500_000}

	for _, tc := range []struct {
		tier  credit.Preference
		model string
		want  int64 // micro-USD: input×rate + output×rate, per million
	}{
		// $5 × 2M in + $25 × 0.5M out.
		{credit.PrefBest, "claude-opus-5", 10*credit.Dollar + 12*credit.Dollar + credit.Dollar/2},
		// $3 × 2M + $15 × 0.5M.
		{credit.PrefBalanced, "claude-sonnet-5", 6*credit.Dollar + 7*credit.Dollar + credit.Dollar/2},
		// $1 × 2M + $5 × 0.5M. A dated snapshot of a model the card names is
		// that model, so the cheap tier is billed cheaply rather than at the
		// dearest-rates fallback — the production finding behind the snapshot rule.
		{credit.PrefFast, "claude-haiku-4-5-20251001", 2*credit.Dollar + 2*credit.Dollar + credit.Dollar/2},
	} {
		t.Run(tc.tier.String(), func(t *testing.T) {
			j := Job{
				Tier:   tc.tier,
				Rates:  credit.DefaultCard(),
				Status: provider.RunStatus{Model: tc.model, Usage: usage},
			}
			if got := j.withCost().Cost; got != tc.want {
				t.Errorf("a %s session on %s cost %d, want %d", tc.tier, tc.model, got, tc.want)
			}
		})
	}

	// And the cheapest tier really is cheaper than the dearest, which is the
	// whole reason to offer the choice.
	cost := func(model string) int64 {
		return Job{Rates: credit.DefaultCard(), Status: provider.RunStatus{Model: model, Usage: usage}}.withCost().Cost
	}
	if cost("claude-haiku-4-5-20251001") >= cost("claude-opus-5") {
		t.Error("the fast tier is not cheaper than the best one; the choice buys nothing")
	}
}

// The tier changes what a session burns, not what it must have to begin. The
// reserve is read with no model in hand, so picking the cheap tier does not let
// a project that could not afford a run start one — and nobody reading this
// change should conclude the gate moved.
func TestTheStartReserveDoesNotVaryByTier(t *testing.T) {
	ctx := context.Background()

	for _, tier := range append([]credit.Preference{""}, credit.Preferences...) {
		ledger := credit.NewMemStore(nil, nil)
		s := NewMemStore(WithCredits(ledger, fixedReserve(testReserve)))

		// A project holding less than one reserve cannot start, whatever tier is
		// asked for.
		fund(t, ledger, "thin", testReserve-1)
		_, err := s.Enqueue(ctx, Submission{
			Principal: "u", Ref: "arxiv:1111.1", Kind: KindAudit, Project: "thin", PaperKey: "arxiv:1111.1", Tier: tier,
		})
		if err == nil {
			t.Errorf("tier %q started a session the project could not afford; the tier is not the gate", tier)
		}

		// And a project holding exactly one reserve starts, holding exactly that —
		// the same amount for every tier.
		fund(t, ledger, "exact", testReserve)
		before, err := ledger.Available(ctx, credit.Pot("exact"))
		if err != nil {
			t.Fatalf("Available: %v", err)
		}
		if _, err := s.Enqueue(ctx, Submission{
			Principal: "u", Ref: "arxiv:2222.2", Kind: KindAudit, Project: "exact", PaperKey: "arxiv:2222.2", Tier: tier,
		}); err != nil {
			t.Fatalf("tier %q: a funded project must start: %v", tier, err)
		}
		after, err := ledger.Available(ctx, credit.Pot("exact"))
		if err != nil {
			t.Fatalf("Available: %v", err)
		}
		if before-after != testReserve {
			t.Errorf("tier %q held %d at the door, want the reserve %d", tier, before-after, testReserve)
		}
	}
}
