// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The project screen (013, 015): a project's title and description, its tabs —
// the papers and their sessions, what the project found, and who works in it —
// with the members card, the findings cards and the verdict rows that fill
// them. The papers card, the credits card and publishing are modules of their
// own.

import { useCallback, useEffect, useMemo, useState } from 'react'
import { CRITERIA, type Criterion, type Findings, rate, scoreOf, verdictTone } from '../findings'
import {
  addMember,
  describeProject,
  draftDescription,
  getFindings,
  getProject,
  getReproduction,
  type Job,
  listMembers,
  listModels,
  listPapers,
  type Me,
  type Member,
  type Project,
  type ProjectCredits,
  type ProjectPaper,
  type PublishBlocker,
  removeMember,
  renameProject,
  type Reproduction,
  restoreProject,
} from '../api'
import { type I18n } from '../i18n'
import { blockerTab, generic } from '../errors'
import { Ico } from '../icons'
import { type ProjectTab, type SessionTab } from '../routes'
import { gradeable, groupByPaper, type Grouped, type PaperGroup } from '../papers'
import { Pill, RowButton, Tip } from '../ui'
import { isMine } from '../account'
import { invalidateProjects } from '../projects-store'
import { CardHead, Empty, Initials, NotReadable, Tab, VisibilityBadge } from './shared'
import { PublishedCard, PublishPanel, PublishWizard } from './publish'
import { ProjectCreditsCard } from './wallet'
import { Decomposition, verdictLabel } from './session'
import { PapersCard } from './papers'

// ProjectDescription is what the project is about, under its title (038).
//
// Edited in place, like the title beside it: a project has one screen, and the
// thing being described is on it. Empty is a state rather than a placeholder —
// its owner is invited to write one, and everybody else is shown nothing at all,
// because a published project should read as what it is, not as a form somebody
// left blank.
function ProjectDescription({
  i18n,
  project,
  canEdit,
  onSaved,
}: {
  i18n: I18n
  project: Project
  canEdit: boolean
  onSaved: () => void
}) {
  const { t } = i18n
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(project.description ?? '')
  const [busy, setBusy] = useState(false)
  const [drafting, setDrafting] = useState(false)
  // Whether this deployment has a model to draft with (039). Asked once, where
  // the tier prices are already asked for.
  const [mayDraft, setMayDraft] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    if (!canEdit) return
    let live = true
    listModels()
      .then((r) => live && setMayDraft(!!r.drafting))
      .catch(() => {})
    return () => {
      live = false
    }
  }, [canEdit])

  const askForADraft = async () => {
    if (drafting) return
    setDrafting(true)
    setErr('')
    try {
      const got = await draftDescription(project.id)
      // Into the editor, never into the project: what is stored is what somebody
      // chose to keep.
      setDraft(got.description)
    } catch {
      setErr(t('draftFailed'))
    } finally {
      setDrafting(false)
    }
  }

  const save = async () => {
    if (busy) return
    setBusy(true)
    setErr('')
    try {
      await describeProject(project.id, draft.trim())
      setEditing(false)
      onSaved()
    } catch {
      setErr(t('describeFailed'))
    } finally {
      setBusy(false)
    }
  }

  if (editing) {
    return (
      <div className="detail-describe" data-test="describe-editor">
        <textarea
          className="text"
          rows={3}
          autoFocus
          maxLength={2000}
          value={draft}
          placeholder={t('describePlaceholder')}
          onChange={(e) => setDraft(e.target.value)}
          data-test="describe-input"
        />
        <div className="detail-describe-foot">
          <button className="btn sm" onClick={save} disabled={busy} data-test="describe-save">
            {busy ? t('saving') : t('save')}
          </button>
          <button
            className="btn ghost sm"
            onClick={() => {
              setDraft(project.description ?? '')
              setEditing(false)
              setErr('')
            }}
            disabled={busy}
          >
            {t('cancel')}
          </button>
          {/* A draft is a suggestion: it lands in the field above for somebody
              to read and change. It is offered inside the editor for that
              reason — there is nowhere else it could land. */}
          {mayDraft && (
            <Tip text={t('draftHint')}>
              <button className="btn ghost sm" onClick={askForADraft} disabled={busy || drafting} data-test="describe-draft">
                <Ico name="sparkles" size={12} /> {drafting ? t('drafting') : t('draftIt')}
              </button>
            </Tip>
          )}
          {err && <span className="err small">{err}</span>}
        </div>
      </div>
    )
  }
  if (!project.description) {
    if (!canEdit) return null
    return (
      <button className="detail-describe-add" onClick={() => setEditing(true)} data-test="describe-add">
        <Ico name="plus" size={12} /> {t('describeAdd')}
      </button>
    )
  }
  return (
    <p className="detail-describe-text" data-test="description">
      {project.description}
      {canEdit && (
        <Tip text={t('describeEdit')}>
          <button
            className="icon-btn"
            aria-label={t('describeEdit')}
            data-test="describe-edit"
            onClick={() => {
              setDraft(project.description ?? '')
              setEditing(true)
            }}
          >
            <Ico name="pencil" size={12} />
          </button>
        </Tip>
      )}
    </p>
  )
}

