// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Runs the browser suite: every test/*.test.mjs, against one Vite server, a few
// at a time, and reports every failure rather than the first.
//
// It replaces a `&&` chain in package.json that stopped at the first failing
// file and had to be edited to admit a new one. A file is in the suite when it
// is in the directory.
//
//   node test/run.mjs               # everything
//   node test/run.mjs session       # files whose name contains "session"
//   JOBS=1 node test/run.mjs        # one at a time
import { spawn } from 'node:child_process'
import { readdirSync } from 'node:fs'
import { availableParallelism } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { findChrome, startVite } from './harness.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const filters = process.argv.slice(2)
const files = readdirSync(here)
  .filter((f) => f.endsWith('.test.mjs'))
  .filter((f) => !filters.length || filters.some((s) => f.includes(s)))
  .sort()

if (!files.length) {
  console.error(`FAIL: no test matches ${filters.join(' ')}`)
  process.exit(1)
}
if (!findChrome()) {
  console.log('skip: no Chrome found (set PUPPETEER_EXECUTABLE_PATH)')
  process.exit(0)
}

const jobs = Math.max(1, Number(process.env.JOBS) || Math.min(4, Math.ceil(availableParallelism() / 2)))
const vite = await startVite()

// one runs a file to completion and keeps its output for the report.
function one(file) {
  return new Promise((res) => {
    const began = Date.now()
    const out = []
    const p = spawn(process.execPath, [join(here, file)], { env: { ...process.env, WEB_TEST_BASE: vite.base }, stdio: ['ignore', 'pipe', 'pipe'] })
    p.stdout.on('data', (d) => out.push(d))
    p.stderr.on('data', (d) => out.push(d))
    p.on('close', (code) => res({ file, code, output: Buffer.concat(out).toString(), ms: Date.now() - began }))
  })
}

const results = []
const queue = [...files]
await Promise.all(
  Array.from({ length: Math.min(jobs, queue.length) }, async () => {
    while (queue.length) {
      const r = await one(queue.shift())
      results.push(r)
      const state = r.code === 0 ? (r.output.startsWith('skip') ? 'skip' : 'ok  ') : 'FAIL'
      console.log(`${state} ${r.file} (${(r.ms / 1000).toFixed(1)}s)`)
    }
  }),
)
vite.stop()

const failed = results.filter((r) => r.code !== 0)
for (const r of failed) {
  console.log(`\n── ${r.file} ──`)
  console.log(r.output.trimEnd())
}
console.log(`\n${results.length - failed.length} passed, ${failed.length} failed`)
process.exit(failed.length ? 1 : 0)
