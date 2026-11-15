// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package access

import (
	"errors"
	"fmt"
	"slices"
)

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

// grantTransitions is every legal change of a grant's state. A new row starts
// invited (a grant or a sign-in created it) or revoked (an address banned before
// its first sign-in). Changing only the tier is not a transition. Revoked is not
// terminal: an admin's grant re-admits the address, to bound when the row
// kept a principal and to invited when it did not.
var grantTransitions = map[GrantState][]GrantState{
	GrantInvited: {GrantBound, GrantRevoked},
	GrantBound:   {GrantRevoked},
	GrantRevoked: {GrantInvited, GrantBound},
}

// grantInitial are the states a first write to an address may create.
var grantInitial = []GrantState{GrantInvited, GrantRevoked}

// ErrIllegalGrantTransition reports a change of grant state the table does not
// list.
var ErrIllegalGrantTransition = errors.New("access: illegal grant transition")

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

// checkGrantStep refuses a change of state the table does not list. A write that
// leaves the state as it is, such as promoting a tier, is not a change and passes.
func checkGrantStep(from, to GrantState) error {
	if from == to {
		return nil
	}
	if !slices.Contains(grantTransitions[from], to) {
		return fmt.Errorf("%w: %s to %s", ErrIllegalGrantTransition, from, to)
	}
	return nil
}

// checkGrantCreate refuses a first write that starts in a state a new row may
// not start in.
func checkGrantCreate(to GrantState) error {
	if !slices.Contains(grantInitial, to) {
		return fmt.Errorf("%w: a new row cannot start %s", ErrIllegalGrantTransition, to)
	}
	return nil
}

// isSelf reports whether the target email or principal of a grant row is the
// admin making the change.
func isSelf(byAdmin Principal, email, principal string) bool {
	if byAdmin.Sub != "" && byAdmin.Sub == principal {
		return true
	}
	return byAdmin.Email != "" && normalizeEmail(byAdmin.Email) == email
}
