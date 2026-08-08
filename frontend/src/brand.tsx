// The identity: one drawn mark and one wordmark, used wherever the product
// names itself. It lives apart from `icons.tsx` on purpose — an icon inherits
// `currentColor` so the same glyph can sit in a brand button and a muted row,
// while the mark owns its colour and must look the same in every context.
//
// Nothing here depends on a font the app does not ship. The mark it replaced
// was the character `r` set in `--font-serif: 'Instrument Serif'`, which has no
// `@font-face` anywhere in the repository, so it had always rendered in Georgia.

// Mark draws the boundary the product defends: the inner loop — the algorithm,
// model, or analysis pipeline — is mechanically re-runnable, so it is a closed
// ring; the outer loop is the study with human participants, which replichai
// never simulates, so it is drawn open. The dashes are sized against the outer
// circumference (2π × 9.25 ≈ 58.1) to land six even segments, so the ring never
// shows a short one where it closes. Six and not more: the mark sits beside
// spinners in this app, and a ring of many small dashes is a spinner.
export function Mark({ size = 24, className }: { size?: number; className?: string }) {
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
      <circle className="mark-outer" cx="12" cy="12" r="9.25" strokeWidth="1.9" strokeDasharray="6.0 3.687" />
      <circle className="mark-inner" cx="12" cy="12" r="5" strokeWidth="2.6" />
    </svg>
  )
}

// Wordmark sets the name as one word with a hairline gap at its own seam:
// `replic` (replication) and `hai` (human–AI, the lineage the `open-hai` forge
// account already carries). The seam is spacing, not colour — the stylesheet
// spends the brand hue on what the operator can act on, and a two-tone wordmark
// would spend it on decoration instead.
export function Wordmark({ className }: { className?: string }) {
  return (
    <span className={className ? `wordmark ${className}` : 'wordmark'}>
      replic<span className="wordmark-seam">hai</span>
    </span>
  )
}

// Lockup is the pair at a fixed optical relationship, so no caller has to
// re-derive how big the mark is beside the name.
export function Lockup({ size = 22, className }: { size?: number; className?: string }) {
  return (
    <span className={className ? `lockup ${className}` : 'lockup'} style={{ fontSize: size * 0.86 }}>
      <Mark size={size} />
      <Wordmark />
    </span>
  )
}
