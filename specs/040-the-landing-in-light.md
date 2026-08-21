# 040 — The landing in light, and the footer it shares with the rest of Latere

**Status:** in progress.

**Scope:** frontend only. The landing's four slabs stop being dark in light mode, and the page ends with the shared Latere footer instead of a one-line strip of its own.

**Depends on:** [033](033-the-landing-rebuilt-from-the-design.md) (the page this redresses), latere-ui v1.28.0 (the React `SiteFooter`).

## Problem

033 built four blocks as a *material* rather than as a theme — the hero slab, the boundary panel, the walkthrough's artifact panel, and the close — and made them `#0a0b0d` in both appearances. That reads as a decision on one block. On four it reads as a page that did not finish its light mode: a reader on paper scrolls past four holes.

Two things stand behind them and neither follows a CSS token:

The **hero's scene** clears to `0x0a0b0d` and every particle layer blends `AdditiveBlending` — the arithmetic of light added to darkness. On a pale ground additive saturates to white immediately and the mark disappears.

The **walkthrough panel** hardcodes `background: #0f1113`, later in the stylesheet than the `.lp-dark` rule it duplicates, so it wins whatever the tokens say.

Separately, the page ends in `.lp-foot`: a lockup and a tagline. Every other Latere surface ends in the shared footer — the product lineup, the legal links, the theme and language controls — and replichai is the odd one out. latere-ui had no React adapter for it; v1.28.0 adds one.

## Decisions

**The blocks are a material with two states, not one colour.** The `--slab-*` locals on `.landing` stay the single place the material is defined, and gain a light half. Light is the default and dark is the override, matching how the rest of the stylesheet is written. Nothing outside `.landing` changes.

**The names stop lying.** `.lp-dark` becomes `.lp-block` and `.lp-on-dark` becomes `.lp-on-slab`. A class called "dark" that is paper half the time is worse than no name.

**The scene is inked rather than lit.** The light hero keeps every particle, every orbit and the whole motion; what changes is the arithmetic. Additive blending becomes normal blending, the per-vertex brightness moves from the colour channels into an alpha channel, and the hues become ink. Density then reads the same way it does in dark — many faint particles converge on full ink instead of on white — so the figure means what it meant. One palette object holds every value the two states disagree about; the geometry code reads it and never branches.

**The footer comes from latere-ui.** `SiteFooter` from `latere-ui/react`, full variant, wired to the app's own theme store. Its stylesheet wants a palette this app does not have (`--text`, `--accent`, …), so those are aliased on `.site-footer` — never by importing `latere-ui/tokens`, which would redefine `--bg-*` for the whole page.

**One language, one option.** replichai ships English only (`Lang = 'en'`), so the footer gets a one-entry `locales` list. A dropdown offering 中文 that does nothing would be a lie the shared component cannot know it is telling.

## Criteria

1. In light mode none of the four blocks paints a near-black ground; each reads as raised paper with a hairline.
2. In dark mode all four render as they did before 040 — same grounds, same inks, same washes.
3. The hero scene picks its palette from the resolved theme and re-mounts when the theme changes.
4. `.lp-walk-panel` carries no colour of its own; it takes the block's.
5. The page ends in the shared Latere footer, full variant, showing the product lineup and the legal links.
6. The footer's theme control drives the app's theme, and the active one is marked.
7. The footer's language control offers English only.
8. `lpFootTagline` and the `.lp-foot` rules are gone, not deprecated.
9. `frontend/test/landing.test.mjs` asserts 1, 2, 5 and 6 in a real browser.
