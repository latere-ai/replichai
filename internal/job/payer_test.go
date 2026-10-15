// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"errors"
	"testing"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/provider"
)

// runPersonPaidContract holds a store to what a session paid by the person
// who started it means (048): the project's pot is never asked, so an empty
// one does not refuse it, nothing is held or charged, and its cost is what
// the platform reported. A project-paid session in the same empty pot is
// still refused, so the gate is moved, not removed.
func runPersonPaidContract(t *testing.T, s Store, ledger credit.Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.Enqueue(ctx, Submission{Principal: "u", Ref: "arxiv:2222.1", Kind: KindAudit, Project: "empty", PaperKey: "arxiv:2222.1"}); !errors.Is(err, ErrNoCredits) {
		t.Fatalf("a project-paid session in an empty pot = %v", err)
	}
	j, err := s.Enqueue(ctx, Submission{
		Principal: "u", Ref: "arxiv:2222.2", Kind: KindAudit, Project: "empty", PaperKey: "arxiv:2222.2",
		Tier: credit.PrefBalanced, Runtime: "latere", Model: "anthropic/claude-sonnet-4.6", Payer: PayerPerson,
	})
	if err != nil {
		t.Fatalf("a person-paid session in an empty pot = %v", err)
	}
	if j.Runtime != "latere" || j.Model != "anthropic/claude-sonnet-4.6" || j.Payer != PayerPerson {
		t.Fatalf("enqueued = %+v", j)
	}
	if err := s.Observe(ctx, j.ID, provider.RunStatus{
		Model: "claude-sonnet-4.6", ModelProvider: "anthropic", ChargedCost: 1234,
		Usage: provider.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000},
	}); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []Phase{PhaseResolving, PhaseRunning, PhasePublishing, PhaseDone} {
		if err := s.Advance(ctx, j.ID, phase, nil); err != nil {
			t.Fatalf("advance to %s: %v", phase, err)
		}
	}
	got, err := s.Get(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cost != 1234 || got.Runtime != "latere" || got.Model != "anthropic/claude-sonnet-4.6" || got.Payer != PayerPerson {
		t.Fatalf("read back = cost %d, %+v", got.Cost, got)
	}
	if balance, _ := ledger.Balance(ctx, credit.Pot("empty")); balance != 0 {
		t.Fatalf("the ledger moved by %d for a session the person paid", balance)
	}
	if available, _ := ledger.Available(ctx, credit.Pot("empty")); available != 0 {
		t.Fatalf("a hold of %d was taken for a session the person paid", -available)
	}
}

func TestMemStoreSessionsAPersonPays(t *testing.T) {
	ledger := credit.NewMemStore(nil, nil)
	runPersonPaidContract(t, NewMemStore(WithCredits(ledger, fixedReserve(testReserve)), WithRates(fixedCard(testCard))), ledger)
}

func TestPGStoreSessionsAPersonPays(t *testing.T) {
	pool := settlePool(t)
	defer pool.Close()
	ledger := credit.NewPGStore(pool, nil, nil)
	runPersonPaidContract(t, NewPGStore(pool, WithPGCredits(ledger, fixedReserve(testReserve)), WithPGRates(fixedCard(testCard))), ledger)
}

func TestAnEmptyPayerIsTheProject(t *testing.T) {
	if !Payer("").PaysProject() || !PayerProject.PaysProject() || PayerPerson.PaysProject() {
		t.Fatal("PaysProject")
	}
}
