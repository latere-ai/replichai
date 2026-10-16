---
title: "The first screen"
status: built
---

# 017 — The first screen

**Restyled by [047](047-set-like-the-work-it-audits.md).** The mark's outer loop is one open arc rather than six dashes, the crimson left the favicon, and the wordmark is text in Inter, which the app now ships.

**Superseded by [029](029-the-landing-is-a-place.md), [033](033-the-landing-rebuilt-from-the-design.md) and [040](040-the-landing-in-light.md).** The mark and `index.html`'s description, icon and social card stand. The two-column landing below does not: it moved into its own module, was rebuilt from a design, and was restyled for light mode. `SignIn` is now `Landing`, the page links to the registry, which a signed-out reader can now read ([028](028-the-registry-opens.md)), and the webfont this spec put out of scope is self-hosted.

**Status:** built. Every acceptance criterion holds. `frontend/src/brand.tsx` owns `Mark`, `Wordmark`, and `Lockup`; the letter-`r` `Logo` and its two tile rules are gone; `SignIn` is the two-column landing, and the three failure cards keep the centered one. `frontend/test/landing.test.mjs` drives criteria 1 and 3 to 6 in Chrome (the geometry at 1440×900 and 390×844, the area reorder, the fold, the `/login?next=` round trip, and the mark painting in both themes) and was verified to fail with the change stashed. The browser suite is green (8 files) and the 83 unit tests still pass.

Two things changed during the build. The mark lives in `brand.tsx`, not `icons.tsx` as this spec said: an icon inherits `currentColor` so one glyph can sit in a brand button and a muted row, while the mark owns its color everywhere, and putting it in the icon set would have contradicted that file's first line. The outer ring draws six dashes rather than the ten first drawn; at 26px ten dashes read as a loading spinner, which this app shows a few pixels away. The favicon and the generated rasters use the same six, so the three are one mark.

One fix outside the spec's scope came with it: `/internal/spa/dist/` was ignored one artifact at a time (`index.html`, `assets/`), so the three new static files landed untracked and would have been left out of the commit without an explicit `git add`. The rule now ignores the directory and un-ignores `PLACEHOLDER`.

**Superseded in part by [029](029-the-landing-is-a-place.md).** Criteria 3 and 4 (the two-column grid, the one-column reorder, and the sign-in button inside the fold at 900px and 844px) are gone with the fold itself: the landing is a page that scrolls, so it cannot guarantee a fold. The replacement guarantee is stronger and independent of page length: a sticky header carrying the sign-in, asserted at the top of the page and at the bottom, on both viewports. `landing.test.mjs` now drives that. Criterion 5's deep link moved too: a public `/p/{id}` renders the app signed out since [028](028-the-registry-opens.md), so the landing's round trip is asserted from an address that still needs an account. Everything else here still holds: the mark, the wordmark, the copy, the head metadata, and the two failure screens keeping their centered card.

**Scope:** the surface an unauthenticated visitor lands on, and the mark it carries. Replaces the 440px sign-in card with a split screen: what replichai is on the left, the sign-in card on the right. Replaces the letter-`r` gradient tile with a drawn mark and a wordmark lockup. Adds the head metadata the app has never had (description, favicon, `og:`/`twitter:` cards). Frontend only: no route, no API, no schema.

**Depends on:** [006e](006e-frontend.md) (the screens and the design tokens), [006q](006q-sign-in-is-open-budget-is-not.md) (sign-in is open, which is what the copy may promise), [013](013-the-address-bar-is-the-state.md) (`/login?next=` carries where to come back to).

## Problem

The first thing a visitor sees is a centered card holding a gradient tile with the letter `r`, the word `replichai`, one sentence (*"Audit the reproducibility of a research paper."*), and a button (`frontend/src/App.tsx:307-321`). Nothing on that screen says what the product does that other tools do not, and the one sentence is the only claim it makes.

That matters more here than for most products, because the differentiator is a boundary rather than a category: the integrity tools publishers use read a paper's text and images, and none of them re-runs a line of its code. The first screen should say that this product executes the paper, and that it refuses to execute the half of the paper a machine cannot honestly execute. A card with room for one sentence cannot say either.

The mark has a second, smaller problem. It is a `<div>` containing the character `r`, styled `font-family: var(--font-serif); font-style: italic`, and `--font-serif: 'Instrument Serif'` is declared at `styles.css:51` with no `@font-face` and no font link anywhere in the repository. The mark has therefore always rendered in Georgia. A mark that depends on a font the app does not ship does not render as designed.

There is also nothing in `<head>`: no description, no icon, no social card (`frontend/index.html` is twelve lines). A link to a replichai project pasted into Slack or a mail client shows a bare URL.

