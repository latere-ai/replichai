// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import "latere.ai/x/pkg/statemachine"

// GrantState is where a grant row stands. It is derived from the row's columns
// rather than stored: a row with no principal and no revocation is invited, a
// row with a principal and no revocation is bound, and a row with a revocation
// is revoked, whatever its principal. An address with no row has no state; the
// row a first write creates starts in one of the initial states.
type GrantState string

// The grant states.
const (
	// GrantInvited is a seeded row no principal has bound yet.
	GrantInvited GrantState = "invited"
	// GrantBound is a row bound to the principal who signed in with its email.
	GrantBound GrantState = "bound"
	// GrantRevoked is a banned row. It keeps its binding, if it had one, so a
	// re-grant returns it to the state it left.
	GrantRevoked GrantState = "revoked"
)

// Grants is every legal change of a grant's state. A new row starts invited (a
// grant or a sign-in created it) or revoked (an address banned before its first
// sign-in). Changing only the tier is not a transition. Revoked is not
// terminal: an admin's grant re-admits the address, to bound when the row kept a
// principal and to invited when it did not.
var Grants = statemachine.Must(statemachine.New("Grant", []GrantState{GrantInvited, GrantRevoked}, map[GrantState][]GrantState{
	GrantInvited: {GrantBound, GrantRevoked},
	GrantBound:   {GrantRevoked},
	GrantRevoked: {GrantInvited, GrantBound},
}))

// grantStateOf derives the state from a row's principal and revocation.
func grantStateOf(principal string, revoked bool) GrantState {
	switch {
	case revoked:
		return GrantRevoked
	case principal != "":
		return GrantBound
	default:
		return GrantInvited
	}
}

// isSelf reports whether the target email or principal of a grant row is the
// admin making the change.
func isSelf(byAdmin Principal, email, principal string) bool {
	if byAdmin.Sub != "" && byAdmin.Sub == principal {
		return true
	}
	return byAdmin.Email != "" && normalizeEmail(byAdmin.Email) == email
}
