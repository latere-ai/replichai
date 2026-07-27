import { expect, test } from 'bun:test'
import { isExternalHref, resolveRelative } from './artifact'

test('resolveRelative resolves against the file the link is written in', () => {
  // A root-level README linking a sibling — the reported bug.
  expect(resolveRelative('README.md', 'REPRODUCIBILITY.md')).toBe('REPRODUCIBILITY.md')
  // Explicit current-dir and nested paths.
  expect(resolveRelative('README.md', './src/run.py')).toBe('src/run.py')
  expect(resolveRelative('docs/guide.md', 'api.md')).toBe('docs/api.md')
  // Parent traversal climbs out of the file's directory.
  expect(resolveRelative('docs/guide.md', '../README.md')).toBe('README.md')
  expect(resolveRelative('a/b/c.md', '../../top.md')).toBe('top.md')
  // A fragment or query names a file, not a URL — it is dropped.
  expect(resolveRelative('README.md', 'NOTES.md#section')).toBe('NOTES.md')
})

test('isExternalHref distinguishes repo paths from links that leave the tree', () => {
  // External: absolute, protocol-relative, schemes, in-page anchors.
  for (const h of ['https://x.com', 'http://x', '//cdn/x', 'mailto:a@b.c', 'tel:1', 'data:x', '#top']) {
    expect(isExternalHref(h)).toBe(true)
  }
  // Repository-relative: intercepted and followed in-app.
  for (const h of ['README.md', './a.md', '../b.md', 'src/run.py', 'results/plot.png']) {
    expect(isExternalHref(h)).toBe(false)
  }
})
