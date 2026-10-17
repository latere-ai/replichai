// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The lists of projects: the reader's own dashboard with its filter and its
// trash dialog, the trash itself with the restore and the purge, and the public
// registry, plus the cells and badges every row shares.

import { useCallback, useEffect, useState } from 'react'
import {
  archiveUrl,
  listPublic,
  listTrash,
  type Me,
  type PaperCitation,
  type ProjectView,
  purgeProject,
  restoreProject,
  trashProject,
} from '../api'
import { invalidateProjects, useProjects } from '../projects-store'
import { countOf, type I18n } from '../i18n'
import { generic, purgeErrLabel, trashErrLabel } from '../errors'
import { formatUSD } from '../money'
import { Ico } from '../icons'
import { listCitation } from '../papers'
import { GlassModal, GlassSegmented } from 'latere-ui/react'
import { openFromRow, Pill, RowLink, Seams, Tip, tipAttrs } from '../ui'
import { pathOf } from '../routes'
import { absoluteTime, relativeTime } from '../when'
import { isMine } from '../account'
import { phaseLabel } from '../phase'
import { Empty, Meter, signerOf, VisibilityBadge } from './shared'

// A project matches the filter when its title or its owner does. The DOI is not
// searched: `ProjectView` does not carry one, and a placeholder that silently
// matches nothing is worse than a field that does not claim to.
type ProjectFilter = 'all' | 'running' | 'published'

function projectMatches(p: ProjectView, q: string, f: ProjectFilter, ownerLabel: string) {
  if (f === 'running' && p.running <= 0) return false
  if (f === 'published' && (p.published_sessions ?? 0) <= 0) return false
  if (!q) return true
  const hay = `${p.title} ${p.owner_name ?? ''} ${ownerLabel}`.toLowerCase()
  return hay.includes(q.toLowerCase())
}

