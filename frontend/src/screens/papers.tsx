// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// What a project is about: the papers card, each paper carrying the sessions
// that are runs of it, the citation and abstract under its title, and the
// dialogs that correct a paper's record or refile it under another project.

import { Fragment, useEffect, useState } from 'react'
import {
  addPaper,
  editPaper,
  type Job,
  listModels,
  type Me,
  type RuntimeOption,
  movePaper,
  type Project,
  type ProjectCredits,
  type ProjectPaper,
  refreshPapers,
  removePaper,
  startSession,
} from '../api'
import { useProjects } from '../projects-store'
import { countOf, type I18n } from '../i18n'
import { addPaperErrLabel, movePaperErrLabel, removePaperErrLabel, startErrLabel } from '../errors'
import { formatUSD } from '../money'
import { Ico } from '../icons'
import { type Choice, RuntimePick, runtimesFrom } from '../runtime-pick'
import { citationBits, type Grouped } from '../papers'
import { Modal, Select, Tip } from '../ui'
import { isMine } from '../account'
import { CardHead, Empty } from './shared'
import { DeleteSessionDialog, SessionRow } from './sessions'

// PapersCard is what a project is about, and where a run is started. Each
// paper carries the sessions that are runs of it.
//
// Adding a paper and starting a session are separate acts. Adding a paper costs
// nothing; starting a session spends. Kept apart, creating a project is never
// refused for money, and a paper whose only run failed stays in its project.
export function PapersCard({
  i18n,
  me,
  project,
  grouped,
  loading,
  credits,
  mayWork,
  onOpenSession,
  onChanged,
  onRefresh,
}: {
  i18n: I18n
  me: Me
  project: Project
  grouped: Grouped
  loading: boolean
  credits?: ProjectCredits
  // Whether this caller may work here as owner or collaborator. Reading needs no
  // role: a public project's papers are what it is about, and the public zone
  // exists so a visitor can check that. Adding a paper and starting a run need
  // one.
  mayWork: boolean
  onOpenSession: (job: Job) => void
  // Something was spent, so the wallet is re-read too.
  onChanged: () => void
  // Papers and sessions moved; re-read them without touching the wallet.
  onRefresh: () => void
}) {
  const { t } = i18n
  const [url, setUrl] = useState('')
  const [adding, setAdding] = useState(false)
  const [busyKey, setBusyKey] = useState('')
  const [err, setErr] = useState('')
  // Where the next session runs and on what: the runtime, the Effort level
  // and, on the Latere platform, one specific model. Not persisted, because a
  // remembered choice would quietly become the project's policy.
  const [choice, setChoice] = useState<Choice>({ runtime: '', tier: '', model: '' })
  const [runtimes, setRuntimes] = useState<RuntimeOption[]>([])
  // The session waiting on a delete confirmation. One dialog for the card
  // rather than one per row, the way the projects list holds its trash dialog.
  const [dropping, setDropping] = useState<Job | null>(null)
  // The paper waiting on a move. One dialog for the card, as above.
  const [moving, setMoving] = useState<ProjectPaper | null>(null)
  // The paper being corrected, and whether a fill-in is running.
  const [editing, setEditing] = useState<ProjectPaper | null>(null)
  const [filling, setFilling] = useState(false)
  const [filled, setFilled] = useState<number | null>(null)
  const refresh = onRefresh
  // Only the owner may remove a session, the same rule as for projects. The
  // trashed clause matches the server, which refuses every write on a project
  // in the trash.
  const mayDelete = isMine(me, project.owner) && !project.deleted_at

  // The tiers and their prices come from the server. Read once per mount, and
  // only where somebody can start a run; a reader of a public project is offered
  // nothing to choose.
  useEffect(() => {
    if (!mayWork) return
    let live = true
    listModels()
      .then((r) => live && setRuntimes(runtimesFrom(r)))
      // A deployment that cannot list them leaves the control out rather than
      // showing an error: the Start button still works, on the default tier.
      .catch(() => {})
    return () => {
      live = false
    }
  }, [mayWork])

  const add = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!url.trim() || adding) return
    setAdding(true)
    setErr('')
    try {
      await addPaper(project.id, url.trim())
      setUrl('')
      refresh()
      // The title resolves after the answer, so a second read a moment later is
      // what turns "Looking up the title…" into the paper's name.
      setTimeout(refresh, 1500)
    } catch (e) {
      setErr(addPaperErrLabel(t, e))
    } finally {
      setAdding(false)
    }
  }

  const start = async (key: string) => {
    if (busyKey) return
    setBusyKey(key)
    setErr('')
    try {
      await startSession(project.id, key, choice)
      refresh()
      onChanged()
    } catch (e) {
      setErr(startErrLabel(t, e, me.credits))
    } finally {
      setBusyKey('')
    }
  }

  const remove = async (key: string) => {
    setErr('')
    try {
      await removePaper(project.id, key)
      refresh()
    } catch (e) {
      setErr(removePaperErrLabel(t, e))
    }
  }

  // Whether the project can afford another run, so the button says so before
  // it is pressed. A run paid from the person's own Latere wallet does not draw
  // on the project's credits, so the project's balance does not hold it back.
  const chosen = runtimes.find((r) => r.name === choice.runtime) ?? runtimes.find((r) => r.default)
  const short = chosen?.payer !== 'person' && !!credits && credits.available < credits.start_reserve
  const shortWhy = credits ? t('startNeedsCredits', { reserve: formatUSD(i18n.lang, credits.start_reserve) }) : ''

  return (
    <div className="card">
      <CardHead label={t('papers')} icon="fileText" tone="var(--accent)">
        {/* The fill-in looks up missing bibliography details. Offered only
            when some paper is missing them. */}
        {mayWork && grouped.papers.some((g) => needsDetails(g.paper)) && (
          <button
            className="btn ghost sm"
            style={{ marginLeft: 'auto' }}
            data-test="fill-in-papers"
            disabled={filling}
            onClick={async () => {
              setFilling(true)
              setErr('')
              try {
                const got = await refreshPapers(project.id)
                setFilled(got.filled)
                refresh()
              } catch {
                setErr(t('fillInFailed'))
              } finally {
                setFilling(false)
              }
            }}
          >
            <Ico name="repeat" size={12} /> {filling ? t('fillingIn') : t('fillIn')}
          </button>
        )}
        {filled !== null && !filling && (
          <span className="muted small" data-test="fill-in-result">
            {filled > 0 ? countOf(t, 'fillInDone', filled) : t('fillInNothing')}
          </span>
        )}
      </CardHead>
      {mayWork && (
      <div className="card-pad stack">
        <form onSubmit={add} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <input
            className="text"
            style={{ flex: 1 }}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder={t('paperPlaceholder')}
            spellCheck={false}
          />
          <button className="btn" disabled={adding || !url.trim()}>
            <Ico name="plus" size={13} w={2.2} /> {adding ? t('addingPaper') : t('addPaperBtn')}
          </button>
        </form>
        <span className="attestation">{t('addPaperHint')}</span>
        {/* The model the next Start runs on. One control for the card, not one
            per row: the choice is not made per paper, and three papers would
            otherwise mean three identical pickers. */}
        {runtimes.length > 0 && runtimes[0].efforts.length > 0 && (
          <div className="tier-pick" style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <span className="muted small">{t('modelTier')}</span>
            <RuntimePick i18n={i18n} runtimes={runtimes} value={choice} onChange={setChoice} />
            <Tip text={t('modelTierHint')}>
              <span className="muted small" aria-label={t('modelTierHint')}>
                <Ico name="info" size={13} />
              </span>
            </Tip>
          </div>
        )}
        {err && (
          <span className="err small">
            <Ico name="alert" size={13} /> {err}
          </span>
        )}
      </div>
      )}
      {loading ? null : grouped.papers.length === 0 && grouped.other.length === 0 ? (
        <Empty icon="fileText" text={t('projectNoPapers')} />
      ) : (
        <div className="rows">
          {grouped.papers.map((g) => (
            <div className="paper-group" key={g.paper.key}>
              <div className="paper-head">
                <div className="paper-id">
                  <h3 className="paper-title">{g.paper.title || t('paperUntitled')}</h3>
                  <Citation i18n={i18n} paper={g.paper} />
                  <Abstract i18n={i18n} text={g.paper.record?.abstract} />
                </div>
                <div className="paper-actions">
                  <a className="muted small mono paper-ref" href={g.paper.ref} target="_blank" rel="noreferrer noopener" onClick={(e) => e.stopPropagation()}>
                    {shortRef(g.paper.ref)}
                  </a>
                {/* The count is the length of what this row lists. The server
                    sends one too, but it is the input to the never-remove-a-run
                    rule, not a second answer to the same question. */}
                  <span className="muted small paper-state">
                    {g.sessions.length > 0 ? countOf(t, 'sessionCount', g.sessions.length) : t('paperNeverRun')}
                  </span>
                {/* Moving is offered whether or not the paper has been run. A
                    paper filed under the wrong project is usually noticed after
                    a replication, which is when removing it stops being
                    allowed. */}
                {mayWork && (
                  <Tip text={t('editPaper')}>
                    <button
                      className="icon-btn"
                      aria-label={t('editPaper')}
                      data-test="edit-paper"
                      onClick={() => setEditing(g.paper)}
                    >
                      <Ico name="pencil" size={13} w={1.9} />
                    </button>
                  </Tip>
                )}
                {mayWork && (
                  <Tip text={t('movePaper')}>
                    <button
                      className="icon-btn"
                      aria-label={t('movePaper')}
                      data-test="move-paper"
                      onClick={() => setMoving(g.paper)}
                    >
                      <Ico name="folder" size={13} w={1.9} />
                    </button>
                  </Tip>
                )}
                {mayWork && g.sessions.length === 0 && (
                  <Tip text={t('removePaper')}>
                    <button
                      className="icon-btn danger"
                      aria-label={t('removePaper')}
                      data-test="remove-paper"
                      onClick={() => remove(g.paper.key)}
                    >
                      <Ico name="x" size={13} w={1.9} />
                    </button>
                  </Tip>
                )}
                {mayWork &&
                  (short ? (
                    <Tip text={shortWhy}>
                      <button className="btn sm" disabled>
                        <Ico name="sparkles" size={12} /> {t('startSession')}
                      </button>
                    </Tip>
                  ) : (
                    <button className="btn sm" disabled={!!busyKey || !!me.frozen} data-tip={me.frozen ? t('frozenControl') : undefined} onClick={() => start(g.paper.key)}>
                      <Ico name="sparkles" size={12} /> {busyKey === g.paper.key ? t('startingSession') : t('startSession')}
                    </button>
                  ))}
                </div>
              </div>
              {g.sessions.map((s) => (
                <SessionRow key={s.id} i18n={i18n} job={s} nested canDelete={mayDelete} onOpen={() => onOpenSession(s)} onChanged={refresh} onDelete={setDropping} />
              ))}
            </div>
          ))}
          {/* A run whose paper the project no longer holds. A paper with no runs
              can be removed, and a job's key is a string rather than a
              reference, so a session can outlive the row it started from. It is
              still a run this project paid for. */}
          {grouped.other.length > 0 && (
            <div className="paper-group">
              {/* A label, not a row: nothing here is a paper to start or open. */}
              <div className="group-head">{t('otherSessions')}</div>
              {grouped.other.map((s) => (
                <SessionRow key={s.id} i18n={i18n} job={s} nested canDelete={mayDelete} onOpen={() => onOpenSession(s)} onChanged={refresh} onDelete={setDropping} />
              ))}
            </div>
          )}
        </div>
      )}

      {dropping && (
        <DeleteSessionDialog
          i18n={i18n}
          job={dropping}
          onClose={() => setDropping(null)}
          onDone={() => {
            setDropping(null)
            refresh()
          }}
        />
      )}

      {editing && (
        <EditPaperDialog
          i18n={i18n}
          project={project}
          paper={editing}
          onClose={() => setEditing(null)}
          onDone={() => {
            setEditing(null)
            refresh()
          }}
        />
      )}

      {moving && (
        <MovePaperDialog
          i18n={i18n}
          project={project}
          paper={moving}
          onClose={() => setMoving(null)}
          onDone={() => {
            setMoving(null)
            refresh()
          }}
        />
      )}
    </div>
  )
}

