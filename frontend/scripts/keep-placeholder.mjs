// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Vite empties internal/spa/dist/ on every build, and that directory holds one
// tracked file: PLACEHOLDER, kept alive by a `!` negation against a gitignored
// dist/*. So a plain `bun run build` deletes a tracked file, and because the
// deletion is in a path a developer really did touch, `git add` stages it
// without complaint.
//
// What it costs is out of proportion to how easy it is to miss: PLACEHOLDER is
// the only thing `//go:embed all:dist` can match in a clean checkout, so losing
// it stops internal/spa compiling, which takes down go vet, go build and go
// test at once for everyone who pulls. internal/spa's TestEmbedSurvivesCleanCheckout
// catches it, but only after it has been committed and pushed.
//
// Restoring it makes the build put back what it removed, so the guard stops
// depending on somebody noticing a deletion in `git status`. It is a Vite
// plugin rather than a step after `vite build` in package.json, because that
// step ran only when the build succeeded: Vite empties the directory just
// before it writes, so a build that failed while writing left dist empty and
// `go build` broken. closeBundle runs in Vite's own finally, whichever way the
// build ended.
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))

// PLACEHOLDER is where the file lives, relative to this script.
export const PLACEHOLDER = resolve(here, '../../internal/spa/dist/PLACEHOLDER')

// Kept byte-for-byte in step with the committed file: a differing body would
// show up as a spurious modification in every build.
export const body = `This file exists so \`//go:embed all:dist\` in spa.go has something to match in a
clean checkout, where the real frontend build output (index.html, assets/) is
gitignored. The release image replaces this directory with the actual
\`bun run build\` output before compiling, so the placeholder never ships.
`

// restorePlaceholder writes the file, and the directory it lives in, back.
export function restorePlaceholder(path = PLACEHOLDER) {
  mkdirSync(dirname(path), { recursive: true })
  writeFileSync(path, body)
}

// keepPlaceholder is the Vite plugin: the restore, at the end of every build,
// succeeded or not.
export function keepPlaceholder() {
  return {
    name: 'keep-placeholder',
    apply: 'build',
    closeBundle() {
      restorePlaceholder()
    },
  }
}
