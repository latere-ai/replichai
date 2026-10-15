// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { SiteFooter } from 'latere-ui/react'
import { useCallback, useEffect, useRef, useState } from 'react'

import { listPublic, type PaperCitation, type ProjectView } from './api'
import { LatereMark, Lockup, Mark, markArc } from './brand'
import { HeroFigure } from './hero-figure'
import type { I18n, TKey } from './i18n'
import { Ico } from './icons'
import { listCitation } from './papers'
import type { ThemeMode } from './theme'
import { Seams } from './ui'
import './landing.css'

// The landing (029, rebuilt by 033, set like a paper by 047): a full page
// rather than a sign-in screen.
//
// 047 sets it the way research is set: numbered sections under a rule rather
// than boxed bands, a serif to state and a sans to read, a figure numbered and
// captioned underneath, tables with three rules and nothing else, and the
// registry as a list of references. The argument and every guarantee below
// are unchanged.
//
// 017 built it as a single fold and 029 made it a page: the argument, three
// figures that show the mechanism rather than assert it, and real published
// work a reader can open before they have an account. 033 redesigned it: the
// hero becomes a slab with the mark rendered inside it, the figures gain the
// artifacts they describe, and the three questions the page never answered,
// what comes back, who it is for and what it costs, get a band each.
//
// The sign-in control never leaves the screen. It is in a sticky header rather
// than above a fold, which holds at any page length, and this page is long on
// purpose.
//
// Everything that moves here is opt-in at the system level: the resting state
// under `prefers-reduced-motion: reduce` is the finished page, never a frozen
// first frame. `frontend/test/landing.test.mjs` asserts exactly that, so a new
// band that dims or collapses has to say so in the same place.

