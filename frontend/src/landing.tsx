import { SiteFooter } from 'latere-ui/react'
import { useCallback, useEffect, useRef, useState } from 'react'

import { listPublic, type PaperCitation, type ProjectView } from './api'
import { LatereMark, Lockup, Mark } from './brand'
import { mountHeroScene } from './hero-scene'
import type { I18n } from './i18n'
import { Ico } from './icons'
import { listCitation } from './papers'
import type { Theme, ThemeMode } from './theme'
import { Seams } from './ui'

// The landing (029, rebuilt by 033). It is a place, not a door.
//
// 017 built it as a fold and 029 made it a page: the argument, three figures
// that show the mechanism rather than assert it, and real published work a
// reader can open before they have an account. 033 spends the design on it —
// the hero becomes a slab with the mark in it as material, the figures gain the
// artifacts they describe, and the three questions the page never answered
// (what comes back, who it is for, what it costs) get a band each.
//
// The way in never leaves the screen. It is in a sticky header rather than
// above a fold, which is a guarantee that survives any page length — and this
// page is long on purpose.
//
// Everything that moves here is opt-in at the system level: the resting state
// under `prefers-reduced-motion: reduce` is the *finished* page, never a frozen
// first frame. `frontend/test/landing.test.mjs` asserts exactly that, so a new
// band that dims or collapses has to say so in the same place.

// useReveal advances a section from its pre-reveal state to its resting one when
// it first comes into view. It sets a class and nothing else: what the class
// does, and whether it does anything at all, is the stylesheet's business — so a
// reader who asked for less motion gets the resting state from the start, with
// no JavaScript branch to keep in step with the media query.
//
// It is a *callback* ref rather than an effect over a ref object, because a
// section on this page can appear after the first render: the published strip
// renders nothing until its fetch lands, so an effect that read `ref.current`
// once on mount read null, never observed anything, and left the section at the
// pre-reveal opacity forever — a band that was on the page and invisible. A
// callback ref runs when the node attaches, whenever that is.
function useReveal<T extends HTMLElement>() {
  const io = useRef<IntersectionObserver | null>(null)
  return useCallback((el: T | null) => {
    io.current?.disconnect()
    io.current = null
    // React calls this with null when the node goes away; disconnecting above is
    // the whole of that case.
    if (!el) return
    // A browser without the observer gets the finished state immediately, which
    // is the same thing reduced motion gets. The page is readable either way.
    if (typeof IntersectionObserver === 'undefined') {
      el.classList.add('in')
      return
    }
    const obs = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (!e.isIntersecting) continue
          e.target.classList.add('in')
          obs.unobserve(e.target)
        }
      },
      { rootMargin: '0px 0px -12% 0px' },
    )
    obs.observe(el)
    io.current = obs
  }, [])
}

const REDUCE = '(prefers-reduced-motion: reduce)'

// Band is one section of the page: an eyebrow naming what it is about, a
// heading, a caption, and whatever shows the thing. It carries the reveal, so no
// screen below has to remember to.
function Band({
  id,
  eyebrow,
  title,
  caption,
  className,
  children,
}: {
  id: string
  eyebrow: string
  title: string
  caption?: string
  className?: string
  children: React.ReactNode
}) {
  const ref = useReveal<HTMLElement>()
  return (
    <section className={className ? `lp-section ${className}` : 'lp-section'} data-test={id} ref={ref}>
      <div className="lp-head">
        <span className="lp-eyebrow">{eyebrow}</span>
        <h2>
          <Seams text={title} />
        </h2>
        {caption && <p>{caption}</p>}
      </div>
      {children}
    </section>
  )
}

// ── The hero ────────────────────────────────────────────────────────────────

