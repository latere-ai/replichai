# 030 — The app on a phone

**Status:** built. Every acceptance criterion holds. `frontend/src/sidebar.ts` gains `useDrawer` beside the untouched `useSidebar`; `App.tsx` carries the scrim, the hamburger, the drawer's own close, and one `goto` that dismisses the drawer as it navigates; `styles.css` gains a single `@media (max-width: 760px)` section, "The phone". `frontend/test/mobile.test.mjs` sweeps all 15 signed-in screens in Chrome and was verified to fail with the phone block removed — 154px of content on every screen, three panes scrolling sideways, and controls ending at x=579, which is the defect as first measured. The browser suite is green (14 files) and the 124 unit tests still pass.

Three things came out of the build, each a correction to a decision above.

The head's control could not do two jobs. This spec said `.rail-fold-btn` would close the drawer below the breakpoint, which needs the code to know the width — and a window resized back to desktop with the drawer still open would have left that button dead. There are two controls now, and CSS shows exactly one, so there is no state to go stale.

Shrinking `.topbar-actions` moved the overflow rather than removing it. Its children are `flex: none`, so the box measured 64px while the controls inside it still ran to 443. What gives is what the bar carries — the appearance switcher leaves, the button drops its label — and the crumb, which already ellipsizes, absorbs the rest.

`overflow-wrap: anywhere` on a value slot broke text that fitted. `anywhere` lets the browser narrow a line box even where the word would have fitted, and the wallet's own statement rows came back as "8/3 /20 25" and "−$ 5. 00". `break-word` breaks only what cannot fit alone, which is the long address on a public profile and nothing else.

The project list went further than "fold two more columns": at 390 the five remaining tracks left ~120px for a paper title and the headings collided above it, so the row became two lines and the heading went with the columns it was naming.

**Scope:** the signed-in shell and every screen inside it, at phone width. Turns the fixed 236px rail into an off-canvas drawer below one breakpoint, lets the topbar's controls fit, and unwraps the multi-column panes that are clipped at 390px. Frontend only: no route, no API, no schema, no new screen. The landing has its own phone layout ([017](017-the-first-screen.md), rebuilt by [029](029-the-landing-is-a-place.md)) and is not touched.

**Depends on:** [006e](006e-frontend.md) (the shell and the design tokens), [013](013-the-address-bar-is-the-state.md) (navigation is the address bar, so opening a destination is what closes the drawer), [017](017-the-first-screen.md) (the one screen that already answers a phone, and the pattern the rest follow).

## Problem

`.shell` is a flex row and `.rail` is `width: 236px; flex: none`, with no shell-level media query anywhere in `frontend/src/styles.css`. On a 390px phone that leaves **154px** of content — measured, on every screen the app has:

```
== projects (/)                    rail=236 main=154
== registry (/registry)            rail=236 main=154
== wallet (/wallet)                rail=236 main=154
== console (/console)              rail=236 main=154
== project-sessions (/p/{id})      rail=236 main=154
== session-transcript (…/s/{job})  rail=236 main=154
   … 15 screens, all of them
```

That number is the whole defect, and everything else follows from it. In 154px:

- The topbar's controls run to **x=579** — the wallet pill and the "New project" button are past the right edge and cannot be tapped. `.topbar-actions` is `flex: none` on purpose (a comment says squeezing them is what pushed a button off-screen once), so on a phone they simply leave.
- Body copy sets three or four words to the line. The Admin screen's lead paragraph is twenty lines tall before the first control.
- `.detail-grid` (`1fr 340px`), `.tx-body` (`220px 1fr 300px`), `.rate-row` (five columns), and `.project-list` (six columns below 760px, still reserving 287px of fixed track) all keep their desktop tracks. The transcript's filter rail, event stream, and inspector are three columns inside 154px.
- None of this trips the usual overflow check. `.screen` is `overflow-y: auto`, which computes `overflow-x` to `auto` as well, so the spill is contained in a pane that scrolls sideways rather than in the document. `documentElement.scrollWidth` reads clean while nothing is legible.

The public profile at `/u/{handle}` renders outside the shell and is nearly right already; only its contact row is clipped.

## Decisions

