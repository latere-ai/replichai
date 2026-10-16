---
title: "The console, rebuilt from the design"
status: built
---

# 034 — The console, rebuilt from the design

**Restyled by [047](047-set-like-the-work-it-audits.md).** The inset card and the deep-ground rail stay; the primitives inside the card, and every label, take the platform's ink and type.

**Status:** built. Every acceptance criterion holds; the whole browser suite passed at both viewports (17 files then; 21 today, the rest added by 040 and 043), and the wallet, settings, registry and trash screens were screenshotted by hand to check what the shared primitives did to screens the design does not show.

Four things came out of the build. `.legend-swatch.empty` also matched the app's own `.empty` no-content block and inherited its padding, which drew a 35×70px box where a 7px swatch belonged; a class name is an interface, and `empty` was already taken. The old `.row-owner`/`.row-modified` rules were fixed column widths with a hide-below-760px, which is wrong once they ride the title's meta line; they are gone rather than overridden. `matches` collided with the transcript module's own import and silently rebound the transcript's event filter, which typechecking caught and nothing else would have. And three tests encoded values the design changed (the folded rail's 56px, the topbar's 52px, and the spend tile's container), each updated with the reason, and in every case the guarantee was kept rather than the number.

**Scope:** the signed-in console takes the same design pass 033 gave the landing. The shell is restyled: the rail loses its border and sits on the deep ground, and the content is an inset card floating on it. The shared primitives every screen is built from (the card, the button, the icon button, the segmented control) move to the design's radii and fills. The projects screen is rebuilt: three counters that restated the list become two cards that decompose the one number this product exists to produce, owner and date leave the column grid for the title's meta line, and the room that frees goes to a **Published** column and a filter row. Frontend only. No route, no schema.

**Depends on:** [033](033-the-landing-rebuilt-from-the-design.md) (the same design system, applied to the front door), [030](030-the-app-on-a-phone.md) (the drawer, the breakpoint, and every guarantee at 390px), [018](018-one-list-of-projects.md) (the one merged list and its owner column), [021e](021e-the-project-rail.md) (the windowed spend), [020](020-download-the-whole-project.md) (what a download is offered on).

## Problem

The console works and looks like a different product from the landing. A reader who arrives on the landing, signs in, and lands on the projects list crosses a visual seam in the middle of the same session.

Underneath that there is a content problem the design also fixes. The projects screen opens with three counters (projects, sessions run, spend), and two of the three are the list below them, counted. A reader learns nothing from "2 projects" while looking at two projects. The number that is missing is the one the product exists to produce: how much of this work has produced evidence somebody else can take.

## Decisions

**The rail sits on the deep ground and the content is an inset card.** The rail loses its right border and its surface fill; it sits directly on `--bg-deep`, and `.main` becomes a card inset by 8px with a 20px radius, a hairline, and a shadow. The seam between navigation and content stops being a line and becomes a depth. Below the phone breakpoint the inset is dropped entirely: a 390px screen has no room to spend on a margin, and the drawer already separates the two.

**The shared primitives move, and that reaches every screen.** The card goes to an 18px radius, the button and the segmented control become pills, and the icon button drops its border and fill for a hover wash. These are used on every screen in the console, so this is the change that makes the console one system rather than two. It is also the change with the widest blast radius, and it is verified only where there is a test or a screenshot: the browser suite covers the projects list, the project screen, the wallet card, the transcript, the admin console, the trash, the registry, and the phone.

**The primary button is ink, not brand.** The same decision 033 made on the landing, for the same reason: the brand hue marks what an operator acts on *within* a screen, and the one control that starts work should be the darkest thing on the bar rather than compete with every link under it. It inverts correctly in dark, because it is `--fg-1` on `--bg`.

**Three counters become two cards, and neither is a bare rate.** *Published evidence* says how many sessions committed a repository, out of how many were run, across how many projects: a count, a denominator, and a bar, never a percentage on its own. *What the credits buy* says the wallet balance, what a session draws on, and the windowed spend. Nothing is lost in the trade: the project count moves to the rail's Projects badge and the first card's "across N projects", the session total becomes the first card's denominator, and `spend_30d` keeps its own row with its window named.

**A sum over an absent field is not zero.** `published_sessions` and `spend_30d` are both optional (a server that could not compute one omits it), so both are guarded the way 021e already guards spend: if no row carries the field, the card shows a dash, which claims nothing, rather than a confident zero, which claims the work produced nothing.

**Owner and date stop being columns.** They move into a meta line under the title (`owner · N sessions · age`), and that is what frees the width for the Published column to exist at all. The owner is still a link to their profile where there is one, and the age still carries the exact moment in its tooltip; what changes is where they sit, not what they say. A column that squeezes the title out is worse than one that leaves.

**The filter is a filter, not a split.** 018 removed the mine/shared tabs because which of the two a project was is not a fact the reader chose. The filter row this adds is the opposite: a search over the titles and owners already on screen, and three states (all, running, published) the reader picks and can unpick. It filters the list that is already loaded and fetches nothing, so it cannot disagree with what the rows say.

**Everything at 390px keeps working.** The phone is a requirement of this change, not a consequence of it: the drawer, the folded rail, the topbar's controls, the single-column list, and the title cell's width are all asserted at 390px today and are asserted at 390px after. The inset is dropped, the boundary cards stack, and the filter row wraps.

## Acceptance criteria

1. Every guarantee the browser suite holds today still holds, at 1440×900 and 390×844: the drawer and its dismissals, the fold and its persistence, the tooltips on a folded rail, the topbar controls on screen, the transcript's single column, and the project title cell at 390px.
2. The rail is 236px open and 64px folded, sits on the deep ground with no border, and `.main` starts at the rail's edge.
3. The projects list's columns line up: every row's cells agree with the heading and with every other row, within a pixel.
4. The two boundary cards read from the same data the list does, and each shows a dash rather than a zero when its field is absent from every row.
5. The filter narrows the rendered rows and restores them, and fetches nothing.
6. The windowed spend is still on the screen and still names its window.
7. No hardcoded user-facing string outside `i18n.ts`.
