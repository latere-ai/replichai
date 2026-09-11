// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A session's lifecycle phase as the screens show it: its tone, its glyph, its
// badge class and its label, so a phase reads the same wherever it appears,
// whether a row badge, a timeline dot or a scrubber band. The list of phases itself is
// transcript.ts's PHASE_ORDER.

import { type I18n } from './i18n'
import { type Phase, PHASE_ORDER } from './transcript'
import { type IconName } from './icons'
import { type Tone } from './ui'
import { terminal } from './session-stop'

// phaseClass tones a phase badge: any non-terminal phase reads as "running".
export const phaseClass = (p: string) => (terminal(p) ? p : 'running')

// phaseTone maps a session phase onto the semantic ramp, so a phase is the same
// colour wherever it appears: a row badge, a timeline dot, a pill.
export function phaseTone(phase: string): Tone {
  if (phase === 'done') return 'ok'
  if (phase === 'failed') return 'err'
  return 'run'
}

// phaseIcon gives each phase a glyph, so the lifecycle is legible before the
// label is read.
export function phaseIcon(phase: string): IconName {
  if (phase === 'done') return 'check'
  if (phase === 'failed') return 'alert'
  if (phase === 'publishing') return 'package'
  if (phase === 'resolving') return 'search'
  return 'clock'
}

// A phase the client knows is named; one it does not is shown as itself. The
// backend can add a phase before the client learns it, and `phase.reconciling`
// on screen is an identifier leaking out, not a label.
const isPhase = (p: string): p is Phase => (PHASE_ORDER as readonly string[]).includes(p)
export function phaseLabel(t: I18n['t'], phase: string) {
  return isPhase(phase) ? t(`phase.${phase}`) : phase.replace(/_/g, ' ')
}
