// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Artifact rendering: markdown → sanitized HTML, code → syntax-highlighted,
// images → served inline. Content comes from repositories (including public
// ones), so every rendered HTML path is sanitized before it reaches the DOM.
import DOMPurify from 'dompurify'
import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import markdown from 'highlight.js/lib/languages/markdown'
import plaintext from 'highlight.js/lib/languages/plaintext'
import python from 'highlight.js/lib/languages/python'
import r from 'highlight.js/lib/languages/r'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'
import { marked } from 'marked'

hljs.registerLanguage('bash', bash)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('json', json)
hljs.registerLanguage('markdown', markdown)
hljs.registerLanguage('plaintext', plaintext)
hljs.registerLanguage('python', python)
hljs.registerLanguage('r', r)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('yaml', yaml)

type FileKind = 'markdown' | 'image' | 'code' | 'text'

const imageExt = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'])
const langByExt: Record<string, string> = {
  py: 'python',
  r: 'r',
  js: 'javascript',
  jsx: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  json: 'json',
  sh: 'bash',
  bash: 'bash',
  yml: 'yaml',
  yaml: 'yaml',
  md: 'markdown',
  markdown: 'markdown',
  html: 'xml',
  xml: 'xml',
  toml: 'plaintext',
  txt: 'plaintext',
}

function ext(path: string): string {
  const i = path.lastIndexOf('.')
  return i < 0 ? '' : path.slice(i + 1).toLowerCase()
}

export function fileKind(path: string): FileKind {
  const e = ext(path)
  if (e === 'md' || e === 'markdown') return 'markdown'
  if (imageExt.has(e)) return 'image'
  if (langByExt[e]) return 'code'
  return 'text'
}

export function langOf(path: string): string {
  return langByExt[ext(path)] ?? 'plaintext'
}

// renderMarkdown turns markdown into HTML and sanitizes it before it is trusted.
// Fenced code blocks are highlighted by highlightIn after mounting.
export function renderMarkdown(text: string): string {
  const raw = marked.parse(text, { async: false, gfm: true }) as string
  return DOMPurify.sanitize(raw)
}

// isExternalHref reports whether a link/src points outside the repository tree:
// an absolute or protocol-relative URL, a scheme (mailto:, tel:, data:), or a
// pure in-page anchor. Everything else is a repository-relative path.
export function isExternalHref(href: string): boolean {
  return /^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith('//') || href.startsWith('#')
}

// resolveRelative resolves a repository-relative href against the file it was
// written in, so a link in `docs/guide.md` to `../README.md` lands on
// `README.md`. Any query or fragment is dropped — it names a file, not a URL.
export function resolveRelative(fromPath: string, href: string): string {
  const clean = href.split('#')[0].split('?')[0]
  const dir = fromPath.includes('/') ? fromPath.slice(0, fromPath.lastIndexOf('/')) : ''
  const parts = dir ? dir.split('/') : []
  for (const seg of clean.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') parts.pop()
    else parts.push(seg)
  }
  return parts.join('/')
}

// highlightIn syntax-highlights every <pre><code> inside a mounted container —
// the fenced code in markdown, and the single block of a code file.
export function highlightIn(el: HTMLElement | null) {
  if (!el) return
  el.querySelectorAll('pre code').forEach((block) => {
    if (!(block as HTMLElement).dataset.hl) {
      hljs.highlightElement(block as HTMLElement)
      ;(block as HTMLElement).dataset.hl = '1'
    }
  })
}