// The projects list: one list of everything the reader may work in, owned or
// shared, with the owner named on the row rather than split into tabs, because
// who created a project is not something the reader chose. The owner and the
// date sit in the title's meta line, which leaves the width for what each
// project has handed over.
export function ProjectsScreen({ i18n, me, onOpen }: { i18n: I18n; me: Me; onOpen: (id: string, title?: string) => void }) {
  const { t } = i18n
  // The shared list: the rail reads the same one, so the count up there and the
  // rows down here cannot disagree.
  const list = useProjects()
  // The project the reader has asked to throw away, held while they confirm.
  const [dropping, setDropping] = useState<ProjectView | null>(null)
  // The filter narrows what is already loaded and fetches nothing, so it can
  // never disagree with what the rows say.
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<ProjectFilter>('all')

  const projects = list ?? []
  const sessionTotal = projects.reduce((n, p) => n + p.session_count, 0)
  // Both sums are over optional fields, and a sum over an absent field is not
  // zero: a server that could not compute one omits it, and a confident 0 there
  // would claim the work produced nothing. `some` rather than a total-of-zero
  // check, because a real zero is a fact worth showing.
  const spendKnown = projects.some((p) => p.spend_30d !== undefined)
  const spend30d = projects.reduce((n, p) => n + (p.spend_30d ?? 0), 0)
  const pubKnown = projects.some((p) => p.published_sessions !== undefined)
  const published = projects.reduce((n, p) => n + (p.published_sessions ?? 0), 0)
  const barren = Math.max(0, sessionTotal - published)

  const shown = projects.filter((p) => projectMatches(p, query.trim(), filter, t('ownerYou')))

  return (
    <div className="screen">
      <div className="screen-head">
        <h1 className="screen-title">
          <Seams i18n={i18n} k="navProjects" />
        </h1>
        <p className="screen-desc">{t('projectsIntro')}</p>
      </div>

      {/* The boundary band: what all the reader's projects have handed over,
          and the wallet. */}
      <div className="boundary-grid" data-test="boundary">
        <div className="boundary" data-test="evidence-card">
          <div className="boundary-head">
            <span className="eyebrow">{t('evidenceEyebrow')}</span>
            <span className="boundary-aside">{countOf(t, 'evidenceAcross', projects.length)}</span>
          </div>
          <div className="boundary-figure">
            <span className="boundary-n" data-test="evidence-n">
              {list === null || !pubKnown ? '—' : published}
            </span>
            <span className="boundary-of">{countOf(t, 'evidenceOf', sessionTotal)}</span>
          </div>
          <Meter pct={sessionTotal ? (published / sessionTotal) * 100 : 0} tone="var(--pub)" />
          <div className="boundary-legend">
            <span className="legend-row">
              <span className="legend-swatch" style={{ background: 'var(--pub)' }} />
              {t('evidencePublished', { n: String(pubKnown ? published : 0) })}
            </span>
            <span className="legend-row sub">
              <span className="legend-swatch legend-swatch-none" />
              {t('evidenceNothing', { n: String(pubKnown ? barren : sessionTotal) })}
            </span>
          </div>
        </div>

        <div className="boundary" data-test="credits-card">
          <div className="boundary-head">
            <span className="eyebrow">{t('creditsEyebrow')}</span>
          </div>
          <div className="boundary-figure">
            <span className="boundary-n">{me.credits ? formatUSD(i18n.lang, me.credits.wallet) : '—'}</span>
            <span className="boundary-of">{t('creditsInWallet')}</span>
          </div>
          <p className="boundary-note">{t('creditsNote')}</p>
          {/* The windowed spend keeps its own row and its window named: a
              figure whose period is not stated is not a figure. */}
          <div className="boundary-foot" data-test="spend-30d">
            <span>{t('statSpend30d')}</span>
            <span className="boundary-foot-n">{list === null || !spendKnown ? '—' : formatUSD(i18n.lang, spend30d)}</span>
          </div>
        </div>
      </div>

      {projects.length > 0 && (
        <div className="list-filter">
          <label className="filter-field">
            <Ico name="search" size={14} />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t('filterProjects')}
              aria-label={t('filterProjects')}
              data-test="project-filter"
            />
          </label>
          {/* A narrowing the reader picks and can undo. The list is not split
              by owner, which is a fact the reader did not choose. */}
          <GlassSegmented
            value={filter}
            options={[
              { value: 'all', label: t('filterAll') },
              { value: 'running', label: t('filterRunning') },
              { value: 'published', label: t('filterPublished') },
            ]}
            onChange={(v) => setFilter(v === 'running' || v === 'published' ? v : 'all')}
            ariaLabel={t('colStatus')}
          />
        </div>
      )}

      <div className="card">
        {list === null ? (
          <div className="empty">…</div>
        ) : projects.length === 0 ? (
          <Empty icon="folder" text={t('noProjects')} />
        ) : shown.length === 0 ? (
          <Empty icon="search" text={t('noMatches')} />
        ) : (
          <div className="rows project-list" data-test="project-rows">
            {/* The header is a row of the same grid, not a table head: one
                template governs both, so a column cannot be renamed in one place
                and widened in another. */}
            <div className="project-head" aria-hidden="true">
              <span className="eyebrow">{t('colPaper')}</span>
              {/* Named so the phone can hide it first: what a project has
                  handed over is context, while its title and its phase identify
                  the row. */}
              <span className="eyebrow row-published">{t('colPublished')}</span>
              <span className="eyebrow">{t('colStatus')}</span>
              <span className="eyebrow row-visibility">{t('colVisibility')}</span>
              <span />
              <span />
              <span />
            </div>
            {shown.map((p) => (
              <div key={p.id} className="row" onClick={openFromRow}>
                <div className="row-main">
                  <div className="row-title">
                    <RowLink href={pathOf({ name: 'project', id: p.id })} onOpen={() => onOpen(p.id, p.title)}>
                      {p.title}
                    </RowLink>
                  </div>
                  {/* Which paper the project is about, directly under its
                      title, as the registry sets it. Two projects can share a
                      title; the citation tells them apart at a glance. */}
                  <RowCitation i18n={i18n} papers={p.papers} />
                  {/* One line of what the project is about, where its owner has
                      said. It sits above the facts rather than among them: it is
                      the only part of the row somebody wrote. */}
                  {p.description && (
                    <div className="row-desc" data-test="row-description">
                      {p.description}
                    </div>
                  )}
                  {/* Owner, count, and age on one line under the title. The
                      owner links to their page and the age holds its exact
                      moment. */}
                  <div className="row-meta">
                    <OwnerCell i18n={i18n} me={me} p={p} />
                    <span className="row-meta-dot" aria-hidden="true" />
                    <span>{countOf(t, 'sessionCount', p.session_count)}</span>
                    <span className="row-meta-dot" aria-hidden="true" />
                    <ModifiedCell i18n={i18n} at={p.modified_at} />
                  </div>
                </div>
                {/* What this project has actually handed over, as a fraction of
                    what it ran. A project that has published nothing says so in
                    words rather than showing an empty bar, which reads as a
                    figure that failed to load. */}
                <div className="row-cell row-published" data-test="published-cell">
                  {(p.published_sessions ?? 0) > 0 ? (
                    <span className="pub-figure">
                      <span className="pub-count">
                        <b>{t('publishedOf', { n: String(p.published_sessions), total: String(p.session_count) })}</b>
                        <span className="pub-unit">{t('publishedUnit')}</span>
                      </span>
                      <Meter
                        pct={p.session_count ? ((p.published_sessions ?? 0) / p.session_count) * 100 : 0}
                        tone="var(--pub)"
                        size="sm"
                      />
                    </span>
                  ) : (
                    <span className="pub-none">{t('publishedNone')}</span>
                  )}
                </div>
                {/* Each badge gets its own cell rather than sizing the column:
                    a badge is as wide as its word, and that width would push
                    the cells to its left out of line, row by row. */}
                <span className="row-cell">
                  <StatusBadge i18n={i18n} p={p} />
                </span>
                <span className="row-cell row-visibility">
                  <VisibilityBadge i18n={i18n} visibility={p.visibility} icon />
                </span>
                {/* An anchor, not a fetch: the browser's own download handles
                    a body this size. The condition is the server's own. The
                    download is a top-level navigation, so offering it on a
                    project with nothing published would replace the app with
                    the server's JSON refusal. */}
                {(p.published_sessions ?? 0) > 0 ? (
                  <Tip text={t('download')}>
                    <a
                      className="icon-btn"
                      href={archiveUrl(p.id)}
                      aria-label={t('download')}
                      data-test="download-project"
                      onClick={(e) => e.stopPropagation()}
                    >
                      <Ico name="download" size={13} />
                    </a>
                  </Tip>
                ) : (
                  <span className="row-slot" />
                )}
                {/* Only on what you own: removing a project you were invited to
                    is not a thing that should be possible. */}
                {isMine(me, p.owner) ? (
                  <Tip text={t('trashProject')}>
                    <button
                      className="icon-btn danger"
                      aria-label={t('trashProject')}
                      data-test="trash-project"
                      onClick={(e) => {
                        e.stopPropagation()
                        setDropping(p)
                      }}
                    >
                      <Ico name="trash" size={13} />
                    </button>
                  </Tip>
                ) : (
                  <span className="row-slot" />
                )}
                <Ico name="chevronRight" size={15} className="chev" />
              </div>
            ))}
          </div>
        )}
      </div>

      {dropping && (
        <TrashDialog
          i18n={i18n}
          p={dropping}
          onClose={() => setDropping(null)}
          onDone={() => {
            setDropping(null)
            invalidateProjects()
          }}
        />
      )}
    </div>
  )
}