// EditableTitle shows a project's title and, for its owner, an inline rename:
// a pencil turns the title into an input; Enter or the check saves, Escape
// cancels (006j PATCH /projects/{id}).
function EditableTitle({ i18n, title, canEdit, onRename }: { i18n: I18n; title: string; canEdit: boolean; onRename: (next: string) => Promise<void> }) {
  const { t } = i18n
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(title)
  const [busy, setBusy] = useState(false)
  // Why the last save did not land. A refused rename used to leave the input
  // open and say nothing, which reads as a save that is still on its way.
  const [err, setErr] = useState('')

  const save = async () => {
    const next = draft.trim()
    if (!next || next === title) {
      setEditing(false)
      return
    }
    setBusy(true)
    setErr('')
    try {
      await onRename(next)
      setEditing(false)
    } catch (e) {
      setErr(generic(t, e, 'renameFailed'))
    } finally {
      setBusy(false)
    }
  }

  if (!editing) {
    return (
      // The control belongs to the last word, not to the right-hand edge: inside
      // the heading it follows the text wherever the text ends, which is where a
      // reader's eye already is. Beside it, on a title that wrapped to two lines,
      // it floated off at the end of the row looking like somebody else's button.
      <h1 className="detail-title">
        {title}
        {canEdit && (
          <Tip text={t('rename')}>
            <button className="icon-btn" aria-label={t('rename')} onClick={() => { setDraft(title); setErr(''); setEditing(true) }}>
              <Ico name="pencil" size={13} />
            </button>
          </Tip>
        )}
      </h1>
    )
  }
  return (
    <div className="detail-title-edit">
      <input
        className="text title-input"
        aria-label={t('rename')}
        value={draft}
        autoFocus
        disabled={busy}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') save()
          if (e.key === 'Escape') setEditing(false)
        }}
      />
      <button className="btn sm" disabled={busy} onClick={save}>
        <Ico name="check" size={12} w={2.2} /> {t('save')}
      </button>
      <button className="btn ghost sm" disabled={busy} onClick={() => setEditing(false)}>
        {t('cancel')}
      </button>
      {err && <span className="err small">{err}</span>}
    </div>
  )
}

