---
title: "The landing, rebuilt from the design"
status: built
---

# 033 — The landing, rebuilt from the design

**Superseded in part by [040](040-the-landing-in-light.md).** The hero scene, the walkthrough and the artifact stack stand. Three things below were reversed: the slabs are no longer dark in both themes, `.lp-dark`/`.lp-on-dark` became `.lp-block`/`.lp-on-slab`, and this page's own footer strip is now the shared Latere `SiteFooter`.

**Status:** built. Every acceptance criterion holds. The page is `frontend/src/landing.tsx` and the scene is `frontend/src/hero-scene.ts`, the only module in the app that imports three, so the split is one file's concern and Vite puts it in a 734 kB chunk (188 kB gzipped) that the 455 kB entry never touches. `frontend/test/landing.test.mjs` keeps all of 029's assertions and adds four: the slab holds its own copy and its own ground at both viewports, the walkthrough lights exactly one step and opens exactly one artifact when it drives, it dims and folds *nothing* under reduced motion, and the file list matches `internal/publish/gate.go`'s five worker-written paths while no DOI-shaped string on the page resolves to a real registrant.

Three things came out of the build. The walkthrough's driver stands down at ≤900px as well as under `reduce`: one column has no second column for a plate to be sticky in, so the resting state is the whole content there too, and the media query and the driver agree because both name the same 900px. The design's step dimming and the reveal stagger were two writers on one `opacity`, which leaves a step stuck at a third of itself; the steps are out of the staggered reveal entirely and their opacity belongs to the driver alone. And the dark panels needed a hairline they do not have in the design: on paper the contrast is the edge, but in dark mode a `#111316` panel on a `#17171b` page is indistinguishable from it.

**Scope:** the signed-out landing is rebuilt against the imported `Landing (redesign)` design: a dark hero slab carrying a WebGL particle field, the artifact stack the product ships, a scroll-driven session walkthrough, an in-app verdict shown rather than described, and two new bands, who it is for and what a session costs. Frontend only. No route, no schema. Extends 029 rather than replacing its argument: the copy, the figures' meaning, and every guarantee `landing.test.mjs` holds are carried forward.

**Depends on:** [029](029-the-landing-is-a-place.md) (the page this rebuilds, and its test), [028](028-the-registry-opens.md) (the live strip's data), [006q](006q-sign-in-is-open-budget-is-not.md) (what the copy may promise), [011](011-what-a-session-costs.md) (what the cost band may say).

## Problem

029 made the landing a page rather than a door, and the argument on it is the right one. What it does not do is *show* anything: three drawn figures explain the mechanism, and every one of them is a diagram. A researcher deciding whether to trust a reproducibility claim is asked to read that the verdict is checkable, and never shown a verdict.

The design closes that. It is also a better page: the hero is a full section rather than a two-column fold, and the sections that were missing (what comes back, who it is for, what it costs) are the three questions a first-time reader asks and the current page does not answer.

## Decisions

**Every colour comes from a token.** The design is authored light-only in literal hex, and every one of those hexes is already a `styles.css` token: `#2369bd` is `--brand`, `#0c0a09` is `--fg-1`, `#3f7a55` is `--ok`, `#6b4fa8` is `--pub`. They are written as tokens, so the page holds in dark without a second palette. The two dark slabs (hero and close) are the exception and are *deliberately* dark in both themes: they are a material, not a theme, and the design's own text colours travel with them.

**The hero scene is bundled, not fetched.** The design imports three.js from a CDN at runtime. It ships as a dependency instead, dynamically imported inside the effect so Vite splits it into its own chunk that the first paint never pays for. A CDN in the critical path of the first screen makes a third party's uptime part of ours, and the design's own `try/catch` already says the page must survive the scene failing; bundling makes that the rare case rather than the network's decision.

**The scene is the mark, at material scale.** Two orbits of particles: the inner one continuous all the way round, the outer one with six permanent voids baked into its geometry, so it visibly never closes however long it is watched. It is the same argument as the drawn mark and the same geometry: six segments, cool inside, warm outside. It is not a spinner and not a logo animation; the rig sways rather than turns.

**Reduced motion gets the finished page, unchanged from 029.** The scene renders one frame and stops. The walkthrough's scroll driver does not start at all, which leaves every step at full opacity and every plate open: the resting state is the whole content, not a first frame of it. This is asserted, not assumed: 029's reduced-motion block already fails on any band below opacity 0.99.

**The walkthrough dims, and dimming is motion.** Four steps on the left, four plates on the right, driven by which step is in the reader's band. Under `no-preference` the inactive steps drop to 0.34 and the inactive plates collapse to their header strip; under `reduce` neither happens. The driver is one scroll listener and no library.

**The verdict panel is an example and says so.** It shows a full verdict: the rate, the claim table, the inner/outer boundary, one numeric mismatch recorded rather than reconciled. That is the strongest thing this product has to show and it belongs on the page. But a fabricated verdict attached to a resolvable DOI, on the site of a product that audits reproducibility claims, is the exact failure the product exists to catch. The panel carries an **Example** marker and an illustrative paper key that resolves to nothing.

**The registry band stays live.** The design draws three sample reproductions with signer names. Those are the design's placeholders, not data: the band keeps 029's `GET /public` fetch and its rule (an empty or failed registry renders no band at all), and only the card is restyled. The design's verdict badge on each card is dropped, because `ProjectView` carries no verdict state and a badge would be invented.

**Two things in the design do not ship.** The institution logo strip is five empty slots with no logos to put in them, and 029 already settled that case for the published strip: a band that never fills makes a claim about a community, and the wrong one. The rate card is a table of prices that live in the database behind an admin-only endpoint; the three cost cards ship without it, so the page cannot drift from what a session is charged.

**Copy is i18n or it is not on the page.** Everything 029 wrote is reused verbatim; the design's text is 029's text, which is where the design got it. The new bands add keys under the same `auth` block. `Lang` is `'en'` only; there is no second locale to keep in step.

## Acceptance criteria

1. Every guarantee `landing.test.mjs` holds today still holds: the sticky sign-in at scroll top and bottom on both viewports, the mark painting in both themes, the three-point inner/outer/verdict split with exactly one drawn open, the four-step and three-part figures, the reduced-motion resting state, the live strip's reveal and its absence on an empty registry, and the `next` round trip.
2. The hero slab renders at both viewports with the claim, the lead, and both calls to action inside it, and nothing scrolls sideways at 390px.
3. The scene degrades to the slab with no visible error when WebGL or the chunk is unavailable, and renders exactly one frame under reduced motion.
4. `three` is in its own build chunk; the entry chunk does not grow by its size.
5. The artifact stack names the five files `internal/publish/gate.go` treats as worker-written, and no others.
6. The verdict panel is marked as an example and names no resolvable paper.
7. No hardcoded user-facing string outside `i18n.ts` (`i18n-coverage.test.ts`).
