// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for the app on a phone (030).
//
// What it guards: `.shell` is a flex row and `.rail` was `width: 236px;
// flex: none` with no shell-level media query anywhere in the stylesheet, so a
// 390px phone got 154px of content on every screen the app has. The topbar's
// controls ran to x=579 and could not be tapped, and body copy set three words
// to the line.
//
// None of that tripped the usual check. `.screen` is `overflow-y: auto`, which
// computes `overflow-x` to `auto` as well, so the spill was contained in a pane
// that scrolls sideways rather than in the document, and `documentElement.
// scrollWidth` read clean while nothing was legible. This test therefore checks
// both: nothing past the right edge, and no pane scrolling sideways to hide it.
//
// The drawer is the other half. Whether it is open is state, and whether the
// rail is folded is a stored preference; `sidebar.ts` states that a width the
// operator chose is never overridden by the window's width. The two are
// asserted apart: the drawer must not read or write `sidebar`, and a stored
// fold must not carry into a drawer that has room for its labels.
//
// It drives Chrome over a Vite dev server with the API mocked. Run it with
// `bun run test:browser`.
import { check, DESKTOP, fail, settled, start, text } from './harness.mjs'

const PHONE = { width: 390, height: 844, isMobile: true, hasTouch: true }
// 360 is the common Android width, and the only one that reaches the drawer's
// `max-width: 84vw` clamp.
const NARROW = { width: 360, height: 780, isMobile: true, hasTouch: true }

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()

const PROJECT = 'proj-1'
// A second project, private and owned by the reader, so the head carries the
// publish button, the sibling that took the title's width away from it.
const PRIVATE = 'proj-private'
// 122 characters. A paper's name is the longest string this app puts in a
// heading, and the head has to hold one without pushing the tabs off a phone.
const LONG_TITLE = 'Effects of Semantic Segmentation Visualization on Trust, Situation Awareness, and Cognitive Load in Highly Automated Vehicles'
// A description of the length the field invites: two sentences.
const DESCRIPTION =
  'Two papers on whether a segmentation overlay changes what a driver trusts, and one replication of each. Both were run on the same instrument.'
const JOB = 'job-a'
const HANDLE = 'ada'

// Deliberately long: a title, an institution and an address that all fit at
// 1440 and none of which fit at 390 are what this test is about.
const TITLE = 'Retention Under Load: A Within-Subjects Study of Prolonged Attention'
const ADDRESS = 'a-rather-long-address@university-of-somewhere.example.edu'

const project = { id: PROJECT, owner: 'p', title: TITLE, visibility: 'public', created_at: at(0), modified_at: at(9000) }
const listed = { ...project, session_count: 3, running: 1, latest_phase: 'running' }
const job = {
  id: JOB,
  kind: 'replicate',
  ref: 'arxiv:2301.03711',
  phase: 'done',
  paper_key: 'pap-a',
  repo_name: 'open-hai/retention-under-load',
  created_at: at(0),
  phase_at: { submitted: at(0), done: at(9000) },
}

const SIGNED_IN = { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { wallet: 20, start_reserve: 5 } }
const SIGNED_OUT = { authenticated: false }