// needsDetails is the client's half of the server's rule: a paper whose record
// has no authors, no venue or no year is one the fill-in has something to do
// for. A title alone does not count: a title is what a paper is called, and the
// record is what it is.
function needsDetails(p: ProjectPaper): boolean {
  const r = p.record
  return !r || !r.authors?.length || !r.venue || !r.year
}

// Citation renders a paper's bibliography the way a reference list would: the
// authors, then where and when it appeared. It is one muted line under the
// title, because it is what a reader scans a project by rather than what they
// click.
//
// Three authors, then "et al.": a fifteen-author paper would otherwise fill the
// whole row, and the full list is in the correction dialog for anyone who needs
// it. A paper whose record nobody could fill in says so, which also invites the
// owner to fill it in.
function Citation({ i18n, paper }: { i18n: I18n; paper: ProjectPaper }) {
  const { t } = i18n
  const bits = citationBits(paper.record, t('etAl'))
  if (bits.length === 0) {
    return (
      <span className="paper-cite" data-test="citation-missing">
        {t('paperNoDetails')}
      </span>
    )
  }
  return (
    <span className="paper-cite" data-test="citation">
      {bits.map((b, i) => (
        <Fragment key={i}>
          {i > 0 && <span className="paper-cite-dot" />}
          <span>{b}</span>
        </Fragment>
      ))}
    </span>
  )
}