// The confirmation before a project leaves the list. It is the app's own dialog,
// not window.confirm: a browser-owned box ignores the theme, cannot carry the
// message below, and blocks the tab while it is up.
//
// The message is that this is reversible and, for a published project, that it
// is not only a removal: the publication ends, and the retraction goes on the
// record.
function TrashDialog({ i18n, p, onClose, onDone }: { i18n: I18n; p: ProjectView; onClose: () => void; onDone: () => void }) {
  const { t } = i18n
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const go = async () => {
    setBusy(true)
    setErr('')
    try {
      await trashProject(p.id)
      onDone()
    } catch (e) {
      setErr(trashErrLabel(t, e))
      setBusy(false)
    }
  }
  return (
    <GlassModal
      open
      title={t('trashTitle')}
      onClose={onClose}
      footer={
        <>
          <button className="btn ghost" onClick={onClose} disabled={busy}>
            {t('cancel')}
          </button>
          <button className="btn danger" onClick={go} disabled={busy} data-test="trash-confirm">
            {busy ? t('trashing') : t('trashConfirm')}
          </button>
        </>
      }
    >
      <span className="attestation">{p.title}</span>
      <p className="muted">{t('trashBody')}</p>
      {p.visibility === 'public' && <p className="warn small">{t('trashPublicNote')}</p>}
      {err && <p className="err small">{err}</p>}
    </GlassModal>
  )
}