// HeroSlab is the first screen: a panel with the mark inside it as a field of
// particles rather than a drawing. The scene is loaded lazily and is allowed to
// fail — the slab, the claim, and both ways in are HTML and never depend on it.
function HeroSlab({ i18n, href, theme }: { i18n: I18n; href: string; theme: Theme }) {
  const { t } = i18n
  const stage = useRef<HTMLDivElement | null>(null)
  const mount = useRef<HTMLDivElement | null>(null)
  // The hero is a band like any other and starts at the pre-reveal opacity, so
  // it needs the observer that ends that. Without it the whole first screen —
  // slab, scene, claim, and both ways in — stays at opacity 0 for every reader
  // who has not asked for less motion, which is nearly all of them.
  const reveal = useReveal<HTMLElement>()

  useEffect(() => {
    const el = mount.current
    if (!el) return
    let live = true
    let scene: Awaited<ReturnType<typeof mountHeroScene>> = null
    const reduce = window.matchMedia?.(REDUCE).matches ?? false
    mountHeroScene(el, stage.current, reduce, theme)
      .then((s) => {
        if (!live) return s?.dispose()
        scene = s
      })
      .catch(() => {})
    return () => {
      live = false
      scene?.dispose()
    }
    // The palette is baked into every buffer the scene builds, so an appearance
    // change is a rebuild rather than a repaint. It costs one teardown at the
    // moment somebody flips the switch, which is the only moment it happens.
  }, [theme])

  return (
    <section className="lp-hero" ref={reveal}>
      <div className="lp-slab" ref={stage}>
        <div className="lp-scene" ref={mount} aria-hidden="true" />
        <div className="lp-slab-veil" aria-hidden="true" />
        <div className="lp-slab-top">
          <span className="lp-eyebrow">{t('lpEyebrowAudited')}</span>
          <span className="lp-slab-aside">{t('lpHeroAside')}</span>
        </div>
        <div className="lp-slab-foot">
          <div className="lp-hero-copy">
            <h1>
              <Seams text={t('landingClaim')} />
            </h1>
            <p className="landing-lead">{t('landingLead')}</p>
            <div className="lp-cta">
              <a className="btn lg" data-test="lp-hero-signin" href={href}>
                <LatereMark height={12} /> {t('signInWith')}
              </a>
              <a className="btn ghost lg lp-on-slab" data-test="lp-hero-registry" href="/registry">
                <Ico name="globe" size={13} /> {t('browseRegistry')}
              </a>
            </div>
            <p className="landing-note">{t('signInBody')}</p>
          </div>
          <div className="lp-slab-legend">
            <span className="lp-legend-row">
              <span className="lp-swatch lp-swatch-inner" /> {t('landingInnerTitle')}
            </span>
            <span className="lp-legend-row">
              <span className="lp-swatch lp-swatch-outer" /> {t('landingOuterTitle')}
            </span>
          </div>
        </div>
      </div>
    </section>
  )
}

// ── What comes back ─────────────────────────────────────────────────────────

// The five paths `internal/publish/gate.go` treats as worker-written. The names
// are filenames, so they are literals rather than copy; what each one answers is
// the translated half.
const SHIPS = [
  { path: 'README.md', body: 'lpShipsReadme' },
  { path: 'REPRODUCIBILITY.md', body: 'lpShipsRepro' },
  { path: 'src/', body: 'lpShipsSrc' },
  { path: 'UNVERIFIED.md', body: 'lpShipsUnverified', warn: true },
  { path: 'SOURCES.md', body: 'lpShipsSources' },
] as const