// Abstract is what the paper is about, in its own source's words. Three lines,
// then "more": enough to tell two papers apart while a project of six still
// shows its sessions. Expanding is per paper and per visit, and not stored.
function Abstract({ i18n, text }: { i18n: I18n; text?: string }) {
  const { t } = i18n
  const [open, setOpen] = useState(false)
  if (!text) return null
  // The clamp is CSS. The control is offered from a length that cannot fit in
  // three lines at this measure, rather than from a measurement that would
  // force a reflow.
  const long = text.length > 260
  return (
    <div className="paper-abstract-wrap">
      <p className={'paper-abstract' + (open ? ' open' : '')} data-test="paper-abstract">
        {text}
      </p>
      {/* Outside the paragraph, not after its last word: the clamp hides
          whatever overflows the third line, and a control inside it is exactly
          what overflows. */}
      {long && (
        <button
          className="paper-more"
          data-test="paper-abstract-toggle"
          onClick={(e) => {
            e.stopPropagation()
            setOpen((v) => !v)
          }}
        >
          {open ? t('showLess') : t('showMore')}
        </button>
      )}
    </div>
  )
}

// shortRef renders a reference as the identifier it is: a DOI or arXiv id
// without the URL scaffolding, and anything else as its host and last segment.
// The full reference is the link's target, so nothing is lost by not printing
// a long URL that would take a third of the row.
function shortRef(ref: string): string {
  const trimmed = ref.trim()
  const doi = trimmed.match(/10\.\d{4,9}\/\S+/)
  if (doi) return doi[0]
  const arxiv = trimmed.match(/arxiv\.org\/(?:abs|pdf)\/([\w.]+?)(?:v\d+)?(?:\.pdf)?$/i)
  if (arxiv) return 'arXiv:' + arxiv[1]
  try {
    const u = new URL(trimmed)
    const last = u.pathname.split('/').filter(Boolean).pop()
    return last ? `${u.hostname}/${last}` : u.hostname
  } catch {
    return trimmed
  }
}