export function ProjectScreen({
  i18n,
  me,
  id,
  tab,
  onTab,
  onTitle,
  onOpenSession,
  onCreditsSpent,
  onTopUp,
}: {
  i18n: I18n
  me: Me
  id: string
  // Which tab is open comes from the address, so a verdict can be linked to.
  tab: ProjectTab
  onTab: (tab: ProjectTab, correction?: boolean) => void
  onTitle: (title: string) => void
  onOpenSession: (jobId: string, ref: string, projectTitle: string, tab?: SessionTab) => void
  onCreditsSpent: () => void
  onTopUp: () => void
}) {
  const { t } = i18n
  const [project, setProject] = useState<Project | null>(null)
  const [sessions, setSessions] = useState<Job[] | null>(null)
  const [papers, setPapers] = useState<ProjectPaper[] | null>(null)
  const [maySubmit, setMaySubmit] = useState(false)
  const [credits, setCredits] = useState<ProjectCredits | null>(null)
  const [signerHandle, setSignerHandle] = useState('')
  // A project this reader cannot see reads as missing, whether it is private,
  // withdrawn, or never existed (028) — the server will not say which. Without
  // this the screen waited on a fetch that had already failed and showed a
  // loading ellipsis forever.
  const [notReadable, setNotReadable] = useState(false)

  // Papers and sessions are read together and by one component, because the
  // screen renders them joined (015). Fetched separately they are two clocks:
  // a paper just started would read "1 session" over an empty list until
  // whichever refresh landed second.
  const refresh = useCallback(() => {
    getProject(id)
      .then((d) => {
        setProject(d.project)
        setSessions(d.sessions ?? [])
        setMaySubmit(d.may_submit)
        setCredits(d.credits ?? null)
        setSignerHandle(d.signer_handle ?? '')
        setNotReadable(false)
      })
      .catch(() => {
        setSessions([])
        setNotReadable(true)
      })
    listPapers(id)
      .then(setPapers)
      .catch(() => setPapers([]))
  }, [id])
  useEffect(() => {
    refresh()
  }, [refresh])
  // changed is refresh after a write: this screen is re-read, and so is the
  // shared list of projects, whose rows carry the title, the description, the
  // session counts and the visibility that a write here can move.
  const changed = useCallback(() => {
    refresh()
    invalidateProjects()
  }, [refresh])

  // A submit spends allowance, so the header pill is re-read after one.
  const onSpent = useCallback(() => {
    changed()
    onCreditsSpent()
  }, [changed, onCreditsSpent])

  // What an owner may do here. A project in the trash is read-only (019): the
  // server refuses every write on one, so an owner control still on screen is a
  // button that can only fail. The banner above says as much; this is the half
  // that makes it true.
  const isOwner = project != null && isMine(me, project.owner) && !project.deleted_at
  const [publishing, setPublishing] = useState(false)
  // Why a restore did not land, shown in the banner that offered it. A refused
  // restore used to leave the banner exactly as it was.
  const [restoreErr, setRestoreErr] = useState('')
  // A publish refusal names the sessions in the way; this is the way to them.
  // Each opens where its own blocker is legible: a flagged manifest on the
  // verdict pane, which is where the redistribution review is shown, and a run
  // that never finished on its transcript, which is where its status is.
  const openBlocker = useCallback(
    (b: PublishBlocker) => {
      setPublishing(false)
      onOpenSession(b.session_id, b.ref ?? '', project?.title ?? '', blockerTab(b))
    },
    [onOpenSession, project?.title],
  )
  // What this project is, joined: every session under the paper it is a run of.
  // Nothing here derives one session for the project — a project holds papers,
  // and a verdict and a repository belong to the session that produced them.
  const grouped = useMemo(() => groupByPaper(papers ?? [], sessions ?? []), [papers, sessions])

  // The breadcrumb's leaf, once it is known: on a link followed from outside, the
  // shell has the id and nothing to name it with.
  useEffect(() => {
    if (project?.title) onTitle(project.title)
  }, [project?.title, onTitle])

  // People is the owner's tab. A reader following that address gets the sessions
  // they came for and a corrected address, rather than an empty panel.
  useEffect(() => {
    if (tab === 'people' && project !== null && !isOwner) onTab('sessions', true)
  }, [tab, project, isOwner, onTab])

  if (notReadable) return <NotReadable i18n={i18n} me={me} />
  if (project === null) return <div className="center-load">…</div>

  return (
    <div className="tx" style={{ overflowY: 'auto' }}>
      {publishing && (
        <PublishWizard
          i18n={i18n}
          projectID={id}
          onClose={() => setPublishing(false)}
          onPublished={() => {
            setPublishing(false)
            changed()
          }}
          onChanged={changed}
          onOpenBlocker={openBlocker}
        />
      )}
      <div className="detail-head">
        <div className="detail-title-row">
          <div className="detail-title-main">
            <div className="detail-title-line">
              <EditableTitle i18n={i18n} title={project.title} canEdit={isOwner} onRename={async (next) => { await renameProject(id, next); changed() }} />
              <VisibilityBadge i18n={i18n} visibility={project.visibility} />
            </div>
            <ProjectDescription
              i18n={i18n}
              project={project}
              canEdit={isOwner && !project.deleted_at}
              onSaved={changed}
            />
            {project.visibility === 'public' && project.signed_off_at && (
              <div className="detail-meta">
                <Ico name="check" size={12} w={2.2} style={{ color: 'var(--ok)' }} />
                {t('publishedNote', { when: new Date(project.signed_off_at).toLocaleDateString() })}
              </div>
            )}
          </div>
          {/* Publishing is an act an operator looks for on the thing they are
              publishing, not under Collaborators — where it lived because that
              was the tab with room (021e). The card there keeps the sign-off
              record and the wizard stays the one path through the decision. */}
          {isOwner && project.visibility === 'private' && !project.deleted_at && (
            <button className="btn" data-test="publish-project" onClick={() => setPublishing(true)}>
              <Ico name="globe" size={13} /> {t('publishStart')}
            </button>
          )}
        </div>
        <div className="detail-tabs">
          <Tab on={tab === 'sessions'} onClick={() => onTab('sessions')} icon="layers" label={t('projectSessions')} />
          <Tab on={tab === 'verdict'} onClick={() => onTab('verdict')} icon="gauge" label={t('verdict')} />
          {isOwner && <Tab on={tab === 'people'} onClick={() => onTab('people')} icon="users" label={t('members')} />}
        </div>
      </div>

      <div className="screen" style={{ animation: 'none' }}>
        {/* A trashed project keeps its address (013), so its owner can still
            follow a link to it — and has to be told why nothing here works,
            with the way back on the same line (019). */}
        {project.deleted_at && (
          <div className="banner warn" data-test="trashed-banner">
            <Ico name="trash" size={14} />
            <span style={{ flex: 1 }}>{t('trashedBanner')}</span>
            {restoreErr && <span className="err small">{restoreErr}</span>}
            <button
              className="btn ghost small"
              data-test="restore"
              onClick={async () => {
                setRestoreErr('')
                try {
                  await restoreProject(id)
                  changed()
                } catch (e) {
                  setRestoreErr(generic(t, e, 'restoreFailed'))
                }
              }}
            >
              <Ico name="repeat" size={13} /> {t('restore')}
            </button>
          </div>
        )}
        {tab === 'sessions' && (
          // The work on the left, the standing facts on the right. Stacked, the
          // credits card ran the full width of the screen to show two numbers
          // and a stepper, and pushed the sessions — the reason the tab exists —
          // below the fold.
          <div className="detail-grid">
            <PapersCard
              i18n={i18n}
              me={me}
              project={project}
              grouped={grouped}
              loading={papers === null}
              credits={credits ?? undefined}
              mayWork={maySubmit}
              onOpenSession={(s) => onOpenSession(s.id, s.ref, project.title)}
              onChanged={onSpent}
              onRefresh={changed}
            />
            <div className="stack">
              {/* Money cannot move in or out of a trashed project either, so the
                  card that moves it goes with the rest of the owner controls. */}
              {credits && !project.deleted_at && <ProjectCreditsCard i18n={i18n} projectID={id} credits={credits} onMoved={onSpent} onTopUp={onTopUp} />}
              {/* One card per paper that has a graded session (021e). Not "the
                  project's rate": a project holds papers and only a session has
                  findings, so a single figure on the project would be the exact
                  claim 015 was written to remove. */}
              <LatestFindings i18n={i18n} grouped={grouped} onOpen={(s) => onOpenSession(s.id, s.ref, project.title, 'verdict')} />
            </div>
          </div>
        )}
        {tab === 'verdict' && <ProjectVerdicts i18n={i18n} grouped={grouped} onOpen={(s) => onOpenSession(s.id, s.ref, project.title, 'verdict')} />}
        {tab === 'people' && isOwner && (
          <div className="detail-grid">
            <div className="stack">
              <Members i18n={i18n} projectID={id} />
            </div>
            {project.visibility === 'private' ? (
              <PublishPanel i18n={i18n} projectID={id} onPublished={changed} onOpenBlocker={openBlocker} />
            ) : (
              <PublishedCard i18n={i18n} project={project} signerHandle={signerHandle} onWithdrawn={changed} />
            )}
          </div>
        )}
      </div>
    </div>
  )
}

