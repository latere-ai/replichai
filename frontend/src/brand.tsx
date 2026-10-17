// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The identity: one drawn mark and one drawn wordmark, used wherever the
// product names itself. It is kept apart from `icons.tsx` because an icon
// inherits `currentColor` so the same glyph can sit in a button and a muted
// row, while the mark owns its colors and must look the same in every context.
//
// The mark depends on no font. The wordmark is set in Inter, which the app
// ships and preloads, so it renders in the face it was drawn for.

// The mark draws the boundary the product defends. The inner loop, the
// algorithm, model or analysis pipeline, is mechanically re-runnable, so it is
// a closed ring in the product's azure. The outer loop is the study with human
// participants, which replichai never simulates, so it is one arc with one
// deliberate gap. A single gap reads as unfinished, which is the claim; a ring
// of dashes would read as a loading indicator.
//
// The gap is centered up and to the right, away from the wordmark, so the
// opening never reads as a letter beside the name.
export const MARK_GAP_AT = -48
export const MARK_GAP = 52

// markArc is the outer loop's path: a circle of radius r about (cx, cy) with
// the gap cut out, drawn clockwise from one side of the gap to the other.
export function markArc(cx: number, cy: number, r: number, gapAt = MARK_GAP_AT, gap = MARK_GAP) {
  const at = (deg: number) => {
    const a = (deg * Math.PI) / 180
    return `${(cx + r * Math.cos(a)).toFixed(3)} ${(cy + r * Math.sin(a)).toFixed(3)}`
  }
  return `M${at(gapAt + gap / 2)}A${r} ${r} 0 1 1 ${at(gapAt - gap / 2)}`
}

// Optical sizes. One drawing at every size is either too thin to survive in a
// browser tab or too heavy to look exact at the size of a figure, so the
// strokes thin as the mark grows, and the inner ring stays the heavier of the
// two at every size because it is the half the product stands behind.
const OPTICAL = [
  { upTo: 20, outer: 2.2, inner: 2.4, ri: 4.6, ro: 9.2 },
  { upTo: 32, outer: 1.8, inner: 2.1, ri: 4.75, ro: 9.35 },
  { upTo: 64, outer: 1.4, inner: 1.7, ri: 4.85, ro: 9.45 },
  { upTo: Infinity, outer: 1.05, inner: 1.3, ri: 4.9, ro: 9.55 },
] as const

export function Mark({ size = 24, className }: { size?: number; className?: string }) {
  const o = OPTICAL.find((s) => size <= s.upTo) ?? OPTICAL[OPTICAL.length - 1]
  return (
    <svg
      className={className ? `mark ${className}` : 'mark'}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden="true"
      focusable="false"
    >
      <path className="mark-outer" d={markArc(12, 12, o.ro)} strokeWidth={o.outer} />
      <circle className="mark-inner" cx="12" cy="12" r={o.ri} strokeWidth={o.inner} />
    </svg>
  )
}

// Wordmark sets the name as one word, in the app's sans, with a hairline gap at
// its seam between `Replic`, for replication, and `HAI`, for human-AI, the
// lineage the `open-hai` forge account carries. The seam is spacing, never
// color.
export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={className ? `wordmark ${className}` : 'wordmark'}>
      Replic<span className="wordmark-seam">HAI</span>
    </span>
  )
}

// LatereMark is the identity of the provider behind the sign-in button, not
// replichai's own. The credential comes from latere, and the button that asks
// for it should look like the place it is going. The paths are the canonical
// mark from `latere-ui`'s `LatereLogoMark`, unchanged. Only the container
// differs: that one is sized into a square and letterboxes itself, while this
// one sits inline in a line of text and is sized by its height.
//
// It fills with `currentColor`, so it is white inside the brand button and ink
// anywhere else, and it needs no second asset for dark mode.
const LATERE_RATIO = 736 / 425