// EditPaperDialog corrects a paper's record. What it writes is permanent: the
// server marks every corrected field, so the next lookup fills only what is
// still empty and never puts a source's wrong answer back.
//
// Authors are one field, one per line, which is what a reference manager
// pastes.
function EditPaperDialog({
  i18n,
  project,
  paper,
  onClose,
  onDone,
}: {
  i18n: I18n
  project: Project
  paper: ProjectPaper
  onClose: () => void
  onDone: () => void
}) {
  const { t } = i18n
  const r = paper.record ?? {}
  const [title, setTitle] = useState(paper.title ?? '')
  const [authors, setAuthors] = useState((r.authors ?? []).join('\n'))
  const [venue, setVenue] = useState(r.venue ?? '')
  const [year, setYear] = useState(r.year ? String(r.year) : '')
  const [volume, setVolume] = useState(r.volume ?? '')
  const [issue, setIssue] = useState(r.issue ?? '')
  const [pages, setPages] = useState(r.pages ?? '')
  const [publisher, setPublisher] = useState(r.publisher ?? '')
  const [abstract, setAbstract] = useState(r.abstract ?? '')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const save = async () => {
    if (busy) return
    const n = Number(year.trim())
    if (year.trim() !== '' && (!Number.isInteger(n) || n < 1600 || n > 2200)) {
      setErr(t('editPaperBadYear'))
      return
    }
    setBusy(true)
    setErr('')
    try {
      // Everything the dialog shows is sent, because everything it shows is
      // something the person just looked at and let stand.
      await editPaper(project.id, paper.key, {
        title: title.trim(),
        authors: authors
          .split('\n')
          .map((a) => a.trim())
          .filter(Boolean),
        venue: venue.trim(),
        year: year.trim() === '' ? 0 : n,
        volume: volume.trim(),
        issue: issue.trim(),
        pages: pages.trim(),
        publisher: publisher.trim(),
        abstract: abstract.trim(),
      })
      onDone()
    } catch {
      setErr(t('editPaperFailed'))
      setBusy(false)
    }
  }

  const field = (label: string, value: string, set: (v: string) => void, test: string) => (
    <label className="field">
      <span className="muted small">{label}</span>
      <input className="text" value={value} onChange={(e) => set(e.target.value)} data-test={test} />
    </label>
  )

  return (
    <Modal
      title={t('editPaperTitle')}
      icon="pencil"
      eyebrow={paper.ref}
      onClose={onClose}
      foot={
        <>
          <button className="btn ghost" onClick={onClose} disabled={busy}>
            {t('cancel')}
          </button>
          <button className="btn" onClick={save} disabled={busy} data-test="edit-paper-save">
            {busy ? t('saving') : t('save')}
          </button>
        </>
      }
    >
      <p className="muted">{t('editPaperBody')}</p>
      <div className="field-grid">
        {field(t('paperFieldTitle'), title, setTitle, 'paper-title')}
        <label className="field">
          <span className="muted small">{t('paperFieldAuthors')}</span>
          <textarea className="text" rows={4} value={authors} onChange={(e) => setAuthors(e.target.value)} data-test="paper-authors" />
          <span className="muted small">{t('paperFieldAuthorsHint')}</span>
        </label>
        {field(t('paperFieldVenue'), venue, setVenue, 'paper-venue')}
        {field(t('paperFieldYear'), year, setYear, 'paper-year')}
        {field(t('paperFieldVolume'), volume, setVolume, 'paper-volume')}
        {field(t('paperFieldIssue'), issue, setIssue, 'paper-issue')}
        {field(t('paperFieldPages'), pages, setPages, 'paper-pages')}
        {field(t('paperFieldPublisher'), publisher, setPublisher, 'paper-publisher')}
        <label className="field field-wide">
          <span className="muted small">{t('paperFieldAbstract')}</span>
          <textarea className="text" rows={6} value={abstract} onChange={(e) => setAbstract(e.target.value)} data-test="paper-abstract-field" />
          <span className="muted small">{t('paperFieldAbstractHint')}</span>
        </label>
      </div>
      {err && <p className="err small">{err}</p>}
    </Modal>
  )
}

