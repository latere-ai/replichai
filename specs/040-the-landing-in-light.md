---
title: "The landing in light, and the footer it shares with the rest of Latere"
status: built
---

# 040 — The landing in light, and the footer it shares with the rest of Latere

**Restyled by [047](047-set-like-the-work-it-audits.md).** The slabs and their two-state material are gone, the self-hosted serif is removed, and Inter loads with `block`. The shared footer and its wrapper stay.

**Status:** built. All nine criteria hold.

**Scope:** frontend only. The landing's four slabs stop being dark in light mode, and the page ends with the shared Latere footer instead of a one-line strip of its own.

**Depends on:** [033](033-the-landing-rebuilt-from-the-design.md) (the page this changes), latere-ui v1.28.0 (the React `SiteFooter`).

The scene rewrite turned out to be one substitution rather than a second renderer. Additive blending and normal blending store a particle's strength in different places: the color channels in one, the alpha channel in the other. So the layers write their brightness through a single `paint()` and the buffers became RGBA (three reads a four-wide color attribute as vertex alpha). Everything downstream, the six orbit builders and the whole animation, is unchanged.

Two things the design did not anticipate. The filmic tone curve exists to roll off an additive bloom; in light mode there is no bloom and the curve only grays the ground, so the light state renders its values as written. And the material's quietest ink had to go a step darker than the page's `--fg-3`: a 10px plate label inside a card inside a panel does not meet the contrast the page's own quiet tier ships at. The new browser assertion caught this rather than a reviewer.

## Problem

033 built four blocks as a material rather than as a theme (the hero slab, the boundary panel, the walkthrough's artifact panel, and the close) and made them `#0a0b0d` in both appearances. On one block that reads as a decision. On four it reads as a page whose light mode is unfinished: a reader in light mode scrolls past four dark blocks.

Two things sit behind them and neither follows a CSS token:

The **hero's scene** clears to `0x0a0b0d` and every particle layer blends `AdditiveBlending`, which adds light to darkness. On a pale ground additive blending saturates to white immediately and the mark disappears.

The **walkthrough panel** hardcodes `background: #0f1113`, later in the stylesheet than the `.lp-dark` rule it duplicates, so it wins regardless of the tokens.

Separately, the page ends in `.lp-foot`: a lockup and a tagline. Every other Latere surface ends in the shared footer (the product lineup, the legal links, the theme and language controls), and replichai is the exception. latere-ui had no React adapter for it; v1.28.0 adds one.

## Decisions

**The blocks are a material with two states, not one color.** The `--slab-*` locals on `.landing` stay the single place the material is defined, and gain a light half. Light is the default and dark is the override, matching how the rest of the stylesheet is written. Nothing outside `.landing` changes.

**The class names describe what they do.** `.lp-dark` becomes `.lp-block` and `.lp-on-dark` becomes `.lp-on-slab`. A class called "dark" that renders light half the time is misleading, and worse than no name.

**The scene uses ink on a light ground rather than light on a dark one.** The light hero keeps every particle, every orbit and the whole motion; what changes is the arithmetic. Additive blending becomes normal blending, the per-vertex brightness moves from the color channels into an alpha channel, and the hues become ink colors. Density then reads the same way it does in dark mode: many faint particles converge on full ink instead of on white, so the figure keeps its meaning. One palette object holds every value the two states disagree about; the geometry code reads it and never branches.

**The footer comes from latere-ui.** `SiteFooter` from `latere-ui/react`, full variant, wired to the app's own theme store. Its stylesheet expects a palette this app does not have (`--text`, `--accent`, and others), so those are aliased on `.site-footer`. They are never provided by importing `latere-ui/tokens`, which would redefine `--bg-*` for the whole page.

**One language, one option.** replichai ships English only (`Lang = 'en'`), so the footer gets a one-entry `locales` list. A dropdown offering 中文 that does nothing would misrepresent the app, and the shared component cannot know that.

One thing shipped as it is rather than as the shared component intends. The footer's logotype and the six product wordmarks are set in Instrument Serif, which this app does not ship. Nor does it ship Inter, the sans-serif the whole product is written in. Both fall back consistently everywhere; vendoring a face for the footer alone would make the footer the only element in the app with a real webfont. So the footer renders in Georgia italic here and in Instrument Serif on latere.ai. That is a font-loading decision for the app to make once, not a footer decision.

## Criteria

1. In light mode none of the four blocks paints a near-black ground; each reads as raised paper with a hairline.
2. In dark mode all four render as they did before 040 — same grounds, same inks, same washes.
3. The hero scene picks its palette from the resolved theme and re-mounts when the theme changes.
4. `.lp-walk-panel` carries no color of its own; it takes the block's.
5. The page ends in the shared Latere footer, full variant, showing the product lineup and the legal links.
6. The footer's theme control drives the app's theme, and the active one is marked.
7. The footer's language control offers English only. Amended 2026-10-08: the footer shows no language control (see below).
8. `lpFootTagline` and the `.lp-foot` rules are gone, not deprecated.
9. `frontend/test/landing.test.mjs` asserts 1, 2, 5 and 6 in a real browser.

## Amendment, 2026-10-08

latere-ui v2.0.0 makes the footer one shape on every Latere site: the full footer as latere.ai shows it, with a site passing only its lockup and its theme and language wiring. Three decisions above change with it.

**The footer opens with replichai's own lockup.** The landing passes the header's lockup, the drawn mark and the wordmark linked home, one component drawing both, in place of the Latere AI lockup the package draws by default.

**The aliases move to a wrapper of the app's own.** The footer's classes are `lu-footer*` in v2 and no sheet of the app names them; a unit test fails on one that does. The palette aliases (`--text`, `--text-secondary`, `--text-muted`, `--accent`) sit on `.lp-footer-wrap`, the element around the footer, which also sets its distance from the close. It aliases `--font-ui` to the app's `--font-sans` as well, so the lockup at the head of the footer is set in the same face as the header's.

**No language control.** v2 shows a language menu only to a site that passes a language change and two languages or more, so this app passes neither and the footer shows none. Criterion 7 now reads: the footer shows no language control, and the browser test asserts it.

The paragraph on Instrument Serif no longer applies to the footer: with the product's own lockup at its head, the footer sets no serif. The self-hosted face stays for the serif `<em>` in a screen's description.
