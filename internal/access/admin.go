// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import "context"

// Resolver resolves a principal's tier, which is the one method every
// store-level admin check needs. Access satisfies it, as does any store that
// wraps one.
type Resolver interface {
	Resolve(ctx context.Context, p Principal) (Tier, bool, error)
}

// RequireAdmin is the single rule behind every store-level admin check: the
// caller resolves, is granted, and holds TierAdmin. A nil resolver admits
// everyone, which is the case for an offline run with no access store and
// never for a served deployment. Every store that gates an administrative
// write calls this rather than carrying its own copy, so the rule cannot drift
// between the ledger, the platform settings, and the grants themselves.
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

// bindOutcome is the result of comparing a seeded row with the principal
// presenting its email: the principal binds it now, already holds it, or it
// belongs to somebody else.
type bindOutcome int

const (
	// bindClaim is an unbound seeded row: this principal binds it now.
	bindClaim bindOutcome = iota
	// bindMine is a row this same principal already bound. It arises on a
	// concurrent first sign-in, where two requests from one person both passed
	// the principal lookup and the second waited on the row lock while the
	// first bound it. It resolves, and must not be treated as somebody else's.
	bindMine
	// bindForeign is a row bound to another principal: not this one's.
	bindForeign
)

// outcomeOf classifies a seeded row for sub. It is a pure function so that
// both the grant and the membership store bind by the same rule, and so that
// the rule has a test of its own rather than one behind a database race.
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