// MovePaperDialog refiles one paper, and its replications, under another
// project.
//
// The destinations are the caller's own projects that are private and not in
// the trash, the same three things the server checks, so the list offers
// nothing that would be refused. The server also lets a lead of either end
// move a paper, which this picker does not list.
function MovePaperDialog({
  i18n,
  project,
  paper,
  onClose,
  onDone,
}: {
  i18n: I18n
  project: Project
  paper: ProjectPaper
  onClose: () => void
  onDone: () => void
}) {
  const { t } = i18n
  const [to, setTo] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  // Where the paper may go: every other private, live project of the reader's,
  // read from the shared list rather than fetched again for the dialog.
  const all = useProjects()
  const targets = all === null ? null : all.filter((p) => p.id !== project.id && p.visibility === 'private' && !p.deleted_at)
  useEffect(() => {
    if (!to && targets && targets.length > 0) setTo(targets[0].id)
  }, [targets, to])

  const go = async () => {
    if (!to || busy) return
    setBusy(true)
    setErr('')
    try {
      await movePaper(project.id, paper.key, to)
      onDone()
    } catch (e) {
      setErr(movePaperErrLabel(t, e))
      setBusy(false)
    }
  }

  const nowhere = targets !== null && targets.length === 0
  return (
    <Modal
      title={t('movePaperTitle')}
      icon="fileText"
      eyebrow={paper.title || paper.ref}
      onClose={onClose}
      foot={
        <>
          <button className="btn ghost" onClick={onClose} disabled={busy}>
            {t('cancel')}
          </button>
          <button className="btn" onClick={go} disabled={busy || !to} data-test="move-paper-confirm">
            {busy ? t('movingPaper') : t('movePaperConfirm')}
          </button>
        </>
      }
    >
      <p className="muted">{t('movePaperBody')}</p>
      {targets === null ? (
        <p className="muted small">{t('loading')}</p>
      ) : nowhere ? (
        <p className="muted small">{t('movePaperNowhere')}</p>
      ) : (
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <span className="muted small">{t('movePaperTo')}</span>
          <Select
            value={to}
            options={targets.map((p) => ({ value: p.id, label: p.title }))}
            onChange={setTo}
            label={t('movePaperTo')}
            width={320}
          />
        </div>
      )}
      {err && <p className="err small">{err}</p>}
    </Modal>
  )
}
