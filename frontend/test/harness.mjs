// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The one place the browser suite finds Chrome, starts Vite, and mocks the API.
//
// Every test used to carry its own copy of all three: forty lines of discovery
// and readiness polling, a hand-picked port that collided with another file's,
// and a request handler that matched paths by hand. The copies drifted — one
// answered a stream with an empty body while the server always sends a snapshot
// first, which is how a reconnect loop went unnoticed for as long as it did.
//
// A test now reads:
//
//   const h = await start()
//   const page = await h.page({ routes })
//   …
//   h.ok('what the test proved')
//   await h.close()
//
// `routes` is a table, first match wins. Each entry is `[matcher, answer]` or
// `[method, matcher, answer]`; the matcher is a path ('/me'), a path with its
// method ('POST /jobs/j/cancel'), or a RegExp over the path. The answer is a
// body (sent as JSON 200), a Reply built with json()/text()/noContent(), or a
// function of the request — `({ params: [id], body, query }) => …` — returning
// either, possibly late (delay()).
//
// A stream at /jobs/{id}/events that no route names is answered the way the
// server answers it: a snapshot of the job first, read through the table's own
// GET /jobs/{id}, then nothing more until the browser reconnects.
import { spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import { createRequire } from 'node:module'
import { createServer } from 'node:net'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
const frontend = resolve(dirname(fileURLToPath(import.meta.url)), '..')

/* ── Chrome ── */

const CHROMES = ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/usr/bin/google-chrome', '/usr/bin/chromium']

// findChrome honours PUPPETEER_EXECUTABLE_PATH, then looks in the usual places.
// A path that was asked for and does not exist is an error, not a skip: the
// person set it on purpose.
export function findChrome() {
  const asked = process.env.PUPPETEER_EXECUTABLE_PATH
  if (asked) {
    if (existsSync(asked)) return asked
    console.error(`FAIL: PUPPETEER_EXECUTABLE_PATH=${asked} does not exist`)
    process.exit(1)
  }
  return CHROMES.find((p) => existsSync(p)) ?? null
}

/* ── Reporting ── */

// fail records a failure and lets the test carry on, so one run reports every
// broken measurement rather than the first.
export function fail(msg) {
  console.error(`FAIL: ${msg instanceof Error ? msg.stack || msg.message : msg}`)
  process.exitCode = 1
}

// check is fail with the condition in front of it.
export function check(cond, msg) {
  if (!cond) fail(msg)
  return !!cond
}

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// waitUntil polls a condition on the node side — a request counter, a mock's
// state — until it holds or the time is up. Its message says what it waited for.
export async function waitUntil(cond, { timeout = 5000, every = 25, what = 'condition' } = {}) {
  const until = Date.now() + timeout
  for (;;) {
    const v = await cond()
    if (v) return v
    if (Date.now() > until) throw new Error(`timed out after ${timeout}ms waiting for ${what}`)
    await sleep(every)
  }
}

// settled waits for every finite animation on the page to finish. A screen's
// entrance fade is the usual reason a measurement has to wait; a pulse that
// never ends is not waited for.
export async function settled(page) {
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((a) => a.effect?.getTiming().iterations !== Infinity)
        .map((a) => a.finished.catch(() => {})),
    ),
  )
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))))
}

// waitForText waits until the element at `selector` contains `text`.
export function waitForText(page, selector, text, opts = {}) {
  return page.waitForFunction((s, t) => (document.querySelector(s)?.textContent ?? '').includes(t), opts, selector, text)
}

// textOf reads the trimmed text of one element, or null when it is not there.
export const textOf = (page, selector) => page.evaluate((s) => document.querySelector(s)?.textContent.trim() ?? null, selector)

/* ── Answers ── */

// Reply is a response the mock sends as it is: a status, a body, a type.
export class Reply {
  constructor(status, body, contentType) {
    this.status = status
    this.body = body
    this.contentType = contentType
  }
}

export const json = (body, status = 200) => new Reply(status, JSON.stringify(body), 'application/json')
export const text = (body, status = 200, contentType = 'text/plain') => new Reply(status, body, contentType)
export const noContent = () => new Reply(204, '', undefined)
export const delay = (ms, value) => new Promise((r) => setTimeout(() => r(value), ms))

// frame is one SSE status event as internal/httpapi/events.go writes it.
export const frame = (job, id = 1) => `id: ${id}\nevent: status\ndata: ${JSON.stringify(job)}\n\n`

// stream is an SSE body: the reconnect interval, then a frame per job in order.
// The interval is long by default so a test sees one connection per open,
// exactly as against a server that holds the socket; a test that wants the
// browser to come back quickly asks for it.
export function stream(jobs, { retry = 30_000 } = {}) {
  return `retry: ${retry}\n\n` + jobs.map((j, i) => frame(j, i + 1)).join('')
}

export const sse = (jobs, opts) => new Reply(200, stream(jobs, opts), 'text/event-stream')

/* ── Routing ── */

const METHODS = /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS) (.+)$/

// compile turns one table entry into a matcher over (method, path).
function compile(entry) {
  let [method, matcher, answer] = entry.length === 3 ? entry : [undefined, entry[0], entry[1]]
  if (typeof matcher === 'string') {
    const m = METHODS.exec(matcher)
    if (m) [, method, matcher] = m
  }
  const test =
    matcher instanceof RegExp
      ? (path) => matcher.exec(path)
      : (path) => (path === matcher ? [path] : null)
  return { method: method?.toUpperCase(), test, answer }
}