// LatestFindings summarises the newest graded session of each paper the project
// holds (021e) — its inner-loop decomposition and the open-science scorecard.
//
// It is per paper on purpose. The operator design puts one rate on the project
// header, which is true only while a project holds one paper; ours hold many
// (012), and 015 exists because two screens made exactly that assumption and
// showed one paper's reproduction under another paper's name. Each card names
// the session it read and links to it.
function LatestFindings({ i18n, grouped, onOpen }: { i18n: I18n; grouped: Grouped; onOpen: (job: Job) => void }) {
  // `done` and a repository, not "terminal and a repository". RepoName marks
  // that publishing *began* and `publishing → failed` is legal (015), so a
  // paper whose newest session reached publishing and then died would select
  // that session, find no findings on it, and hide the graded run underneath.
  const cards = grouped.papers
    .map((g) => ({ paper: g.paper, job: g.sessions.find((s) => s.phase === 'done' && !!s.repo_name) }))
    .filter((c): c is { paper: PaperGroup['paper']; job: Job } => !!c.job)
  if (cards.length === 0) return null
  return (
    <div className="findings-rail">
      {cards.map((c) => (
        <LatestFindingsCard key={c.paper.key} i18n={i18n} paper={c.paper} job={c.job} onOpen={() => onOpen(c.job)} />
      ))}
    </div>
  )
}

