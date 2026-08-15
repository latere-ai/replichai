# 032 — The loop has depth

**Status:** designed.

**Scope:** replace the landing hero's flat two-circle diagram with one responsive, physically suggestive orbital instrument over an original editorial research-world image. Frontend only: SVG structure, one optimized image asset, pointer-driven perspective, CSS motion, and browser verification.

**Depends on:** [029](029-the-landing-is-a-place.md) (the landing, its loop figure, and its reduced-motion contract), [017](017-the-first-screen.md) (the closed-inner/open-outer visual doctrine).

## Problem

The first figure on the landing carries the product's strongest idea, but it presently reads as a chart: one blue circle, one dashed red circle, and a runner rotating on the blue stroke. Its geometry is correct and its movement is technically animation, yet neither communicates a mechanism with mass, energy, or response. The hero therefore feels flatter than the product it introduces.

The improvement cannot trade meaning for spectacle. The inner loop must remain visibly closed and continuously runnable; the outer loop must remain visibly broken and must never appear to complete. Motion must not become a generic loading spinner, pointer input must remain decorative rather than required, and a reader who requests reduced motion must get the complete figure without movement.

## Decisions

**The figure becomes an orbital instrument, not a new logo.** `LoopFigure` keeps the same inner/outer-loop semantics and legend. A perspective stage holds an inline SVG whose planes are visibly tilted: the inner loop is a closed glass-like track with a luminous runner and a short wake; the outer loop is six separate capsules at a different inclination. A dim core, soft cast shadow, orbital nodes, and restrained highlights supply depth without WebGL.

**One physical image grounds the mechanism in research.** The hero figure uses an original, art-directed still life of papers, glass, graphite, and measuring tools as its backdrop. It contains no people, customers, logos, UI, or readable research claims, so it cannot be mistaken for evidence or a testimonial. A directional mask and blue/coral colour treatment let the animated instrument emerge from the photograph while the figure remains legible in both themes. The committed asset is a web-optimized WebP under 150 KB; it is decorative and has no alt text.

**The two loops obey different physics.** The inner assembly rotates continuously at a steady rate: mechanical work can be re-run. The outer assembly precesses slowly in the opposite direction while its six gaps remain structural: a human study cannot be completed by the machine. Small nodes travel only on the closed inner track. No element crosses the boundary between the two planes.

**The instrument responds to proximity, not clicks.** Pointer movement over the figure maps its position to bounded CSS custom properties (`--loop-rx` and `--loop-ry`), tilting the stage by at most seven degrees around its resting pose. The component uses one `requestAnimationFrame` slot so pointer events never cause React renders. Pointer exit eases back to rest. The figure has no controls and remains `aria-hidden`; the adjacent heading, caption, and legend carry its meaning.

**CSS is the renderer.** The existing inline SVG gains grouped layers, gradients, masks, and filter primitives; CSS supplies perspective, transforms, opacity, and keyframes. There is no canvas, WebGL runtime, animation dependency, or fetched image. The SVG remains crisp in either theme and at phone width.

**Motion remains opt-out at the system level.** All automatic rotations, pulses, wake movement, and transform transitions live under `prefers-reduced-motion: no-preference`. The base CSS is the finished, readable pose. Under `reduce`, pointer handlers do not change the pose, all animation names compute to `none`, and the open/closed distinction remains visible.

**Performance has a hard boundary.** Only `transform`, `opacity`, and SVG stroke offsets animate. Filters are static and constrained to the figure. The stage declares transform preservation and the moving assemblies declare `will-change: transform`; no layout property changes per frame.

## Acceptance criteria

1. The hero renders one perspective stage containing a closed inner track, one luminous runner and wake, six physically separate outer segments, and at least three depth cues (core, cast shadow, highlights, or nodes).
2. With ordinary motion preference, the inner and outer assemblies have different, non-`none` animations and the runner moves on the inner track; no outer segment animation changes the segment gaps.
3. Moving the pointer across the figure changes bounded tilt custom properties without changing React state, and leaving restores the neutral pose.
4. With `prefers-reduced-motion: reduce`, every element in the orbital figure computes to `animation-name: none`, the stage has no transform transition, and pointer movement leaves the neutral pose unchanged.
5. At 1440×900 and 390×844, the figure fits without horizontal overflow; its inner and outer strokes paint in distinct colours in both light and dark themes.
6. The original editorial backdrop loads at intrinsic width of at least 1200px, transfers under 150 KB, and remains decorative; the landing contains no invented customer or research claim.
7. `frontend/test/landing.test.mjs` drives the figure, image load, pointer response, reduced-motion state, semantics, and both viewport sizes end to end; the new assertions fail against the flat figure.

## Not in scope

The compact product mark in `brand.tsx` stays flat and unchanged; a 22px rail mark cannot carry this depth legibly. The other landing figures, copy, page structure, routes, and API calls stay unchanged. There are no stock customer portraits, invented logos, sound, click interaction, scroll capture, device-orientation input, or full-page particle field.
