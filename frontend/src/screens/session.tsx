// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// One replication session and what it produced (015): the screen with its
// three panes, the verdict pane with the run's findings and the manifest's
// record of the run, and the artifacts pane, a file browser over the repository
// the session published. The transcript pane is screens/transcript.tsx.

import { useEffect, useRef, useState } from 'react'
import { fileKind, highlightIn, isExternalHref, langOf, renderMarkdown, resolveRelative } from '../artifact'
import {
  type ClaimOutcome,
  type Components,
  CRITERIA,
  type Criterion,
  type Findings,
  type Mismatch,
  outcomeTone,
  rate,
  type ReleaseResult,
  releaseTone,
  scoreOf,
  unresolved,
  verdictTone,
} from '../findings'
import { artifactUrl, getArtifact, getFindings, getJob, getReproduction, type Job, listArtifacts, type Reproduction } from '../api'
import { type I18n } from '../i18n'
import { dateOnly } from '../locale'
import { Ico, type IconName } from '../icons'
import { type SessionTab } from '../routes'
import { Pill, RowButton, Tip } from '../ui'
import { terminal } from '../session-stop'
import { useJobStream } from '../job-stream'
import { CardHead, Empty, NotReadable, Tab } from './shared'
import { SessionTranscript } from './transcript'

// verdictLabel renders a grade for a reader. The grader's own names are
// snake_case identifiers such as `needs_revision` in internal/provider/enum.go,
// and the manifest carries them verbatim, which is right for the record and
// wrong on screen, where "Needs_revision" reads as a leaked field name.
export function verdictLabel(v: string) {
  if (!v) return ''
  const words = v.replace(/_/g, ' ')
  return words[0].toUpperCase() + words.slice(1)
}

// manifestTone maps the manifest's grade to a semantic color, and verdictIcon
// to the matching glyph, so a passed reproduction and a failed one are told
// apart without reading the word. The findings' own verdict has its own tone in
// `findings.ts`: the vocabularies differ, passed/failed against
// reproducible/partial/not_reproducible, and folding them would misreport one
// of the two.
function manifestTone(v: string) {
  const s = v.toLowerCase()
  if (s.includes('pass')) return 'var(--ok)'
  if (s.includes('fail') || s.includes('not')) return 'var(--err)'
  return 'var(--warn)'
}
function verdictIcon(v: string): IconName {
  const s = v.toLowerCase()
  if (s.includes('pass')) return 'check'
  if (s.includes('fail') || s.includes('not')) return 'alert'
  return 'info'
}