// The trash: what an owner threw away, and the two things they can do about it.
// It is a screen rather than an undo toast because the permanent delete is not
// a decision to make inside a five-second window.
export function TrashScreen({ i18n, me }: { i18n: I18n; me: Me }) {
  const { t } = i18n
  const [list, setList] = useState<ProjectView[] | null>(null)
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [purging, setPurging] = useState<ProjectView | null>(null)

  const load = useCallback(() => {
    listTrash()
      .then((p) => setList(p ?? []))
      .catch(() => setList([]))
  }, [])
  useEffect(load, [load])

  const restore = async (p: ProjectView) => {
    setBusy(p.id)
    setErr('')
    try {
      await restoreProject(p.id)
      load()
      invalidateProjects()
    } catch (e) {
      setErr(generic(t, e, 'restoreFailed'))
    }
    setBusy('')
  }

  const projects = list ?? []
  return (
    <div className="screen">
      <div className="screen-head">
        <h1 className="screen-title">
          <Seams i18n={i18n} k="navTrash" />
        </h1>
        <p className="screen-desc">{t('trashIntro')}</p>
      </div>
      <div className="card">
        {list === null ? (
          <div className="empty">…</div>
        ) : projects.length === 0 ? (
          <Empty icon="trash" text={t('noTrash')} />
        ) : (
          <div className="rows" data-test="trash-rows">
            {projects.map((p) => (
              <div key={p.id} className="row" style={{ cursor: 'default' }}>
                <div className="row-main">
                  <div className="row-title">{p.title}</div>
                  <div className="row-sub">{countOf(t, 'sessionCount', p.session_count)}</div>
                </div>
                <OwnerCell i18n={i18n} me={me} p={p} />
                <ModifiedCell i18n={i18n} at={p.deleted_at} />
                <button className="btn ghost small" disabled={busy === p.id} data-test="restore" onClick={() => restore(p)}>
                  <Ico name="repeat" size={13} /> {busy === p.id ? t('restoring') : t('restore')}
                </button>
                <Tip text={t('purge')}>
                  <button className="icon-btn danger" aria-label={t('purge')} data-test="purge" onClick={() => setPurging(p)}>
                    <Ico name="trash" size={13} />
                  </button>
                </Tip>
              </div>
            ))}
          </div>
        )}
        {err && <p className="err small" style={{ padding: '0 14px 12px' }}>{err}</p>}
      </div>

      {purging && (
        <PurgeDialog
          i18n={i18n}
          p={purging}
          onClose={() => setPurging(null)}
          onDone={() => {
            setPurging(null)
            load()
          }}
        />
      )}
    </div>
  )
}

// The permanent delete's confirmation. It states what is not removed: the
// repositories close rather than being destroyed, because a reproduction the
// agent wrote is evidence and this product does not destroy evidence on a
// click.
function PurgeDialog({ i18n, p, onClose, onDone }: { i18n: I18n; p: ProjectView; onClose: () => void; onDone: () => void }) {
  const { t } = i18n
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const go = async () => {
    setBusy(true)
    setErr('')
    try {
      await purgeProject(p.id)
      onDone()
    } catch (e) {
      setErr(purgeErrLabel(t, e))
      setBusy(false)
    }
  }
  return (
    <GlassModal
      open
      title={t('purgeTitle')}
      onClose={onClose}
      footer={
        <>
          <button className="btn ghost" onClick={onClose} disabled={busy}>
            {t('cancel')}
          </button>
          <button className="btn danger" onClick={go} disabled={busy} data-test="purge-confirm">
            {busy ? t('purging') : t('purgeConfirm')}
          </button>
        </>
      }
    >
      <span className="attestation">{p.title}</span>
      <p className="muted">{t('purgeBody')}</p>
      <p className="muted small">{t('purgeKeepsRepos')}</p>
      {err && <p className="err small">{err}</p>}
    </GlassModal>
  )
}

// The Owner column. The reader's own projects say "You", which is the common
// case and one nobody should have to read a name to recognize. Somebody else's
// shows their profile name, linking to their public page when they have one. A
// name the server could not resolve is a dash: the opaque subject it would
// otherwise fall back to names nobody either.
function OwnerCell({ i18n, me, p }: { i18n: I18n; me: Me; p: ProjectView }) {
  const { t } = i18n
  // Both sides can be absent: a reader with no stake is not sent the owning
  // principal, and a signed-out reader has none of their own. Two absences
  // must not compare as the same person.
  const mine = isMine(me, p.owner)
  const label = mine ? t('ownerYou') : p.owner_name || t('ownerUnknown')
  return (
    <span className="row-owner" data-test="owner">
      {p.owner_handle && !mine ? (
        <a className="link" href={`/u/${p.owner_handle}`} onClick={(e) => e.stopPropagation()}>
          {label}
        </a>
      ) : (
        label
      )}
    </span>
  )
}

