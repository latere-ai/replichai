// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser test for what a failed session says about its failure.
//
// It guards that a failure report is readable whole. A runner that panics
// records its whole stack trace, which no tooltip can hold, so the session
// view shows the report itself: the first line as selectable text, the rest
// behind a disclosure in a box of bounded height. The row's tooltip quotes only
// the first line and points to the session.
//
// What needs a browser is the geometry: whether the tooltip stays on screen,
// whether a long trace stays inside its box, and whether the note fits a phone.
// Run it with `bun run test:browser`.
import { fail, settled, start } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()

// What the worker records when a runner panics: the step, the panic, and the
// stack under it (internal/job/worker.go).
const FIRST = 'resolve: runner panicked: runtime error: invalid memory address or nil pointer dereference'
const stack = ['goroutine 87 [running]:', 'runtime/debug.Stack()', '\t/usr/local/go/src/runtime/debug/stack.go:26 +0x5e']
for (let i = 0; i < 30; i++) stack.push(`latere.ai/replichai/internal/github.(*App).call${i}(...)`, `\t/src/internal/github/app.go:${100 + i} +0x${(i * 37).toString(16)}`)
const PANIC = `${FIRST}\n${stack.join('\n')}`

const failed = {
  id: 'job-1',
  kind: 'replicate',
  ref: 'https://dl.acm.org/doi/10.1145/3544548.3581433',
  phase: 'failed',
  error: PANIC,
  created_at: at(0),
  phase_at: { submitted: at(0), resolving: at(1000), failed: at(2000) },
}
const stopped = { ...failed, id: 'job-2', error: 'cancelled by owner' }
const project = { id: 'proj-1', owner: 'p', title: 'Walk This Beam', visibility: 'private', created_at: at(0) }

const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'user', credits: { wallet: 20, start_reserve: 5 } }],
  ['/me/profile', {}],
  [/^\/(projects|public|shared)$/, []],
  ['/projects/proj-1', { project, sessions: [failed, stopped], may_submit: true, role: 'owner' }],
  ['/jobs/job-1', failed],
  ['/jobs/job-2', stopped],
  [/^\/jobs\/job-[12]\/transcript$/, { events: [], usage: null, live: false }],
]