// mock installs request interception for one page. It returns the log of API
// calls the page made, as `METHOD /path?query` strings, and the page errors it
// threw, so a test can assert on either.
async function mock(page, base, { routes = [], fallback, sse: sseOpts = {} } = {}) {
  const table = routes.map(compile)
  const calls = []
  const pageErrors = []
  page.on('pageerror', (e) => pageErrors.push(e.message))

  const lookup = (method, path) => {
    for (const r of table) {
      if (r.method && r.method !== method) continue
      const m = r.test(path)
      if (m) return { m, answer: r.answer }
    }
    return null
  }

  const send = async (req, reply) => {
    try {
      if (reply instanceof Reply) {
        await req.respond({ status: reply.status, contentType: reply.contentType, body: reply.body })
      } else {
        await req.respond({ status: 200, contentType: 'application/json', body: JSON.stringify(reply) })
      }
    } catch {
      /* the page navigated away before the answer landed */
    }
  }

  await page.setRequestInterception(true)
  page.on('request', async (req) => {
    const u = new URL(req.url())
    const method = req.method()
    const type = req.resourceType()
    const api = type === 'fetch' || type === 'xhr' || type === 'eventsource'
    if (u.origin !== base) return req.continue()

    const hit = lookup(method, u.pathname)
    if (api || hit) calls.push(`${method} ${u.pathname}${u.search}`)
    const r = {
      method,
      path: u.pathname,
      query: u.searchParams,
      params: hit ? hit.m.slice(1) : [],
      url: u,
      raw: req,
      get body() {
        const d = req.postData()
        if (!d) return undefined
        try {
          return JSON.parse(d)
        } catch {
          return d
        }
      },
    }
    if (hit) {
      const v = typeof hit.answer === 'function' ? await hit.answer(r) : hit.answer
      return send(req, v)
    }

    // A stream the table does not name: snapshot first, as the server does.
    const ev = /^\/jobs\/([^/]+)\/events$/.exec(u.pathname)
    if (ev) {
      let job = sseOpts.jobOf?.(ev[1])
      if (job === undefined) {
        const snap = lookup('GET', `/jobs/${ev[1]}`)
        if (snap) {
          const v = typeof snap.answer === 'function' ? await snap.answer({ ...r, params: snap.m.slice(1) }) : snap.answer
          if (v instanceof Reply) {
            if (v.status === 200 && v.contentType === 'application/json') job = JSON.parse(v.body)
          } else job = v
        }
      }
      return send(req, sse(job ? [job] : [], sseOpts))
    }

    if (fallback) {
      const v = await fallback(r)
      if (v !== undefined) return send(req, v)
    }
    return req.continue()
  })
  return { calls, pageErrors }
}

/* ── Vite ── */

function freePort() {
  return new Promise((res, rej) => {
    const s = createServer()
    s.on('error', rej)
    s.listen(0, '127.0.0.1', () => {
      const { port } = s.address()
      s.close(() => res(port))
    })
  })
}

// startVite serves the app on a free port, unless a runner already serves it
// and says so in WEB_TEST_BASE — the whole suite then shares one server.
export async function startVite() {
  if (process.env.WEB_TEST_BASE) return { base: process.env.WEB_TEST_BASE, stop() {} }
  const port = await freePort()
  const base = `http://localhost:${port}`
  const proc = spawn('npx', ['vite', '--port', String(port), '--strictPort'], { stdio: 'ignore', cwd: frontend })
  try {
    await waitUntil(() => fetch(base).then((r) => r.ok, () => false), { timeout: 60_000, every: 250, what: 'vite to start' })
  } catch (e) {
    proc.kill('SIGTERM')
    throw e
  }
  return { base, stop: () => proc.kill('SIGTERM') }
}

/* ── The harness ── */

export const DESKTOP = { width: 1440, height: 900 }

// start brings up the server and the browser, or ends the process with a skip
// when there is no Chrome to drive. What it returns opens pages against the
// mocked API and closes everything at the end.
export async function start() {
  const chrome = findChrome()
  if (!chrome) {
    console.log('skip: no Chrome found (set PUPPETEER_EXECUTABLE_PATH)')
    process.exit(0)
  }
  const vite = await startVite()
  const puppeteer = (await import(require.resolve('puppeteer-core'))).default
  const browser = await puppeteer.launch({ executablePath: chrome, headless: true })
  return {
    base: vite.base,
    browser,
    // page opens a tab against the mocked API. Everything about the mock is per
    // page, so a test that wants a different server for its second act opens a
    // second page.
    async page({ viewport = DESKTOP, ...mocking } = {}) {
      const page = await browser.newPage()
      await page.setViewport(viewport)
      const { calls, pageErrors } = await mock(page, vite.base, mocking)
      page.calls = calls
      page.pageErrors = pageErrors
      page.open = (path, opts) => page.goto(vite.base + path, { waitUntil: 'domcontentloaded', ...opts })
      return page
    },
    // ok prints the one line a green run leaves, unless something failed first.
    ok(what) {
      if (!process.exitCode) console.log(`ok: ${what}`)
    },
    async close() {
      await browser.close().catch(() => {})
      vite.stop()
    },
  }
}
