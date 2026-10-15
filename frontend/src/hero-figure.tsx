// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef, useState, type RefObject } from 'react'

import { MARK_GAP, MARK_GAP_AT, markArc } from './brand'
import type { I18n } from './i18n'
import { Ico } from './icons'

// Figure 1 on the landing (047): the mark at figure scale, drawn as a scientific
// figure rather than as a particle field. It carries 033's argument, that the
// identity and the argument are one drawing, in a form a reader can measure.
//
// The inner loop is a closed ring with the four things a run does on it. A
// single marker travels it at constant speed, and each lap is one run. The
// outer loop is the mark's open arc, with its gap labeled, and nothing ever
// travels it. Nothing sways and nothing eases inside a lap: the only motion is
// the one the figure is about.
//
// Everything is inline SVG drawn from tokens, so it holds in both themes and
// needs no asset, no canvas and no chunk of its own. The box reserves its
// aspect ratio, so mounting it moves nothing.

const W = 680
const H = 560
const CX = 340
const CY = 278
// 017's ratio, 5 : 9.25, so the figure and the mark are one geometry.
const RI = 108
const RO = 200

// One lap is one run.
const PERIOD = 8000
// The entrance draws the rings first; the marker sets off once they are drawn.
const ENTER = 1300
// How long after the marker passes a station it stays lit, in degrees of the
// lap. The fade back is the stylesheet's.
const LIT = 64
// The resting frame, under reduced motion: the marker parked on "record", the
// end of the first run.
const REST = 180

const STATIONS = [
  { key: 'figImplement', at: -90 },
  { key: 'figRerun', at: 0 },
  { key: 'figCompare', at: 90 },
  { key: 'figRecord', at: 180 },
] as const

const TICKS = Array.from({ length: 48 }, (_, i) => i * 7.5)

const rad = (deg: number) => (deg * Math.PI) / 180
const px = (deg: number, r: number) => CX + r * Math.cos(rad(deg))
const py = (deg: number, r: number) => CY + r * Math.sin(rad(deg))
const mod = (n: number, m: number) => ((n % m) + m) % m

const REDUCE = '(prefers-reduced-motion: reduce)'
const NARROW = '(max-width: 760px)'

// At phone width the figure is framed tighter rather than set in larger type:
// the full frame scaled to a phone sets its labels at seven pixels, and larger
// labels in the full frame collide with the outer ring. The tight frame keeps
// the rings and every label at the size they are drawn, and moves the gap's
// label above the arc, where the frame has room for it.
const FRAME = { wide: `0 0 ${W} ${H}`, narrow: '124 40 432 470' }

function useNarrow() {
  const [narrow, setNarrow] = useState(() => typeof window !== 'undefined' && !!window.matchMedia?.(NARROW).matches)
  useEffect(() => {
    const mq = window.matchMedia?.(NARROW)
    if (!mq) return
    const on = () => setNarrow(mq.matches)
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])
  return narrow
}

// useAmbient says whether the figure should be moving: it is on screen, the tab
// is visible, the reader has not asked for less motion, and has not paused it.
// It is the platform figure's rule, so the family's two moving figures stop and
// start for the same reasons.
function useAmbient(ref: RefObject<Element | null>, paused: boolean) {
  const [inView, setInView] = useState(false)
  const [visible, setVisible] = useState(() => typeof document === 'undefined' || document.visibilityState !== 'hidden')
  const [still, setStill] = useState(() => typeof window !== 'undefined' && !!window.matchMedia?.(REDUCE).matches)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    if (typeof IntersectionObserver === 'undefined') {
      setInView(true)
      return
    }
    const io = new IntersectionObserver((es) => setInView(es.some((e) => e.isIntersecting)), { threshold: 0.2 })
    io.observe(el)
    return () => io.disconnect()
  }, [ref])

  useEffect(() => {
    const on = () => setVisible(document.visibilityState !== 'hidden')
    document.addEventListener('visibilitychange', on)
    return () => document.removeEventListener('visibilitychange', on)
  }, [])

  useEffect(() => {
    const mq = window.matchMedia?.(REDUCE)
    if (!mq) return
    const on = () => setStill(mq.matches)
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [])

  return { running: inView && visible && !still && !paused, still }
}

