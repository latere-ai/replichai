// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A browser regression test for the vertical rhythm between stacked cards.
//
// The bug it guards: a screen that renders two cards as siblings must put them
// in a `.stack`, which is the one place the 16px gap between cards is written.
// The public researcher page dropped the wrapper and hung both cards straight
// off `.screen`, which is a plain block — so the identity card and the
// signed-work card sat edge to edge with their 1px borders touching, reading as
// one mis-drawn box rather than two cards. (Settings had the same defect until
// 024a split it into one-card-per-tab, so it no longer stacks two siblings.)
//
// "The cards exist" passes against the broken code, and so does "the second card
// starts below the first". The discriminating measurement is the distance
// between the first card's bottom edge and the second card's top edge: zero when
// the wrapper is missing, a full gap when it is there. The screen is
// URL-addressable, so the test loads it directly with the API mocked. Run it
// with `bun run test:browser`.
import { fail, settled, start } from './harness.mjs'

const at = (ms) => new Date(1754_215_200_000 + ms).toISOString()

// The settings screen's second card is ProfileCard, and it renders nothing until
// /me/profile answers — an empty body is what the API sends before a profile is
// ever saved, and it is enough to draw the card.
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

// What a `.stack` puts between two cards. The floor is well under it so a change
// of taste in the gap does not fail the test, and well over zero so a stray 1px
// margin cannot pass for a gap.
const MIN_GAP = 8

const h = await start()
try {
  const page = await h.page({ routes })

  // A card is measured only against the card next to it: `.screen .card` also
  // matches cards nested inside another one, and those carry their own spacing.
  const gaps = async (label, want) => {
    const m = await page.evaluate(() => {
      const screen = document.querySelector('.screen')
      if (!screen) return { cards: 0 }
      const cards = [...screen.children].flatMap((c) => (c.classList.contains('card') ? [c] : [...c.children].filter((g) => g.classList.contains('card'))))
      return {
        cards: cards.length,
        gaps: cards.slice(1).map((c, i) => c.getBoundingClientRect().top - cards[i].getBoundingClientRect().bottom),
      }
    })
    // Measuring a gap between cards that are not both on screen proves nothing,
    // so the count is asserted before the distance.
    if (m.cards !== want) return fail(`${label} drew ${m.cards} top-level cards, want ${want} — the mock or the screen changed and the gap below is not the reported bug`)
    m.gaps.forEach((g, i) => {
      if (g < MIN_GAP) fail(`${label}: cards ${i + 1} and ${i + 2} are ${g.toFixed(1)}px apart, want at least ${MIN_GAP}px — they need a .stack wrapper`)
    })
    return m
  }

  // The defect, on the page a signed-out reader sees: identity above, signed
  // work below. (Settings no longer stacks two cards since 024a tabbed it.)
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
