// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import "context"

// Resolver answers who a principal is: the one method every store-level admin
// check needs. Access satisfies it; so does any store that fronts one.
type Resolver interface {
	Resolve(ctx context.Context, p Principal) (Tier, bool, error)
}

// RequireAdmin is the one rule behind every store-level admin check: the
// caller resolves, is granted, and holds TierAdmin. A nil resolver admits
// everyone, which is the offline run with no access store wired and never a
// served deployment. Every store that gates an administrative write calls
// this rather than carrying its own copy, so the rule cannot drift between
// the ledger, the platform settings, and the grants themselves.
func RequireAdmin(ctx context.Context, auth Resolver, p Principal) error {
	if auth == nil {
		return nil
	}
	tier, granted, err := auth.Resolve(ctx, p)
	if err != nil {
		return err
	}
	if !granted || tier != TierAdmin {
		return ErrNotAdmin
	}
	return nil
}

// bindOutcome is what a seeded row means for the principal presenting its
// email: bind it now, it is already theirs, or it belongs to somebody else.
type bindOutcome int

const (
	// bindClaim is an unbound seeded row: this principal binds it now.
	bindClaim bindOutcome = iota
	// bindMine is a row this same principal already bound — the concurrent
	// first sign-in, where two requests from one person both passed the
	// principal lookup and the second waited on the row lock while the first
	// bound it. It resolves; it must not read as somebody else's.
	bindMine
	// bindForeign is a row bound to another principal: not this one's.
	bindForeign
)

// outcomeOf decides a seeded row for sub. It is a pure function so both the
// grant and the membership store bind by the same rule, and so the rule has
// a test of its own rather than one behind a database race.
func outcomeOf(bound *string, sub string) bindOutcome {
	switch {
	case bound == nil:
		return bindClaim
	case *bound == sub:
		return bindMine
	default:
		return bindForeign
	}
}
