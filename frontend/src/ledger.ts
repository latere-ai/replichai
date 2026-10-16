// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// What a wallet statement row says. The ledger's kinds are the server's
// vocabulary; these name each entry in the reader's terms, with the project a
// move captured. Pure functions, so the tests read them without a browser.

import { type LedgerEntry } from './api'
import { type I18n } from './i18n'

// ledgerKindLabel names an entry in the reader's terms. A wallet only ever sees
// three of the seven kinds, because holds, releases and charges happen to a
// project's pot, never to a person, so the rest fall through to a neutral label
// rather than leaking vocabulary nobody here needs.
export function ledgerKindLabel(t: I18n['t'], kind: LedgerEntry['kind']): string {
  if (kind === 'topup') return t('ledgerTopup')
  if (kind === 'allocate') return t('ledgerAllocate')
  if (kind === 'reclaim') return t('ledgerReclaim')
  return t('ledgerOther')
}

// ledgerLabel names one statement row. An allocate or reclaim that carries a
// project snapshot names it: a link while the project still exists, a plain
// name once it is purged. Every other kind, and a move with no project
// snapshot, falls back to the kind label.
export function ledgerLabel(t: I18n['t'], e: LedgerEntry): { kind: 'plain'; text: string } | { kind: 'project'; prefix: string; name: string; href?: string } {
  if ((e.kind === 'allocate' || e.kind === 'reclaim') && e.project_name) {
    return {
      kind: 'project',
      prefix: e.kind === 'allocate' ? t('ledgerAllocateTo') : t('ledgerReclaimFrom'),
      name: e.project_name,
      href: e.project_alive && e.project_id ? `/p/${e.project_id}` : undefined,
    }
  }
  // A card payment names its source, not "an admin": the actor distinguishes a
  // purchase, whose actor starts with stripe:, from an admin grant, and a
  // refund, stripe:refund:, from a manual correction.
  const byCard = e.actor.startsWith('stripe:')
  if (e.kind === 'topup') return { kind: 'plain', text: byCard ? t('ledgerCardTopup') : t('ledgerTopup') }
  if (e.kind === 'adjust' && byCard) return { kind: 'plain', text: t('ledgerRefund') }
  return { kind: 'plain', text: ledgerKindLabel(t, e.kind) }
}
