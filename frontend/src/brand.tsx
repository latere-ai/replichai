// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The identity: one drawn mark and one drawn wordmark, used wherever the
// product names itself. It is kept apart from `icons.tsx` because an icon
// inherits `currentColor` so the same glyph can sit in a button and a muted
// row, while the mark owns its colors and must look the same in every context.
//
// Nothing here depends on a font. The wordmark is set in the family's face,
// Instrument Serif italic, the face latere-ui sets the product's name in on
// every other Latere site, but as outlines rather than as text: an identity
// that waits on a download can be missing, and one drawn as paths cannot.

// The mark draws the boundary the product defends (017). The inner loop, the
// algorithm, model or analysis pipeline, is mechanically re-runnable, so it is
// a closed ring, in the product's azure. The outer loop is the study with human
// participants, which replichai never simulates, so it is drawn open: one arc
// with one deliberate gap (047). It was six dashes until 047, and a ring of
// dashes reads as a loading indicator or a selection marquee; a ring with one
// gap reads as unfinished, which is the claim.
//
// The gap is centered up and to the right, away from the wordmark, so the
// opening never reads as a letter beside the name. The radius ratio is 017's,
// so the mark, the hero figure and the favicon are one geometry.
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

// The wordmark's outlines: "Replic" and "HAI" from Instrument Serif italic at
// 100 units, kerned, with two units of optical gap at the seam. `Replic` is for
// replication and `HAI` for human-AI, the lineage the `open-hai` forge account
// carries; the seam is spacing, never color. Regenerate rather than edit: the
// numbers are the face's, laid out by opentype.js.
const WORD_W = 353.7
const WORD_H = 94.8
const REPLIC = 'M43.8 75.2L43.8 75.2Q39.6 75.2 37 72.2Q34.3 69.2 34.4 63.8L34.4 63.8L34.5 52.7Q34.6 47.3 33.9 44.5Q33.2 41.7 31.3 40.7Q29.4 39.7 25.7 39.7L25.7 39.7Q23.5 39.7 22.5 40.5Q21.4 41.3 21 43L21 43L15.2 67.3Q14.7 69.4 15.3 70.2Q15.9 71 18.4 71.4L18.4 71.4L22.8 72Q24.2 72.1 24.2 73.1L24.2 73.1Q24.2 74.3 22.6 74.3L22.6 74.3L1.4 74.3Q0 74.3 0 73.3L0 73.3Q0 72.2 2 71.9L2 71.9L4 71.6Q6.2 71.3 7.1 70.6Q7.9 69.8 8.3 67.7L8.3 67.7L21.9 8.9Q22.4 6.8 21.9 6.1Q21.4 5.4 19.4 5L19.4 5L17.4 4.6Q16.1 4.3 16.1 3.5L16.1 3.5Q16.1 2.3 17.9 2.3L17.9 2.3L38.2 2.3Q44.1 2.3 48 4.4Q51.9 6.5 53.9 10Q55.8 13.5 55.8 17.7L55.8 17.7Q55.8 23 53.1 27.3Q50.4 31.6 46.1 34.6Q41.7 37.5 36.7 38.9L36.7 38.9Q36.1 39 36.1 39.4Q36 39.9 36.6 40.2L36.6 40.2Q39.4 41.7 40.4 44.4Q41.4 47.1 41.4 51.6L41.4 51.6L41.4 61.8Q41.4 67.3 42.6 69.4Q43.8 71.4 45.9 71.4L45.9 71.4Q46.7 71.4 47.3 71.1Q47.9 70.9 48.9 70.2L48.9 70.2Q49.7 69.5 50.4 69.8Q51 70.1 51 70.9L51 70.9Q51 72.3 48.9 73.8Q46.8 75.2 43.8 75.2ZM29.2 37.2L29.2 37.2Q34.2 37.2 38.6 34.5Q43 31.8 45.8 27.2Q48.5 22.6 48.5 16.9L48.5 16.9Q48.5 11.3 45.8 8Q43 4.8 36.2 4.8L36.2 4.8Q29.8 4.8 28.8 8.8L28.8 8.8L23.5 31.7Q22.2 37.2 29.2 37.2ZM65.8 75.2L65.8 75.2Q60.5 75.2 57.3 71.1Q54.1 66.9 54.1 59.3L54.1 59.3Q54.1 52.7 56.5 46.2Q58.8 39.7 62.6 34.4Q66.4 29.1 70.9 25.9Q75.5 22.7 79.8 22.7L79.8 22.7Q84.5 22.7 86.5 24.9Q88.6 27.2 88.6 31L88.6 31Q88.6 35.5 85.4 39.9Q82.3 44.4 76.6 47.9Q71 51.4 63.4 53.1L63.4 53.1Q61.2 53.6 61.1 55.6L61.1 55.6Q61 56.3 61 57.1Q61 57.8 61 58.5L61 58.5Q61 64.2 63 67.1Q64.9 70 68.3 70L68.3 70Q72.1 70 75.6 66.5Q79.2 63 81.6 56.6L81.6 56.6Q81.9 55.7 82.8 55.7L82.8 55.7Q83.4 55.7 83.6 56.2Q83.7 56.7 83.5 57.4L83.5 57.4Q80.3 66.2 75.8 70.7Q71.2 75.2 65.8 75.2ZM62.1 49.3L62.1 49.3Q61.9 50 62.4 50.4Q62.8 50.9 63.5 50.7L63.5 50.7Q69.7 49 73.9 45.6Q78.1 42.1 80.3 37.8Q82.4 33.5 82.4 29.4L82.4 29.4Q82.4 25.2 79.1 25.2L79.1 25.2Q76.2 25.2 72.8 28.6Q69.4 32 66.5 37.5Q63.6 43 62.1 49.3ZM98.1 94.8L80.1 94.8Q78.8 94.8 78.8 93.7L78.8 93.7Q78.8 92.7 80.4 92.5L80.4 92.5L81.8 92.3Q83.8 92 84.7 91.2Q85.7 90.3 86.2 88.1L86.2 88.1L99.5 30.7Q99.7 29.9 99.2 29.4Q98.8 29 98 29.5L98 29.5Q97 30 96.1 30.6Q95.2 31.1 94.3 31.7L94.3 31.7Q93.4 32.3 92.8 32.1Q92.2 31.8 92.2 31.1L92.2 31.1Q92.2 30.4 93.2 29.7L93.2 29.7Q96 27.8 99 26.4L99 26.4Q100.7 25.6 101.1 23.8L101.1 23.8L102.4 18.1Q102.9 16 105 16L105 16L106.8 16Q108 16 108.6 16.7Q109.2 17.4 108.9 18.7L108.9 18.7L108.2 21.6Q107.9 23.3 109.5 23L109.5 23Q111.8 22.7 113.9 22.7L113.9 22.7Q121.3 22.7 126 26.9Q130.7 31.1 130.7 39.8L130.7 39.8Q130.7 46.6 128.2 53Q125.7 59.3 121.6 64.3Q117.5 69.3 112.6 72.3Q107.7 75.2 102.8 75.2L102.8 75.2Q100.1 75.2 97.7 74.1L97.7 74.1Q96.2 73.4 95.9 74.9L95.9 74.9L92.9 88.1Q92.4 90.3 92.9 91Q93.5 91.7 95.4 92L95.4 92L98 92.4Q99.8 92.7 99.8 93.6L99.8 93.6Q99.8 94.8 98.1 94.8L98.1 94.8ZM106.6 28.8L98.7 62.8Q96.9 72.7 103 72.7L103 72.7Q106.5 72.7 110.1 69.8Q113.7 66.8 116.8 61.9Q119.9 56.9 121.8 50.7Q123.7 44.5 123.7 38L123.7 38Q123.7 31.3 120.8 28.3Q117.9 25.4 112.3 25.4L112.3 25.4Q107.3 25.4 106.6 28.8L106.6 28.8ZM140.1 75.3L140.1 75.3Q136.8 75.3 135.4 72.9Q134 70.4 135.3 64.8L135.3 64.8L148 10.3Q148.5 8.5 148 7.8Q147.6 7.1 146.2 7L146.2 7L143.2 6.7Q141.7 6.6 141.9 5.4L141.9 5.4Q142.1 4.5 143.3 4.3L143.3 4.3Q146.9 3.7 149.7 2.8Q152.5 2 154.2 0.6L154.2 0.6Q155 0 155.7 0L155.7 0Q156.9 0 156.6 1.5L156.6 1.5L141.8 64.9Q141.3 67.1 141.6 68.4Q142 69.6 143.5 69.6L143.5 69.6Q145.3 69.6 147.5 67Q149.6 64.3 152.3 56.9L152.3 56.9Q152.7 55.6 153.7 55.6L153.7 55.6Q155.2 55.6 154.5 57.4L154.5 57.4Q152.4 64.2 149.9 68.1Q147.3 72.1 144.8 73.7Q142.3 75.3 140.1 75.3ZM177.1 13.8L177.1 13.8Q175.2 13.8 174 12.4Q172.7 11.1 172.7 9.2L172.7 9.2Q172.7 6.5 174.4 4.9Q176 3.3 178.1 3.3L178.1 3.3Q180.1 3.3 181.5 4.5Q182.8 5.8 182.8 7.8L182.8 7.8Q182.8 10.6 181.1 12.2Q179.4 13.8 177.1 13.8ZM168.9 75.3L168.9 75.3Q165.6 75.3 164.2 72.9Q162.8 70.4 164.1 64.8L164.1 64.8L171.5 33.1Q172 30.9 171.6 29.6Q171.3 28.4 169.8 28.4L169.8 28.4Q168 28.4 165.9 31.1Q163.7 33.7 161 41.1L161 41.1Q160.6 42.4 159.6 42.4L159.6 42.4Q158.1 42.4 158.8 40.6L158.8 40.6Q161 33.7 163.5 29.8Q165.9 25.9 168.4 24.3Q170.9 22.7 173 22.7L173 22.7Q176.5 22.7 177.9 25.1Q179.3 27.6 178 33.2L178 33.2L170.6 64.9Q170.1 67.1 170.5 68.4Q170.8 69.6 172.3 69.6L172.3 69.6Q174.1 69.6 176.3 67Q178.4 64.3 181.1 56.9L181.1 56.9Q181.5 55.6 182.5 55.6L182.5 55.6Q184 55.6 183.3 57.4L183.3 57.4Q181.2 64.2 178.7 68.1Q176.1 72.1 173.6 73.7Q171.1 75.3 168.9 75.3ZM199.7 75.2L199.7 75.2Q194.2 75.2 190.6 70.6Q187 66.1 187 59L187 59Q187 52.3 189.2 45.8Q191.3 39.4 195 34.1Q198.6 28.9 203.1 25.8Q207.5 22.7 212 22.7L212 22.7Q216.8 22.7 220.1 25.3L220.1 25.3Q220.6 25.7 220.7 25.9Q220.8 26.2 220.6 27L220.6 27L217.9 40.1Q217.6 41.5 216.6 41.5L216.6 41.5Q215.5 41.5 215.3 40L215.3 40L214.8 32.9Q214.5 28.3 213.5 26.9Q212.4 25.5 210.4 25.5L210.4 25.5Q207.6 25.5 204.7 28.2Q201.8 31 199.4 35.6Q196.9 40.3 195.5 45.9Q194 51.6 194 57.4L194 57.4Q194 70 202.2 70L202.2 70Q206.1 70 209.3 66.4Q212.4 62.8 215.1 55.2L215.1 55.2Q215.4 54.2 216.5 54.2L216.5 54.2Q217.1 54.2 217.2 54.6Q217.3 55.1 217 56.4L217 56.4Q216 61.9 213.4 66.1Q210.8 70.3 207.3 72.8Q203.7 75.2 199.7 75.2Z'
const HAI = 'M238.4 74.3L220.4 74.3Q219 74.3 219 73.3L219 73.3Q219 72.2 221 71.9L221 71.9L223 71.6Q225.2 71.3 226.1 70.6Q226.9 69.8 227.3 67.7L227.3 67.7L240.9 8.9Q241.4 6.8 240.9 6.1Q240.4 5.4 238.4 5L238.4 5L236.4 4.6Q235.1 4.3 235.1 3.5L235.1 3.5Q235.1 2.3 236.9 2.3L236.9 2.3L254.9 2.3Q256.3 2.3 256.3 3.4L256.3 3.4Q256.3 4.5 254.5 4.7L254.5 4.7L252.1 5Q249.9 5.3 249.1 6.1Q248.3 6.9 247.8 9L247.8 9L242 34.1Q241.7 35.5 243.1 35.5L243.1 35.5L262.9 35.5Q264.3 35.5 264.6 34.1L264.6 34.1L270.4 9Q270.9 6.9 270.4 6.1Q269.9 5.3 267.9 5L267.9 5L265.7 4.7Q264.3 4.6 264.3 3.5L264.3 3.5Q264.3 2.3 266.2 2.3L266.2 2.3L284.2 2.3Q285.6 2.3 285.6 3.3L285.6 3.3Q285.6 4.4 283.6 4.7L283.6 4.7L281.6 5Q279.4 5.3 278.6 6Q277.8 6.8 277.3 8.9L277.3 8.9L263.7 67.7Q263.3 69.8 263.8 70.5Q264.2 71.2 266.2 71.6L266.2 71.6L268.2 72Q269.5 72.3 269.5 73.1L269.5 73.1Q269.5 74.3 267.7 74.3L267.7 74.3L249.7 74.3Q248.3 74.3 248.3 73.2L248.3 73.2Q248.3 72.1 250.1 71.9L250.1 71.9L252.5 71.6Q254.7 71.3 255.6 70.5Q256.4 69.7 256.8 67.6L256.8 67.6L263.2 39.9Q263.5 38.5 262.2 38.5L262.2 38.5L242.4 38.5Q240.9 38.5 240.6 39.9L240.6 39.9L234.2 67.6Q233.8 69.7 234.3 70.5Q234.7 71.3 236.7 71.6L236.7 71.6L238.9 71.9Q240.3 72 240.3 73.1L240.3 73.1Q240.3 74.3 238.4 74.3L238.4 74.3ZM284.4 74.3L269.9 74.3Q268.7 74.3 268.7 73.3L268.7 73.3Q268.7 72.4 270.3 72L270.3 72L271.9 71.7Q273.9 71.3 275.4 69.8Q276.8 68.2 278.1 65.4L278.1 65.4L308 2.9Q308.5 1.9 309 1.6Q309.5 1.3 309.9 1.3L309.9 1.3Q310.4 1.3 310.8 1.6Q311.1 1.9 311.1 2.9L311.1 2.9L312.2 66.3Q312.3 69.1 312.9 70.1Q313.5 71.2 315.4 71.7L315.4 71.7L316.7 72Q317.9 72.3 317.9 73.2L317.9 73.2Q317.9 74.3 316.2 74.3L316.2 74.3L299.7 74.3Q298.4 74.3 298.4 73.3L298.4 73.3Q298.4 72.2 300.4 72L300.4 72L302.4 71.8Q304.1 71.6 304.8 70.7Q305.4 69.8 305.4 67.6L305.4 67.6L305.2 52.1Q305.2 49.5 302.5 49.5L302.5 49.5L291.8 49.5Q289.2 49.5 287.9 52.1L287.9 52.1L281.4 65.5Q278.7 71.2 283.2 71.8L283.2 71.8L284.6 72Q286.1 72.2 286.1 73.2L286.1 73.2Q286.1 74.3 284.4 74.3L284.4 74.3ZM292.2 46.5L292.2 46.5L303.4 46.5Q305.1 46.5 305.1 44.8L305.1 44.8L304.9 17.5L304.4 17.5L291.3 44.8Q290.9 45.5 291.2 46Q291.5 46.5 292.2 46.5ZM335.8 74.3L318 74.3Q316.6 74.3 316.6 73.3L316.6 73.3Q316.6 72.2 318.6 71.9L318.6 71.9L320.6 71.6Q322.8 71.3 323.7 70.6Q324.5 69.8 324.9 67.7L324.9 67.7L338.5 8.9Q339 6.8 338.5 6.1Q338 5.4 336 5L336 5L334 4.6Q332.7 4.3 332.7 3.5L332.7 3.5Q332.7 2.3 334.5 2.3L334.5 2.3L352.3 2.3Q353.7 2.3 353.7 3.3L353.7 3.3Q353.7 4.4 351.7 4.7L351.7 4.7L349.7 5Q347.5 5.3 346.7 6Q345.9 6.8 345.4 8.9L345.4 8.9L331.8 67.7Q331.4 69.8 331.8 70.5Q332.3 71.2 334.3 71.6L334.3 71.6L336.3 72Q337.6 72.3 337.6 73.1L337.6 73.1Q337.6 74.3 335.8 74.3L335.8 74.3Z'

