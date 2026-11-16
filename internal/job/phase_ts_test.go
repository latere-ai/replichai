// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var updatePhaseTS = flag.Bool("update", false, "rewrite frontend/src/generated/job-phase.ts from the phase table")

// phaseTSPath is where the browser's copy of the phase table lives.
var phaseTSPath = filepath.Join("..", "..", "frontend", "src", "generated", "job-phase.ts")

// renderPhaseTS renders the phase table as the TypeScript module the browser
// reads, so a phase the server adds is known to the browser by name and by
// whether it is final, rather than shown as running.
func renderPhaseTS() string {
	phases := Phases.States()
	quote := func(ps []Phase) string {
		q := make([]string, len(ps))
		for i, p := range ps {
			q[i] = "'" + string(p) + "'"
		}
		return strings.Join(q, ", ")
	}
	var terminal []Phase
	for _, p := range phases {
		if Phases.Terminal(p) {
			terminal = append(terminal, p)
		}
	}
	var b strings.Builder
	b.WriteString("// SPDX-FileCopyrightText: 2026 Latere AI\n")
	b.WriteString("// SPDX-License-Identifier: AGPL-3.0-or-later\n\n")
	b.WriteString("// Code generated from the session phase table in internal/job/job.go. DO NOT EDIT.\n")
	b.WriteString("// Regenerate with: go test ./internal/job -run TestPhaseTSIsCurrent -update\n\n")
	fmt.Fprintf(&b, "export const PHASES = [%s] as const\n\n", quote(phases))
	b.WriteString("export type Phase = (typeof PHASES)[number]\n\n")
	b.WriteString("// NEXT is every phase's legal successors. A final phase has none.\n")
	b.WriteString("export const NEXT: Readonly<Record<Phase, readonly Phase[]>> = {\n")
	for _, p := range phases {
		next := Phases.Next(p)
		fmt.Fprintf(&b, "  %s: [%s],\n", p, quote(next))
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, "export const TERMINAL_PHASES: readonly Phase[] = [%s]\n\n", quote(terminal))
	b.WriteString("export const isPhase = (s: string): s is Phase => (PHASES as readonly string[]).includes(s)\n\n")
	b.WriteString("// isTerminal reports a phase no session leaves. An unknown phase is not final.\n")
	b.WriteString("export const isTerminal = (s: string): boolean => isPhase(s) && NEXT[s].length === 0\n")
	return b.String()
}

// TestPhaseTSIsCurrent fails when the browser's copy of the phase table no
// longer matches the table. Run it with -update to rewrite the copy.
func TestPhaseTSIsCurrent(t *testing.T) {
	want := renderPhaseTS()
	if *updatePhaseTS {
		if err := os.WriteFile(phaseTSPath, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(phaseTSPath)
	if err != nil {
		t.Fatalf("read %s: %v (run go test ./internal/job -run TestPhaseTSIsCurrent -update)", phaseTSPath, err)
	}
	if string(got) != want {
		t.Fatalf("%s is out of date with the phase table; run go test ./internal/job -run TestPhaseTSIsCurrent -update", phaseTSPath)
	}
}

func TestActivePhasesAreTheNonFinalOnes(t *testing.T) {
	got := ActivePhases()
	want := []Phase{PhasePublishing, PhaseResolving, PhaseRunning, PhaseSubmitted}
	if !slices.Equal(got, want) {
		t.Fatalf("ActivePhases() = %v, want %v", got, want)
	}
	for _, p := range got {
		if !p.Active() || p.Terminal() {
			t.Fatalf("%s is listed active but Active()=%v Terminal()=%v", p, p.Active(), p.Terminal())
		}
	}
}