**One breakpoint, 760px, and it is the app's.** The stylesheet already has 760 (the project list's columns), 820 (the findings head), and 900 (the landing). 760 stays the phone breakpoint and every rule this spec adds uses it; the other two are about their own screens and are left alone. 768px — an iPad in portrait — keeps the rail.

**The rail becomes a drawer, not a narrower rail.** Below 760px `.rail` is `position: fixed` and translated off the left edge, so `.main` gets the full viewport; a scrim and a hamburger in the topbar are what bring it back. Folding it to icons was the cheaper option and is rejected: the folded rail still spends 52px of 390, and it names its destinations only through `data-tip`, which is a hover affordance a touch screen does not have. A phone would get five unlabelled icons.

**The drawer is state, the fold is a preference, and they do not touch.** `sidebar.ts` says it plainly — *"There is no media query here on purpose: the rail folds when it is asked to and at no other time, so a width the operator chose is never overridden by one the window happened to be."* That holds. `useSidebar` is unchanged; a new `useDrawer` holds whether the drawer is open, in memory only, because a drawer standing open is not something to remember across reloads. Below the breakpoint CSS neutralises `.rail-fold` outright: a drawer that has room for labels shows them whatever the desktop preference says.

**Opening a destination closes the drawer.** A drawer over the screen you were reading is a modal, and navigating is the act that dismisses it. Escape and a tap on the scrim do the same. This is why the dependency on 013 is real: every destination in the rail is a `setView`, so one wrapper around the navigate call covers all of them.

**The head's one control does one thing per width.** `.rail-fold-btn` already sits at the head's right edge and is the only control the rail keeps when folded. Below the breakpoint it closes the drawer instead of folding it, and takes the matching label. No second button, and nothing new in the rail's markup.

**The topbar sheds what is duplicated elsewhere, and shrinks what is not.** Below 760px the theme switcher leaves the bar — it is on `/settings` as well (`App.tsx:1418`), so nothing becomes unreachable — and "New project" keeps its icon and drops its label. `.topbar-actions` may shrink at this width; the crumb, which already ellipsizes, yields first.

**A pane that is columns because it is wide becomes rows.** `.detail-grid`, `.tx-body`, and `.findings-head` stack. `.project-list` drops to title + status below 500px; the row's own chevron stays, because tapping the row is how it opens. `.rate-row` and the ledger keep their columns inside a container that scrolls sideways — a table of numbers read across is not improved by stacking it, and it is already `overflow-x: auto`.

**Scrolling is the viewport's height, not the window's.** `.shell` is `height: 100vh`, which on mobile Safari is the tall viewport behind the address bar, so the rail's foot and the screen's last row sit under browser chrome. It becomes `100dvh` with the `100vh` kept above it as the fallback.

**Control sizing is out of scope.** `.seg-btn` computes to 24px tall and `.icon-btn` to 30px, both under the 44px touch guidance. They are reachable and tappable; raising them changes the app's control system on every width, and that is a different spec than making the phone work.

## Acceptance criteria

1. At 390×844, on every screen in the shell, `.main` is the full viewport width and `.rail` contributes nothing to the flow.
2. At 390×844 the drawer is closed on arrival; a hamburger in the topbar opens it; the scrim, Escape, the head's control, and opening any destination each close it.
3. At 390×844 the drawer shows its labels, whatever `localStorage.sidebar` holds — and reading or writing that key is not how the drawer opens or closes.
4. At 1440×900 nothing changes: the rail is 236px and in flow, the fold still folds and still persists, and no hamburger or scrim is in the DOM's flow.
5. At 390×844 every control in the topbar has its bounding box inside the viewport.
6. At 390×844 no element that is not inside its own `overflow-x` container extends past the right edge, on any of the 15 screens — asserted on both `documentElement` and `.screen`, since the pane's own sideways scroll is what hid this.
7. `.detail-grid`, `.tx-body`, and `.findings-head` are one column at 390px, and the project list shows a legible title.
8. A browser test (`frontend/test/mobile.test.mjs`, wired into `bun run test:browser`) drives 1, 2, 3, 5, 6, and 7 in Chrome against a mocked API across all 15 screens, and fails without the change.

## Not in scope

44px touch targets; a bottom tab bar; the landing (017 owns it); the public profile beyond its one clipped row; gestures — the drawer opens by tap, not by swipe; a second locale (`Lang` is `'en'` only, `i18n.ts:8`).
