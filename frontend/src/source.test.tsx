// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { renderToStaticMarkup } from 'react-dom/server'

import { translator } from './i18n'
import { SOURCE, SourceLink } from './source'

const i18n = { ...translator('en'), lang: 'en' } as never

// Off, the offer is not on the page at all: a link to a private repository
// would answer 404 to the very readers it is offered to.
test('the source link renders nothing while the repository is private', () => {
  expect(renderToStaticMarkup(<SourceLink i18n={i18n} source={{ url: SOURCE.url, published: false }} />)).toBe('')
})

// On, it is a plain link to the repository, opened beside the app.
test('the source link points at the repository once it is published', () => {
  const html = renderToStaticMarkup(<SourceLink i18n={i18n} source={{ url: SOURCE.url, published: true }} />)
  expect(html).toContain(`href="${SOURCE.url}"`)
  expect(html).toContain('Source code')
  expect(html).toContain('rel="noreferrer"')
})
