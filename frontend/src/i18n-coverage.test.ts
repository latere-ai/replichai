// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { readdirSync, readFileSync, statSync } from 'fs'
import { join } from 'path'
import ts from 'typescript'

// Every user-facing string must route through t() (i18n.ts), so a locale added
// later translates the whole product rather than most of it. A hardcoded string
// in a component fails here.
//
// It parses the real source with the TypeScript compiler rather than grepping,
// so a generic type (`Promise<Wallet>`) or a comment is never mistaken for a
// string. Three things count as user-facing: literal JSX text, a literal value
// on an attribute that renders as text, and, in the plain .ts modules that
// format what the screens show, a string literal that reads as a phrase, or a
// date, time or number formatted in the browser's locale rather than the app's.

// brand.tsx holds the wordmark, which is not translated. i18n.ts is the strings
// themselves.
const EXEMPT = new Set(['brand.tsx', 'i18n.ts'])

// Literals that are not translatable text and so ride around t():
//   HAI: the brand wordmark
//   GitHub: a proper noun, the forge's own name
//   /u/: a fixed URL prefix shown before a handle, as in /u/ada-lovelace
//   pre code: a CSS selector, not a sentence
// Add to this set with a reason; do not weaken the rule to admit real copy.
const ALLOW = new Set(['HAI', 'GitHub', '/u/', 'pre code'])

// Attributes whose value a person reads. Others, such as className, href, id
// and role, are not text and are ignored.
const TEXT_ATTRS = new Set(['placeholder', 'aria-label', 'title', 'alt', 'data-tip'])

// The formatters that follow a locale. Called with none, or with `undefined`
// or `[]`, which mean the browser's, they render a date in the reader's OS
// language rather than the one the app is showing.
const LOCALE_FORMATTERS = new Set(['toLocaleString', 'toLocaleDateString', 'toLocaleTimeString'])

function sourceFiles(dir: string): string[] {
  const out: string[] = []
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) {
      out.push(...sourceFiles(p))
      continue
    }
    if (!/\.tsx?$/.test(name) || /\.test\.tsx?$/.test(name) || EXEMPT.has(name)) continue
    out.push(p)
  }
  return out
}

const hasLetters = (s: string) => /[A-Za-z]/.test(s)
// A phrase: two or more words of letters with a space between. That is the
// shape of copy, which an identifier, a path, a wire code or a CSS class never
// has.
const isPhrase = (s: string) => /[A-Za-z]{2,}\s+[A-Za-z]{2,}/.test(s)

// followsBrowserLocale reports a locale formatter called without a language.
function followsBrowserLocale(node: ts.CallExpression): boolean {
  if (!ts.isPropertyAccessExpression(node.expression)) return false
  if (!LOCALE_FORMATTERS.has(node.expression.name.text)) return false
  const first = node.arguments[0]
  if (!first) return true
  if (first.kind === ts.SyntaxKind.UndefinedKeyword) return true
  if (ts.isIdentifier(first) && first.text === 'undefined') return true
  return ts.isArrayLiteralExpression(first) && first.elements.length === 0
}

test('every user-facing string routes through t()', () => {
  const offenders: string[] = []
  for (const file of sourceFiles(import.meta.dir)) {
    const rel = file.slice(import.meta.dir.length + 1)
    const tsx = file.endsWith('.tsx')
    const src = readFileSync(file, 'utf8')
    const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, tsx ? ts.ScriptKind.TSX : ts.ScriptKind.TS)

    const visit = (node: ts.Node) => {
      // Literal text between JSX tags. An interpolation such as {t('…')} is a
      // JsxExpression, not JsxText, so a translated node produces nothing here.
      if (ts.isJsxText(node)) {
        const text = node.text.trim()
        if (hasLetters(text) && !ALLOW.has(text)) offenders.push(`${rel}: JSX text ${JSON.stringify(text)}`)
      }
      // A text-rendering attribute set to a string literal, such as
      // aria-label="Save", rather than to {t('save')}.
      if (ts.isJsxAttribute(node) && node.initializer && ts.isStringLiteral(node.initializer)) {
        const name = node.name.getText(sf)
        const value = node.initializer.text.trim()
        // An aria-label is always read aloud, so a literal one is always
        // copy, even a "+" or a "−", which a screen reader says as a word
        // and the letter test above would pass.
        const text = name === 'aria-label' ? value !== '' : hasLetters(value)
        if (TEXT_ATTRS.has(name) && text && !ALLOW.has(value)) {
          offenders.push(`${rel}: ${name}=${JSON.stringify(value)}`)
        }
      }
      // In a .ts module there is no JSX to read, so the tell is the shape of the
      // string: a phrase is copy wherever it is built. A formatter that follows
      // the browser's locale is an offender in either kind of file.
      if (!tsx && (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node))) {
        const value = node.text.trim()
        if (isPhrase(value) && !ALLOW.has(value)) offenders.push(`${rel}: string ${JSON.stringify(value)}`)
      }
      if (ts.isCallExpression(node) && followsBrowserLocale(node)) {
        const { line } = sf.getLineAndCharacterOfPosition(node.getStart(sf))
        offenders.push(`${rel}:${line + 1}: ${node.getText(sf)} follows the browser's locale, not the app's`)
      }
      ts.forEachChild(node, visit)
    }
    visit(sf)
  }
  expect(offenders).toEqual([])
})
