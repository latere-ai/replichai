// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import (
	"context"
	"sync"
	"testing"
)

// seedableAccess is a store the contracts can seed a grant into.
type seedableAccess interface {
	Access
	SeedGrant(ctx context.Context, email string, tier Tier) error
}

func TestOutcomeOfASeededRow(t *testing.T) {
	me, them := "sub-me", "sub-them"
	if got := outcomeOf(nil, me); got != bindClaim {
		t.Errorf("unbound row = %v, want bindClaim", got)
	}
	if got := outcomeOf(&me, me); got != bindMine {
		t.Errorf("row bound to me = %v, want bindMine", got)
	}
	if got := outcomeOf(&them, me); got != bindForeign {
		t.Errorf("row bound to them = %v, want bindForeign", got)
	}
}

// runBindContract is the first-login rule both stores hold: a seeded grant
// binds to the first principal presenting its email, resolves for that same
// principal however many times and however concurrently they ask, and never
// resolves for a second principal.
func runBindContract(t *testing.T, acc seedableAccess, email string) {
	t.Helper()
	if err := acc.SeedGrant(ctx, email, TierUser); err != nil {
		t.Fatalf("SeedGrant: %v", err)
	}
	me := Principal{Sub: "sub-" + email, Email: email}

	// The concurrent first sign-in: the app fires more than one request the
	// moment the callback lands, and every one of them is the same person.
	var wg sync.WaitGroup
	results := make([]bool, 8)
	errs := make([]error, 8)
	for i := range results {
		wg.Go(func() {
			_, results[i], errs[i] = acc.Resolve(ctx, me)
		})
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("Resolve #%d: %v", i, errs[i])
		}
		if !results[i] {
			t.Fatalf("Resolve #%d of the same principal was refused: the row it had just bound read as somebody else's", i)
		}
	}

	// A second principal presenting the same email is not admitted.
	if _, granted, err := acc.Resolve(ctx, Principal{Sub: "impostor", Email: email}); err != nil || granted {
		t.Fatalf("second principal on a bound email: granted=%v err=%v, want refused", granted, err)
	}
}

func TestMemStoreBindsOnce(t *testing.T) {
	runBindContract(t, NewMemStore(), "first@example.com")
}