// LatestFindingsCard reads one session's findings. A paper whose newest
// published session has none renders nothing rather than a zeroed card: a
// decomposition of nought components is not a finding about the paper.
function LatestFindingsCard({ i18n, paper, job, onOpen }: { i18n: I18n; paper: PaperGroup['paper']; job: Job; onOpen: () => void }) {
  const { t } = i18n
  const [f, setF] = useState<Findings | null>(null)
  useEffect(() => {
    let live = true
    getFindings(job.id)
      .then((r) => live && setF(r))
      .catch(() => live && setF(null))
    return () => {
      live = false
    }
  }, [job.id])
  if (!f) return null
  const r = rate(f.components)
  const scores = f.scorecard ?? []
  return (
    <div className="card card-pad stack" style={{ gap: 12 }}>
      <div className="stack" style={{ gap: 3 }}>
        <span className="eyebrow">{t('latestSessionInner')}</span>
        <span className="small muted" title={paper.title}>
          {paper.title || paper.ref}
        </span>
      </div>
      <div className="findings-decomp">
        {r !== null && <span className="findings-rate" style={{ fontSize: 28 }}>{r.toFixed(2)}</span>}
        <span className="small muted">{t('ofComponents', { n: String(f.components.total) })}</span>
      </div>
      <Decomposition c={f.components} />
      <div className="stack" style={{ gap: 6 }}>
        <CountLine tone="var(--ok)" label={t('outVerified')} n={f.components.verified} total={f.components.total} />
        <CountLine tone="var(--warn)" label={t('outPartial')} n={f.components.partial} total={f.components.total} />
        <CountLine tone="var(--fg-3)" label={t('outBlocked')} n={f.components.blocked} total={f.components.total} />
      </div>
      {scores.length > 0 && (
        <div className="stack" style={{ gap: 7, paddingTop: 4, borderTop: '1px solid var(--border)' }}>
          <span className="eyebrow">{t('scorecard')}</span>
          {CRITERIA.map((c) => {
            const sc = scoreOf(f, c)
            if (!sc) return null
            return (
              <div key={c} className="score-row">
                <span className="score-mark" style={{ color: sc.found ? 'var(--ok)' : 'var(--err)' }}>{sc.found ? '✓' : '✕'}</span>
                <span style={{ color: 'var(--fg-2)' }}>{t(scoreKey(c))}</span>
              </div>
            )
          })}
        </div>
      )}
      <button className="btn ghost sm" onClick={onOpen}>
        <Ico name="gauge" size={12} /> {t('openVerdict')}
      </button>
    </div>
  )
}

