// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import "testing"

// TestValidClosesTheSet checks that Valid accepts only the offered set. The
// boundary that accepts a person's choice validates against this set, and the
// adapter passes anything it does not recognise through as a literal model id,
// so a preference outside the set must never be valid.
func TestValidClosesTheSet(t *testing.T) {
	for _, p := range Preferences {
		if !p.Valid() {
			t.Errorf("%q is offered by the engine and did not validate", p)
		}
	}
	for _, p := range []Preference{"", "opus", "claude-opus-5", "Best", "cheapest"} {
		if p.Valid() {
			t.Errorf("%q validated; an unoffered tier reaching the backend is what this guards", p)
		}
	}
}

// TestOrDefaultResolvesUnsetToBest checks that unset is a state, not a tier: a
// row written before the choice existed carries it, and every reader has to
// resolve it the same way for that row to keep running on the model it always
// ran on.
func TestOrDefaultResolvesUnsetToBest(t *testing.T) {
	if got := Preference("").OrDefault(); got != PrefBest {
		t.Errorf("unset resolved to %q, want %q — a session that predates the choice changed model", got, PrefBest)
	}
	if got := PrefFast.OrDefault(); got != PrefFast {
		t.Errorf("a chosen tier was rewritten to %q", got)
	}
}