// SessionVerdict shows one session's manifest verdict and metadata, real data
// only. Per-component scorecards are not something the backend produces, so they
// are not shown.
//
// It takes a job id rather than picking a session out of a project (015): a
// verdict is a property of the run that was graded, and "the project's verdict"
// would have to guess which run was meant.
function SessionVerdict({ i18n, jobId }: { i18n: I18n; jobId: string }) {
  const { t } = i18n
  const [repro, setRepro] = useState<Reproduction | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'none'>('loading')
  // The findings are fetched beside the manifest and never gate it: a session
  // can have a verdict with no findings, because it predates the file or its
  // file was refused, and the pane still has something true to show. `why`
  // keeps the two absences apart, because "the run wrote nothing" and "the
  // system refused what it wrote" are different facts about the run.
  const [findings, setFindings] = useState<Findings | null>(null)
  const [why, setWhy] = useState<'none' | 'invalid' | null>(null)
  useEffect(() => {
    setState('loading')
    getReproduction(jobId)
      .then((r) => {
        setRepro(r)
        setState('ready')
      })
      .catch(() => setState('none'))
  }, [jobId])
  useEffect(() => {
    setFindings(null)
    setWhy(null)
    getFindings(jobId)
      .then(setFindings)
      .catch((e) => setWhy(e?.status === 422 ? 'invalid' : 'none'))
  }, [jobId])

  if (state === 'none')
    return (
      <div className="card">
        <Empty icon="gauge" text={t('noVerdict')} />
      </div>
    )
  if (state === 'loading' || !repro) return <div className="center-load">…</div>
  const tone = manifestTone(repro.verdict)
  // The run's record, which is not the paper's verdict.
  //
  // `repro.verdict` is the grader's judgment of the run against the method's
  // rubric: passed, needs_revision, exhausted, failed. The findings' verdict is
  // about the paper: reproducible, partial, not_reproducible. Both used to be
  // headed "Verdict", which put two verdict words on one screen for one paper
  // and invited the reader to take the grader's "Passed" as a statement that the
  // paper reproduced. It is not: a well-executed audit of a paper that does not
  // reproduce passes the rubric.
  //
  // So the paper's verdict is the only headline, and this card is the record of
  // the run that produced it, with the rubric grade as one row.
  const manifest = (
    <div className="card card-pad stack" style={{ maxWidth: 720 }} data-test="run-record">
      <span className="eyebrow eyebrow-ico">
        <Ico name="shieldCheck" size={12} /> {t('runRecord')}
      </span>
      <div className="usage-rows">
        <div className="usage-line">
          <Ico name={verdictIcon(repro.verdict)} size={13} style={{ color: tone }} />
          {t('methodCheck')}
          <span className="v" style={{ color: tone }}>
            {verdictLabel(repro.verdict) || '—'}
          </span>
        </div>
        {repro.created && (
          <div className="usage-line">
            <Ico name="clock" size={13} style={{ color: 'var(--fg-3)' }} />
            {t('graded')}
            <span className="v">{dateOnly(i18n.lang, repro.created)}</span>
          </div>
        )}
        <div className="usage-line">
          <Ico name="shieldCheck" size={13} style={{ color: repro.review === 'clear' ? 'var(--ok)' : 'var(--warn)' }} />
          {t('redistribution')}
          <span className="v">
            <Pill tone={repro.review === 'clear' ? 'ok' : 'warn'}>{repro.review || '—'}</Pill>
          </span>
        </div>
        {/* This row is the reason a project cannot go public, and the reader
            following a refusal here arrives at a pill with one word in it. The
            sentence says what the word means and that this is the thing in the
            way. It is shown permanently rather than as a notice on arrival,
            because it is still true the next time they come. */}
        {repro.review && repro.review !== 'clear' && (
          <div className="banner warn inline" data-test="blocks-publishing">
            <Ico name="alert" size={14} />
            <span>
              <strong>{t('blocksPublishing')}</strong> —{' '}
              {t(repro.review === 'secret_in_transcript' ? 'blockerSecret' : 'blockerNotClear')}
            </span>
          </div>
        )}
        {repro.method_digest && (
          <div className="usage-line">
            <Ico name="key" size={13} style={{ color: 'var(--fg-3)' }} />
            {t('methodDigest')}
            <span className="v mono small">{repro.method_digest.replace('sha256:', '').slice(0, 12)}</span>
          </div>
        )}
        <div className="usage-line">
          <Ico name="github" size={13} style={{ color: 'var(--fg-3)' }} />
          {t('repository')}
          <a className="v mono small link" href={`https://github.com/${repro.repo}`} target="_blank" rel="noreferrer">
            {repro.repo}
            <Ico name="externalLink" size={11} />
          </a>
        </div>
      </div>
    </div>
  )

  // Findings first when there are any: the per-component table is the result,
  // and the manifest card below it is the system's record about the run rather
  // than about the paper. Without findings the pane says which absence it is
  // instead of rendering an empty table, which would read as a paper that had
  // no boundary.
  return (
    <div className="verdict-pane">
      {findings ? (
        <SessionFindings i18n={i18n} findings={findings} />
      ) : (
        why && (
          <div className="card card-pad small muted" style={{ maxWidth: 720 }}>
            {t(why === 'invalid' ? 'findingsInvalid' : 'noFindings')}
          </div>
        )
      )}
      {manifest}
    </div>
  )
}

