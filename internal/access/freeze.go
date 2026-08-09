package access

import "context"

// FreezeOverrides records an admin's decision to let a negative-wallet account
// keep working despite its debt (022d). It is mutable recorded state — granted,
// then cleared when the wallet recovers — so it lives here beside revocation and
// never in the append-only credit ledger, which corrects a mistake with another
// entry rather than retiring a fact. Both access stores satisfy it.
type FreezeOverrides interface {
	// OverrideActive reports whether an uncleared override is in force for email.
	OverrideActive(ctx context.Context, email string) (bool, error)
	// GrantOverride records an override in force for email, naming the admin.
	// Idempotent: it (re)activates, so granting an already-active override
	// changes nothing material, and granting after a clear starts a fresh one.
	GrantOverride(ctx context.Context, byAdmin Principal, email string) error
	// ClearOverride retires the override for email, if any. Idempotent — the
	// top-up that brought the wallet back to solvent calls it, and so may an
	// admin.
	ClearOverride(ctx context.Context, email string) error
}
