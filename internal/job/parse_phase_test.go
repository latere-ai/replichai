// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import "testing"

// TestParsePhaseKnowsExactlyTheTable checks that a phase read from outside the
// store is accepted only when the phase table has it.
func TestParsePhaseKnowsExactlyTheTable(t *testing.T) {
	for _, p := range Phases.States() {
		got, ok := ParsePhase(string(p))
		if !ok || got != p {
			t.Errorf("ParsePhase(%q) = %q, %v; want %q, true", p, got, ok, p)
		}
	}
	for _, name := range []string{"", "paused", "Done", " done"} {
		if _, ok := ParsePhase(name); ok {
			t.Errorf("ParsePhase(%q) accepted a phase the table does not have", name)
		}
	}
}
