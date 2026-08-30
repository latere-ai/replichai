// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package webauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/httpapi"
)

// passthrough answers a distinctive code so a test can tell "the gate let it
// through" from "the gate refused it 402".
const passthrough = 299

func frozenGate(t *testing.T) (http.Handler, *credit.MemStore, *access.MemStore) {
	t.Helper()
	ledger := credit.NewMemStore(nil, nil)
	ov := access.NewMemStore()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(passthrough) })
	return FreezeGate(ledger, ov)(next), ledger, ov
}

func driveAs(gate http.Handler, method, path, email string) int {
	r := httptest.NewRequest(method, path, nil)
	r = r.WithContext(httpapi.WithIdentity(r.Context(), httpapi.Identity{Sub: "s", Email: email}))
	w := httptest.NewRecorder()
	gate.ServeHTTP(w, r)
	return w.Code
}

func TestFreezeGateRefusesAMutatingRequestFromANegativeWallet(t *testing.T) {
	ctx := context.Background()
	email := "debtor@example.com"
	gate, ledger, _ := frozenGate(t)
	// A purchase then an over-refund drives the wallet to -$2.
	if err := ledger.TopupPaid(ctx, email, 5*credit.Dollar, "pi_1", "a"); err != nil {
		t.Fatalf("TopupPaid: %v", err)
	}
	if err := ledger.RefundPaid(ctx, email, 7*credit.Dollar, "re_1", "a"); err != nil {
		t.Fatalf("RefundPaid: %v", err)
	}

	if code := driveAs(gate, "POST", "/projects", email); code != http.StatusPaymentRequired {
		t.Fatalf("mutating while frozen = %d, want 402 account_frozen", code)
	}
	if code := driveAs(gate, "GET", "/projects", email); code != passthrough {
		t.Fatalf("read while frozen = %d, want passthrough", code)
	}
	if code := driveAs(gate, "POST", "/me/credits/checkout", email); code != passthrough {
		t.Fatalf("top-up while frozen = %d, want passthrough (the one escape)", code)
	}
	// A solvent account is never frozen.
	if code := driveAs(gate, "POST", "/projects", "solvent@example.com"); code != passthrough {
		t.Fatalf("solvent account = %d, want passthrough", code)
	}
}

func TestFreezeGateThawsAndRespectsAOneShotOverride(t *testing.T) {
	ctx := context.Background()
	email := "debtor@example.com"
	admin := access.Principal{Sub: "admin", Email: "admin@example.com"}
	gate, ledger, ov := frozenGate(t)
	post := func() int { return driveAs(gate, "POST", "/projects", email) }

	// -$2: frozen.
	_ = ledger.TopupPaid(ctx, email, 5*credit.Dollar, "pi_1", "a")
	_ = ledger.RefundPaid(ctx, email, 7*credit.Dollar, "re_1", "a")
	if post() != http.StatusPaymentRequired {
		t.Fatalf("negative wallet should be frozen")
	}

	// An admin override thaws a still-negative account.
	if err := ov.GrantOverride(ctx, admin, email); err != nil {
		t.Fatalf("GrantOverride: %v", err)
	}
	if post() != passthrough {
		t.Fatalf("override should thaw a negative account")
	}
	// A further debit keeps it writable — the override survives.
	_ = ledger.RefundPaid(ctx, email, 1*credit.Dollar, "re_2", "a") // -$3
	if post() != passthrough {
		t.Fatalf("override should survive a further debit")
	}

	// A top-up to solvent passes and clears the override on the solvent read.
	_ = ledger.TopupPaid(ctx, email, 5*credit.Dollar, "pi_2", "a") // +$2
	if post() != passthrough {
		t.Fatalf("solvent account should pass")
	}
	if active, _ := ov.OverrideActive(ctx, email); active {
		t.Fatalf("override should be cleared once the wallet is solvent")
	}

	// A fresh chargeback re-freezes: the waiver did not outlive its debt.
	_ = ledger.RefundPaid(ctx, email, 5*credit.Dollar, "re_3", "a") // -$3
	if post() != http.StatusPaymentRequired {
		t.Fatalf("a new debt should re-freeze, a fresh decision")
	}
}