// Wordmark is sized by the font size it sits in, like the text it replaced:
// the drawing is one em of the face tall from its ascender to its descender,
// and its baseline sits on the surrounding text's baseline.
export function Wordmark({ className }: { className?: string }) {
  return (
    <svg
      className={className ? `wordmark ${className}` : 'wordmark'}
      viewBox={`0 0 ${WORD_W} ${WORD_H}`}
      role="img"
      aria-label="ReplicHAI"
      focusable="false"
    >
      <path d={REPLIC} />
      <path className="wordmark-seam" d={HAI} />
    </svg>
  )
}

// LatereMark is the identity of the provider behind the sign-in button, not
// replichai's own. The credential comes from latere, and the button that asks
// for it should look like the place it is going. The paths are the canonical
// mark from `latere-ui`'s `LatereLogoMark.vue`, unchanged. Only the container
// differs: that one is sized into a square and letterboxes itself, while this
// one sits inline in a line of text and is sized by its height.
//
// It fills with `currentColor`, so it is white inside the brand button and ink
// anywhere else, and it needs no second asset for dark mode.
const LATERE_RATIO = 736 / 425

export function LatereMark({ height = 11, className }: { height?: number; className?: string }) {
  return (
    <svg
      className={className}
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
// re-derive how big the mark is beside the name. The name is set a touch
// smaller than the mark, which is what the family's lockups do.
export function Lockup({ size = 22, className }: { size?: number; className?: string }) {
  return (
    <span className={className ? `lockup ${className}` : 'lockup'} style={{ fontSize: size * 0.92 }}>
      <Mark size={size} />
      <Wordmark />
    </span>
  )
}
