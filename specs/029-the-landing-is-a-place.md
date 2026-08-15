# 029 — The landing is a place

**Status:** designed, not built.

**Scope:** the signed-out landing stops being a door and becomes a page a researcher can read: a sticky header that carries the way in, the claim, three drawn figures that show what the product does rather than assert it, a strip of real reproductions pulled from the now-open registry, and a close. Frontend only — no route, no API, no schema. Supersedes 017's two-column fold.

**Depends on:** [028](028-the-registry-opens.md) (without it there is nothing to link to and no data to show), [017](017-the-first-screen.md) (the mark, the lockup, the copy, and the fold this replaces), [013](013-the-address-bar-is-the-state.md), [006q](006q-sign-in-is-open-budget-is-not.md) (what the copy may promise).

## Problem

017 built the landing as a fold: two columns, sign-in guaranteed above 900px, and — by its own decision — no link anywhere, because the registry was closed. It says what the product is in six sentences and then stops. That is the right shape for a door and the wrong shape for the audience: a researcher deciding whether to trust a reproducibility claim wants to see one, and the strongest thing this product has to show is a verdict somebody signed and a transcript anybody can take apart.

028 makes those readable. This spec spends that.

## Decisions

**The fold goes, and the sign-in moves into a sticky header.** 017's criteria 3 and 4 assert the sign-in button's bounding box lies above 900px and 844px, and `frontend/test/landing.test.mjs` drives exactly that in Chrome. A page that scrolls cannot hold them. The replacement guarantee is stronger and survives any page length: a sticky header carrying the lockup and the **Sign in** button is visible at every scroll position, on both viewports. 017's fold criteria are recorded as superseded rather than quietly deleted, and the landing test is rewritten against the new guarantee.

**The figures show the mechanism; they do not decorate it.** Three, each earning its place by explaining something the copy can only assert:

1. **The two loops.** The mark, at figure scale, animated: the inner ring closes and traces a full cycle — implement, run, record — while the outer dashed ring stays open and never completes. The product's one boundary, drawn. It is the same geometry as the mark in the header, so the identity and the argument are one thing.
2. **What a session does.** A four-step walk — the paper, what the authors released, the re-run, the verdict and its transcript — advancing on scroll, each step naming the artifact it produces rather than the verb it performs.
3. **Anatomy of a verdict.** The claim / evidence / verdict triple pulled apart: the agent's claim, the transcript that evidences it, and the manifest the system authored over both, with the signer's name beside it. This is the figure that says why a number here is checkable and a number in a paper often is not.

All three are inline SVG built from the design tokens, so they hold in light and dark, need no image asset, and cannot go stale against the palette. No animation library: CSS transitions, SVG `stroke-dasharray`, and one `IntersectionObserver` for the scroll-driven steps.

**Motion is opt-out at the system level.** Every animation is inside a `@media (prefers-reduced-motion: no-preference)` block, and the reduced-motion rendering is the finished state of each figure — not a still frame of an unstarted one. A reader who has asked for less motion gets the whole argument, statically.

**The brand hue stays spent on what an operator can act on.** `styles.css:1-9` reserves it, and 017 kept it off the wordmark for that reason. The figures are drawn in the neutral ramp with the publish green (`--pub`) marking published state where they show it. The one brand-coloured thing on the page is the mark and the sign-in button — which is exactly the thing to act on.

**The live strip is progressive enhancement, and the page is complete without it.** Below the figures, up to six recent public reproductions read from `GET /public`: title, signer (linking to their `/u/{handle}` page), and verdict state, each linking into the project. The call fails or returns nothing → the section is not rendered at all, and the page reads as designed. No skeletons that never fill, no "0 reproductions" claiming the community is empty. A **Browse the registry** link sits under it, and — because 028 opened it — is also present when the strip is absent.

**Copy stays the copy.** 017's claim, lead, and three points are the argument and are reused verbatim; this spec adds section headings, the figure captions, the strip heading, and the closing line. Every string is in `i18n.ts` under `auth`; `Lang` is `'en'` only, so there is no second locale.

**The landing is what a signed-out visitor gets at every address that is not public.** 028 decides which addresses render the shell signed out; everything else lands here, and the header's sign-in still carries `next={current path}`, so a deep link into a private project still round-trips through sign-in to where it pointed.

**The failure screens keep the centred card.** `AuthUnavailable` and `NoAccess` are unchanged, for 017's reason: somebody whose session just broke is not an audience for a pitch.

## The copy

| Slot | Text |
|---|---|
| Header action | Sign in |
| Claim, lead, three points | 017's, verbatim |
| Figure 1 heading | One loop closes. One never will. |
| Figure 1 caption | The algorithm re-runs. The study with people does not, and nothing here pretends it does. |
| Figure 2 heading | What a session actually does |
| Figure 2 caption | Every step leaves an artifact you can open: the release it found, the repository it wrote, the transcript of how. |
| Figure 3 heading | A verdict you can take apart | 
| Figure 3 caption | The agent claims. The transcript evidences. The system authors the verdict, and a named researcher signs it. |
| Strip heading | Published reproductions |
| Strip link | Browse the registry |
| Close | Read one before you sign up. Everything published here is open. |

## Acceptance criteria

1. At 1440×900 and 390×844, signed out, the sign-in button is visible at scroll top *and* after scrolling to the bottom of the page — the sticky-header guarantee, replacing 017's criteria 3 and 4.
2. All three figures render as SVG that paints, in light and dark, at both viewports.
3. With `prefers-reduced-motion: reduce`, every figure is in its finished state and no transition runs.
4. With `GET /public` answering six projects, the strip renders six links; with an empty answer or a failure, the section is absent and the registry link is still on the page.
5. The registry link and every strip link resolve to a screen a signed-out reader can read (028), asserted by following one.
6. The header sign-in href is `/login?next={current path}`, and the deep-link round trip from a private `/p/{id}` still holds.
7. `AuthUnavailable` and `NoAccess` render the centred card, unchanged.
8. Every string is in `i18n.ts`; `i18n-coverage.test.ts` passes.
9. 017 carries a drift note naming its superseded criteria, and `landing.test.mjs` asserts criterion 1 rather than the fold.