## Decisions

**The mark represents the boundary the product defends.** A closed solid ring inside a dashed open one: the inner loop is mechanically re-runnable, so it is drawn closed; the outer loop is the study with human participants, so it is drawn open, and the product never closes it. The mark is an inline SVG in its own `brand.tsx`, colored from `--brand` and scaled by an explicit `size`, so it works in dark mode and needs no font and no raster. It is not in `icons.tsx`: an icon there inherits `currentColor` on purpose, and the mark must look the same in every context it is used in.

**The wordmark carries the same seam.** `replichai` is `replic` (replication) and `hai` (human–AI, the lineage the `open-hai` forge account already names). The lockup sets it as one word with a hairline optical gap at that seam and tightened tracking. It is *not* tinted: the stylesheet's rule is that one brand hue marks what the operator can act on (`styles.css:1-9`), and a two-tone wordmark would spend that hue on decoration. The color goes on the mark.

**The landing is two columns, and the sign-in card stays inside the fold.** Left: the lockup, the claim, the lead paragraph, and three lines naming the inner loop, the outer loop, and the verdict. Right: the sign-in card. Below 900px the grid becomes one column ordered brand, then card, then the three lines, so the button is reachable on a phone without scrolling past the argument.

**Nothing on this screen links anywhere a signed-out visitor cannot go.** `GET /public` is gated (`internal/httpapi/handlers.go:87`), so a "browse the public registry" link would return the visitor to the screen they clicked from. The public registry becomes linkable from here when it becomes readable signed-out, which is its own spec. The only action is signing in.

**The copy promises exactly what 006q made true.** Signing in is open and costs nothing; a session is what spends credits. The card says that rather than "get started free", which would be a claim about credits a new account does not have.

**`AuthUnavailable` and `NoAccess` keep the centered card.** They are failure screens, not invitations, and an argument for the product is the wrong content for somebody whose session just broke. They inherit the new mark and lockup and nothing else.

## The copy

| Slot | Text |
|---|---|
| Claim | Does the paper actually reproduce? |
| Lead | Give it a paper. replichai finds what the authors released, implements and re-runs the part a machine can run, records every decision the paper left unwritten, and returns a verdict you can take apart. |
| Inner loop | The algorithm, model, or analysis pipeline. Implemented, run, and published as a repository with the agent's own commit history. |
| Outer loop | The study that validates it with people. Named, bounded, and never simulated — pretending otherwise is how reproducibility claims go wrong. |
| The verdict | Authored by the system, signed by a named researcher, and published with the full transcript, so you can check the reasoning instead of taking the number. |
| Card | Sign in — Sign in to give it a paper, watch the audit run, and sign the verdict that comes back. |

Every string lives in `i18n.ts` under the `auth` section. `Lang` is `'en'` only (`i18n.ts:6`), so there is no second locale to update.

## Head metadata

`frontend/index.html` gains a description, `og:`/`twitter:` cards pointing at `https://replichai.latere.ai/og.png`, a `theme-color`, and icons. `frontend/public/` gains `favicon.svg` (the mark, white on brand, in a rounded tile so it holds at 16px), `apple-touch-icon.png`, and `og.png` (1200×630). The rasters are generated from the same SVG source by `frontend/scripts/brand.mjs` over the Chrome that already drives the browser tests, and committed, so a build needs no browser.

## Acceptance criteria

1. `Mark` renders as SVG at both rail and landing sizes, in light and dark, with no dependence on `--font-serif`; the letter-`r` tile and its two CSS rules are deleted, not left beside the new mark.
2. The wordmark is one component used at every site that currently hardcodes `replichai` (`App.tsx:249`, `:313`, `:440`).
3. At 1440×900 the landing renders two columns, and the sign-in button's bounding box lies entirely above 900px.
4. At 390×844 the landing renders one column, the order is brand → card → points, and the sign-in button is entirely above 844px.
5. The sign-in button still links to `/login?next={current path}`, and the deep-link case (`/p/{id}` signed out) still round-trips.
6. `AuthUnavailable` and `NoAccess` still render their centered card with the new mark.
7. `index.html` carries a description, an `og:image`, and an icon; `favicon.svg`, `apple-touch-icon.png`, and `og.png` are served from the built bundle's root.
8. A browser test (`frontend/test/landing.test.mjs`) drives 3, 4, 5, and 6 in Chrome against a mocked `/me`, and fails without the change.

## Not in scope

A public, signed-out registry; a marketing site with its own routes; a pricing page; loading `Inter`/`Instrument Serif` as real webfonts (the declaration stays a fallback chain, and nothing in the new design depends on it); any second locale.