const h = await start()
try {
  /* ── The row's tooltip is the first line, and stays on screen ── */

  {
    const page = await h.page({ routes })
    await page.open('/p/proj-1')
    await page.waitForSelector('.rows .row .err')
    await settled(page)
    await page.hover('.rows .row .err')
    await page.waitForFunction(() => document.querySelector('.tip-bubble')?.style.visibility !== 'hidden', { timeout: 5000 })
    const tip = await page.evaluate(() => {
      const b = document.querySelector('.tip-bubble')
      const r = b.getBoundingClientRect()
      return { text: b.textContent, top: r.top, bottom: r.bottom, height: r.height, vh: window.innerHeight }
    })
    if (!tip.text.includes(FIRST)) fail(`the row's tooltip reads "${tip.text}"; it must quote the run's first line`)
    if (/goroutine|app\.go/.test(tip.text)) fail('the row\'s tooltip still carries the stack trace')
    if (!/open the session/i.test(tip.text)) fail(`the row's tooltip does not say where the rest is: "${tip.text}"`)
    if (tip.top < 0 || tip.bottom > tip.vh) fail(`the row's tooltip runs off screen: ${tip.top}px to ${tip.bottom}px of ${tip.vh}px`)
    if (tip.height > 120) fail(`the row's tooltip is ${tip.height}px tall; a glance is a few lines`)
    await page.close()
  }

  /* ── The session shows what the run reported ── */

  {
    const page = await h.page({ routes })
    await page.open('/p/proj-1/s/job-1')
    await page.waitForSelector('[data-test="session-failure"]', { timeout: 15000 })
    await settled(page)
    const note = await page.evaluate(() => ({
      cause: document.querySelector('[data-test="failure-cause"]')?.textContent,
      detail: !!document.querySelector('[data-test="failure-detail"]'),
      select: getComputedStyle(document.querySelector('[data-test="failure-cause"]')).userSelect,
    }))
    if (note.cause !== FIRST) fail(`the session's failure reads "${note.cause}", want the run's first line`)
    if (note.detail) fail('the stack trace is open before the reader asks for it')
    if (note.select === 'none') fail('the failure cannot be selected, so it cannot be copied')
    // The class is read from the first line; the stack under a panic names the
    // GitHub client, and must not make this read as a failure to publish.
    const head = await page.$eval('[data-test="session-failure"] .fail-head', (el) => el.textContent.trim())
    if (/github/i.test(head)) fail(`a panic in the resolve step reads as "${head}"`)

    // The cause can be selected and copied as it reads.
    const copied = await page.evaluate(() => {
      const el = document.querySelector('[data-test="failure-cause"]')
      const range = document.createRange()
      range.selectNodeContents(el)
      const sel = window.getSelection()
      sel.removeAllRanges()
      sel.addRange(range)
      return sel.toString()
    })
    if (copied !== FIRST) fail(`selecting the failure copies "${copied}"`)

    await page.click('[data-test="session-failure"] .disc-head')
    await page.waitForSelector('[data-test="failure-detail"]')
    const box = await page.evaluate(() => {
      const pre = document.querySelector('[data-test="failure-detail"]')
      const note = document.querySelector('[data-test="session-failure"]')
      return {
        text: pre.textContent,
        scrolls: pre.scrollHeight > pre.clientHeight,
        height: pre.getBoundingClientRect().height,
        noteBottom: note.getBoundingClientRect().bottom,
        vh: window.innerHeight,
      }
    })
    if (!box.text.startsWith('goroutine 87 [running]:') || !box.text.includes('app.go:129')) fail('the rest of the report is not the stack the run recorded')
    if (box.text.includes(FIRST)) fail('the first line is repeated inside the rest of the report')
    if (!box.scrolls) fail('a long stack trace is not kept in a scrolling box')
    if (box.height > box.vh * 0.35 + 1) fail(`the stack trace box is ${box.height}px tall, more than 35% of a ${box.vh}px window`)
    if (box.noteBottom > box.vh) fail(`opening the stack trace pushed the note past the window (${box.noteBottom}px of ${box.vh}px)`)

    // The note belongs to the session, not to one pane of it.
    await page.click('.detail-tabs button:nth-child(2)')
    await page.waitForFunction(() => location.pathname.endsWith('/verdict'))
    if (!(await page.$('[data-test="session-failure"]'))) fail('the failure is not shown on the verdict pane')
    await page.close()
  }

  /* ── A session stopped by hand reports no fault ── */

  {
    const page = await h.page({ routes })
    await page.open('/p/proj-1/s/job-2')
    await page.waitForSelector('.session-ref')
    await page.waitForFunction(() => document.querySelector('.session-ref')?.textContent !== '…')
    await settled(page)
    if (await page.$('[data-test="session-failure"]')) fail('a session its owner stopped is shown as a failure')
    await page.close()
  }

  /* ── The note fits a phone ── */

  {
    const page = await h.page({ routes, viewport: { width: 390, height: 844, isMobile: true, hasTouch: true } })
    await page.open('/p/proj-1/s/job-1')
    await page.waitForSelector('[data-test="session-failure"]', { timeout: 15000 })
    await settled(page)
    await page.click('[data-test="session-failure"] .disc-head')
    await page.waitForSelector('[data-test="failure-detail"]')
    const spill = await page.evaluate(() => {
      const note = document.querySelector('[data-test="session-failure"]')
      return { doc: document.documentElement.scrollWidth - window.innerWidth, right: note.getBoundingClientRect().right - window.innerWidth }
    })
    if (spill.doc > 1 || spill.right > 1) fail(`on a phone the failure spills: ${JSON.stringify(spill)}`)
    await page.close()
  }

  h.ok("a failed session shows what the run reported, and the row's tooltip stays a short line on screen")
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