// SessionFindings renders one run's verdict.json (021b): the summary first, then
// the per-component table the rubric calls the result, then what did not
// reproduce, then the decisions the paper left unwritten.
//
// The rate is never rendered alone. It appears beside its decomposition
// everywhere, because a rate depends on how the paper was sliced and the rubric
// forbids reading it as a decomposition-independent measurement.
function SessionFindings({ i18n, findings }: { i18n: I18n; findings: Findings }) {
  const { t } = i18n
  const f = findings
  const open = unresolved(f)
  const r = rate(f.components)
  const outcomeLabel = (o: ClaimOutcome) =>
    o === 'verified' ? t('outVerified') : o === 'partial' ? t('outPartial') : o === 'blocked' ? t('outBlocked') : t('outNotScored')

  // Set as a results section: the outcome in words, then the tables, each with
  // its caption above it as a journal sets a table's (047). The rate is the
  // boundary table's footnote, under the rows it is derived from, never a
  // headline over them.
  return (
    <div className="findings-doc">
      <header className="findings-head">
        <span className="eyebrow">{t('verdict')}</span>
        <h2 className="findings-word" style={{ color: verdictTone(f.verdict) }}>
          {verdictLabel(f.verdict)}
        </h2>
        <p className="findings-summary">{f.summary}</p>
        <span className="findings-graded">
          {t('graded')} {dateOnly(i18n.lang, f.graded_at)}
        </span>
      </header>

      <section className="findings-sec">
        <p className="table-caption">
          <b>{t('tableLabel', { n: '1' })}</b> {t('boundary')}
        </p>
        {/* Four columns read across, so on a phone the box scrolls sideways
            rather than the table restacking into something that is no longer a
            comparison (030). */}
        <div className="grid-table-wrap">
          <table className="grid-table">
            <thead>
              <tr>
                <th>{t('claim')}</th>
                <th style={{ width: 96 }}>{t('loop')}</th>
                <th style={{ width: 132 }}>{t('outcome')}</th>
                <th style={{ width: '34%' }}>{t('evidenceOrBlocker')}</th>
              </tr>
            </thead>
            <tbody>
              {f.boundary.map((c, i) => (
                // An outer row is muted as well as labeled: the inner/outer
                // boundary is the rule this product defends, and it has to be
                // visible without reading the column.
                <tr key={i} className={c.loop === 'outer' ? 'row-outer' : undefined}>
                  <td>
                    <div className="claim-text">{c.claim}</div>
                    <div className="claim-cite">{c.citation}</div>
                  </td>
                  <td>
                    <span className={c.loop === 'inner' ? 'loop-pill inner' : 'loop-pill outer'}>{c.loop === 'inner' ? t('loopInner') : t('loopOuter')}</span>
                  </td>
                  <td>
                    <span className={'outcome-tag ' + c.outcome} style={{ color: outcomeTone(c.outcome) }}>
                      {outcomeLabel(c.outcome)}
                    </span>
                  </td>
                  <td className="claim-ev">
                    {c.evidence}
                    {c.mismatches?.map((m, j) => <MismatchLine key={j} i18n={i18n} m={m} />)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {/* The decomposition and the rate, one line, never apart, under the
            table they are derived from. */}
        <p className="findings-decomp">
          <Decomposition c={f.components} />
          <span>
            {t('innerLoopRate')} {r !== null && <span className="findings-rate">{r.toFixed(2)}</span>}
          </span>
          <span>
            {t('componentsOf', {
              verified: String(f.components.verified),
              partial: String(f.components.partial),
              blocked: String(f.components.blocked),
              total: String(f.components.total),
            })}
          </span>
        </p>
      </section>

      <section className="findings-sec">
        <p className="table-caption">
          <b>{t('tableLabel', { n: '2' })}</b> {t('unresolved')}. {t('unresolvedHint')}
        </p>
        {open.length === 0 ? (
          <p className="findings-none">{t('unresolvedNone')}</p>
        ) : (
          <div className="ruled-list">
            {open.map((c, i) => (
              <div key={i} className="unresolved-row">
                <span className={'outcome-tag ' + c.outcome} style={{ color: outcomeTone(c.outcome) }}>
                  {outcomeLabel(c.outcome)}
                </span>
                <div className="stack" style={{ gap: 3, minWidth: 0 }}>
                  <span className="claim-text">{c.claim}</span>
                  <span className="claim-ev">{c.evidence}</span>
                  {c.mismatches?.map((m, j) => <MismatchLine key={j} i18n={i18n} m={m} />)}
                </div>
                <span className="claim-cite" style={{ marginLeft: 'auto' }}>
                  {c.citation}
                </span>
              </div>
            ))}
          </div>
        )}
      </section>

      {!!f.decisions?.length && (
        <section className="findings-sec">
          <p className="table-caption">
            <b>{t('tableLabel', { n: '3' })}</b> {t('decisions')}
          </p>
          <div className="ruled-list">
            {f.decisions.map((d, i) => (
              <div key={i} className="decision">
                <div className="claim-text">{d.question}</div>
                <dl className="decision-facts">
                  <dt>{t('decisionWhere')}</dt>
                  <dd>{d.where}</dd>
                  <dt>{t('decisionAssumed')}</dt>
                  <dd>{d.assumed}</dd>
                  <dt>{t('decisionSensitivity')}</dt>
                  <dd>{d.sensitivity}</dd>
                </dl>
              </div>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}

// MismatchLine prints both values of one disagreement. An outcome alone cannot
// say that a run reproduced a claim and still found the paper's figure wrong.
// A claim can disagree on several quantities, one line each, so the line names
// the quantity it compares when the run said which, and carries the note that
// says how large the gap is or what caused it.
function MismatchLine({ i18n, m }: { i18n: I18n; m: Mismatch }) {
  const { t } = i18n
  const kind =
    m.kind === 'numeric' ? t('mismatchNumeric')
      : m.kind === 'ordering' ? t('mismatchOrdering')
        : m.kind === 'contradiction' ? t('mismatchContradiction')
          : t('mismatchTypo')
  return (
    <div className="mismatch">
      <span className="mismatch-kind">{kind}</span>
      {m.what && <span className="mismatch-what small">{m.what}</span>}
      <span className="mismatch-values">
        {t('reportedIs')} <b>{m.reported}</b> · {t('observedIs')} <b>{m.observed}</b>
      </span>
      {m.note && <span className="mismatch-note small">{m.note}</span>}
    </div>
  )
}

// Decomposition is the stacked bar behind the rate: verified, partial, blocked.
// A blocked share is drawn as an empty track rather than a color, so it reads
// as work not done rather than as a fourth result.
export function Decomposition({ c }: { c: Components }) {
  if (!c || c.total <= 0) return null
  const pct = (n: number) => `${(n / c.total) * 100}%`
  return (
    <span className="decomp-bar" aria-hidden="true">
      <span style={{ width: pct(c.verified), background: 'var(--ok)' }} />
      <span style={{ width: pct(c.partial), background: 'var(--warn)' }} />
    </span>
  )
}

// fileIcon names a repository file by what it is, reusing the same kinds the
// viewer renders by, so the list and the pane never disagree.
function fileIcon(path: string): IconName {
  const kind = fileKind(path)
  if (kind === 'image') return 'image'
  if (kind === 'code') return 'terminal'
  return 'fileText'
}

// SessionArtifacts is the in-app file browser over one session's reproduction
// repository, a forge pass-through that works even when the repository stays
// private. Markdown is formatted, code highlighted, images shown inline.
//
// The repository belongs to the session that published it. A project has as many
// as it has published sessions, which is why the project-level version of this
// browser was removed rather than made to choose between them (015).
function SessionArtifacts({ i18n, jobId }: { i18n: I18n; jobId: string }) {
  const { t } = i18n
  const [files, setFiles] = useState<string[] | null>(null)
  const [sel, setSel] = useState<string | null>(null)
  // What the authors released comes from the same findings file the verdict pane
  // reads (021c). A run without it renders the browser alone rather than an
  // empty "nothing released" table, which would be a claim the run never made.
  const [findings, setFindings] = useState<Findings | null>(null)

  useEffect(() => {
    setFiles(null)
    listArtifacts(jobId)
      .then((r) => {
        const list = r.files ?? []
        setFiles(list)
        setSel(list.find((f) => /readme/i.test(f)) ?? list[0] ?? null)
      })
      .catch(() => setFiles([]))
  }, [jobId])
  useEffect(() => {
    setFindings(null)
    getFindings(jobId).then(setFindings).catch(() => setFindings(null))
  }, [jobId])

  if (files === null) return <div className="center-load">…</div>
  if (files.length === 0 && !findings)
    return (
      <div className="card">
        <Empty icon="package" text={t('noArtifacts')} />
      </div>
    )

  const browser = (
    <div className="detail-grid" style={{ gridTemplateColumns: '260px 1fr' }}>
      <div className="card" style={{ maxHeight: '78vh', overflowY: 'auto' }}>
        <CardHead label={t('artifacts')} icon="package" tone="var(--warn)" />
        <div className="rows">
          {files.map((f) => (
            <RowButton key={f} className={'row file-row' + (sel === f ? ' on' : '')} style={{ padding: '7px 14px' }} selected={sel === f} onOpen={() => setSel(f)}>
              <Ico name={fileIcon(f)} size={13} className="file-ico" />
              <span className="row-mono row-title" style={{ fontSize: 12 }}>
                {f}
              </span>
            </RowButton>
          ))}
        </div>
      </div>
      {sel ? (
        <FileView
          i18n={i18n}
          jobId={jobId}
          path={sel}
          onNavigate={(target) => {
            // A relative link resolves to a repository path; follow it in-app when
            // it names a file we have, rather than letting the browser navigate.
            if (files.includes(target)) setSel(target)
          }}
        />
      ) : (
        <div className="card card-pad">
          <Empty icon="fileText" text={t('selectArtifact')} />
        </div>
      )}
    </div>
  )

  // Authors first, then the reproduction. The reproduction exists because of
  // what was and was not released, and reading the repository first inverts
  // that.
  return (
    <div className="stack" style={{ gap: 16 }}>
      {findings && <ReleasedByAuthors i18n={i18n} findings={findings} />}
      {files.length > 0 && browser}
    </div>
  )
}

// ReleasedByAuthors is what the run found when it went looking for the paper's
// own artifacts, and the five-criterion scorecard beside it (021c).
function ReleasedByAuthors({ i18n, findings }: { i18n: I18n; findings: Findings }) {
  const { t } = i18n
  const released = findings.released ?? []
  const scores = findings.scorecard ?? []
  if (!released.length && !scores.length) return null
  const label = (r: ReleaseResult) =>
    r === 'found' ? t('resFound')
      : r === 'partial' ? t('resPartial')
        : r === 'dead' ? t('resDead')
          : r === 'not_fetched' ? t('resNotFetched')
            : t('resNone')
  const critLabel: Record<Criterion, string> = {
    code: t('critCode'),
    data: t('critData'),
    license: t('critLicense'),
    preregistration: t('critPreregistration'),
    supplement: t('critSupplement'),
  }

  return (
    <div className="detail-grid" style={{ gridTemplateColumns: '1fr 300px', alignItems: 'start' }}>
      {!!released.length && (
        <div className="card">
          <div className="card-head">
            <span className="eyebrow">{t('releasedTitle')}</span>
            <span className="small muted" style={{ marginLeft: 'auto' }}>{t('releasedHint')}</span>
          </div>
          {released.map((r, i) => (
            <div key={i} className="release-row">
              <span className="release-dot" style={{ background: releaseTone(r.result) }} />
              <span className="release-where">{r.where}</span>
              <span className="small" style={{ flex: 1, minWidth: 0 }}>
                {r.url ? (
                  <a className="link" href={r.url} target="_blank" rel="noreferrer">
                    {r.what || r.url}
                  </a>
                ) : (
                  r.what
                )}
              </span>
              <span className="mono small" style={{ color: releaseTone(r.result), fontWeight: 600, letterSpacing: '0.08em' }}>
                {label(r.result)}
              </span>
            </div>
          ))}
        </div>
      )}
      {!!scores.length && (
        <div className="card card-pad stack" style={{ gap: 10 }}>
          <span className="eyebrow">{t('scorecard')}</span>
          {CRITERIA.map((c) => {
            const sc = scoreOf(findings, c)
            if (!sc) return null
            return (
              <div key={c} className="stack" style={{ gap: 2 }}>
                <div className="score-row">
                  <span className="score-mark" style={{ color: sc.found ? 'var(--ok)' : 'var(--err)' }}>{sc.found ? '✓' : '✕'}</span>
                  <span style={{ color: 'var(--fg-2)' }}>{critLabel[c]}</span>
                </div>
                {sc.found && sc.url && (
                  <a className="link mono small" style={{ paddingLeft: 21 }} href={sc.url} target="_blank" rel="noreferrer">
                    {sc.url}
                  </a>
                )}
                {/* Not found is only a finding when it says where it looked. */}
                {!sc.found && !!sc.searched?.length && (
                  <span className="small muted" style={{ paddingLeft: 21 }}>
                    {t('searchedIn')}: {sc.searched.join(', ')}
                  </span>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

// FileView renders one artifact by its kind: an image inline, markdown as
// formatted and sanitized HTML, code syntax-highlighted, anything else as text.
function FileView({ i18n, jobId, path, onNavigate }: { i18n: I18n; jobId: string; path: string; onNavigate: (target: string) => void }) {
  const { t } = i18n
  const kind = fileKind(path)
  const [text, setText] = useState<string | null>(null)
  const [err, setErr] = useState(false)
  const bodyRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (kind === 'image') return
    setText(null)
    setErr(false)
    getArtifact(jobId, path)
      .then(setText)
      .catch(() => setErr(true))
  }, [jobId, path, kind])

  // After the markdown mounts: highlight code, point relative image sources at
  // the in-app artifact endpoint so a committed PNG renders rather than 404ing,
  // and send external links to a new tab so the app is never navigated away.
  useEffect(() => {
    const el = bodyRef.current
    if (text === null || !el) return
    highlightIn(el)
    el.querySelectorAll('img').forEach((img) => {
      const src = img.getAttribute('src') ?? ''
      if (src && !isExternalHref(src)) img.src = artifactUrl(jobId, resolveRelative(path, src))
    })
    el.querySelectorAll('a').forEach((a) => {
      if (isExternalHref(a.getAttribute('href') ?? '')) {
        a.target = '_blank'
        a.rel = 'noopener noreferrer'
      }
    })
  }, [text, jobId, path])

  // A click on a repository-relative link switches the viewed file in-app
  // instead of letting the browser resolve it against the SPA's URL.
  const onBodyClick = (e: React.MouseEvent) => {
    const a = (e.target as HTMLElement).closest('a')
    if (!a) return
    const href = a.getAttribute('href') ?? ''
    if (!href || isExternalHref(href)) return
    e.preventDefault()
    onNavigate(resolveRelative(path, href))
  }

  return (
    <div className="card card-pad" style={{ minWidth: 0 }}>
      <div className="eyebrow eyebrow-ico mono" style={{ marginBottom: 12 }}>
        <Ico name={fileIcon(path)} size={12} /> {path}
      </div>
      {kind === 'image' ? (
        <img src={artifactUrl(jobId, path)} alt={path} style={{ maxWidth: '100%', borderRadius: 8, background: 'var(--bg-raised)' }} />
      ) : err ? (
        <div className="err small">
          <Ico name="alert" size={13} /> {t('artifactError')}
        </div>
      ) : text === null ? (
        <div className="center-load">…</div>
      ) : kind === 'markdown' ? (
        <div ref={bodyRef} className="markdown-body" onClick={onBodyClick} dangerouslySetInnerHTML={{ __html: renderMarkdown(text) }} />
      ) : kind === 'code' ? (
        <div ref={bodyRef}>
          <pre className="event-io" style={{ maxHeight: '72vh' }}>
            <code className={`language-${langOf(path)}`}>{text}</code>
          </pre>
        </div>
      ) : (
        <pre className="event-io" style={{ maxHeight: '72vh' }}>
          {text || t('noContent')}
        </pre>
      )}
    </div>
  )
}

// SessionScreen is one replication session, and everything it produced: the
// transcript of the run, the verdict it was graded to, and the repository it
// published. All three are the session's, which is why all three are addressed
// under it (015). A project holds papers and a paper has runs, so there is no
// level above this one at which "the verdict" or "the repository" names one
// thing.
export function SessionScreen({
  i18n,
  jobId,
  tab,
  onTab,
  onLoaded,
  onBack,
  signedIn,
}: {
  i18n: I18n
  jobId: string
  tab: SessionTab
  onTab: (tab: SessionTab) => void
  // The paper this session is about, reported once the job is read. A session
  // opened by its own link has nothing else to name it with.
  onLoaded: (ref: string) => void
  onBack: () => void
  // Whether there is an account behind this reader, which decides whether the
  // live stream is worth opening at all (028).
  signedIn: boolean
}) {
  const { t } = i18n
  const [job, setJob] = useState<Job | null>(null)
  // A session under a project this reader may not see reads as missing, the same
  // answer its project gives (028).
  const [notReadable, setNotReadable] = useState(false)

  useEffect(() => {
    let live = true
    getJob(jobId)
      .then((j) => {
        if (!live) return
        setJob(j)
        setNotReadable(false)
      })
      .catch(() => live && setNotReadable(true))
    return () => {
      live = false
    }
  }, [jobId])

  // The run is watched here rather than in the transcript, because all three
  // panes are about it and only one of them is open at a time.
  //
  // It watches only what there is to watch. The live stream is an operator route
  // and stays gated (028), so for a reader with no account it would be a
  // connection refused and reopened for as long as the page is up, and a session
  // that has finished has nothing left to send. Whether to watch is a boolean,
  // so the snapshot the stream delivers cannot reopen the stream.
  useJobStream(jobId, signedIn && job != null && !terminal(job.phase), setJob)
  useEffect(() => {
    if (job?.ref) onLoaded(job.ref)
  }, [job?.ref, onLoaded])

  if (notReadable) return <NotReadable i18n={i18n} me={{ authenticated: signedIn }} />

  return (
    <div className="tx">
      <div className="detail-head session-head">
        <div className="detail-title-row" style={{ alignItems: 'center', gap: 10 }}>
          <Tip text={t('back')}>
            <button className="icon-btn" onClick={onBack} aria-label={t('back')}>
              <Ico name="chevronLeft" size={14} w={2} />
            </button>
          </Tip>
          <span className="row-title session-ref">{job?.ref ?? '…'}</span>
        </div>
        <div className="detail-tabs">
          <Tab on={tab === 'transcript'} onClick={() => onTab('transcript')} icon="layers" label={t('transcript')} />
          <Tab on={tab === 'verdict'} onClick={() => onTab('verdict')} icon="gauge" label={t('verdict')} />
          <Tab on={tab === 'artifacts'} onClick={() => onTab('artifacts')} icon="package" label={t('artifacts')} />
        </div>
      </div>
      {tab === 'transcript' ? (
        <SessionTranscript i18n={i18n} jobId={jobId} job={job} />
      ) : (
        <div className="screen" style={{ animation: 'none', overflowY: 'auto' }}>
          {tab === 'verdict' ? <SessionVerdict i18n={i18n} jobId={jobId} /> : <SessionArtifacts i18n={i18n} jobId={jobId} />}
        </div>
      )}
    </div>
  )
}
