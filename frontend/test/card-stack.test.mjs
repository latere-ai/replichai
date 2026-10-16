// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for the vertical rhythm between stacked cards.
//
// It guards that a screen rendering two cards as siblings puts them in a
// `.stack`, the only place the 16px gap between cards is written. Placed
// directly under `.screen`, a plain block, two cards sit edge to edge with
// their 1px borders touching and read as one mis-drawn box.
//
// "The cards exist" passes without the wrapper, and so does "the second card
// starts below the first". The discriminating measurement is the distance
// between the first card's bottom edge and the second card's top edge: zero when
// the wrapper is missing, a full gap when it is there. The screen is
// URL-addressable, so the test loads it directly with the API mocked. Run it
// with `bun run test:browser`.
import { fail, settled, start } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()

// An empty /me/profile body is what the API sends before a profile is ever
// saved.
const routes = [
  ['/me', { authenticated: true, principal: 'p', email: 'tester@example.com', tier: 'admin', credits: { wallet: 42.5, start_reserve: 5 } }],
  ['/me/profile', {}],
  ['/projects', []],
  [/^\/(public|shared)$/, []],
  [
    /^\/profiles\/[^/]+$/,
    {
      handle: 'tester',
      display_name: 'A Tester',
      institution: 'Some University',
      contact_email: 'tester@example.com',
      projects: [{ id: 'proj-1', title: 'Walk This Beam', signed_off_at: at(0) }],
    },
  ],
]

// What a `.stack` puts between two cards. The floor is well under it so a design
// change to the gap does not fail the test, and well over zero so a stray 1px
// margin cannot pass for a gap.
const MIN_GAP = 8

const h = await start()
try {
  const page = await h.page({ routes })

  // A card is measured only against the block next to it: `.screen .card` also
  // matches cards nested inside another one, and those carry their own spacing.
  // A researcher's signed work is a captioned section rather than a second
  // card, so a stacked block is either one; the gap between them is the same
  // defect whichever the second is.
  const gaps = async (label, want) => {
    const m = await page.evaluate(() => {
      const screen = document.querySelector('.screen')
      if (!screen) return { cards: 0 }
      const block = (e) => e.classList.contains('card') || e.classList.contains('findings-sec')
      const cards = [...screen.children].flatMap((c) => (block(c) ? [c] : [...c.children].filter(block)))
      return {
        cards: cards.length,
        gaps: cards.slice(1).map((c, i) => c.getBoundingClientRect().top - cards[i].getBoundingClientRect().bottom),
      }
    })
    // Measuring a gap between cards that are not both on screen proves nothing,
    // so the count is asserted before the distance.
    if (m.cards !== want) return fail(`${label} drew ${m.cards} top-level blocks, want ${want} — the mock or the screen changed and the gap below is not the reported bug`)
    m.gaps.forEach((g, i) => {
      if (g < MIN_GAP) fail(`${label}: cards ${i + 1} and ${i + 2} are ${g.toFixed(1)}px apart, want at least ${MIN_GAP}px — they need a .stack wrapper`)
    })
    return m
  }

  // The page a signed-out reader sees: identity above, signed work below.
  await page.open('/u/tester')
  await page.waitForSelector('.screen .card')
  await settled(page)
  await gaps('public profile', 2)

  h.ok('stacked cards keep their gap')
} catch (e) {
  fail(e)
} finally {
  await h.close()
}