const routes = [
  ['/me/profile', { handle: HANDLE, display_name: 'Ada Lovelace', institution: 'University of Somewhere', contact_email: ADDRESS }],
  [
    '/me/credits',
    {
      balance: 12_500_000,
      spread_bps: 500,
      spread_fixed_micro: 300_000,
      min_topup_micro: 5_000_000,
      entries: [
        { id: 'e1', holder: 'p', amount: 20_000_000, kind: 'topup', actor: 'p', created_at: at(0) },
        { id: 'e2', holder: 'p', amount: -5_000_000, kind: 'allocate', project_id: PROJECT, project_name: TITLE, project_alive: true, actor: 'p', created_at: at(1000) },
      ],
    },
  ],
  ['/projects', [listed]],
  ['/projects/trash', [{ ...listed, id: 'proj-2', running: 0, latest_phase: 'done' }]],
  [/^\/projects\/[^/]+\/papers$/, [{ key: 'pap-a', ref: job.ref, title: TITLE, sessions: 1 }]],
  [/^\/projects\/[^/]+\/members$/, [{ email: ADDRESS, role: 2, bound: true }]],
  [
    /^\/projects\/([^/]+)$/,
    ({ params: [id] }) => ({
      project: {
        ...project,
        id,
        // The private one is the head's worst case: a long title, the publish
        // button beside it, and a description under it (038). The height below
        // is asserted against that shape.
        ...(id === PRIVATE ? { visibility: 'private', title: LONG_TITLE, description: DESCRIPTION } : {}),
      },
      sessions: [job],
      may_submit: true,
      signer_handle: HANDLE,
      credits: { balance: 5_000_000, available: 4_000_000, start_reserve: 1_000_000, may_reclaim: true },
    }),
  ],
  [
    /^\/jobs\/[^/]+\/transcript$/,
    {
      events: [
        { role: 'user', kind: 'message', content: 'Reproduce this paper.', time: at(0) },
        { role: 'assistant', kind: 'tool_use', name: 'bash', content: 'python analysis/recompute_attention_decline.py --input data/trials.csv --bootstrap 10000', time: at(2000) },
      ],
      usage: { InputTokens: 12345, OutputTokens: 6789, CachedInputTokens: 4096 },
      live: false,
    },
  ],
  [/^\/jobs\/([^/]+)\/reproduction$/, ({ params: [id] }) => ({ id, repo: 'open-hai/retention-under-load', title: TITLE, verdict: 'passed', review: 'clear', can_publish: true })],
  [/^\/jobs\/[^/]+\/artifacts$/, { files: ['README.md', 'analysis/recompute_attention_decline.py'] }],
  [/^\/jobs\/[^/]+\/artifact$/, text('# a file\n\nOne line.\n')],
  [
    /^\/jobs\/[^/]+\/verdict$/,
    {
      schema: 1,
      verdict: 'reproducible',
      summary: 'The inner loop reproduces at the reported magnitude.',
      graded_at: at(9000),
      components: { verified: 3, partial: 1, blocked: 0, total: 4 },
      boundary: [
        { claim: 'The attention score declines monotonically over the session.', loop: 'inner', outcome: 'verified', evidence: 'Recomputed from the released trial table.', citation: '§4.2' },
        { claim: 'Participants self-report lower engagement after 40 minutes.', loop: 'outer', outcome: 'blocked', evidence: 'Requires people; never simulated.', citation: '§5.1' },
      ],
    },
  ],
  [/^\/jobs\/[^/]+$/, job],
  [/^\/(public|shared)$/, [{ ...listed, running: 0, latest_phase: 'done' }]],
  [
    /^\/profiles\/[^/]+$/,
    { handle: HANDLE, display_name: 'Ada Lovelace', institution: 'University of Somewhere, Department of Rather Long Names', contact_email: ADDRESS, projects: [{ id: PROJECT, title: TITLE, signed_off_at: at(9000) }] },
  ],
  [
    '/admin/access',
    [
      // Two ordinary rows and one crowded one: the tiers differ in width, which
      // is what used to move every column after them.
      { email: 'ada@example.com', tier: 'user', wallet: 10_000_000, created_at: at(0), bound: true },
      { email: 'grace@example.com', tier: 'admin', wallet: 0, created_at: at(0), bound: true },
      { email: ADDRESS, tier: 'user', wallet: -1_500_000, created_at: at(0), bound: true, frozen: true },
    ],
  ],
  ['/admin/solvency', { outstanding_micro: 1_500_000 }],
  [/^\/admin\//, {}],
]

// Every screen inside the shell. The landing has its own test (017) and is a
// different layout, so it is not swept here.
const SCREENS = [
  ['projects', '/'],
  ['registry', '/registry'],
  ['trash', '/trash'],
  ['submit', '/new'],
  ['wallet', '/wallet'],
  ['settings', '/settings'],
  ['settings/profile', '/settings/profile'],
  ['console', '/console'],
  ['profile', `/u/${HANDLE}`],
  ['project', `/p/${PROJECT}`],
  ['project/verdict', `/p/${PROJECT}/verdict`],
  ['project/people', `/p/${PROJECT}/people`],
  ['session', `/p/${PROJECT}/s/${JOB}`],
  ['session/verdict', `/p/${PROJECT}/s/${JOB}/verdict`],
  ['session/artifacts', `/p/${PROJECT}/s/${JOB}/artifacts`],
]

// The four views a signed-out reader is shown (`isPublicView`, 028): the
// registry, a project in it, a session under that project, and a researcher's
// page. Everything else is somebody's workspace and renders the landing.
const PUBLIC_SCREENS = SCREENS.filter(([, path]) => path === '/registry' || path.startsWith('/p/') || path.startsWith('/u/'))

// spill reports what a phone cannot see: anything painted past the right edge,
// and any pane hiding that behind its own sideways scroll.
//
// The closed drawer is excluded, because it is off the left edge on purpose,
// and so is anything inside a container this app intends to scroll sideways.
// That allow-list is named rather than derived, because `.screen` computes to
// `overflow-x: auto` too and deriving it would excuse the whole bug.
const spill = (page, vw) =>
  page.evaluate((w) => {
    const nav = document.querySelector('.rail')
    const over = []
    for (const e of document.querySelectorAll('body *')) {
      if (nav && (e === nav || nav.contains(e))) continue
      const r = e.getBoundingClientRect()
      if (r.width === 0 && r.height === 0) continue
      if (r.right <= w + 1 && r.left >= -1) continue
      const cs = getComputedStyle(e)
      if (cs.position === 'fixed' || cs.overflowX === 'auto' || cs.overflowX === 'scroll' || cs.overflowX === 'hidden') continue
      if (e.closest('.grid-table-wrap, .rate-card, pre, code')) continue
      if (e.parentElement && over.some((o) => o.el === e.parentElement)) continue
      over.push({ el: e, sel: e.tagName.toLowerCase() + (typeof e.className === 'string' && e.className ? '.' + e.className.trim().split(/\s+/).join('.') : ''), right: Math.round(r.right) })
    }
    const panes = []
    for (const e of document.querySelectorAll('.screen, .tx-body, .main, .topbar')) {
      if (e.scrollWidth > e.clientWidth + 1) panes.push(`${e.className.split(/\s+/)[0]} (${e.scrollWidth} > ${e.clientWidth})`)
    }
    return {
      doc: document.documentElement.scrollWidth - window.innerWidth,
      panes,
      over: over.slice(0, 4).map(({ sel, right }) => `${sel.slice(0, 70)} ends at ${right}`),
    }
  }, vw)

// drawerIs waits for the drawer to be open or shut, then for its slide to end,
// because a drawer mid-slide measures where it currently is. It gives up
// quietly; whether it got there is the assertion that follows.
const drawerIs = async (page, open) => {
  await page.waitForFunction((o) => document.querySelector('.rail').classList.contains('rail-open') === o, { timeout: 2000 }, open).catch(() => {})
  await settled(page)
}

const h = await start()

// open serves the mocked API to `me` and returns a page at `path`, with
// everything the screen fetches on it, so the measurement is of the loaded
// screen. `sidebar` seeds the stored fold, which the phone must ignore and the
// desktop must obey.
const open = async (path, { viewport = PHONE, sidebar, me = SIGNED_IN } = {}) => {
  const page = await h.page({ viewport, routes: [['/me', me], ...routes] })
  // The fold is always written, never left over: localStorage is shared by
  // every page this browser opens, and one test seeding `folded` would
  // otherwise decide what the next one measures.
  await page.evaluateOnNewDocument(
    (s) => {
      localStorage.setItem('theme', 'light')
      localStorage.setItem('sidebar', s)
    },
    sidebar ?? 'open',
  )
  await page.open(path, { waitUntil: 'networkidle0' })
  return page
}

try {
  /* ── The rail leaves the flow, on every screen ── */

  // sweep measures one reader at one width. The signed-out pass matters: 028
  // gave the app a second shell, in which the rail's foot becomes a sign-in and
  // the topbar loses its controls, and a stranger opening a shared link on a
  // phone is the likeliest mobile visit this app gets. The 264px drawer is a
  // third width that foot has never been sized against.
  const sweep = async (screens, { viewport, me, who }) => {
    for (const [name, path] of screens) {
      const page = await open(path, { viewport, me })
      const label = `${who} ${name} @${viewport.width}`
      try {
        // A session is its own full-height layout rather than a `.screen` pane.
        await page.waitForSelector('.screen, .tx-body', { timeout: 15000 })
      } catch {
        fail(`${label}: ${path} rendered no screen`)
        await page.close()
        continue
      }
      // Give the entry animation its 0.3s: a card mid-fade is mid-transform,
      // and a transformed box measures where it is, not where it lands.
      await settled(page)

      // The public profile is a page for strangers and renders outside the shell.
      const shell = await page.evaluate(() => !!document.querySelector('.shell'))
      if (shell) {
        const w = await page.evaluate(() => ({
          main: Math.round(document.querySelector('.main').getBoundingClientRect().width),
          railInFlow: document.querySelector('.rail').getBoundingClientRect().right > 0,
        }))
        check(w.main === viewport.width, `${label}: the content column is ${w.main}px of a ${viewport.width}px phone; the rail is still taking its 236`)
        check(!w.railInFlow, `${label}: the rail is still on screen with the drawer closed`)
      }

      const sp = await spill(page, viewport.width)
      check(sp.doc <= 1, `${label}: the document scrolls ${sp.doc}px sideways`)
      for (const q of sp.panes) fail(`${label}: ${q} scrolls sideways — a pane hiding its own overflow is what made this invisible`)
      for (const o of sp.over) fail(`${label}: ${o}, past the ${viewport.width}px edge`)
      await page.close()
    }
  }

  await sweep(SCREENS, { viewport: PHONE, me: SIGNED_IN, who: 'signed in' })
  await sweep(SCREENS, { viewport: NARROW, me: SIGNED_IN, who: 'signed in' })
  await sweep(PUBLIC_SCREENS, { viewport: PHONE, me: SIGNED_OUT, who: 'signed out' })

  /* ── The drawer opens, and everything closes it ── */

  {
    const page = await open('/')
    await page.waitForSelector('.rail-open-btn', { visible: true })
    const isOpen = () => page.evaluate(() => document.querySelector('.rail').classList.contains('rail-open'))
    const railLeft = () => page.evaluate(() => Math.round(document.querySelector('.rail').getBoundingClientRect().left))

    check(!(await isOpen()), 'the drawer is open on arrival; a phone must land on the screen the link named')
    check(await page.evaluate(() => getComputedStyle(document.querySelector('.rail-open-btn')).display !== 'none'), 'the phone has no control that opens the drawer')

    const openIt = async () => {
      await page.click('.rail-open-btn')
      await drawerIs(page, true)
      check(await isOpen(), 'clicking the hamburger did not open the drawer')
      check((await railLeft()) === 0, `the open drawer sits at x=${await railLeft()}, not against the edge`)
    }
    const shut = async (how) => {
      await drawerIs(page, false)
      check(!(await isOpen()), `${how} did not close the drawer`)
    }

    await openIt()
    // Beside the drawer, not through it: the scrim spans the viewport and its
    // center is behind the 264px rail, so clicking the element's middle would
    // land on the rail.
    await page.mouse.click(340, 500)
    await shut('a tap on the scrim')

    await openIt()
    await page.keyboard.press('Escape')
    await shut('Escape')

    await openIt()
    await page.click('.rail-close-btn')
    await shut('the drawer’s own close control')

    // Opening a destination dismisses it: the drawer is covering the screen it
    // is navigating to.
    await openIt()
    await page.evaluate(() => [...document.querySelectorAll('.rail-nav .nav-btn')].find((b) => /wallet/i.test(b.textContent)).click())
    await shut('opening a destination')
    check((await page.evaluate(() => location.pathname)) === '/wallet', 'opening a destination from the drawer did not navigate')

    await page.close()
  }

  /* ── The drawer works for a stranger too ── */

  // 028 replaced the rail's foot with a sign-in when there is nobody to name.
  // That foot was sized against a 236px column and a 56px folded rail; the
  // drawer is a third width, and the sign-in must still fit it.
  {
    const page = await open('/registry', { me: SIGNED_OUT })
    await page.waitForSelector('.screen')
    const bar = await page.waitForSelector('.rail-open-btn', { visible: true, timeout: 15000 }).then(() => true).catch(() => false)
    await settled(page)
    if (!bar) fail('signed out, a phone has no control that opens the drawer')
    else {
      await page.click('.rail-open-btn')
      await drawerIs(page, true)
      const read = await page.evaluate((w) => {
        const rail = document.querySelector('.rail').getBoundingClientRect()
        const foot = document.querySelector('.rail-foot')
        const f = foot?.getBoundingClientRect()
        const wide = [...document.querySelectorAll('.rail *')]
          .filter((e) => {
            const r = e.getBoundingClientRect()
            return r.width > 0 && r.right > rail.right + 1
          })
          .map((e) => (typeof e.className === 'string' ? e.className : e.tagName))
        return { open: document.querySelector('.rail').classList.contains('rail-open'), left: Math.round(rail.left), footBottom: f ? Math.round(f.bottom) : null, h: w, wide: wide.slice(0, 3) }
      }, PHONE.height)
      check(read.open, 'signed out, the hamburger did not open the drawer')
      check(read.left === 0, `signed out, the open drawer sits at x=${read.left}`)
      if (read.footBottom !== null && read.footBottom > read.h) fail(`the drawer's foot ends at ${read.footBottom}px, below the ${read.h}px screen — that is where the sign-in lives`)
      for (const c of read.wide) fail(`signed out, ${c} runs past the drawer's own right edge`)
      // And the sign-in is reachable from it.
      const href = await page.evaluate(() => document.querySelector('.rail-foot a[href^="/login"]')?.getAttribute('href') ?? null)
      check(href !== null, 'the signed-out drawer offers no way in')
    }
    await page.close()
  }

  /* ── The fold is a desktop preference, and the drawer is not it ── */

  {
    // A rail left folded on a laptop must not reach the phone as five unlabelled
    // icons: the drawer has room for the words.
    const page = await open('/', { sidebar: 'folded' })
    await page.waitForSelector('.rail-open-btn', { visible: true })
    await page.click('.rail-open-btn')
    await drawerIs(page, true)
    const read = await page.evaluate(() => ({
      labels: [...document.querySelectorAll('.rail-nav .nav-label')].filter((e) => getComputedStyle(e).display !== 'none').length,
      width: Math.round(document.querySelector('.rail').getBoundingClientRect().width),
      stored: localStorage.getItem('sidebar'),
    }))
    check(read.labels >= 3, `the open drawer names ${read.labels} of its destinations; a touch screen has no hover to read the rest`)
    check(read.width >= 200, `the open drawer is ${read.width}px wide — that is the folded rail, not a drawer`)
    check(read.stored === 'folded', `opening the drawer rewrote the stored fold to ${read.stored}; it is a preference this width must not touch`)
    await page.close()
  }

  /* ── Every control in the topbar is reachable ── */

  {
    for (const path of ['/', `/p/${PROJECT}/s/${JOB}`]) {
      const page = await open(path)
      await page.waitForSelector('.topbar-actions')
      await settled(page)
      const outside = await page.evaluate((w) => {
        const bad = []
        for (const e of document.querySelectorAll('.topbar button, .topbar a')) {
          const r = e.getBoundingClientRect()
          if (r.width === 0) continue
          if (r.right > w + 1 || r.left < -1) bad.push(`${e.getAttribute('aria-label') || e.textContent.trim().slice(0, 24)} [${Math.round(r.left)}..${Math.round(r.right)}]`)
        }
        return bad
      }, PHONE.width)
      for (const b of outside) fail(`at ${path} the topbar control ${b} is off a ${PHONE.width}px screen`)
      await page.close()
    }
  }

  /* ── The panes that were columns are rows ── */

  {
    const page = await open(`/p/${PROJECT}/s/${JOB}`)
    await page.waitForSelector('.tx-body')
    const cols = await page.evaluate(() => getComputedStyle(document.querySelector('.tx-body')).gridTemplateColumns.split(' ').length)
    check(cols === 1, `the transcript is ${cols} columns at 390px`)
    await page.close()
  }

  {
    const page = await open('/')
    await page.waitForSelector('.project-list .row')
    // A paper title is the row's main content. Squeezed into a 1fr track beside
    // five fixed ones it had about 120px; on its own line it has the card's
    // width.
    const title = await page.evaluate(() => Math.round(document.querySelector('.project-list .row .row-main').getBoundingClientRect().width))
    check(title >= 280, `a project's title cell is ${title}px wide; the row is still trying to be a table`)
    await page.close()
  }

  /* ── A project's head leaves room for the project ── */

  // The title shared a flex row with the publish button, which cannot shrink,
  // so the title took what was left: an 80px column, `overflow-wrap: anywhere`
  // breaking a paper's name one syllable to the line, and a head 582px tall on
  // an 844px phone. The tabs, which are how the rest of the project is reached,
  // started at y=605.
  {
    const page = await open(`/p/${PRIVATE}`)
    await page.waitForSelector('[data-test="publish-project"]')
    await settled(page)
    const head = await page.evaluate(() => {
      const box = (s) => {
        const e = document.querySelector(s)
        const b = e.getBoundingClientRect()
        return { w: Math.round(b.width), h: Math.round(b.height), top: Math.round(b.top) }
      }
      return { head: box('.detail-head'), title: box('.detail-title'), tabs: box('.detail-tabs') }
    })
    check(head.title.w >= 260, `the project title is ${head.title.w}px wide; the button beside it is taking the line again`)
    // 300 with a description under the title; the head was 582 before this was
    // fixed, and the number is asserted so it cannot creep back.
    check(head.head.h <= 300, `the project head is ${head.head.h}px of an 844px phone`)
    check(head.tabs.top <= 330, `the tabs start at y=${head.tabs.top}; a phone cannot reach them without scrolling past the title`)
    await page.close()
  }

  /* ── People read as columns ── */

  // Every row sized its own tier, amount and controls, so a USER row and an
  // ADMIN row put them in different places and a phone broke the two controls
  // across two more lines. A table needs the same field in the same place on
  // every row.
  {
    const page = await open('/console')
    await page.waitForSelector('.member-row')
    await settled(page)
    const rows = await page.evaluate(() =>
      [...document.querySelectorAll('.member-row')].map((r) => {
        const at = (s) => {
          const e = r.querySelector(s)
          if (!e) return null
          const b = e.getBoundingClientRect()
          return { l: Math.round(b.left), r: Math.round(b.right) }
        }
        return {
          h: Math.round(r.getBoundingClientRect().height),
          controls: r.querySelectorAll('.row-actions button').length,
          actions: at('.row-actions'),
          money: at('.member-wallet'),
        }
      }),
    )
    check(rows.length >= 3, `the console listed ${rows.length} people; this measures alignment across rows`)
    if (rows.some((r) => !r.actions)) {
      fail('a person’s controls are loose in the row; grouped is what lets every row end them in the same place')
      await page.close()
      throw new Error('people rows have no control group')
    }
    const edges = new Set(rows.map((r) => r.actions.r))
    check(edges.size === 1, `the controls end at ${[...edges].join(', ')} — every row puts them somewhere else`)
    const plain = rows.filter((r) => r.controls === 2)
    const money = new Set(plain.map((r) => r.money.l))
    check(money.size === 1, `the amount starts at ${[...money].join(', ')}; the tier beside it is still setting the column`)
    for (const r of plain) {
      check(r.h <= 100, `a person's row is ${r.h}px tall on a phone; it is an address, a tier, an amount and two controls`)
    }
    await page.close()
  }

  /* ── Desktop is untouched ── */

  {
    const page = await open('/', { viewport: DESKTOP })
    await page.waitForSelector('.rail')
    const desk = await page.evaluate(() => ({
      rail: Math.round(document.querySelector('.rail').getBoundingClientRect().width),
      left: Math.round(document.querySelector('.rail').getBoundingClientRect().left),
      hamburger: getComputedStyle(document.querySelector('.rail-open-btn')).display,
      close: getComputedStyle(document.querySelector('.rail-close-btn')).display,
      fold: getComputedStyle(document.querySelector('.rail-fold-btn')).display,
      scrim: !!document.querySelector('.rail-scrim'),
      label: !!document.querySelector('.btn-topbar .btn-label')?.offsetWidth,
    }))
    check(desk.rail === 236, `at ${DESKTOP.width}px the rail is ${desk.rail}px, want the 236 it has always been`)
    check(desk.left === 0, 'the desktop rail left the flow')
    check(desk.hamburger === 'none', 'the hamburger is showing on a desktop that has its rail')
    check(desk.close === 'none', 'the drawer’s close control is showing on a desktop')
    check(desk.fold !== 'none', 'the desktop lost its fold control')
    check(!desk.scrim, 'a scrim is in the desktop DOM')
    check(desk.label, 'the New project button lost its label on a desktop')

    // And the fold still folds, and still persists. The rail's width eases
    // shut, so it is measured once the fold has landed.
    await page.click('.rail-fold-btn')
    await page.waitForFunction(() => document.querySelector('.rail').classList.contains('rail-fold'), { timeout: 2000 }).catch(() => {})
    await settled(page)
    const folded = await page.evaluate(() => ({ w: Math.round(document.querySelector('.rail').getBoundingClientRect().width), stored: localStorage.getItem('sidebar') }))
    check(folded.w <= 80, `folding the desktop rail left it ${folded.w}px wide`)
    check(folded.stored === 'folded', `the fold was not stored (sidebar=${folded.stored})`)
    await page.close()
  }

  h.ok('on a phone the rail is a drawer, nothing spills past the edge, and the desktop keeps its rail and its fold')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