export function HeroFigure({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  const root = useRef<HTMLElement | null>(null)
  const marker = useRef<SVGGElement | null>(null)
  const trail = useRef<SVGCircleElement | null>(null)
  const stations = useRef<(SVGGElement | null)[]>([])
  const [paused, setPaused] = useState(false)
  const [run, setRun] = useState(1)
  const { running, still } = useAmbient(root, paused)
  const narrow = useNarrow()
  const gapLabel = narrow
    ? { x: 552, y: 70, anchor: 'end' as const, reach: 48 }
    : { x: px(MARK_GAP_AT, RO + 36), y: py(MARK_GAP_AT, RO + 36) - 4, anchor: 'start' as const, reach: 30 }

  // Where the lap is, in milliseconds, kept across pauses so play resumes from
  // the frame it stopped on rather than jumping.
  const phase = useRef(0)
  const born = useRef(0)

  // paint puts the marker, its trail and the lit stations at an angle. It
  // writes the DOM directly: sixty React renders a second to move one circle
  // would be the imprecision this figure exists to replace.
  const paint = (deg: number) => {
    marker.current?.setAttribute('transform', `translate(${px(deg, RI).toFixed(2)} ${py(deg, RI).toFixed(2)})`)
    trail.current?.setAttribute('stroke-dashoffset', String(-mod(deg - 52, 360)))
    STATIONS.forEach((s, i) => {
      const lit = mod(deg - s.at, 360) < LIT
      const el = stations.current[i]
      if (el && (el.dataset.lit === '1') !== lit) el.dataset.lit = lit ? '1' : '0'
    })
  }

  useEffect(() => {
    if (still) {
      paint(REST)
      setRun(1)
      return
    }
    paint(-90 + (360 * phase.current) / PERIOD)
    if (!running) return
    let raf = 0
    let last = performance.now()
    if (!born.current) born.current = last
    const tick = (now: number) => {
      const dt = Math.min(now - last, 64)
      last = now
      if (now - born.current >= ENTER) phase.current += dt
      const deg = -90 + (360 * phase.current) / PERIOD
      paint(deg)
      // A run begins at "implement", at the top of the loop.
      const n = Math.floor(phase.current / PERIOD) + 1
      setRun((r) => (r === n ? r : n))
      raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
    // paint reads only refs and module constants, so it is not a dependency.
  }, [running, still])

  return (
    <figure className="hf" ref={root} data-test="lp-figure">
      <div className="hf-board">
        <svg className="hf-svg" viewBox={narrow ? FRAME.narrow : FRAME.wide} role="img" aria-label={t('figAlt')}>
          {/* The outer loop: the mark's open arc. Nothing travels it. */}
          <path className="hf-outer" d={markArc(CX, CY, RO)} pathLength={1} />
          {/* The gap, labeled: a leader from inside the opening out to the
              words, so the label points at the absence rather than the arc. */}
          <g className="hf-gap">
            <line x1={px(MARK_GAP_AT, RO - 16)} y1={py(MARK_GAP_AT, RO - 16)} x2={px(MARK_GAP_AT, RO + gapLabel.reach)} y2={py(MARK_GAP_AT, RO + gapLabel.reach)} />
            <circle cx={px(MARK_GAP_AT, RO - 16)} cy={py(MARK_GAP_AT, RO - 16)} r={2} />
            <text className="hf-gap-label" x={gapLabel.x} y={gapLabel.y} textAnchor={gapLabel.anchor}>
              {t('figOpen')}
            </text>
          </g>
          {/* The gap's two ends, marked, so the opening reads as cut on purpose. */}
          {[MARK_GAP_AT - MARK_GAP / 2, MARK_GAP_AT + MARK_GAP / 2].map((a) => (
            <line key={a} className="hf-gap-end" x1={px(a, RO - 6)} y1={py(a, RO - 6)} x2={px(a, RO + 6)} y2={py(a, RO + 6)} />
          ))}

          {/* The inner loop: closed, with the graduation of an instrument. */}
          <g className="hf-ticks">
            {TICKS.map((a) => (
              <line key={a} x1={px(a, RI - 4)} y1={py(a, RI - 4)} x2={px(a, RI - (a % 45 === 0 ? 11 : 8))} y2={py(a, RI - (a % 45 === 0 ? 11 : 8))} />
            ))}
          </g>
          <circle className="hf-inner" cx={CX} cy={CY} r={RI} pathLength={1} />
          <circle ref={trail} className="hf-trail" cx={CX} cy={CY} r={RI} pathLength={360} strokeDasharray="52 308" />

          {STATIONS.map((s, i) => {
            const lx = px(s.at, RI + 24)
            const ly = py(s.at, RI + 24)
            const anchor = s.at === 0 ? 'start' : s.at === 180 ? 'end' : 'middle'
            const dy = s.at === -90 ? -2 : s.at === 90 ? 14 : 5
            return (
              <g
                key={s.key}
                className="hf-station"
                data-lit="0"
                style={{ '--i': i } as React.CSSProperties}
                ref={(el) => {
                  stations.current[i] = el
                }}
              >
                <circle className="hf-node" cx={px(s.at, RI)} cy={py(s.at, RI)} r={6.5} />
                <text className="hf-label" x={lx} y={ly + dy} textAnchor={anchor}>
                  {t(s.key)}
                </text>
              </g>
            )
          })}

          <g ref={marker} className="hf-marker">
            <circle className="hf-halo" r={13} />
            <circle className="hf-dot" r={5.5} />
          </g>

          {/* The run count, at the center of the loop it counts. */}
          <text className="hf-run-label" x={CX} y={CY - 18} textAnchor="middle">
            {t('figRunLabel')}
          </text>
          <text className="hf-run-n" x={CX} y={CY + 30} textAnchor="middle" key={run}>
            {run}
          </text>

          {/* Panel letters, as a journal sets them; the caption names them. */}
          <text className="hf-panel" x={px(222, RI + 40)} y={py(222, RI + 40)}>
            {t('figPanelA')}
          </text>
          <text className="hf-panel" x={px(136, RO + 22)} y={py(136, RO + 22)}>
            {t('figPanelB')}
          </text>
        </svg>
      </div>
      <figcaption className="hf-caption">
        <p>
          <b>{t('figLabel', { n: '1' })}</b> {t('figTitle')} {t('figBody')}
        </p>
        <button
          type="button"
          className="hf-pause"
          data-test="hf-pause"
          aria-pressed={paused}
          aria-label={paused ? t('figPlay') : t('figPause')}
          data-tip={paused ? t('figPlay') : t('figPause')}
          onClick={() => setPaused((p) => !p)}
        >
          <Ico name={paused ? 'play' : 'pause'} size={13} w={1.9} />
        </button>
      </figcaption>
    </figure>
  )
}
