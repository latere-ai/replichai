// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { readdirSync, readFileSync, statSync } from 'fs'
import { join } from 'path'
import ts from 'typescript'

// Every user-facing string must route through t() (i18n.ts), so a locale added
// later translates the whole product rather than most of it. This is the
// enforcement the codebase had by discipline and now has by CI: a hardcoded
// string in a component fails here.
//
// It parses the real JSX with the TypeScript compiler rather than grepping, so a
// generic type (`Promise<Wallet>`) or a comment is never mistaken for a string.
// Two things count as user-facing: literal JSX text, and a literal value on an
// attribute that renders as text.

// brand.tsx holds the wordmark, which is not translated. Everything else is
// scanned.
const EXEMPT = new Set(['brand.tsx'])

// Literals that are not translatable text and so ride around t():
//   HAI     — the brand wordmark
//   GitHub  — a proper noun (the forge's own name)
//   /u/     — a fixed URL prefix shown before a handle (/u/ada-lovelace)
// Add to this set with a reason; do not weaken the rule to admit real copy.
const ALLOW = new Set(['HAI', 'GitHub', '/u/'])

// Attributes whose value a person reads. Others (className, href, id, role, …)
// are not text and are ignored.
const TEXT_ATTRS = new Set(['placeholder', 'aria-label', 'title', 'alt', 'data-tip'])

function tsxFiles(dir: string): string[] {
  const out: string[] = []
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) {
      out.push(...tsxFiles(p))
      continue
    }
    if (!name.endsWith('.tsx') || name.endsWith('.test.tsx') || EXEMPT.has(name)) continue
    out.push(p)
  }
  return out
}

const hasLetters = (s: string) => /[A-Za-z]/.test(s)

test('every user-facing string routes through t()', () => {
  const offenders: string[] = []
  for (const file of tsxFiles(import.meta.dir)) {
    const rel = file.slice(import.meta.dir.length + 1)
    const src = readFileSync(file, 'utf8')
    const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)

    const visit = (node: ts.Node) => {
      // Literal text between JSX tags. An interpolation ({t('…')}) is a
      // JsxExpression, not JsxText, so a translated node produces nothing here.
      if (ts.isJsxText(node)) {
        const text = node.text.trim()
        if (hasLetters(text) && !ALLOW.has(text)) offenders.push(`${rel}: JSX text ${JSON.stringify(text)}`)
      }
      // A text-rendering attribute set to a string literal (aria-label="Save"),
      // rather than to {t('save')}.
      if (ts.isJsxAttribute(node) && node.initializer && ts.isStringLiteral(node.initializer)) {
        const name = node.name.getText(sf)
        const value = node.initializer.text.trim()
        // An aria-label is always read aloud, so a literal one is always
        // copy — even a "+" or a "−", which a screen reader says as a word
        // and the letter test above would wave through.
        const text = name === 'aria-label' ? value !== '' : hasLetters(value)
        if (TEXT_ATTRS.has(name) && text && !ALLOW.has(value)) {
          offenders.push(`${rel}: ${name}=${JSON.stringify(value)}`)
        }
      }
      ts.forEachChild(node, visit)
    }
    visit(sf)
  }
  expect(offenders).toEqual([])
})