// scoreKey maps a criterion to its label key, so the rail and the artifacts
// pane cannot end up naming the same criterion two different ways.
const scoreKey = (c: Criterion) =>
  c === 'code' ? 'critCode' : c === 'data' ? 'critData' : c === 'license' ? 'critLicense' : c === 'preregistration' ? 'critPreregistration' : 'critSupplement'

// CountLine is one row of the decomposition legend: a swatch, a label, and the
// count over its total — the total repeated on every line because the number
// that matters is the share, not the count.
function CountLine({ tone, label, n, total }: { tone: string; label: string; n: number; total: number }) {
  return (
    <div className="score-row">
      <span style={{ width: 7, height: 7, borderRadius: 2, background: tone, flex: 'none' }} />
      <span style={{ color: 'var(--fg-2)' }}>{label}</span>
      <span className="mono small tabular" style={{ marginLeft: 'auto' }}>
        {n} / {total}
      </span>
    </div>
  )
}

// ProjectVerdicts is what a project found: every session that finished with a
// repository, under the paper it is a run of, each row opening that session's
// own verdict (015).
//
// It is the read across papers that the deleted project-level verdict tab was
// pretending to be. That one picked the newest session with a repository and
// showed it under the project's name — so a project's second paper hid its
// first, and a run that failed while publishing could be presented as the
// result. Here every graded run has a row and reads its own manifest.
function ProjectVerdicts({ i18n, grouped, onOpen }: { i18n: I18n; grouped: Grouped; onOpen: (job: Job) => void }) {
  const { t } = i18n
  const rows = useMemo(() => gradeable(grouped), [grouped])
  if (rows.length === 0)
    return (
      <div className="card">
        <Empty icon="gauge" text={t('noVerdict')} />
      </div>
    )
  return (
    <div className="card">
      <CardHead label={t('verdict')} icon="gauge" tone="var(--ok)" />
      <div className="rows">
        {rows.map(({ paper, session }) => (
          <VerdictRow key={session.id} i18n={i18n} paper={paper} job={session} onOpen={() => onOpen(session)} />
        ))}
      </div>
    </div>
  )
}