// The Last-modified column: an age to scan, the exact moment to read.
function ModifiedCell({ i18n, at }: { i18n: I18n; at?: string }) {
  const rel = relativeTime(i18n.t, i18n.lang, at)
  const tip = tipAttrs(absoluteTime(i18n.lang, at))
  return (
    <span {...tip} className={`row-modified ${tip.className}`} data-test="modified">
      {rel || i18n.t('ownerUnknown')}
    </span>
  )
}

// RowCitation is the first paper's citation and how many others the project
// holds. The registry and the dashboard both render it, because a row is
// scanned for which paper it is about first. It renders nothing when no
// source could fill the record in; the invitation to complete a record lives
// on the project screen, not once per row.
function RowCitation({ i18n, papers }: { i18n: I18n; papers?: PaperCitation[] }) {
  const { t } = i18n
  const { bits, more } = listCitation(papers, t('etAl'))
  if (bits.length === 0) return null
  return (
    <div className="row-cite" data-test="row-citation">
      {[...bits, ...(more > 0 ? [countOf(t, 'morePapers', more)] : [])].join(' · ')}
    </div>
  )
}

export function PublicScreen({ i18n, onOpen }: { i18n: I18n; onOpen: (id: string, title?: string) => void }) {
  const { t } = i18n
  const [list, setList] = useState<ProjectView[] | null>(null)
  useEffect(() => {
    listPublic().then((p) => setList(p ?? [])).catch(() => setList([]))
  }, [])
  return (
    <div className="screen">
      <div className="screen-head">
        <h1 className="screen-title">
          <Seams i18n={i18n} k="navPublic" />
        </h1>
        <p className="screen-desc">{t('publicIntro')}</p>
      </div>
      {list === null ? (
        <div className="empty">…</div>
      ) : list.length === 0 ? (
        <div className="card">
          <Empty icon="globe" text={t('noPublic')} />
        </div>
      ) : (
        <div className="card">
          {/* The same row as the dashboard's: the title, the paper it
              reproduces, then who signed it. Every row here is public and
              signed, so a visibility badge would repeat on every line. */}
          <div className="rows" data-test="registry-rows">
            {list.map((p) => (
              <div key={p.id} className="row" onClick={openFromRow}>
                <div className="row-main">
                  <div className="row-title">
                    <RowLink href={pathOf({ name: 'project', id: p.id })} onOpen={() => onOpen(p.id, p.title)}>
                      {p.title}
                    </RowLink>
                  </div>
                  <RowCitation i18n={i18n} papers={p.papers} />
                  {p.description && (
                    <div className="row-desc" data-test="row-description">
                      {p.description}
                    </div>
                  )}
                  <div className="row-meta">
                    <span>
                      {t('signedBy')}{' '}
                      {p.signer_handle ? (
                        // The link opens the signer's page, so the click must
                        // not also open the row.
                        <a className="link" href={`/u/${p.signer_handle}`} onClick={(e) => e.stopPropagation()}>
                          {signerOf(p)}
                        </a>
                      ) : (
                        signerOf(p)
                      )}
                    </span>
                    <span className="row-meta-dot" aria-hidden="true" />
                    <span>{countOf(t, 'sessionCount', p.session_count)}</span>
                  </div>
                </div>
                <StatusBadge i18n={i18n} p={p} />
                <Ico name="chevronRight" size={15} className="chev" />
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

// StatusBadge shows a project's session status at a glance: how many are
// running, drawn pulsing, else the newest session's terminal phase.
function StatusBadge({ i18n, p }: { i18n: I18n; p: ProjectView }) {
  const { t } = i18n
  if (p.running > 0)
    return (
      <Pill tone="run" dot pulse>
        {t('runningN', { n: String(p.running) })}
      </Pill>
    )
  if (p.latest_phase === 'done')
    return (
      <Pill tone="ok" icon="check">
        {phaseLabel(t, 'done')}
      </Pill>
    )
  if (p.latest_phase === 'failed')
    return (
      <Pill tone="err" icon="alert">
        {phaseLabel(t, 'failed')}
      </Pill>
    )
  return null
}
