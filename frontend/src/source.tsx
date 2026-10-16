// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import type { I18n } from './i18n'

// Where this app's source is. ReplicHAI is AGPL-3.0, and section 13 asks a
// service run over a network to offer every user the source of the version
// they are using. `published` turns the offer on. It stays false while the
// repository is private, because a link to a private repository answers 404
// to everybody outside it.
export const SOURCE = { url: 'https://github.com/latere-ai/replichai', published: false } as const

// SourceLink is the offer, wherever the app makes it: under the lockup at the
// head of the landing's footer, and at the foot of the console's rail. It
// renders nothing while the repository is private.
export function SourceLink({ i18n, source = SOURCE, className }: { i18n: I18n; source?: { url: string; published: boolean }; className?: string }) {
  if (!source.published) return null
  return (
    <a className={className ? `source-link ${className}` : 'source-link'} data-test="source-link" href={source.url} target="_blank" rel="noreferrer">
      {i18n.t('sourceCode')}
    </a>
  )
}
