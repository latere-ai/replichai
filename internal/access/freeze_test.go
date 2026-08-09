package access

import (
	"errors"
	"testing"
)

// runFreezeContract asserts the FreezeOverrides invariants, run against both
// stores so the in-memory and Postgres implementations cannot drift.
func runFreezeContract(t *testing.T, ov FreezeOverrides) {
	t.Helper()
	admin := Principal{Sub: "admin-sub", Email: "admin@example.com"}
	const who = "Debtor@Example.com"

	if active, err := ov.OverrideActive(ctx, who); err != nil || active {
		t.Fatalf("fresh override active = %v, %v; want inactive", active, err)
	}

	// A grant is in force, and is keyed by normalised email so a differently
	// cased read finds it.
	if err := ov.GrantOverride(ctx, admin, who); err != nil {
		t.Fatalf("GrantOverride: %v", err)
	}
	if active, _ := ov.OverrideActive(ctx, "debtor@example.com "); !active {
		t.Fatal("granted override not active under the normalised email")
	}
	// Re-granting an active override is a no-op, not an error.
	if err := ov.GrantOverride(ctx, admin, who); err != nil {
		t.Fatalf("re-grant: %v", err)
	}

	// Clear retires it, idempotently; a fresh grant reactivates — so a later
	// chargeback re-freezes as a new decision.
	if err := ov.ClearOverride(ctx, who); err != nil {
		t.Fatalf("ClearOverride: %v", err)
	}
	if active, _ := ov.OverrideActive(ctx, who); active {
		t.Fatal("cleared override still active")
	}
	if err := ov.ClearOverride(ctx, who); err != nil {
		t.Fatalf("clear again (idempotent): %v", err)
	}
	if err := ov.GrantOverride(ctx, admin, who); err != nil {
		t.Fatalf("re-grant after clear: %v", err)
	}
	if active, _ := ov.OverrideActive(ctx, who); !active {
		t.Fatal("override after re-grant should be active")
	}

	// An override needs an email to key on.
	if err := ov.GrantOverride(ctx, admin, "  "); !errors.Is(err, ErrNoEmail) {
		t.Fatalf("empty email: want ErrNoEmail, got %v", err)
	}
}

func TestMemStoreSatisfiesTheFreezeContract(t *testing.T) {
	runFreezeContract(t, NewMemStore())
}
