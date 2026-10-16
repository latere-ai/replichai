// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import type { PriceList } from './api'

// Which rows of the rate card follow the price list and which an admin owns.
//
// The server reads a price list every hour and writes its prices into the rows
// for the models it lists, so an edit to one of those rows lasts until the next
// read. The admin console shows those rows as the list's, not as fields an
// admin can change. A row is the list's when its model is one the server last
// applied, matched the way the server matches it: the row's provider-qualified
// ref, or an Anthropic model of the row's name for a row written before refs.

type Ref = { provider?: unknown; id?: unknown; billing_variant?: unknown }

const text = (v: unknown) => (typeof v === 'string' ? v.trim() : '')
const keyOf = (provider: string, id: string, variant: string) => `${provider}\u0000${id}\u0000${variant}`

// refKey is the identity the server matches a row by, or '' for a row that names
// no model.
export function refKey(row: { model?: unknown; model_ref?: unknown }): string {
  const ref = (row.model_ref && typeof row.model_ref === 'object' ? row.model_ref : {}) as Ref
  if (text(ref.provider) && text(ref.id)) return keyOf(String(ref.provider), String(ref.id), typeof ref.billing_variant === 'string' ? ref.billing_variant : '')
  const model = text(row.model)
  return model ? keyOf('anthropic', model, '') : ''
}

// listedKeys is the set of models the list priced when the server last applied
// it. Empty when the list is off or has not been applied since the server
// started, in which case every row is the admin's.
export function listedKeys(list: PriceList | undefined): Set<string> {
  return new Set((list?.models ?? []).map((m) => refKey({ model_ref: m })).filter(Boolean))
}

// followsList reports whether a row as the server sent it is one the list
// prices. A row the admin added has no server row behind it and is theirs.
export function followsList(row: { model?: unknown; model_ref?: unknown }, listed: Set<string>): boolean {
  const key = refKey(row)
  return key !== '' && listed.has(key)
}

// listName is what the console calls the list: OpenRouter for its public one,
// otherwise the host it is read from, or the address as the server gave it
// when it names no host, which is a misconfiguration worth seeing as typed.
export function listName(url: string | undefined): string {
  const given = (url ?? '').trim()
  let host = ''
  try {
    host = new URL(given).hostname.toLowerCase()
  } catch {
    return given
  }
  if (host === 'openrouter.ai' || host.endsWith('.openrouter.ai')) return 'OpenRouter'
  return host || given
}
