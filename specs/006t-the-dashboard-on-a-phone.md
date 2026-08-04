# 006t — The dashboard on a phone

**Status:** built
**Scope:** the operator dashboard adapts to a narrow viewport — the rail folds into a drawer, the top bar fits, and the width-assuming layouts (the project detail's two columns, the transcript's three) collapse to one. A child of [006e](006e-frontend.md); a pure client change, no API.
**Depends on:** [006e](006e-frontend.md) (the shell it reshapes), [006s](006s-reading-a-session-in-time.md) (the transcript viewer it stacks).

## Problem

The sign-in screen already reflows, but everything behind it was drawn for a desk. The shell is a fixed 236px rail beside the content, the top bar lays its breadcrumb and three actions out in a row, and the two deep screens assume horizontal room the beta never bounded — the project detail is `1fr 340px`, the transcript viewer is `220px 1fr 300px`. On a phone the rail eats two-thirds of the width and can't be put away, the top bar's actions push the breadcrumb off, and the three-column transcript overflows the viewport sideways. The app is reachable on a phone and unusable on one.

## Decisions

**The rail is a column on a desk and a drawer on a phone — one DOM, one breakpoint.** Below 820px the rail leaves the flow (`position: fixed`, translated off-screen) and a hamburger in the top bar slides it back over a scrim. There is no second navigation to keep in sync: the same `<nav class="rail">` is either in the layout or floating over it, decided entirely in CSS. The drawer's open state is the shell's only new React state (`railOpen`).

**Navigation closes the drawer.** Moving in the app is state, not a route (006e), so a `useEffect` on the current view closes the drawer whenever the view changes — a tap on a destination opens the screen it named rather than leaving the overlay on top of it. Tapping the scrim closes it too; the Escape key is out of scope for the beta.

**The top bar sheds width before it sheds function.** Under the breakpoint its padding tightens and the "New project" button keeps its `+` glyph but drops its label — the one action a thumb reaches most stays a target, just a smaller one. The breadcrumb already ellipsises; it simply gets a shorter ceiling. Nothing is removed, only compressed.

**The width-assuming grids fall to one column.** The project detail's side panel drops below its main column; the transcript's filter rail, stream, and inspector stack in reading order — search first, the stream in the middle, usage last — and the body scrolls as one piece instead of three. The desktop three-column geometry (and its layout regression test, which runs at 1440px) is untouched: the stack lives entirely inside the mobile media query.

## Acceptance criteria

1. Below 820px the rail is off-screen and claims no layout width; a hamburger in the top bar is visible and opens it; the scrim and any navigation close it — asserted in a headless browser at a phone viewport.
2. At a phone viewport the document does not scroll horizontally on any operator screen (projects, a project, a session transcript).
3. At desktop width the rail is a static column, no hamburger shows, and the transcript keeps its three columns — the 006s layout test still passes unchanged.
4. The "New project" action is reachable at every width (labelled on a desk, icon-only on a phone).

## Out of scope

A component library or breakpoint system — one `max-width: 820px` block against the existing hand-written sheet is enough (006e). Closing the drawer on Escape or on a swipe. Reflowing the admin console's tables beyond letting them scroll. A tablet-specific middle layout: the single breakpoint is desk-or-phone, and the phone rules are what a tablet gets.