function ShipsBand({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  return (
    <Band id="lp-ships" className="lp-split" eyebrow={t('lpShipsEyebrow')} title={t('lpShipsTitle')} caption={t('lpShipsCaption')}>
      <ol className="lp-stack">
        {SHIPS.map((f, i) => (
          <li key={f.path} className={'warn' in f && f.warn ? 'lp-file warn' : 'lp-file'} style={{ '--i': i } as React.CSSProperties}>
            <span className="lp-file-name">{f.path}</span>
            <span className="lp-file-body">{t(f.body)}</span>
          </li>
        ))}
      </ol>
    </Band>
  )
}

// ── The boundary ────────────────────────────────────────────────────────────

// LoopFigure is the product's one boundary, drawn: an inner ring a machine
// re-runs — closed, and traced by a runner that goes round — and an outer ring
// that stands for the study with people, drawn open and never completed.
//
// It is the mark at figure scale, deliberately: the identity and the argument
// are one thing, so a reader who sees this ten times in a browser tab has
// already been told what the tab means.
function LoopFigure() {
  return (
    <figure className="lp-loop" aria-hidden="true">
      <svg viewBox="0 0 240 240" className="lp-loop-svg" role="img">
        {/* The outer loop: open, and it stays open. The dash pattern is the
            mark's own, scaled — six dashes, so it never reads as a spinner. */}
        <circle className="lp-ring-outer" cx="120" cy="120" r="92" strokeDasharray="60 36.87" />
        {/* The inner loop: closed, because it is the half that re-runs. */}
        <circle className="lp-ring-inner" cx="120" cy="120" r="50" />
        {/* The runner: one arc travelling the closed loop. It is what "re-runs"
            looks like. */}
        <circle className="lp-ring-runner" cx="120" cy="120" r="50" strokeDasharray="40 274" />
      </svg>
    </figure>
  )
}

const POINTS = [
  { key: 'Inner', open: false },
  { key: 'Outer', open: true },
  { key: 'Verdict', open: false },
] as const

function LoopsBand({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  const ref = useReveal<HTMLElement>()
  return (
    <section className="lp-section lp-loops" data-test="lp-loops" ref={ref}>
      <div className="lp-panel lp-block">
        <div className="lp-panel-copy">
          <span className="lp-eyebrow">{t('lpLoopsEyebrow')}</span>
          <h2>
            <Seams text={t('lpLoopsTitle')} />
          </h2>
          <p>{t('lpLoopsCaption')}</p>
          <div className="landing-points">
            {POINTS.map((p) => (
              <div key={p.key} className={p.open ? 'landing-point open' : 'landing-point'}>
                <h3>{t(`landing${p.key}Title`)}</h3>
                <p>{t(`landing${p.key}Body`)}</p>
              </div>
            ))}
          </div>
        </div>
        <LoopFigure />
      </div>
    </section>
  )
}

// ── A session, step by step ─────────────────────────────────────────────────

// The four steps of a session, each named by the artifact it leaves rather than
// the verb it performs — a reader can open every one of them afterwards, which
// is the whole claim.
const STEPS = ['Paper', 'Release', 'Rerun', 'Verdict'] as const

// The commit log on the third plate. The hashes are not copy — a short hash is a
// hash in every language — so only the messages route through t().
const COMMITS = [
  ['a1c9f04', 'lpPlateCommit1'],
  ['7be2d51', 'lpPlateCommit2'],
  ['f04ab8e', 'lpPlateCommit3'],
] as const

// WalkBand pairs the four steps with the four artifacts they leave. Which plate
// is open follows which step is in the reader's band.
//
// The driver dims and collapses, and both of those are motion: it does not run
// under `reduce`, and it does not run in the one-column layout either, where
// there is no second column for a plate to be sticky in. In both cases the
// resting state — every step lit, every plate open — is the whole content, so
// nothing is hidden behind a listener that never fired.
function WalkBand({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  const root = useRef<HTMLElement | null>(null)
  const reveal = useReveal<HTMLElement>()

  useEffect(() => {
    const el = root.current
    if (!el) return
    const steps = Array.from(el.querySelectorAll<HTMLElement>('[data-step]'))
    const plates = Array.from(el.querySelectorAll<HTMLElement>('[data-plate]'))
    if (!steps.length || !plates.length) return

    const still = window.matchMedia?.(REDUCE)
    const narrow = window.matchMedia?.('(max-width: 900px)')
    // Measured before any collapse is applied, and re-measured on resize: a card
    // that is not the reader's step folds down to its header strip, so no two
    // cards ever show their bodies through each other.
    const HEAD = 44
    let heights: number[] = []
    let active = -1

    const rest = () => {
      active = -1
      for (const s of steps) s.style.opacity = ''
      for (const p of plates) {
        p.style.maxHeight = ''
        p.style.opacity = ''
      }
    }
    const measure = () => {
      rest()
      heights = plates.map((p) => Math.ceil(p.getBoundingClientRect().height))
    }
    const paint = (n: number) => {
      if (n === active) return
      active = n
      steps.forEach((s, i) => {
        s.style.opacity = i === n ? '1' : '0.34'
      })
      plates.forEach((p, i) => {
        const on = i === n
        p.style.opacity = on ? '1' : '0.4'
        p.style.maxHeight = (on ? heights[i] : HEAD) + 'px'
      })
    }
    const driving = () => !(still?.matches ?? false) && !(narrow?.matches ?? true)
    const onScroll = () => {
      if (!driving()) return
      const mid = window.innerHeight * 0.46
      let best = 0
      let bestD = Infinity
      steps.forEach((s, i) => {
        const r = s.getBoundingClientRect()
        const d = Math.abs(r.top + r.height / 2 - mid)
        if (d < bestD) {
          bestD = d
          best = i
        }
      })
      paint(best)
    }
    const sync = () => {
      if (!driving()) {
        rest()
        return
      }
      measure()
      onScroll()
    }

    sync()
    window.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', sync)
    still?.addEventListener('change', sync)
    narrow?.addEventListener('change', sync)
    return () => {
      window.removeEventListener('scroll', onScroll)
      window.removeEventListener('resize', sync)
      still?.removeEventListener('change', sync)
      narrow?.removeEventListener('change', sync)
    }
  }, [])

  return (
    <section
      className="lp-section lp-walk"
      data-test="lp-pipeline"
      ref={(el) => {
        root.current = el
        reveal(el)
      }}
    >
      <div className="lp-head">
        <span className="lp-eyebrow">{t('lpPipelineEyebrow')}</span>
        <h2>
          <Seams text={t('lpPipelineTitle')} />
        </h2>
        <p>{t('lpPipelineCaption')}</p>
      </div>
      <div className="lp-walk-grid">
        <ol className="lp-walk-steps">
          {STEPS.map((s, i) => (
            <li key={s} className="lp-step" data-step={i}>
              <span className="lp-step-n">{t('lpStepN', { n: String(i + 1).padStart(2, '0') })}</span>
              <h3>{t(`lpStep${s}`)}</h3>
              <p>{t(`lpStep${s}Body`)}</p>
              <span className="lp-step-artifact">
                <Ico name="check" size={11} w={2.4} /> {t(`lpStep${s}Artifact`)}
              </span>
            </li>
          ))}
        </ol>
        <div className="lp-walk-panel lp-block">
          <div className="lp-plates">
            <div className="lp-plate" data-plate="0">
              <span className="lp-plate-label">{t('lpPlateKey')}</span>
              <div className="lp-plate-key">{t('lpPlateKeyValue')}</div>
              <div className="lp-plate-note">{t('lpPlateKeyNote')}</div>
            </div>
            <div className="lp-plate" data-plate="1">
              <span className="lp-plate-label">{t('lpPlateRelease')}</span>
              <div className="lp-plate-rows">
                <span>
                  {t('lpPlateCode')}
                  <b className="ok">{t('lpPlateFound')}</b>
                </span>
                <span>
                  {t('lpPlateData')}
                  <b className="warn">{t('lpPlatePartial')}</b>
                </span>
                <span>
                  {t('lpPlatePrereg')}
                  <b className="err">{t('lpPlateNone')}</b>
                </span>
              </div>
            </div>
            <div className="lp-plate" data-plate="2">
              <span className="lp-plate-label">{t('lpPlateCommitted')}</span>
              <div className="lp-plate-log">
                {COMMITS.map(([sha, msg]) => (
                  <span key={sha}>
                    {/* A short hash is a hash in every language. */}
                    <b>{sha}</b> {t(msg)}
                  </span>
                ))}
              </div>
            </div>
            <div className="lp-plate" data-plate="3">
              <span className="lp-plate-label">{t('lpPlateManifest')}</span>
              <div className="lp-plate-rate">
                <span className="lp-rate-n">{t('lpDemoRate')}</span>
                <span className="lp-rate-of">{t('lpPlateInnerLoop')}</span>
              </div>
              <span className="lp-meter" aria-hidden="true">
                <span className="lp-meter-ok" style={{ width: '56%' }} />
                <span className="lp-meter-warn" style={{ width: '22%' }} />
              </span>
              <div className="lp-plate-note">{t('lpPlateSigned')}</div>
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}

// ── A verdict you can take apart ────────────────────────────────────────────

// The demo verdict. It is a fabrication and says so: an Example marker, and a
// paper key under the unregistered 10.0000 prefix. A made-up verdict against a
// resolvable DOI is the exact failure this product exists to catch, and on this
// page it would be ours.
const DEMO_ROWS = [
  { claim: 'lpDemoRow1', loop: 'lpDemoInner', loopKind: 'inner', outcome: 'lpDemoVerified', tone: 'ok', ev: 'lpDemoRow1Evidence' },
  { claim: 'lpDemoRow2', loop: 'lpDemoInner', loopKind: 'inner', outcome: 'lpDemoPartial', tone: 'warn', ev: 'lpDemoRow2Evidence' },
  { claim: 'lpDemoRow3', loop: 'lpDemoOuter', loopKind: 'outer', outcome: 'lpDemoNotScored', tone: 'none', ev: 'lpDemoRow3Evidence' },
] as const

// AnatomyFigure pulls a verdict apart into the three things it is made of. It is
// HTML rather than one drawn SVG because every part of it is a sentence: text in
// a figure has to wrap, translate, and be selectable, and a picture of a sentence
// does none of that.
const PARTS = ['Claim', 'Evidence', 'Verdict'] as const
const PART_ICONS = { Claim: 'layers', Evidence: 'fileText', Verdict: 'shieldCheck' } as const

function VerdictBand({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  return (
    <Band id="lp-anatomy" eyebrow={t('lpAnatomyEyebrow')} title={t('lpAnatomyTitle')} caption={t('lpAnatomyCaption')}>
      <div className="lp-showcase">
        <div className="lp-window">
          <div className="lp-window-bar">
            <span className="lp-window-ico">
              <Ico name="shieldCheck" size={13} />
            </span>
            <span className="lp-window-title">{t('lpDemoTitle')}</span>
            {/* Said twice on purpose: the marker on the frame, and the key
                inside it that resolves to nothing. */}
            <span className="lp-tag example">{t('lpDemoExample')}</span>
            <span className="lp-tag pub">{t('lpDemoPublished')}</span>
          </div>
          <div className="lp-window-body">
            <div className="lp-verdict-head">
              <div className="lp-verdict-rate">
                <span className="lp-verdict-outcome">{t('lpDemoOutcome')}</span>
                <span className="lp-verdict-n">
                  <b>{t('lpDemoRate')}</b>
                  <span className="lp-meter" aria-hidden="true">
                    <span className="lp-meter-ok" style={{ width: '56%' }} />
                    <span className="lp-meter-warn" style={{ width: '22%' }} />
                  </span>
                </span>
                <span className="lp-verdict-tally">{t('lpDemoTally')}</span>
              </div>
              <p className="lp-verdict-summary">{t('lpDemoSummary')}</p>
            </div>
            <div className="lp-table-scroll">
              <table className="lp-verdict-table">
                <thead>
                  <tr>
                    <th>{t('lpDemoColClaim')}</th>
                    <th>{t('lpDemoColLoop')}</th>
                    <th>{t('lpDemoColOutcome')}</th>
                    <th>{t('lpDemoColEvidence')}</th>
                  </tr>
                </thead>
                <tbody>
                  {DEMO_ROWS.map((r) => (
                    <tr key={r.claim}>
                      <td className="lp-td-claim">{t(r.claim)}</td>
                      <td>
                        <span className={`lp-tag ${r.loopKind}`}>{t(r.loop)}</span>
                      </td>
                      <td>
                        <span className={`lp-tag ${r.tone}`}>{t(r.outcome)}</span>
                      </td>
                      <td className="lp-td-ev">{t(r.ev)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>
      <div className="lp-anatomy">
        {PARTS.map((p, i) => (
          <div key={p} className="lp-part" style={{ '--i': i } as React.CSSProperties}>
            <span className="lp-part-ico">
              <Ico name={PART_ICONS[p]} size={15} />
            </span>
            <div className="lp-part-copy">
              <h3>{t(`lpPart${p}`)}</h3>
              <p>{t(`lpPart${p}Body`)}</p>
            </div>
            <span className="lp-part-by">{t(`lpPart${p}By`)}</span>
          </div>
        ))}
      </div>
    </Band>
  )
}

// ── Who it is for, and what it costs ────────────────────────────────────────

const FIELDS = [
  { key: 'Hci', accent: false },
  { key: 'Psych', accent: false },
  { key: 'Social', accent: false },
  { key: 'Else', accent: true },
] as const

function WhoBand({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  return (
    <Band id="lp-who" className="lp-split" eyebrow={t('lpWhoEyebrow')} title={t('lpWhoTitle')} caption={t('lpWhoCaption')}>
      <div className="lp-fields">
        {FIELDS.map((f) => (
          <div key={f.key} className={f.accent ? 'lp-field accent' : 'lp-field'}>
            <h3>{t(`lpWho${f.key}`)}</h3>
            <p>{t(`lpWho${f.key}Body`)}</p>
          </div>
        ))}
      </div>
    </Band>
  )
}

// What a session costs, and nothing about what it is priced at: the rate card is
// a database row behind an admin endpoint, and a price typed onto this page
// would be one that drifts from what a session is actually charged.
const COSTS = ['SignIn', 'Fund', 'Run'] as const

function CostBand({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  const ref = useReveal<HTMLElement>()
  return (
    <section className="lp-section" data-test="lp-cost" ref={ref}>
      <div className="lp-panel lp-quiet">
        <div className="lp-head">
          <span className="lp-eyebrow">{t('lpCostEyebrow')}</span>
          <h2>
            <Seams text={t('lpCostTitle')} />
          </h2>
          <p>{t('lpCostCaption')}</p>
        </div>
        <div className="lp-costs">
          {COSTS.map((c, i) => (
            <div key={c} className="lp-cost">
              {/* A numeral needs no translation, so it is one. */}
              <span className="lp-cost-n">{String(i + 1).padStart(2, '0')}</span>
              <h3>{t(`lpCost${c}`)}</h3>
              <p>{t(`lpCost${c}Body`)}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}

// ── The registry ────────────────────────────────────────────────────────────

// PublishedStrip is real work, read from the open registry (028).
//
// It is progressive enhancement and the page is complete without it: a call that
// fails, or a registry with nothing in it yet, renders nothing at all. A skeleton
// that never fills, or a "0 reproductions" heading, would each make a claim about
// a community — and the wrong one.
function PublishedStrip({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  const [rows, setRows] = useState<ProjectView[]>([])
  const ref = useReveal<HTMLElement>()
  useEffect(() => {
    let live = true
    listPublic()
      .then((all) => live && setRows((all ?? []).slice(0, 6)))
      .catch(() => {})
    return () => {
      live = false
    }
  }, [])
  if (rows.length === 0) return null
  return (
    <section className="lp-section" data-test="lp-strip" ref={ref}>
      <div className="lp-strip-head">
        <div className="lp-head">
          <span className="lp-eyebrow">{t('lpStripEyebrow')}</span>
          <h2>
            <Seams text={t('lpStripTitle')} />
          </h2>
          <p>{t('lpStripCaption')}</p>
        </div>
        <a className="btn ghost sm" href="/registry">
          <Ico name="globe" size={13} /> {t('browseRegistry')}
        </a>
      </div>
      <div className="lp-strip">
        {rows.map((p, i) => (
          <a key={p.id} className="lp-card" href={`/p/${p.id}`} style={{ '--i': i } as React.CSSProperties}>
            <span className="lp-card-title">{p.title}</span>
            {/* Which paper this is a reproduction of (043). A card's title is a
                name its owner typed; the citation is the paper's own, and it is
                what a stranger scans the registry by. A project holding more
                than one says so rather than letting the first stand for all. */}
            <PaperCite i18n={i18n} papers={p.papers} />
            {/* Named, and named as what: a bare name under a paper's title reads
                as the paper's author, which is the one person this line is never
                about. The signer is who put their name to the *reproduction*. */}
            <span className="lp-card-by">
              <Ico name="shieldCheck" size={11} />
              {t('lpCardSigner', { name: p.signed_off_name || p.owner_name || t('ownerUnknown') })}
            </span>
            <span className="lp-card-foot">
              <Ico name="globe" size={11} /> {t('sessionCount', { n: String(p.session_count) })}
            </span>
          </a>
        ))}
      </div>
    </section>
  )
}

// PaperCite is the citation line on a registry card: the first paper's authors,
// venue and year, and a count of the others. A project whose papers carry no
// record renders nothing — the reader here is a stranger who cannot fill one in,
// so "No details yet" would be a line addressed to nobody.
function PaperCite({ i18n, papers }: { i18n: I18n; papers?: PaperCitation[] }) {
  const { t } = i18n
  const { bits, more } = listCitation(papers, t('etAl'))
  if (bits.length === 0) return null
  return (
    <span className="lp-card-cite" data-test="lp-card-cite">
      {bits.join(' · ')}
      {more > 0 && <span className="lp-card-more">{t('morePapers', { n: String(more) })}</span>}
    </span>
  )
}

// ── The page ────────────────────────────────────────────────────────────────

export function Landing({
  i18n,
  next,
  theme,
}: {
  i18n: I18n
  next?: string
  theme: { mode: ThemeMode; theme: Theme; setMode: (m: ThemeMode) => void }
}) {
  const { t } = i18n
  const href = next ? `/login?next=${encodeURIComponent(next)}` : '/login'

  return (
    <div className="landing">
      {/* The way in, at every scroll position. This replaces 017's fold: a page
          that scrolls cannot promise a button above one, and a sticky header
          promises more than that promise did. */}
      <header className="lp-bar">
        <a className="lp-bar-brand" href="/">
          <Lockup size={20} className="landing-lockup" />
        </a>
        <nav className="lp-bar-actions">
          <a className="btn ghost sm lp-bar-registry" href="/registry">
            <Ico name="globe" size={13} /> {t('browseRegistry')}
          </a>
          <a className="btn" data-test="lp-signin" href={href}>
            <LatereMark height={11} /> {t('signInWith')}
          </a>
        </nav>
      </header>

      <HeroSlab i18n={i18n} href={href} theme={theme.theme} />
      <ShipsBand i18n={i18n} />
      <LoopsBand i18n={i18n} />
      <WalkBand i18n={i18n} />
      <VerdictBand i18n={i18n} />
      <WhoBand i18n={i18n} />
      <CostBand i18n={i18n} />
      <PublishedStrip i18n={i18n} />

      <section className="lp-close-wrap">
        <div className="lp-close lp-block">
          <Mark size={46} className="lp-close-mark" />
          <h2>
            <Seams text={t('lpCloseTitle')} />
          </h2>
          <p>{t('lpCloseBody')}</p>
          <div className="lp-cta">
            <a className="btn lg" href={href}>
              <LatereMark height={12} /> {t('signInWith')}
            </a>
            <a className="btn ghost lg lp-on-slab" href="/registry">
              <Ico name="globe" size={13} /> {t('browseRegistry')}
            </a>
          </div>
          <span className="landing-note">{t('signInNote')}</span>
        </div>
      </section>

      {/* The footer every Latere surface ends in, from latere-ui. It is the
          product's place in a lineup, the legal links, and the appearance
          control — none of which this page should be authoring itself.

          One language, because the app has one: an app that ships English only
          must not offer a dropdown that pretends otherwise, and the shared
          component cannot know which languages its host actually has. */}
      <SiteFooter
        theme={theme.mode}
        onThemeChange={(m) => theme.setMode(m as ThemeMode)}
        locale="en"
        locales={[{ code: 'en', label: 'EN', name: 'English' }]}
      />
    </div>
  )
}
