import type { ReactNode } from 'react'

// One stroke-drawn icon set, all on a 24×24 grid so a single stroke width reads
// evenly at every size. Icons carry no colour of their own: they inherit
// currentColor, which is what lets one glyph sit in a brand button, a muted row,
// and a semantic pill without a variant per place.

const p = (d: string, key?: string) => <path key={key ?? d.slice(0, 8)} d={d} />

export const ICONS = {
  folder: p('M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z'),
  globe: [
    <circle key="c" cx="12" cy="12" r="9" />,
    p('M3 12h18', 'a'),
    p('M12 3a15 15 0 0 1 0 18 15 15 0 0 1 0-18Z', 'b'),
  ],
  shield: p('M20 12c0 5-3.5 7.5-7.7 9a1 1 0 0 1-.6 0C7.5 19.5 4 17 4 12V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.2-2.7a1.2 1.2 0 0 1 1.6 0C14.5 3.8 17 5 19 5a1 1 0 0 1 1 1Z'),
  shieldCheck: [
    p('M20 12c0 5-3.5 7.5-7.7 9a1 1 0 0 1-.6 0C7.5 19.5 4 17 4 12V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.2-2.7a1.2 1.2 0 0 1 1.6 0C14.5 3.8 17 5 19 5a1 1 0 0 1 1 1Z', 'a'),
    p('m9 12 2 2 4-4', 'b'),
  ],
  plus: p('M5 12h14M12 5v14'),
  minus: p('M5 12h14'),
  x: p('M18 6 6 18M6 6l12 12'),
  check: p('M20 6 9 17l-5-5'),
  chevronRight: p('m9 18 6-6-6-6'),
  chevronLeft: p('m15 18-6-6 6-6'),
  chevronDown: p('m6 9 6 6 6-6'),
  pencil: [
    p('M12 20h9', 'a'),
    p('M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z', 'b'),
  ],
  sun: [
    <circle key="c" cx="12" cy="12" r="4" />,
    p('M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M6.3 17.7l-1.4 1.4M19.1 4.9l-1.4 1.4', 'r'),
  ],
  moon: p('M12 3a6.5 6.5 0 0 0 9 9 9 9 0 1 1-9-9Z'),
  monitor: [
    <rect key="r" x="2" y="3" width="20" height="14" rx="2" />,
    p('M8 21h8M12 17v4', 'a'),
  ],
  users: [
    p('M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2', 'a'),
    <circle key="c" cx="9" cy="7" r="4" />,
    p('M22 21v-2a4 4 0 0 0-3-3.9M16 3.1a4 4 0 0 1 0 7.8', 'b'),
  ],
  user: [
    <circle key="c" cx="12" cy="8" r="4" />,
    p('M4.5 21v-1.5a5.5 5.5 0 0 1 5.5-5.5h4a5.5 5.5 0 0 1 5.5 5.5V21', 'a'),
  ],
  userPlus: [
    p('M15 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2', 'a'),
    <circle key="c" cx="8.5" cy="7" r="4" />,
    p('M19 8v6M22 11h-6', 'b'),
  ],
  layers: [
    p('M12.8 2.2a2 2 0 0 0-1.6 0L2.6 6.1a1 1 0 0 0 0 1.8l8.6 3.9a2 2 0 0 0 1.6 0l8.6-3.9a1 1 0 0 0 0-1.8Z', 'a'),
    p('m6.1 9.5-3.5 1.6a1 1 0 0 0 0 1.8l8.6 3.9a2 2 0 0 0 1.6 0l8.6-3.9a1 1 0 0 0 0-1.8l-3.5-1.6', 'b'),
    p('m6.1 14.5-3.5 1.6a1 1 0 0 0 0 1.8l8.6 3.9a2 2 0 0 0 1.6 0l8.6-3.9a1 1 0 0 0 0-1.8l-3.5-1.6', 'c'),
  ],
  gauge: [
    p('M3.3 19a10 10 0 1 1 17.4 0', 'a'),
    p('m12 14 4-4', 'b'),
  ],
  package: [
    p('M21 8a2 2 0 0 0-1-1.7l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.7l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16Z', 'a'),
    p('m3.3 7 8.7 5 8.7-5M12 22V12', 'b'),
  ],
  github: [
    p('M15 22v-4a4.8 4.8 0 0 0-1-3.5c3 0 6-2 6-5.5a5.4 5.4 0 0 0-1-3.5 5 5 0 0 0 0-3.5s-1 0-3 1.5a12.3 12.3 0 0 0-8 0C6 2 5 2 5 2a5 5 0 0 0 0 3.5A5.4 5.4 0 0 0 4 9c0 3.5 3 5.5 6 5.5a4.8 4.8 0 0 0-1 3.5v4', 'a'),
    p('M9 18c-4.5 2-5-2-7-2', 'b'),
  ],
  mail: [
    <rect key="r" x="2" y="4" width="20" height="16" rx="2" />,
    p('m2 7 10 7 10-7', 'a'),
  ],
  trash: p('M3 6h18M8 6V4a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2m3 0v14a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V6'),
  // An allowance reads as a card, not a purse: the purse silhouette collapses
  // into a blob at 12px, where a rounded rectangle with a stripe still reads.
  wallet: [
    <rect key="r" x="2.5" y="5" width="19" height="14" rx="3" />,
    p('M2.5 10h19', 'a'),
    p('M6.5 14.5h4.5', 'b'),
  ],
  logOut: p('M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9'),
  filter: p('M22 4H2l8 9.5V21l4-2v-5.5Z'),
  wrench: p('M14.5 6.5a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.3-3.3a6 6 0 0 1-7.9 7.4l-6.5 6.5a2.1 2.1 0 0 1-3-3l6.5-6.5a6 6 0 0 1 7.4-7.9Z'),
  stop: <rect key="r" x="6" y="6" width="12" height="12" rx="2.5" />,
  externalLink: p('M15 3h6v6M10.5 13.5 21 3M19 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h6'),
  lock: [
    <rect key="r" x="3.5" y="10.5" width="17" height="10.5" rx="2" />,
    p('M7.5 10.5V7a4.5 4.5 0 0 1 9 0v3.5', 'a'),
  ],
  sparkles: [
    p('M12 2.5 13.7 8 19 9.7 13.7 11.4 12 17l-1.7-5.6L5 9.7 10.3 8Z', 'a'),
    p('M18.5 15.5 19.3 18l2.2.8-2.2.8-.8 2.4-.8-2.4-2.2-.8 2.2-.8ZM4.5 2.5l.6 1.8L7 5l-1.9.7L4.5 8l-.6-2.3L2 5l1.9-.7Z', 'b'),
  ],
  alert: [
    <circle key="c" cx="12" cy="12" r="9.5" />,
    p('M12 7.5v5.5M12 16.5h.01', 'a'),
  ],
  clock: [
    <circle key="c" cx="12" cy="12" r="9.5" />,
    p('M12 6.5V12l3.5 2', 'a'),
  ],
  fileText: [
    p('M14.5 2.5H6.5a2 2 0 0 0-2 2v15a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V7.5Z', 'a'),
    p('M14.5 2.5v5h5M8.5 13h7M8.5 17h4.5', 'b'),
  ],
  image: [
    <rect key="r" x="3" y="3" width="18" height="18" rx="2.5" />,
    <circle key="c" cx="8.5" cy="8.5" r="1.6" />,
    p('m20.5 15.5-4.5-4.5L4 22.5', 'a'),
  ],
  beaker: p('M9 3v6.2a2 2 0 0 1-.3 1L4.4 17a2 2 0 0 0 1.7 3h11.8a2 2 0 0 0 1.7-3l-4.3-6.8a2 2 0 0 1-.3-1V3M7.5 3h9M6.5 14h11'),
  repeat: p('M3 12a9 9 0 0 1 9-9 9 9 0 0 1 6.7 3L21 8M21 3.5V8h-4.5M21 12a9 9 0 0 1-9 9 9 9 0 0 1-6.7-3L3 16M3 20.5V16h4.5'),
  message: p('M21 14.5a2 2 0 0 1-2 2H7.5L3.5 20.5V5a2 2 0 0 1 2-2h13.5a2 2 0 0 1 2 2Z'),
  terminal: p('m4.5 17 5.5-5-5.5-5M12.5 19h7'),
  eye: [
    p('M2 12s3.8-7 10-7 10 7 10 7-3.8 7-10 7-10-7-10-7Z', 'a'),
    <circle key="c" cx="12" cy="12" r="3" />,
  ],
  eyeOff: p('M10.7 5.2A9.8 9.8 0 0 1 12 5c6.2 0 10 7 10 7a17 17 0 0 1-3.2 4M6.3 6.5A17 17 0 0 0 2 12s3.8 7 10 7a9.6 9.6 0 0 0 4.5-1.1M3 3l18 18M9.9 9.9a3 3 0 0 0 4.2 4.2'),
  search: [
    <circle key="c" cx="11" cy="11" r="7" />,
    p('m20 20-4-4', 'a'),
  ],
  key: [
    p('m15.5 7.5 3 3L22 7l-3-3M21 2l-9.5 9.5', 'a'),
    <circle key="c" cx="7.5" cy="15.5" r="5.5" />,
  ],
  bolt: p('M13 2 4 14h7l-1 8 9-12h-7Z'),
  info: [
    <circle key="c" cx="12" cy="12" r="9.5" />,
    p('M12 16.5V11M12 7.5h.01', 'a'),
  ],
  dot: <circle key="c" cx="12" cy="12" r="4.5" />,
} satisfies Record<string, ReactNode>

export type IconName = keyof typeof ICONS

// Ico draws one icon. `w` tunes the stroke so a 12px glyph does not look
// spindly next to a 20px one; `fill` is for the few solid marks (a status dot).
export function Ico({ name, size = 14, w = 1.7, className, style }: { name: IconName; size?: number; w?: number; className?: string; style?: React.CSSProperties }) {
  return (
    <svg
      className={className}
      style={style}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={w}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {ICONS[name]}
    </svg>
  )
}
