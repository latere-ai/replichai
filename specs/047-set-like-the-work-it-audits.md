---
title: "Set like the work it audits"
status: drafted
---

# 047 — Set like the work it audits

**Status:** drafted. A prototype covers the landing, the hero figure, the mark, the projects list, a session with its verdict, and the registry. The remaining screens follow once the direction is approved.

**Scope:** the look of the whole website, as one visual system built on latere-ui. Type, color, layout, motion, the mark and the wordmark. Frontend only: no route, no API, no schema, no copy that changes what the product claims. Every surface is in scope:

- the landing;
- the public registry, a public project and session, and a researcher's public profile;
- the signed-in console: the projects list, a project, its papers, a session, the verdict and findings panes, and the transcript viewer;
- settings, the wallet and billing;
- the admin console;
- sign-in, the failure screens and the 404.

**The family** in this spec means Latere's products and the shared design system they are built on, latere-ui. **The platform** is the family's developer product at platform.latere.ai, whose landing and console are the nearest reference this spec follows.

**Depends on:** [017](017-the-first-screen.md) (the mark and what it means), [029](029-the-landing-is-a-place.md) (the landing's guarantees), [030](030-the-app-on-a-phone.md) (the phone), [033](033-the-landing-rebuilt-from-the-design.md) and [034](034-the-console-rebuilt-from-the-design.md) (the landing and console this restyles), [040](040-the-landing-in-light.md) (light mode and the shared footer), latere-ui v2.2.0 (the ink palette and the footer).

## Problem

A design review of the live site found four problems. The site does not look like academic work, which is what its readers produce and read. Its style differs from the rest of the family. The animation on its first screen looks imprecise. And its mark could be better.

Each of those has a concrete cause in the code.

**The type reads as a developer tool and as startup marketing at once.** Every label on the landing and in the console is monospace, letter-spaced and upper case: "REPRODUCIBILITY AUDITS", "STEP 01", "PUBLISHED EVIDENCE", the table heads "PAPER / STATUS", the status badges "DONE" and "FAILED", the role badge "USER". The display type is heavy Inter with tight tracking. The app names Inter and never ships it, so every reader on a machine without Inter installed sees the system face, which the platform does not.

**The palette and shapes are replichai's own.** The platform and its sign-in pages use latere-ui's ink ladder, the shared palette: a white page, neutral grays with no hue, ink as the accent, the primary action an ink fill. replichai paints a warm stone ground, spends its azure on buttons, the current page and the focus ring, sets every button as a pill, and floats the console's content in an inset card with a shadow. A reader moving from platform.latere.ai to replichai.latere.ai crosses a seam the family does not have anywhere else.

**The hero animation has nothing exact in it.** It is a three.js particle field (733 kB, 188 kB gzipped): soft points that sway, a bloom, no edge anywhere. It was meant as the mark at material scale (033), and the argument is right. But a product whose whole job is to check numbers opens on an image in which nothing can be measured, and that is the opposite of what an audit should say about itself.

**The mark reads as a spinner.** Six dashes round a ring is the shape of a loading indicator and of a selection marquee, and 017 already had to cut ten dashes to six for exactly this reason. The favicon paints the outer ring in the crimson the app uses for failure (`--err`). And the wordmark is set in a different face from every other lockup in the family, which pairs a mark with a serif name ("Latere", "Platform").

## References

**The family.** platform.latere.ai's landing and console, and latere-ui v2.2.0:

- `latere-ui/ink`, the platform's ink ladder, contrast-tested by latere-ui: every text tone reaches 4.5:1 on every surface it is set on, and control edges reach 3:1.
- `brand.css`, which already registers replichai's color (`--lu-brand-replichai`, `#2369bd`, `#78b1ed` in dark) and sets the product's name in Instrument Serif italic (`.replichai-brand`).
- The platform landing's measures: a serif display at `clamp(56px, 6.2vw, 84px)/0.98`, a 17px lede, a `min(1200px, 100% − 96px)` container, 8px-radius calls to action.
- The platform's motion: `150ms` and `220ms` with `cubic-bezier(0.2, 0.8, 0.2, 1)`, and a hero figure that runs only while it is on screen, in a visible tab, without reduced motion, and not paused.

**Research venues**, each for one trait:

- *Distill* (distill.pub): a figure that explains the mechanism rather than decorates it, numbered and captioned underneath; a measured text column.
- *Nature* article pages: a serif title over sans metadata, and figure captions that open with their number and a title ("Fig. 1: …").
- LaTeX's *booktabs* convention: a table has three horizontal rules and nothing else, with no vertical rules and no fills.
- The reference list of a paper (APA or ACM): a list of works set as citations, with authors, year, title and venue, in a hanging indent.

The trait the platform has that this spec does not take is its monospace, upper-case eyebrow. That style reads as a developer tool, and replichai's audience is researchers, not engineers.

## Decisions

### Color: the platform's ink, and the azure only where the product draws

**The palette is latere-ui's ink ladder, imported rather than copied.** `<html data-design="ink">` and `import 'latere-ui/ink'`. The ink selector is scoped (`:root[data-design="ink"]`), so it neither leaks into nor depends on anything else. It also outranks replichai's bare `:root`, so the stylesheet's own ground, text, border and status declarations are deleted rather than left shadowed. `latere-ui/tokens` stays unimported for 040's reason: it redefines unprefixed names for the whole page.

**The primary action is ink, as everywhere in the family.** This finishes what 033 and 034 started for the landing and the console's primary button. It also covers the current page in the rail, which becomes a `--bg-hover` tint at weight 600 as on the platform, and the focus ring, which becomes the family's 2px ink outline.

**The azure is the product's drawing color, and nothing else.** It is the inner ring of the mark and the inner loop in every figure: the part a machine re-runs. Spending it on buttons would make the drawing and the controls compete, which is the platform's rule for product colors ("embellishment, never a page theme").

**Status tones map onto ink's.**

| replichai | becomes |
|---|---|
| `--ok` (passed) | ink `--ok` |
| `--warn` (needs attention) | ink `--warn` |
| `--err` (failed) | ink `--danger` |
| `--run` (running) | ink `--info` |
| `--pub` (published) | the product azure |

`--pub` moves for a second reason too: its violet (`#6b4fa8`) is within a few degrees of ink's `--audience-admin` (`#5b4a8f`), and the admin console shows both. Published work is what the product makes, so it takes the product's color.

### Type: a serif to state, a sans to read

**Inter ships.** Its 400, 500 and 600 woff2 files are copied from the platform byte for byte, under the same paths, so a reader who has visited either site already has them cached. 400 and 500 are preloaded. Instrument Serif is already self-hosted (040); the regular face is now preloaded too. Every face is `font-display: block`, as on the platform: the faces are preloaded with the document, and a swap reflows text a reader is already looking at, which is a layout shift.

**Instrument Serif regular sets everything that states something.** That is the landing's h1 and section headings, the figure titles, a paper's title on the project screen, and the console's page titles. The second half of the hero claim is italic, as the platform sets "Build with *Latere.*". Inter sets everything that is read: body, controls, tables, captions.

**No monospace label, and no upper-case label, anywhere.** Eyebrows, table heads, rail groups, status and role badges are Inter, sentence case, 12 to 14px, in `--fg-2`. Monospace stays for what is literally code or an identifier: a file name, a commit hash, a paper key.

**Numbers are tabular.** Counts, money and rates use `font-variant-numeric: tabular-nums`, so a column of figures lines up.

### Layout: a page laid out like a paper

**One container.** `min(1200px, 100% − 96px)`, as on the platform, and `100% − 32px` at or below 760px, the app's phone breakpoint (030).

**Sections are numbered and ruled, not boxed.** Each landing section opens with a hairline, a numeral, and a serif heading ("2  The boundary"), like a paper's sections. The slabs and tinted panels 033 and 040 built (`.lp-block`) go. On paper, a block of color around a section says "advertisement", and a rule says "next section".

**Figures are numbered and captioned underneath.** "Figure 1. The boundary every audit draws." and "Table 1. An example verdict." The caption carries the figure's argument in one or two sentences, the way a journal figure does.

**The example verdict is a booktabs table.** It has three rules and no fills. Its status words carry a small dot in their tone and keep their word, so color is never the only signal. It is still marked as an example, and its paper key still resolves to nothing (033).

**A rate is a footnote, never a headline.** The verdict leads with its outcome in words and its per-component table. The overall rate is derived from those rows and is set under them at the size of a footnote, with the counts it comes from. One large number would be the thing a reader has to trust, and the rows are what can be checked. This holds on the landing's example and on every verdict in the app.

**The landing lists every file a session commits.** Table 1 names the six files `method.Deliverables()` lists, which the publication gate reads too, and `src/`. The four reports come first, because they are what a reader opens. The two JSON files follow, because they carry the same findings and the study as data.

**The registry strip is a reference list.** Each published reproduction is set as a citation (authors, year, title, venue) with the signer and the session count after it, in a hanging indent. That is the form an academic reader scans a list of works in, and it is what the registry's data already is (043).

### Motion: exact, and only where something changes

**The platform's tokens.** `--motion-fast: 150ms` for hover and press, `--motion-enter: 220ms` for a thing arriving, `--motion-ease: cubic-bezier(0.2, 0.8, 0.2, 1)`. A section's entrance is an 8px rise and a fade, once.

**The hero figure is the mark at figure scale, drawn as a scientific figure.** It replaces the particle field and keeps 033's argument, now in a form that can be measured:

- The inner loop is a closed ring with four stations: implement, run, compare and record.
- The outer loop is a dashed ring, open at one gap, labeled as the study with people.
- Both are annotated with leader lines and set on the platform's dotted ground.

**The figure moves like an instrument, not like weather.** On entrance it draws its rings once, as strokes. After that a single marker travels the inner loop at constant speed, which is the re-run. Each station fills as the marker passes it, and a run count beside the loop advances on each lap. The outer loop never moves and never closes. There is no sway and no easing inside a lap.

**A run count is the reader's.** Pausing and resuming, or scrolling away and back, keeps the count where it was, because the figure stopped rather than restarted. Only reduced motion shows a fixed count, the finished frame's.

**The figure runs only when it should.** As on the platform's figure, it runs only while it is in view, the tab is visible, reduced motion is off, and the reader has not paused it with the control under the figure. Otherwise it rests on its finished frame: both rings drawn, every station named, the marker parked on "record".

**three.js leaves the bundle.** The figure is inline SVG with the Web Animations API, built from tokens, so it holds in both themes with no second palette. Its box has a fixed aspect ratio, so mounting it shifts nothing.

### The mark and the wordmark

**The meaning stays; the drawing gets exact.** 017's mark is a closed inner ring, the loop a machine re-runs, inside an open outer one, the study with people that replichai never simulates. That is the product's thesis and it stays. What changes is how "open" is drawn: one deliberate gap in a single arc instead of six dashes. A ring with one gap reads as unfinished. A ring of dashes reads as busy. The radius ratio (5 : 9.25) is 017's, so the mark, the hero figure and the favicon share one geometry.

**Two colors, ink and azure.** The outer ring is ink, `#0d0d0d` in light and `#ececec` in dark, like every Latere mark. The inner ring is the product azure, `#2369bd` and `#78b1ed`, latere-ui's registered value. The crimson leaves the favicon.

**The wordmark is drawn in the family's face.** "ReplicHAI" is set as outlines of Instrument Serif italic, which is what latere-ui's `.replichai-brand` sets the name in on every other Latere site. 017's rule that the wordmark depends on no font holds: the outlines are SVG paths, so nothing waits on a download. The `Replic`/`HAI` seam stays as a group inside the drawing, with its optical gap.

**The favicon follows the platform's.** It is an SVG with a `prefers-color-scheme` style, so the ink ring swaps for a dark browser tab. The rasters (`apple-touch-icon.png`, `og.png`) are regenerated from the same source by `scripts/brand.mjs`.

### In the app: the same hand

A session reads like a paper's results section. The verdict's outcome comes first, in words, in the serif, and then its per-component table. The rate is the table's footnote. The findings tables (the boundary, the decisions the paper left unstated, the open-science scorecard, what the authors released) are booktabs tables with captions. A mismatch is a row with its two numbers side by side, never a colored strip.

The registry is a reference list, set as the landing's is: a numbered citation, the reproduction's title, and who signed it. A public profile reads like a researcher's publication list.

Settings, the wallet and the admin console use the platform's sectioned form: a title and description on the left and the controls on the right. Money is tabular, and a statement is a booktabs table.

### The console: the platform's shell

The console moves to the platform's shell:

- The rail sits on the page ground with a hairline on its right edge, instead of floating the content in an inset card.
- The top bar is 56px with a hairline under it.
- Rail rows are 36px with an 8px radius. The current page is shown by a tint and weight, never by a colored edge.
- Page titles are serif.
- Tables are 13px with 40px rows, a sentence-case 12px head in `--fg-2`, and hairline separators.
- Status is a dot and a word.
- Buttons stay capsules at 32px and 28px, which is what latere-ui's console buttons are.
- Inputs are 32px with the control radius.

The mechanisms are untouched: 030's drawer and breakpoint, the fold and its persistence, the tooltips on a folded rail, and 034's two evidence cards and filter row. Those mechanisms were sound. Only their paint changes.

## What stays

Every guarantee the browser suite holds survives, and only the measures that encode the old look change:

- 029's sticky sign-in at the top and the bottom, on both viewports;
- the reduced-motion resting state;
- the walkthrough's driver and its stand-down at 900px;
- the live strip's absence on an empty registry;
- the example marker and the unresolvable key;
- the shared footer with no language menu;
- the `next` round trip.

`AuthUnavailable` and `NoAccess` keep their centered card (017).

## What this changes in earlier specs

These are recorded here rather than edited into the older files. When this spec is built, each older spec gains a one-line pointer to this one.

- **017:** the six-dash outer ring becomes one open arc, and the crimson leaves the favicon. The wordmark stays font-independent, now as outlines of the family's face.
- **029:** the three drawn figures become one hero figure, the walkthrough, and the verdict table. Their meaning is unchanged.
- **033:** the WebGL scene, its chunk, and the hero slab are gone. Its criterion 3 (degrade when WebGL fails) and criterion 4 (`three` in its own chunk) retire with the scene. The slab-ground assertion retires with the slab.
- **034:** the inset card, the deep-ground rail and the pill segmented control go. The primitives move to the platform's.
- **040:** the light and dark `--slab-*` material is gone, because there are no slabs. The footer wrapper stays. The faces move from `swap` to `block`, for the reason under Type.

## Acceptance criteria

1. `<html>` carries `data-design="ink"`, the app imports `latere-ui/ink`, and `styles.css` declares no ground, text or border color of its own.
2. Inter and Instrument Serif load from `/fonts/`, and no rendered text falls back to a system face. The browser suite checks `document.fonts` after load.
3. No element sets a monospace face or `text-transform: uppercase` on a label, eyebrow, table head, badge or rail group. A test walks the landing and the 15 screens `mobile.test.mjs` sweeps.
4. No element has a colored `border-left` used as an accent. Selection and the current page are shown by a tint.
4a. No verdict, on the landing or in the app, sets its overall rate larger than the table it is derived from.
5. Every text tone on the landing and the projects screen reaches 4.5:1 against the surface it sits on, in both themes, and every focus ring and control edge reaches 3:1.
6. Every control reachable by Tab shows a visible focus ring.
7. The hero figure is inline SVG with no canvas and no WebGL, and `three` is not in `package.json`. Its box reserves its aspect ratio before mount, so the layout shift it causes is 0.
8. The figure runs only in view, in a visible tab, without reduced motion and not paused. Under reduced motion it renders its finished frame and no animation runs. The pause control toggles it and says which state it is in.
9. The mark renders at 16, 24, 48 and 160px in both themes. Its outer ring has exactly one gap, and its inner ring is closed. The favicon swaps its ink for a dark tab.
10. The wordmark is SVG with an accessible name of "ReplicHAI", and it sets no font.
11. Every guarantee in "What stays" still holds, at 1440×900 and 390×844.
12. The whole browser suite, the unit tests and the repository's full check (`make check`, which runs `go tool lateregate`) pass.
