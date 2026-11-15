// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Code generated from the session phase table in internal/job/job.go. DO NOT EDIT.
// Regenerate with: go test ./internal/job -run TestPhaseTSIsCurrent -update

export const PHASES = ['done', 'failed', 'publishing', 'resolving', 'running', 'submitted'] as const

export type Phase = (typeof PHASES)[number]

// NEXT is every phase's legal successors. A final phase has none.
export const NEXT: Readonly<Record<Phase, readonly Phase[]>> = {
  done: [],
  failed: [],
  publishing: ['done', 'failed'],
  resolving: ['failed', 'running'],
  running: ['failed', 'publishing'],
  submitted: ['failed', 'resolving'],
}

export const TERMINAL_PHASES: readonly Phase[] = ['done', 'failed']

export const isPhase = (s: string): s is Phase => (PHASES as readonly string[]).includes(s)

// isTerminal reports a phase no session leaves. An unknown phase is not final.
export const isTerminal = (s: string): boolean => isPhase(s) && NEXT[s].length === 0
