import { useCallback, useEffect, useRef, useState } from 'react'

import { listPublic, type ProjectView } from './api'
import { LatereMark, Lockup } from './brand'
import type { I18n } from './i18n'
import { Ico } from './icons'

// The landing (029). It is a place, not a door.
//
// 017 built it as a fold: two columns, sign-in guaranteed above 900px, and — by
// its own decision — no link anywhere, because the registry was closed. 028
// opened the registry, and this is what spends that: the argument, three figures
// that show the mechanism rather than assert it, and real published work a
// reader can open before they have an account.
//
// The way in never leaves the screen. It is in a sticky header rather than above
// a fold, which is a guarantee that survives any page length — and this page is
// long on purpose.

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

// Section is one band of the page: a heading, a caption, and whatever shows the
// thing. It carries the reveal, so no screen below has to remember to.
function Section({
  id,
  title,
  caption,
  children,
}: {
  id: string
  title: string
  caption: string
  children: React.ReactNode
}) {
  const ref = useReveal<HTMLElement>()
  return (
    <section className="lp-section" data-test={id} ref={ref}>
      <div className="lp-head">
        <h2>{title}</h2>
        <p>{caption}</p>
      </div>
      {children}
    </section>
  )
}

// LoopFigure is the product's one boundary, drawn: an inner ring a machine
// re-runs — closed, and traced by a runner that goes round — and an outer ring
// that stands for the study with people, drawn open and never completed.
//
// It is the mark at figure scale, deliberately: the identity and the argument
// are one thing, so a reader who sees this ten times in a browser tab has
// already been told what the tab means.
function LoopFigure({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  return (
    <figure className="lp-loop" aria-hidden="true">
      <svg viewBox="0 0 240 240" className="lp-loop-svg" role="img">
        {/* The outer loop: open, and it stays open. The dash pattern is the
            mark's own, scaled — six dashes, so it never reads as a spinner. */}
        <circle className="lp-ring-outer" cx="120" cy="120" r="92" strokeDasharray="60 36.87" />
        {/* The inner loop: closed, because it is the half that re-runs. */}
        <circle className="lp-ring-inner" cx="120" cy="120" r="50" />
        {/* The runner: one arc travelling the closed loop. It is what "re-runs"
            looks like, and it is the only thing on this page that moves on its
            own. */}
        <circle className="lp-ring-runner" cx="120" cy="120" r="50" strokeDasharray="40 274" />
      </svg>
      <figcaption className="lp-loop-legend">
        <span className="lp-legend-row">
          <span className="lp-swatch lp-swatch-inner" /> {t('landingInnerTitle')}
        </span>
        <span className="lp-legend-row">
          <span className="lp-swatch lp-swatch-outer" /> {t('landingOuterTitle')}
        </span>
      </figcaption>
    </figure>
  )
}

// The four steps of a session, each named by the artifact it leaves rather than
// the verb it performs — a reader can open every one of them afterwards, which
// is the whole claim.
const STEPS = ['Paper', 'Release', 'Rerun', 'Verdict'] as const
const STEP_ICONS = { Paper: 'search', Release: 'package', Rerun: 'layers', Verdict: 'gauge' } as const

function PipelineFigure({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  return (
    <ol className="lp-pipeline">
      {STEPS.map((s, i) => (
        <li key={s} className="lp-step" style={{ '--i': i } as React.CSSProperties}>
          <span className="lp-step-ico">
            <Ico name={STEP_ICONS[s]} size={16} />
          </span>
          <h3>{t(`lpStep${s}` as never)}</h3>
          <p>{t(`lpStep${s}Body` as never)}</p>
          <span className="lp-step-artifact">
            <Ico name="check" size={11} w={2.4} /> {t(`lpStep${s}Artifact` as never)}
          </span>
        </li>
      ))}
    </ol>
  )
}

// AnatomyFigure pulls a verdict apart into the three things it is made of. It is
// HTML rather than one drawn SVG because every part of it is a sentence: text in
// a figure has to wrap, translate, and be selectable, and a picture of a sentence
// does none of that. The connector between the bands is the only drawn part.
const PARTS = ['Claim', 'Evidence', 'Verdict'] as const
const PART_ICONS = { Claim: 'layers', Evidence: 'fileText', Verdict: 'shieldCheck' } as const

function AnatomyFigure({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  return (
    <div className="lp-anatomy">
      {PARTS.map((p, i) => (
        <div key={p} className="lp-part" style={{ '--i': i } as React.CSSProperties}>
          <span className="lp-part-ico">
            <Ico name={PART_ICONS[p]} size={15} />
          </span>
          <div className="lp-part-copy">
            <h3>{t(`lpPart${p}` as never)}</h3>
            <p>{t(`lpPart${p}Body` as never)}</p>
          </div>
          <span className="lp-part-by">{t(`lpPart${p}By` as never)}</span>
        </div>
      ))}
    </div>
  )
}

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
      <div className="lp-head">
        <h2>{t('lpStripTitle')}</h2>
        <p>{t('lpStripCaption')}</p>
      </div>
      <div className="lp-strip">
        {rows.map((p, i) => (
          <a key={p.id} className="lp-card" href={`/p/${p.id}`} style={{ '--i': i } as React.CSSProperties}>
            <span className="lp-card-title">{p.title}</span>
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
      <a className="btn ghost lp-strip-more" href="/registry">
        <Ico name="globe" size={13} /> {t('browseRegistry')}
      </a>
    </section>
  )
}

export function Landing({ i18n, next }: { i18n: I18n; next?: string }) {
  const { t } = i18n
  const href = next ? `/login?next=${encodeURIComponent(next)}` : '/login'
  const points = [
    { key: 'Inner', open: false },
    { key: 'Outer', open: true },
    { key: 'Verdict', open: false },
  ] as const
  const hero = useReveal<HTMLElement>()

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

      <section className="lp-hero" ref={hero}>
        <div className="lp-hero-copy">
          <h1>{t('landingClaim')}</h1>
          <p className="landing-lead">{t('landingLead')}</p>
          <div className="lp-cta">
            <a className="btn lg" data-test="lp-hero-signin" href={href}>
              <LatereMark height={12} /> {t('signInWith')}
            </a>
            <a className="btn ghost lg" data-test="lp-hero-registry" href="/registry">
              <Ico name="globe" size={13} /> {t('browseRegistry')}
            </a>
          </div>
          <p className="landing-note">{t('signInBody')}</p>
        </div>
        <LoopFigure i18n={i18n} />
      </section>

      <Section id="lp-loops" title={t('lpLoopsTitle')} caption={t('lpLoopsCaption')}>
        <div className="landing-points">
          {points.map((p) => (
            <div key={p.key} className={p.open ? 'landing-point open' : 'landing-point'}>
              <h2>{t(`landing${p.key}Title` as never)}</h2>
              <p>{t(`landing${p.key}Body` as never)}</p>
            </div>
          ))}
        </div>
      </Section>

      <Section id="lp-pipeline" title={t('lpPipelineTitle')} caption={t('lpPipelineCaption')}>
        <PipelineFigure i18n={i18n} />
      </Section>

      <Section id="lp-anatomy" title={t('lpAnatomyTitle')} caption={t('lpAnatomyCaption')}>
        <AnatomyFigure i18n={i18n} />
      </Section>

      <PublishedStrip i18n={i18n} />

      <footer className="lp-close">
        <h2>{t('lpCloseTitle')}</h2>
        <p>{t('lpCloseBody')}</p>
        <div className="lp-cta">
          <a className="btn lg" href={href}>
            <LatereMark height={12} /> {t('signInWith')}
          </a>
          <a className="btn ghost lg" href="/registry">
            <Ico name="globe" size={13} /> {t('browseRegistry')}
          </a>
        </div>
        <span className="landing-note">{t('signInNote')}</span>
      </footer>
    </div>
  )
}
