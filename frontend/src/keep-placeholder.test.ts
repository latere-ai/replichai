// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { expect, test } from 'bun:test'
import { existsSync, mkdtempSync, readFileSync, rmSync } from 'fs'
import { tmpdir } from 'os'
import { join } from 'path'
import { body, keepPlaceholder, PLACEHOLDER, restorePlaceholder } from '../scripts/keep-placeholder.mjs'

// The build empties internal/spa/dist and has to put PLACEHOLDER back, or a
// clean checkout has nothing for go:embed to match and `go build` fails for
// everyone who pulls. The restore used to be a step after `vite build` in
// package.json, so a build that failed while writing, after the directory was
// emptied, skipped it. It is a plugin hook now, one Vite calls in its own
// finally, and this pins both halves: the restore writes the committed file
// byte for byte, and the plugin runs it at closeBundle, for builds only.

test('the restore writes the placeholder, and the directory it lives in, back', () => {
  const dir = mkdtempSync(join(tmpdir(), 'spa-dist-'))
  try {
    const path = join(dir, 'nested', 'PLACEHOLDER')
    expect(existsSync(path)).toBe(false)
    restorePlaceholder(path)
    expect(readFileSync(path, 'utf8')).toBe(body)
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
})

test('the placeholder body is the committed file, byte for byte', () => {
  // The default path points at the tracked file; in a checkout it is there and
  // it says what the restore would write. A drift between the two shows up here
  // rather than as a spurious modification after every build.
  if (existsSync(PLACEHOLDER)) expect(readFileSync(PLACEHOLDER, 'utf8')).toBe(body)
})

test('the plugin restores at closeBundle, for builds only', () => {
  const plugin = keepPlaceholder()
  expect(plugin.name).toBe('keep-placeholder')
  expect(plugin.apply).toBe('build')
  expect(typeof plugin.closeBundle).toBe('function')
})
