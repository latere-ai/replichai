// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import "context"

// FreezeOverrides records an admin's decision to let a negative-wallet account
// keep working despite its debt (022d). It is mutable recorded state, granted
// and then cleared when the wallet recovers, so it belongs here beside
// revocation and never in the append-only credit ledger, which corrects a
// mistake with another entry rather than retiring a fact. Both access stores
// satisfy it.
type FreezeOverrides interface {
	// OverrideActive reports whether an uncleared override is in force for email.
	OverrideActive(ctx context.Context, email string) (bool, error)
	// GrantOverride records an override in force for email, naming the admin.
	// It is idempotent: it activates or reactivates, so granting an
	// already-active override changes nothing material, and granting after a
	// clear starts a new one.
	GrantOverride(ctx context.Context, byAdmin Principal, email string) error
	// ClearOverride retires the override for email, if any. It is idempotent.
	// The top-up that made the wallet solvent calls it, and so may an admin.
	ClearOverride(ctx context.Context, email string) error
}