// useReveal advances a section from its pre-reveal state to its resting one when
// it first comes into view. It sets a class and nothing else: what the class
// does, and whether it does anything at all, is decided by the stylesheet, so a
// reader who asked for less motion gets the resting state from the start, with
// no JavaScript branch to keep in step with the media query.
//
// It is a callback ref rather than an effect over a ref object, because a
// section on this page can appear after the first render: the published strip
// renders nothing until its fetch lands, so an effect that read `ref.current`
// once on mount read null, never observed anything, and left the section at the
// pre-reveal opacity forever, so the band was on the page and invisible. A
// callback ref runs when the node attaches, whenever that is.
function useReveal<T extends HTMLElement>() {
  const io = useRef<IntersectionObserver | null>(null)
  return useCallback((el: T | null) => {
    io.current?.disconnect()
    io.current = null
    // React calls this with null when the node goes away; disconnecting above
    // handles that case.
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

// Band is one section of the page, set like a section of a paper: a rule, its
// number and name in the margin, a serif heading, a caption, and whatever shows
// the thing. It carries the reveal, so no section below has to remember to.
function Band({
  id,
  n,
  eyebrow,
  i18n,
  titleKey,
  caption,
  className,
  children,
}: {
  id: string
  n: number
  i18n: I18n
  eyebrow: string
  // The key, not the sentence: the heading is set by Seams, which is the only
  // thing that can read a seam.
  titleKey: TKey
  caption?: string
  className?: string
  children: React.ReactNode
}) {
  const ref = useReveal<HTMLElement>()
  return (
    <section className={className ? `lp-section ${className}` : 'lp-section'} data-test={id} ref={ref}>
      <SectionHead i18n={i18n} n={n} eyebrow={eyebrow} titleKey={titleKey} caption={caption} />
      {children}
    </section>
  )
}

// SectionHead is the margin and the heading: the number and name of the section
// on the left, the heading and its caption on the right, as a paper sets a
// numbered section. On a phone the two stack.
function SectionHead({ i18n, n, eyebrow, titleKey, caption }: { i18n: I18n; n: number; eyebrow: string; titleKey: TKey; caption?: string }) {
  return (
    <div className="lp-head">
      <span className="lp-eyebrow">
        <span className="lp-n">{n}</span>
        {eyebrow}
      </span>
      <div className="lp-head-main">
        <h2>
          <Seams i18n={i18n} k={titleKey} />
        </h2>
        {caption && <p>{caption}</p>}
      </div>
    </div>
  )
}

// ── The hero ────────────────────────────────────────────────────────────────

// Hero is the first screen: the claim and the way in on the left, Figure 1 on
// the right. Every word and both calls to action are HTML and depend on
// nothing that loads; the figure is inline SVG and has nothing to fail.
function Hero({ i18n, href }: { i18n: I18n; href: string }) {
  const { t } = i18n
  // The hero is a band like any other and starts at the pre-reveal opacity, so
  // it needs the observer that ends that. Without it the whole first screen
  // stays at opacity 0 for every reader who has not asked for less motion.
  const reveal = useReveal<HTMLElement>()
  return (
    <section className="lp-hero" ref={reveal}>
      <div className="lp-hero-copy">
        <span className="lp-eyebrow">{t('lpEyebrowAudited')}</span>
        <h1>
          <Seams i18n={i18n} k="landingClaim" />
        </h1>
        <p className="landing-lead">{t('landingLead')}</p>
        <div className="lp-cta">
          <a className="btn lg" data-test="lp-hero-signin" href={href}>
            <LatereMark height={12} /> {t('signInWith')}
          </a>
          <a className="btn ghost lg" data-test="lp-hero-registry" href="/registry">
            {t('browseRegistry')} <Ico name="arrowRight" size={14} />
          </a>
        </div>
        <p className="landing-note">{t('signInBody')}</p>
      </div>
      <HeroFigure i18n={i18n} />
    </section>
  )
}

// ── What comes back ─────────────────────────────────────────────────────────

// Every file the method asks a session to write (`method.Deliverables()`, which
// the publication gate reads too), and src/. The four reports come first,
// because they are what a reader opens; the two JSON files carry the same
// findings and the study as data, for a program to read. The names are
// filenames, so they are literals rather than copy; what each one answers is
// the translated half.
const SHIPS = [
  { path: 'README.md', body: 'lpShipsReadme' },
  { path: 'REPRODUCIBILITY.md', body: 'lpShipsRepro' },
  { path: 'UNVERIFIED.md', body: 'lpShipsUnverified', warn: true },
  { path: 'SOURCES.md', body: 'lpShipsSources' },
  { path: 'verdict.json', body: 'lpShipsVerdictJson' },
  { path: 'instrument.json', body: 'lpShipsInstrument' },
  { path: 'src/', body: 'lpShipsSrc' },
] as const

function ShipsBand({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  return (
    <Band id="lp-ships" n={3} i18n={i18n} eyebrow={t('lpShipsEyebrow')} titleKey="lpShipsTitle" caption={t('lpShipsCaption')}>
      {/* A table, set with three rules and nothing else; its caption above it,
          where a journal sets a table's. */}
      <div className="lp-table-wrap">
        <p className="lp-table-caption">
          <b>{t('tableLabel', { n: '1' })}</b> {t('lpShipsTable')}
        </p>
        <ol className="lp-stack">
          <li className="lp-file lp-file-head" aria-hidden="true">
            <span>{t('lpShipsColFile')}</span>
            <span>{t('lpShipsColAnswers')}</span>
          </li>
          {SHIPS.map((f, i) => (
            <li key={f.path} className={'warn' in f && f.warn ? 'lp-file warn' : 'lp-file'} style={{ '--i': i } as React.CSSProperties}>
              <span className="lp-file-name">{f.path}</span>
              <span className="lp-file-body">{t(f.body)}</span>
            </li>
          ))}
        </ol>
      </div>
    </Band>
  )
}

// ── The boundary ────────────────────────────────────────────────────────────

// The three points carry a glyph each, drawn from the mark: the closed ring for
// the inner loop, the open arc for the outer one, and the two together for the
// verdict, which is written across both. The color is the mark's: azure for
// what re-runs, ink for what does not.
function PointGlyph({ kind }: { kind: 'Inner' | 'Outer' | 'Verdict' }) {
  return (
    <svg className="lp-point-glyph" width="28" height="28" viewBox="0 0 24 24" fill="none" aria-hidden="true">
      {kind !== 'Inner' && <path className="mark-outer" d={markArc(12, 12, 9.35)} strokeWidth={1.4} />}
      {kind !== 'Outer' && <circle className="mark-inner" cx="12" cy="12" r="4.75" strokeWidth={1.7} />}
    </svg>
  )
}

const POINTS = [
  { key: 'Inner', open: false },
  { key: 'Outer', open: true },
  { key: 'Verdict', open: false },
] as const

function LoopsBand({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  return (
    <Band id="lp-loops" n={1} i18n={i18n} className="lp-loops" eyebrow={t('lpLoopsEyebrow')} titleKey="lpLoopsTitle" caption={t('lpLoopsCaption')}>
      <div className="landing-points">
        {POINTS.map((p) => (
          <div key={p.key} className={p.open ? 'landing-point open' : 'landing-point'}>
            <PointGlyph kind={p.key} />
            <h3>{t(`landing${p.key}Title`)}</h3>
            <p>{t(`landing${p.key}Body`)}</p>
          </div>
        ))}
      </div>
    </Band>
  )
}

// ── A session, step by step ─────────────────────────────────────────────────

// The four steps of a session, each named by the artifact it leaves rather than
// the verb it performs, because a reader can open every one of them afterwards,
// which is the claim the page makes.
const STEPS = ['Paper', 'Release', 'Rerun', 'Verdict'] as const

// The commit log on the third plate. The hashes are not copy, because a short
// hash is the same in every language, so only the messages route through t().
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
// resting state, every step lit and every plate open, is the whole content, so
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
      <SectionHead i18n={i18n} n={2} eyebrow={t('lpPipelineEyebrow')} titleKey="lpPipelineTitle" caption={t('lpPipelineCaption')} />
      <div className="lp-walk-grid">
        <ol className="lp-walk-steps">
          {STEPS.map((s, i) => (
            <li key={s} className="lp-step" data-step={i}>
              <span className="lp-step-n">{t('lpStepN', { n: String(i + 1) })}</span>
              <h3>{t(`lpStep${s}`)}</h3>
              <p>{t(`lpStep${s}Body`)}</p>
              <span className="lp-step-artifact">
                <Ico name="check" size={11} w={2.4} /> {t(`lpStep${s}Artifact`)}
              </span>
            </li>
          ))}
        </ol>
        <div className="lp-walk-panel">
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
              <div className="lp-plate-rows">
                <span>
                  {t('lpDemoVerified')}
                  <b className="ok">5</b>
                </span>
                <span>
                  {t('lpDemoPartial')}
                  <b className="warn">2</b>
                </span>
                <span>
                  {t('lpPlateBlocked')}
                  <b className="err">2</b>
                </span>
              </div>
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
    <Band id="lp-anatomy" n={4} i18n={i18n} eyebrow={t('lpAnatomyEyebrow')} titleKey="lpAnatomyTitle" caption={t('lpAnatomyCaption')}>
      <div className="lp-showcase">
        <p className="lp-table-caption">
          <b>{t('tableLabel', { n: '2' })}</b> {t('lpDemoTable')}
        </p>
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
              <span className="lp-verdict-outcome">{t('lpDemoOutcome')}</span>
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
            {/* The rate is derived from the table above and is set as the
                table's footnote, never as a headline: one number would have to
                be trusted, and the rows are what can be checked. */}
            <p className="lp-verdict-foot">
              <span className="lp-meter" aria-hidden="true">
                <span className="lp-meter-ok" style={{ width: '56%' }} />
                <span className="lp-meter-warn" style={{ width: '22%' }} />
              </span>
              {t('lpDemoFootnote')}
            </p>
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
    <Band id="lp-who" n={5} i18n={i18n} eyebrow={t('lpWhoEyebrow')} titleKey="lpWhoTitle" caption={t('lpWhoCaption')}>
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
  return (
    <Band id="lp-cost" n={6} i18n={i18n} eyebrow={t('lpCostEyebrow')} titleKey="lpCostTitle" caption={t('lpCostCaption')}>
      <ol className="lp-costs">
        {COSTS.map((c, i) => (
          <li key={c} className="lp-cost">
            {/* A numeral needs no translation, so it is one. */}
            <span className="lp-cost-n">{i + 1}</span>
            <h3>{t(`lpCost${c}`)}</h3>
            <p>{t(`lpCost${c}Body`)}</p>
          </li>
        ))}
      </ol>
    </Band>
  )
}

// ── The registry ────────────────────────────────────────────────────────────

// PublishedStrip is real work, read from the open registry (028).
//
// It is progressive enhancement and the page is complete without it: a call that
// fails, or a registry with nothing in it yet, renders nothing at all. A skeleton
// that never fills, or a "0 reproductions" heading, would each make a claim about
// the community, and a wrong one.
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
      <SectionHead i18n={i18n} n={7} eyebrow={t('lpStripEyebrow')} titleKey="lpStripTitle" caption={t('lpStripCaption')} />
      {/* Set as a paper sets its references: numbered, the citation first, the
          work's own title after it, and what makes it a reproduction last. */}
      <ol className="lp-strip">
        {rows.map((p, i) => (
          <li key={p.id} style={{ '--i': i } as React.CSSProperties}>
            <a className="lp-card" href={`/p/${p.id}`}>
              <span className="lp-card-n">{i + 1}</span>
              <span className="lp-card-body">
                {/* Which paper this is a reproduction of (043). A card's title
                    is a name its owner typed; the citation is the paper's own,
                    and it is what a stranger scans the registry by. */}
                <PaperCite i18n={i18n} papers={p.papers} />
                <span className="lp-card-title">{p.title}</span>
                {/* Named, and named as what: a bare name under a paper's title
                    reads as the paper's author, which is the one person this
                    line is never about. The signer is who put their name to
                    the reproduction. */}
                <span className="lp-card-by">
                  {t('lpCardSigner', { name: p.signed_off_name || p.owner_name || t('ownerUnknown') })}
                  <span className="lp-card-foot"> · {t('sessionCount', { n: String(p.session_count) })}</span>
                </span>
              </span>
              <Ico name="arrowRight" size={14} className="lp-card-go" />
            </a>
          </li>
        ))}
      </ol>
      <a className="lp-more" href="/registry">
        {t('browseRegistry')} <Ico name="arrowRight" size={13} />
      </a>
    </section>
  )
}

// PaperCite is the citation line on a registry card: the first paper's authors,
// venue and year, and a count of the others. A project whose papers carry no
// record renders nothing, because the reader here is a stranger who cannot fill one in,
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

// HomeLockup is the product's lockup linked home, as the header sets it. The
// shared footer opens with the same element, so the two never disagree.
function HomeLockup() {
  return (
    <a className="lp-bar-brand" href="/">
      <Lockup size={20} className="landing-lockup" />
    </a>
  )
}

export function Landing({
  i18n,
  next,
  theme,
}: {
  i18n: I18n
  next?: string
  theme: { mode: ThemeMode; setMode: (m: ThemeMode) => void }
}) {
  const { t } = i18n
  const href = next ? `/login?next=${encodeURIComponent(next)}` : '/login'
  const closeRef = useReveal<HTMLElement>()

  return (
    <div className="landing">
      {/* The sign-in control, at every scroll position. This replaces 017's
          fold: a page that scrolls cannot keep a button above the fold, and a
          sticky header keeps it visible at every position. */}
      <header className="lp-bar">
        <div className="lp-bar-in">
          <HomeLockup />
          <nav className="lp-bar-actions">
            <a className="lp-bar-link lp-bar-registry" href="/registry">
              {t('lpNavRegistry')}
            </a>
            <a className="btn" data-test="lp-signin" href={href}>
              <LatereMark height={11} /> {t('signInWith')}
            </a>
          </nav>
        </div>
      </header>

      <Hero i18n={i18n} href={href} />
      <LoopsBand i18n={i18n} />
      <WalkBand i18n={i18n} />
      <ShipsBand i18n={i18n} />
      <VerdictBand i18n={i18n} />
      <WhoBand i18n={i18n} />
      <CostBand i18n={i18n} />
      <PublishedStrip i18n={i18n} />

      <section className="lp-close-wrap" ref={closeRef}>
        <div className="lp-close">
          <Mark size={40} className="lp-close-mark" />
          <h2>
            <Seams i18n={i18n} k="lpCloseTitle" />
          </h2>
          <p>{t('lpCloseBody')}</p>
          <div className="lp-cta">
            <a className="btn lg" href={href}>
              <LatereMark height={12} /> {t('signInWith')}
            </a>
            <a className="btn ghost lg" href="/registry">
              {t('browseRegistry')} <Ico name="arrowRight" size={14} />
            </a>
          </div>
          <span className="landing-note">{t('signInNote')}</span>
        </div>
      </section>

      {/* The footer every Latere site ends in, from latere-ui, in the one
          shape every site shares. It is the product's place in the lineup,
          the legal links and the appearance control, none of which this page
          implements itself. The page passes its own lockup and its theme, and
          nothing else; the wrapper places the footer and joins the app's
          palette to the names the footer reads.

          No language menu: the app ships English only, and the footer offers
          one only to a site that passes a language change and two languages
          or more. */}
      <div className="lp-footer-wrap">
        <SiteFooter
          brand={<HomeLockup />}
          theme={theme.mode}
          onThemeChange={(m) => theme.setMode(m as ThemeMode)}
          locale="en"
        />
      </div>
    </div>
  )
}