export function LatereMark({ height = 11 }: { height?: number }) {
  return (
    <svg
      width={Math.round(height * LATERE_RATIO)}
      height={height}
      viewBox="147 279 736 425"
      fill="currentColor"
      aria-hidden="true"
      focusable="false"
    >
      <g transform="translate(0 1024) scale(0.1 -0.1)">
        <path d="M7281 7439 c-263 -25 -575 -124 -883 -280 -385 -196 -764 -463 -1133 -799 -163 -148 -575 -562 -702 -704 -315 -353 -539 -670 -638 -905 -19 -45 -35 -87 -35 -93 0 -6 22 23 48 63 168 256 665 790 1042 1120 638 557 1244 947 1735 1115 304 105 506 139 760 131 175 -6 239 -17 385 -63 103 -32 144 -52 238 -117 225 -155 371 -428 402 -751 35 -378 -122 -885 -405 -1311 -209 -314 -531 -641 -865 -882 -505 -364 -1124 -588 -1747 -633 -196 -14 -423 -1 -648 36 -166 28 -381 81 -464 113 -82 33 -73 17 15 -27 387 -193 909 -283 1409 -242 878 73 1740 531 2341 1245 348 413 589 914 670 1390 23 136 24 434 1 566 -68 393 -280 698 -607 872 -244 130 -585 188 -919 156z" />
        <path d="M3790 7343 c-199 -13 -403 -45 -550 -84 -738 -199 -1279 -609 -1578 -1197 -73 -143 -122 -285 -159 -457 -24 -118 -27 -149 -26 -330 0 -175 3 -215 26 -321 59 -279 177 -525 353 -734 325 -388 794 -609 1348 -637 313 -15 763 72 1096 212 262 110 529 267 730 428 99 80 286 263 365 357 57 68 155 210 155 224 0 4 -43 -32 -96 -78 -286 -251 -789 -554 -1129 -679 -629 -232 -1234 -231 -1698 3 -127 64 -211 123 -311 219 -217 210 -357 460 -422 761 -15 68 -19 127 -19 290 0 186 3 215 27 315 171 719 788 1271 1658 1484 488 119 948 121 1424 6 55 -13 102 -23 103 -21 6 6 -184 85 -277 115 -274 89 -728 145 -1020 124z" />
        <path d="M4355 6874 c-431 -32 -757 -119 -1081 -288 -453 -236 -748 -562 -860 -951 -87 -303 -41 -671 114 -915 170 -269 431 -430 787 -487 140 -22 438 -14 589 16 141 29 289 77 402 133 93 46 209 116 203 123 -2 1 -44 -11 -94 -27 -218 -74 -505 -110 -709 -90 -268 27 -449 86 -641 209 -364 235 -484 682 -295 1099 207 458 718 825 1385 994 444 113 807 127 1130 43 13 -3 17 -2 10 5 -33 32 -307 102 -480 122 -100 11 -382 20 -460 14z" />
        <path d="M7115 6809 c-463 -70 -919 -303 -1510 -775 -518 -413 -995 -938 -1203 -1328 -24 -43 -41 -82 -40 -87 2 -4 38 36 80 89 256 323 656 710 1038 1008 596 465 1139 743 1568 805 118 17 317 7 412 -21 225 -64 378 -206 446 -412 25 -75 28 -98 28 -228 0 -105 -5 -169 -18 -230 -80 -368 -281 -709 -631 -1069 -258 -265 -535 -468 -865 -632 -531 -264 -991 -378 -1539 -380 -210 0 -213 -9 -13 -36 168 -22 500 -22 675 1 333 43 709 152 1027 299 169 77 407 220 570 340 566 418 941 947 1065 1502 25 112 31 372 10 480 -60 317 -255 543 -549 634 -152 47 -389 64 -551 40z" />
        <path d="M6770 6241 c-199 -43 -425 -141 -623 -271 -60 -40 -107 -74 -104 -76 2 -2 42 14 88 35 122 57 334 127 455 151 334 64 544 -32 595 -273 19 -88 6 -238 -30 -346 -40 -118 -133 -301 -218 -428 -198 -297 -501 -598 -818 -811 -264 -177 -470 -272 -845 -391 -59 -18 127 -5 260 19 226 41 432 114 693 247 647 330 1105 859 1233 1424 12 50 17 120 18 214 0 121 -3 149 -23 205 -54 155 -154 253 -305 299 -88 27 -253 28 -376 2z" />
        <path d="M5346 5248 a294 294 0 1 0 588 0 a294 294 0 1 0 -588 0z" />
      </g>
    </svg>
  )
}

// Lockup is the pair at a fixed optical relationship, so no caller has to
// re-derive how big the mark is beside the name.
export function Lockup({ size = 22, className, by }: { size?: number; className?: string; by?: string }) {
  return (
    <span className={className ? `lockup ${className}` : 'lockup'} style={{ fontSize: size * 0.82 }}>
      <Mark size={size} />
      <Wordmark />
      {/* The maker, after a hairline, as the platform sets "Latere | Platform". */}
      {by && <span className="lockup-by">{by}</span>}
    </span>
  )
}