// VerdictRow reads one session's manifest. Each row fetches its own, so a slow
// forge delays a row rather than the page, and a repository that cannot be read
// says so where it is rather than emptying the view.
function VerdictRow({ i18n, paper, job, onOpen }: { i18n: I18n; paper: PaperGroup['paper']; job: Job; onOpen: () => void }) {
  const { t } = i18n
  const [repro, setRepro] = useState<Reproduction | null>(null)
  const [missing, setMissing] = useState(false)
  // The paper's verdict, when the run recorded one. The manifest's grade is the
  // rubric's judgement of the run, so a list about papers must not print it in
  // the column a reader reads as "did this reproduce" — that was the same
  // conflation the session pane had, one level up.
  const [findings, setFindings] = useState<Findings | null>(null)
  useEffect(() => {
    let live = true
    getReproduction(job.id)
      .then((r) => live && setRepro(r))
      .catch(() => live && setMissing(true))
    getFindings(job.id)
      .then((f) => live && setFindings(f))
      .catch(() => live && setFindings(null))
    return () => {
      live = false
    }
  }, [job.id])
  const tone = findings ? verdictTone(findings.verdict) : 'var(--fg-3)'
  return (
    <RowButton className="row file-row" onOpen={onOpen}>
      <span className="row-title">{paper.title || paper.ref}</span>
      <span className="muted small">{job.created_at ? new Date(job.created_at).toLocaleDateString() : ''}</span>
      {repro?.review && <Pill tone={repro.review === 'clear' ? 'ok' : 'warn'}>{repro.review}</Pill>}
      <span className="verdict-cell" style={{ color: tone }} data-test="paper-verdict">
        {findings ? (
          <>
            {verdictLabel(findings.verdict)}
            <span className="muted small tabular">{decompositionOf(findings)}</span>
          </>
        ) : missing ? (
          <>
            <Ico name="alert" size={13} /> {t('verdictUnread')}
          </>
        ) : repro ? (
          // The run finished and recorded no findings, so this paper has no
          // verdict — saying so is the finding, and printing the rubric grade
          // here would answer a question nobody asked.
          <span className="muted small">{t('noVerdictRecorded')}</span>
        ) : (
          '…'
        )}
      </span>
    </RowButton>
  )
}

// decompositionOf is the counts the verdict word summarises, so a row never
// carries a judgement without the slicing it came from.
function decompositionOf(f: Findings): string {
  const c = f.components
  return c?.total ? `${c.verified}/${c.total}` : ''
}

function Members({ i18n, projectID }: { i18n: I18n; projectID: string }) {
  const { t } = i18n
  const [members, setMembers] = useState<Member[]>([])
  const [email, setEmail] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const refresh = useCallback(() => {
    listMembers(projectID).then((m) => setMembers(m ?? [])).catch(() => setMembers([]))
  }, [projectID])
  useEffect(() => {
    refresh()
  }, [refresh])

  const invite = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!email.trim() || busy) return
    setBusy(true)
    setErr('')
    try {
      await addMember(projectID, email.trim())
      setEmail('')
      refresh()
    } catch {
      setErr(t('inviteError'))
    } finally {
      setBusy(false)
    }
  }
  // A refused removal used to be swallowed and the list re-read, so the person
  // still on it looked, to the owner who clicked, like a removal that worked.
  const drop = async (target: string) => {
    setErr('')
    try {
      await removeMember(projectID, target)
    } catch (e) {
      setErr(generic(t, e, 'removeMemberFailed'))
    }
    refresh()
  }
  return (
    <div className="card">
      <CardHead label={t('members')} icon="users" tone="var(--brand)" />
      {members.length === 0 ? (
        <Empty icon="userPlus" text={t('noMembers')} />
      ) : (
        members.map((m) => (
          <div key={m.email} className="member-row">
            <Initials email={m.email} />
            <span className="member-email">{m.email}</span>
            {!m.bound && (
              <Pill tone="warn" icon="clock">
                {t('pending')}
              </Pill>
            )}
            <Tip text={t('remove')}>
              <button className="icon-btn danger" aria-label={t('remove')} onClick={() => drop(m.email)}>
                <Ico name="trash" size={13} />
              </button>
            </Tip>
          </div>
        ))
      )}
      <form className="field-row" onSubmit={invite}>
        <input className="text" style={{ flex: 1 }} type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder={t('inviteEmail')} spellCheck={false} />
        <button className="btn" disabled={busy}>
          <Ico name="mail" size={13} /> {busy ? t('submitting') : t('invite')}
        </button>
      </form>
      <div style={{ padding: '0 14px 12px' }}>
        <span className="muted small">{t('inviteHint')}</span>
      </div>
      {err && <p className="err small" style={{ padding: '0 14px 12px' }}>{err}</p>}
    </div>
  )
}
