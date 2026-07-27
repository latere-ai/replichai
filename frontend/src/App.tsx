import { useCallback, useEffect, useRef, useState } from 'react'
import { fileKind, highlightIn, isExternalHref, langOf, renderMarkdown, resolveRelative } from './artifact'
import {
  addMember,
  artifactUrl,
  cancelSession,
  createProject,
  getArtifact,
  getJob,
  getMe,
  getProject,
  getReproduction,
  getTranscript,
  listArtifacts,
  listMembers,
  listProjects,
  listPublic,
  listShared,
  makeProjectPublic,
  removeMember,
  renameProject,
  resolvePaper,
  submitPaper,
  watchJob,
  type ResolvedPaper,
  type Event,
  type Job,
  type Me,
  type Member,
  type Project,
  type ProjectView,
  type Reproduction,
  type Transcript,
  type Usage,
} from './api'
import { useI18n } from './i18n'

type I18n = ReturnType<typeof useI18n>

// A view is where the operator is. Navigation is state, not a router.
type View =
  | { name: 'projects' }
  | { name: 'public' }
  | { name: 'submit' }
  | { name: 'settings' }
  | { name: 'project'; id: string; title?: string }
  | { name: 'session'; jobId: string; projectId: string; ref?: string; projectTitle?: string }

// useTheme persists the light/dark choice and stamps it on <html>. Dark is the
// default, matching the operator console.
function useTheme(): [string, () => void] {
  const [theme, setTheme] = useState(() => localStorage.getItem('theme') || 'dark')
  useEffect(() => {
    document.documentElement.dataset.theme = theme
    localStorage.setItem('theme', theme)
  }, [theme])
  return [theme, () => setTheme((t) => (t === 'dark' ? 'light' : 'dark'))]
}

const terminal = (p: string) => p === 'done' || p === 'failed'
// phaseClass tones a phase badge: any non-terminal phase reads as "running".
const phaseClass = (p: string) => (terminal(p) ? p : 'running')

export default function App() {
  const i18n = useI18n()
  const [theme, toggleTheme] = useTheme()
  const [me, setMe] = useState<Me | null>(null)

  useEffect(() => {
    getMe().then(setMe).catch(() => setMe({ authenticated: false }))
  }, [])

  if (me === null) return <div className="center-load">…</div>
  if (!me.authenticated) return <SignIn i18n={i18n} />
  if ((me.tier ?? 'none') === 'none') return <NotInvited i18n={i18n} email={me.email ?? ''} />
  return <Shell i18n={i18n} me={me} theme={theme} toggleTheme={toggleTheme} />
}

/* ─── Auth screens ─── */

function Logo({ big }: { big?: boolean }) {
  return <div className={big ? 'auth-logo' : 'rail-logo'}>r</div>
}

function SignIn({ i18n }: { i18n: I18n }) {
  return (
    <div className="auth-center">
      <div className="auth-card">
        <Logo big />
        <h2>replichai</h2>
        <p>{i18n.t('tagline')}</p>
        <a className="btn lg" href="/login" style={{ justifyContent: 'center' }}>
          {i18n.t('signInWith')}
        </a>
      </div>
    </div>
  )
}

function NotInvited({ i18n, email }: { i18n: I18n; email: string }) {
  return (
    <div className="auth-center">
      <div className="auth-card">
        <Logo big />
        <h2>{i18n.t('notInvitedTitle')}</h2>
        <p>{i18n.t('notInvitedBody', { email })}</p>
      </div>
    </div>
  )
}

/* ─── Shell ─── */

const Icon = {
  folder: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z',
  globe: 'M3 12h18M12 3c2.5 2.7 2.5 15.3 0 18M12 3c-2.5 2.7-2.5 15.3 0 18',
  gear: '', // rendered as circle below
  plus: 'M12 5v14M5 12h14',
  moon: 'M12 3a9 9 0 1 0 9 9 7 7 0 0 1-9-9Z',
  back: 'm15 6-6 6 6 6',
  chev: 'm9 6 6 6-6 6',
  x: 'M6 6l12 12M18 6L6 18',
  pencil: 'M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z',
}

function Svg({ d, size = 14, w = 1.7 }: { d: string; size?: number; w?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={w} strokeLinecap="round" strokeLinejoin="round">
      {d === 'globe' ? (
        <>
          <circle cx="12" cy="12" r="9" />
          <path d={Icon.globe} />
        </>
      ) : (
        <path d={d} />
      )}
    </svg>
  )
}

function Shell({ i18n, me, theme, toggleTheme }: { i18n: I18n; me: Me; theme: string; toggleTheme: () => void }) {
  const { t } = i18n
  const [view, setView] = useState<View>({ name: 'projects' })
  const openProject = (id: string, title?: string) => setView({ name: 'project', id, title })
  const openSession = (jobId: string, projectId: string, ref?: string, projectTitle?: string) =>
    setView({ name: 'session', jobId, projectId, ref, projectTitle })

  return (
    <div className="shell">
      <nav className="rail">
        <div className="rail-head">
          <Logo />
          <span className="rail-name">replichai</span>
          <span className="rail-beta">BETA</span>
        </div>
        <div className="rail-nav">
          <div className="rail-group">
            <div className="eyebrow">{t('workspace')}</div>
            <button className={'nav-btn' + (view.name === 'projects' || view.name === 'project' || view.name === 'session' ? ' active' : '')} onClick={() => setView({ name: 'projects' })}>
              <Svg d={Icon.folder} /> {t('navProjects')}
            </button>
            <button className={'nav-btn' + (view.name === 'public' ? ' active' : '')} onClick={() => setView({ name: 'public' })}>
              <Svg d="globe" /> {t('navPublic')}
            </button>
          </div>
        </div>
        <div className="rail-foot">
          <button className="user-btn" onClick={() => setView({ name: 'settings' })}>
            <span className="user-avatar">{initials(me.email)}</span>
            <span className="user-meta">
              <span className="user-email">{me.email}</span>
              <span className="user-tier">{tierLabel(t, me.tier)}</span>
            </span>
          </button>
        </div>
      </nav>

      <div className="main">
        <header className="topbar">
          <Breadcrumb i18n={i18n} view={view} setView={setView} />
          <div className="topbar-actions">
            <button className="icon-btn" onClick={toggleTheme} title={theme === 'dark' ? 'Light' : 'Dark'} aria-label="theme">
              <Svg d={Icon.moon} />
            </button>
            <button className="btn" onClick={() => setView({ name: 'submit' })}>
              <Svg d={Icon.plus} size={13} w={2.1} /> {t('newProject')}
            </button>
          </div>
        </header>

        {view.name === 'projects' && <ProjectsScreen i18n={i18n} onOpen={openProject} />}
        {view.name === 'public' && <PublicScreen i18n={i18n} onOpen={openProject} />}
        {view.name === 'submit' && <SubmitScreen i18n={i18n} onSubmitted={(id) => openProject(id)} />}
        {view.name === 'settings' && <SettingsScreen i18n={i18n} me={me} theme={theme} toggleTheme={toggleTheme} />}
        {view.name === 'project' && <ProjectScreen i18n={i18n} me={me} id={view.id} onOpenSession={(jobId, ref, title) => openSession(jobId, view.id, ref, title)} />}
        {view.name === 'session' && (
          <SessionScreen i18n={i18n} jobId={view.jobId} onBack={() => openProject(view.projectId, view.projectTitle)} />
        )}
      </div>
    </div>
  )
}

function Breadcrumb({ i18n, view, setView }: { i18n: I18n; view: View; setView: (v: View) => void }) {
  const { t } = i18n
  const root = () =>
    view.name === 'public' ? t('navPublic') : view.name === 'submit' ? t('newProject') : view.name === 'settings' ? t('settings') : t('navProjects')
  const rootView: View = view.name === 'public' ? { name: 'public' } : { name: 'projects' }
  return (
    <div className="crumb">
      <button onClick={() => setView(rootView)}>{root()}</button>
      {view.name === 'project' && view.title && (
        <>
          <span className="sep">/</span>
          <span className="leaf">{view.title}</span>
        </>
      )}
      {view.name === 'session' && (
        <>
          <span className="sep">/</span>
          <button onClick={() => setView({ name: 'project', id: view.projectId, title: view.projectTitle })}>{view.projectTitle ?? '…'}</button>
          <span className="sep">/</span>
          <span className="leaf">{view.ref ?? t('transcript')}</span>
        </>
      )}
    </div>
  )
}

/* ─── Projects ─── */

function ProjectsScreen({ i18n, onOpen }: { i18n: I18n; onOpen: (id: string, title?: string) => void }) {
  const { t } = i18n
  const [tab, setTab] = useState<'mine' | 'shared'>('mine')
  const [mine, setMine] = useState<ProjectView[] | null>(null)
  const [shared, setShared] = useState<ProjectView[]>([])

  useEffect(() => {
    listProjects().then((p) => setMine(p ?? [])).catch(() => setMine([]))
    listShared().then((p) => setShared(p ?? [])).catch(() => setShared([]))
  }, [])

  const list = tab === 'mine' ? mine ?? [] : shared
  const sessionTotal = (mine ?? []).reduce((n, p) => n + p.session_count, 0)

  return (
    <div className="screen">
      <div className="screen-head">
        <h1 className="screen-title">{t('navProjects')}</h1>
        <p className="screen-desc">{t('projectsIntro')}</p>
      </div>

      <div className="stat-grid">
        <div className="stat">
          <span className="eyebrow">{t('statProjects')}</span>
          <span className="stat-value">{mine === null ? '—' : mine.length}</span>
        </div>
        <div className="stat">
          <span className="eyebrow">{t('statSessions')}</span>
          <span className="stat-value">{mine === null ? '—' : sessionTotal}</span>
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <div className="segmented">
            <button className={'seg-btn' + (tab === 'mine' ? ' on' : '')} onClick={() => setTab('mine')}>
              {t('myProjects')}
            </button>
            <button className={'seg-btn' + (tab === 'shared' ? ' on' : '')} onClick={() => setTab('shared')}>
              {t('sharedWithMe')}
            </button>
          </div>
        </div>
        {mine === null ? (
          <div className="empty">…</div>
        ) : list.length === 0 ? (
          <div className="empty">{tab === 'mine' ? t('noProjects') : t('noShared')}</div>
        ) : (
          <div className="rows">
            {list.map((p) => (
              <div key={p.id} className="row" onClick={() => onOpen(p.id, p.title)}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div className="row-title">{p.title}</div>
                  <div className="row-sub">
                    {tab === 'shared' ? `${t('initiator')}: ${p.owner} · ` : ''}
                    {t('sessionCount', { n: String(p.session_count) })}
                  </div>
                </div>
                <StatusBadge i18n={i18n} p={p} />
                <span className={'badge ' + p.visibility}>{t(p.visibility)}</span>
                <Svg d={Icon.chev} size={14} />
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

/* ─── Public registry (zone) ─── */

function PublicScreen({ i18n, onOpen }: { i18n: I18n; onOpen: (id: string, title?: string) => void }) {
  const { t } = i18n
  const [list, setList] = useState<ProjectView[] | null>(null)
  useEffect(() => {
    listPublic().then((p) => setList(p ?? [])).catch(() => setList([]))
  }, [])
  return (
    <div className="screen">
      <div className="screen-head">
        <h1 className="screen-title">{t('navPublic')}</h1>
        <p className="screen-desc">{t('publicIntro')}</p>
      </div>
      <div className="card">
        {list === null ? (
          <div className="empty">…</div>
        ) : list.length === 0 ? (
          <div className="empty">{t('noPublic')}</div>
        ) : (
          <div className="rows">
            {list.map((p) => (
              <div key={p.id} className="row" onClick={() => onOpen(p.id, p.title)}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div className="row-title">{p.title}</div>
                  <div className="row-sub">
                    {t('initiator')}: {p.owner} · {t('sessionCount', { n: String(p.session_count) })}
                  </div>
                </div>
                <StatusBadge i18n={i18n} p={p} />
                <span className="badge public">{t('public')}</span>
                <Svg d={Icon.chev} size={14} />
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

/* ─── Submit / New audit ─── */

// A paper row in the New-project form: the reference, and its live-resolved
// preview (title) or error, filled by the Resolve button.
type PaperRow = { url: string; preview?: ResolvedPaper; error?: string; resolving?: boolean }

// sessionErrLabel turns a per-paper error code from POST /projects into a short
// human label.
function sessionErrLabel(t: I18n['t'], code?: string): string {
  if (code === 'active_session') return t('errActiveSession')
  if (code === 'unresolvable_paper') return t('errUnresolvable')
  return t('errEnqueue')
}

function SubmitScreen({ i18n, onSubmitted }: { i18n: I18n; onSubmitted: (projectId: string) => void }) {
  const { t } = i18n
  const [title, setTitle] = useState('')
  const [rows, setRows] = useState<PaperRow[]>([{ url: '' }])
  const [collaborators, setCollaborators] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const setRow = (i: number, patch: Partial<PaperRow>) => setRows((rs) => rs.map((r, k) => (k === i ? { ...r, ...patch } : r)))
  const addRow = () => setRows((rs) => [...rs, { url: '' }])
  const removeRow = (i: number) => setRows((rs) => (rs.length > 1 ? rs.filter((_, k) => k !== i) : rs))

  // Resolve one row's reference and show the previewed title (or an error).
  const resolveRow = async (i: number) => {
    const url = rows[i].url.trim()
    if (!url) return
    setRow(i, { resolving: true, preview: undefined, error: undefined })
    try {
      const preview = await resolvePaper(url)
      setRow(i, { preview, resolving: false })
    } catch (e) {
      const code = (e as Error).message
      setRow(i, { error: code === '400' ? t('unresolvablePaper') : t('resolveFailed'), resolving: false })
    }
  }

  const submit = async () => {
    const papers = rows.map((r) => r.url.trim()).filter(Boolean)
    if (papers.length === 0) {
      setErr(t('noPapers'))
      return
    }
    if (busy) return
    setBusy(true)
    setErr('')
    const collabs = collaborators.split(/[,\s]+/).map((s) => s.trim()).filter(Boolean)
    try {
      const { status, data } = await createProject(title.trim(), papers, collabs)
      if (status === 201 && data.project_id) {
        onSubmitted(data.project_id)
        return
      }
      // 422: every paper failed — surface the per-paper reasons on their rows.
      const byURL = new Map((data.sessions ?? []).map((s) => [s.url, s.error]))
      setRows((rs) => rs.map((r) => (byURL.has(r.url.trim()) ? { ...r, error: sessionErrLabel(t, byURL.get(r.url.trim())) } : r)))
      setErr(t('allPapersFailed'))
    } catch {
      setErr(t('submitError'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="screen">
      <div className="submit-form">
        <div className="screen-head">
          <h1 className="screen-title">{t('newProject')}</h1>
          <p className="screen-desc">{t('newProjectDesc')}</p>
        </div>

        <label className="field">
          {t('projectNameLabel')}
          <input className="text" value={title} onChange={(e) => setTitle(e.target.value)} placeholder={t('projectNamePlaceholder')} spellCheck={false} />
        </label>

        <div className="field">
          {t('papersLabel')}
          <div className="stack" style={{ gap: 10 }}>
            {rows.map((row, i) => (
              <div key={i} className="paper-row">
                <div className="paper-row-input">
                  <input
                    className="text"
                    value={row.url}
                    onChange={(e) => setRow(i, { url: e.target.value, preview: undefined, error: undefined })}
                    placeholder={t('paperPlaceholder')}
                    spellCheck={false}
                    onKeyDown={(e) => e.key === 'Enter' && resolveRow(i)}
                  />
                  <button className="btn ghost sm" disabled={!row.url.trim() || row.resolving} onClick={() => resolveRow(i)}>
                    {row.resolving ? t('resolvingPaper') : t('resolve')}
                  </button>
                  {rows.length > 1 && (
                    <button className="icon-btn" aria-label={t('removePaper')} onClick={() => removeRow(i)}>
                      <Svg d={Icon.x} size={13} w={1.8} />
                    </button>
                  )}
                </div>
                {row.preview && <div className="paper-preview">{row.preview.title || row.preview.key}</div>}
                {row.error && <div className="paper-preview err">{row.error}</div>}
              </div>
            ))}
          </div>
          <button className="btn ghost sm" onClick={addRow} style={{ alignSelf: 'flex-start', marginTop: 4 }}>
            <Svg d={Icon.plus} size={12} w={2} /> {t('addPaper')}
          </button>
        </div>

        <label className="field">
          {t('collaboratorsLabel')}
          <input className="text" value={collaborators} onChange={(e) => setCollaborators(e.target.value)} placeholder={t('collaboratorsPlaceholder')} spellCheck={false} />
          <span className="attestation">{t('collaboratorsHint')}</span>
        </label>

        <div className="submit-input-row">
          <button className="btn lg" disabled={busy} onClick={submit}>
            {busy ? t('creatingProject') : t('createProjectBtn')}
          </button>
          <span className="attestation" style={{ alignSelf: 'center' }}>{t('submitNoSpend')}</span>
        </div>
        {err && <p className="err small">{err}</p>}
      </div>
    </div>
  )
}

/* ─── Settings ─── */

function SettingsScreen({ i18n, me, theme, toggleTheme }: { i18n: I18n; me: Me; theme: string; toggleTheme: () => void }) {
  const { t } = i18n
  return (
    <div className="screen">
      <div className="screen-head">
        <h1 className="screen-title">{t('settings')}</h1>
      </div>
      <div className="card card-pad stack" style={{ maxWidth: 520 }}>
        <div className="usage-rows">
          <div className="usage-line">
            {t('signedInAs')}
            <span className="v">{me.email}</span>
          </div>
          <div className="usage-line">
            {t('access')}
            <span className="v">{tierLabel(t, me.tier)}</span>
          </div>
          <div className="usage-line">
            {t('theme')}
            <span className="v">
              <button className="btn ghost sm" onClick={toggleTheme}>
                {theme === 'dark' ? t('themeDark') : t('themeLight')}
              </button>
            </span>
          </div>
        </div>
        <a className="btn ghost" href="/logout" style={{ justifyContent: 'center' }}>
          {t('signOut')}
        </a>
      </div>
    </div>
  )
}

/* ─── Project detail ─── */

// EditableTitle shows a project's title and, for its owner, an inline rename:
// a pencil turns the title into an input; Enter or the check saves, Escape
// cancels (006j PATCH /projects/{id}).
function EditableTitle({ i18n, title, canEdit, onRename }: { i18n: I18n; title: string; canEdit: boolean; onRename: (next: string) => Promise<void> }) {
  const { t } = i18n
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(title)
  const [busy, setBusy] = useState(false)

  const save = async () => {
    const next = draft.trim()
    if (!next || next === title) {
      setEditing(false)
      return
    }
    setBusy(true)
    try {
      await onRename(next)
      setEditing(false)
    } finally {
      setBusy(false)
    }
  }

  if (!editing) {
    return (
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0 }}>
        <h1 className="detail-title">{title}</h1>
        {canEdit && (
          <button className="icon-btn" aria-label={t('rename')} onClick={() => { setDraft(title); setEditing(true) }}>
            <Svg d={Icon.pencil} size={13} w={1.7} />
          </button>
        )}
      </div>
    )
  }
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
      <input
        className="text"
        style={{ minWidth: 280, fontSize: 15 }}
        value={draft}
        autoFocus
        disabled={busy}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') save()
          if (e.key === 'Escape') setEditing(false)
        }}
      />
      <button className="btn ghost sm" disabled={busy} onClick={save}>
        {t('save')}
      </button>
      <button className="btn ghost sm" disabled={busy} onClick={() => setEditing(false)}>
        {t('cancel')}
      </button>
    </div>
  )
}

function ProjectScreen({
  i18n,
  me,
  id,
  onOpenSession,
}: {
  i18n: I18n
  me: Me
  id: string
  onOpenSession: (jobId: string, ref: string, projectTitle: string) => void
}) {
  const { t } = i18n
  const [project, setProject] = useState<Project | null>(null)
  const [sessions, setSessions] = useState<Job[] | null>(null)
  const [tab, setTab] = useState<'sessions' | 'verdict' | 'artifacts' | 'people'>('sessions')

  const refresh = useCallback(() => {
    getProject(id)
      .then((d) => {
        setProject(d.project)
        setSessions(d.sessions ?? [])
      })
      .catch(() => setSessions([]))
  }, [id])
  useEffect(() => {
    refresh()
  }, [refresh])

  const isOwner = project != null && project.owner === me.principal
  // The latest published session (with a repository) backs the verdict and
  // artifact views; sessions are newest-first.
  const published = (sessions ?? []).find((s) => s.repo_name) ?? null

  if (project === null) return <div className="center-load">…</div>

  return (
    <div className="tx" style={{ overflowY: 'auto' }}>
      <div className="detail-head">
        <div className="detail-title-row">
          <div style={{ minWidth: 0 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 9, flexWrap: 'wrap' }}>
              <EditableTitle i18n={i18n} title={project.title} canEdit={isOwner} onRename={async (next) => { await renameProject(id, next); refresh() }} />
              <span className={'badge ' + project.visibility}>{t(project.visibility)}</span>
            </div>
            {project.visibility === 'public' && project.signed_off_at && (
              <div className="detail-meta">{t('publishedNote', { when: new Date(project.signed_off_at).toLocaleDateString() })}</div>
            )}
          </div>
          {isOwner && (
            <div className="detail-actions">
              <button className="btn ghost" onClick={() => setTab('sessions')}>
                {t('addReplication')}
              </button>
            </div>
          )}
        </div>
        <div className="detail-tabs">
          <button className={'tab' + (tab === 'sessions' ? ' on' : '')} onClick={() => setTab('sessions')}>
            {t('projectSessions')}
          </button>
          <button className={'tab' + (tab === 'verdict' ? ' on' : '')} onClick={() => setTab('verdict')}>
            {t('verdict')}
          </button>
          <button className={'tab' + (tab === 'artifacts' ? ' on' : '')} onClick={() => setTab('artifacts')}>
            {t('artifacts')}
          </button>
          {isOwner && (
            <button className={'tab' + (tab === 'people' ? ' on' : '')} onClick={() => setTab('people')}>
              {t('members')}
            </button>
          )}
        </div>
      </div>

      <div className="screen" style={{ animation: 'none' }}>
        {tab === 'sessions' && (
          <div className="stack">
            {isOwner && <ProjectSubmit i18n={i18n} projectID={id} onSubmitted={refresh} />}
            <div className="card">
              <div className="card-head">
                <span className="eyebrow">{t('projectSessions')}</span>
              </div>
              {sessions === null || sessions.length === 0 ? (
                <div className="empty">{t('noSessions')}</div>
              ) : (
                <div className="rows">
                  {sessions.map((s) => (
                    <SessionRow key={s.id} i18n={i18n} job={s} onOpen={() => onOpenSession(s.id, s.ref, project.title)} onChanged={refresh} />
                  ))}
                </div>
              )}
            </div>
          </div>
        )}
        {tab === 'verdict' && <VerdictTab i18n={i18n} session={published} />}
        {tab === 'artifacts' && <ArtifactsTab i18n={i18n} session={published} />}
        {tab === 'people' && isOwner && (
          <div className="detail-grid">
            <Members i18n={i18n} projectID={id} />
            {project.visibility === 'private' ? <PublishPanel i18n={i18n} projectID={id} onPublished={refresh} /> : <PublishedCard i18n={i18n} project={project} />}
          </div>
        )}
      </div>
    </div>
  )
}

// verdictTone maps a manifest verdict to a semantic colour.
function verdictTone(v: string) {
  const s = v.toLowerCase()
  if (s.includes('pass')) return 'var(--ok)'
  if (s.includes('fail') || s.includes('not')) return 'var(--err)'
  return 'var(--warn)'
}

// VerdictTab shows the latest published session's manifest verdict and metadata —
// real data only. Per-component scorecards are not something the backend
// produces, so they are not shown.
function VerdictTab({ i18n, session }: { i18n: I18n; session: Job | null }) {
  const { t } = i18n
  const [repro, setRepro] = useState<Reproduction | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'none'>('loading')
  useEffect(() => {
    if (!session) {
      setState('none')
      return
    }
    getReproduction(session.id)
      .then((r) => {
        setRepro(r)
        setState('ready')
      })
      .catch(() => setState('none'))
  }, [session])

  if (state === 'none') return <div className="card"><div className="empty">{t('noVerdict')}</div></div>
  if (state === 'loading' || !repro) return <div className="center-load">…</div>
  return (
    <div className="card card-pad stack" style={{ maxWidth: 720 }}>
      <span className="eyebrow">{t('verdict')}</span>
      <div style={{ fontSize: 24, fontWeight: 600, letterSpacing: '-0.02em', color: verdictTone(repro.verdict) }}>
        {repro.verdict ? repro.verdict[0].toUpperCase() + repro.verdict.slice(1) : t('noVerdict')}
      </div>
      <div className="usage-rows">
        {repro.created && (
          <div className="usage-line">
            {t('graded')}
            <span className="v">{new Date(repro.created).toLocaleDateString()}</span>
          </div>
        )}
        <div className="usage-line">
          {t('redistribution')}
          <span className="v" style={{ color: repro.review === 'clear' ? 'var(--ok)' : 'var(--warn)' }}>
            {repro.review || '—'}
          </span>
        </div>
        {repro.method_digest && (
          <div className="usage-line">
            {t('methodDigest')}
            <span className="v mono small">{repro.method_digest.replace('sha256:', '').slice(0, 12)}</span>
          </div>
        )}
        <div className="usage-line">
          {t('repository')}
          <a className="v mono small" href={`https://github.com/${repro.repo}`} target="_blank" rel="noreferrer">
            ↗ {repro.repo}
          </a>
        </div>
      </div>
    </div>
  )
}

// ArtifactsTab is the in-app file browser over the reproduction repository — a
// forge pass-through that works even when the repository stays private. Files
// render properly: markdown formatted, code highlighted, images inline.
function ArtifactsTab({ i18n, session }: { i18n: I18n; session: Job | null }) {
  const { t } = i18n
  const [files, setFiles] = useState<string[] | null>(null)
  const [sel, setSel] = useState<string | null>(null)

  useEffect(() => {
    if (!session) {
      setFiles([])
      return
    }
    listArtifacts(session.id)
      .then((r) => {
        const list = r.files ?? []
        setFiles(list)
        setSel(list.find((f) => /readme/i.test(f)) ?? list[0] ?? null)
      })
      .catch(() => setFiles([]))
  }, [session])

  if (files === null) return <div className="center-load">…</div>
  if (files.length === 0 || !session) return <div className="card"><div className="empty">{session ? t('noArtifacts') : t('noVerdict')}</div></div>

  return (
    <div className="detail-grid" style={{ gridTemplateColumns: '260px 1fr' }}>
      <div className="card" style={{ maxHeight: '78vh', overflowY: 'auto' }}>
        <div className="card-head">
          <span className="eyebrow">{t('artifacts')}</span>
        </div>
        <div className="rows">
          {files.map((f) => (
            <div key={f} className="row" style={{ padding: '8px 14px' }} onClick={() => setSel(f)}>
              <span className="row-mono row-title" style={{ fontSize: 12, color: sel === f ? 'var(--fg-1)' : 'var(--fg-2)' }}>
                {f}
              </span>
            </div>
          ))}
        </div>
      </div>
      {sel ? (
        <FileView
          i18n={i18n}
          jobId={session.id}
          path={sel}
          onNavigate={(target) => {
            // A relative link resolves to a repository path; follow it in-app when
            // it names a file we have, rather than letting the browser navigate.
            if (files.includes(target)) setSel(target)
          }}
        />
      ) : (
        <div className="card card-pad"><div className="empty">{t('selectArtifact')}</div></div>
      )}
    </div>
  )
}

// FileView renders one artifact by its kind: an image inline, markdown as
// formatted (sanitized) HTML, code syntax-highlighted, anything else as text.
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
  // the in-app artifact endpoint (so a committed PNG renders, not 404s), and
  // send external links to a new tab so the app is never navigated away.
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
      <div className="eyebrow mono" style={{ marginBottom: 12 }}>
        {path}
      </div>
      {kind === 'image' ? (
        <img src={artifactUrl(jobId, path)} alt={path} style={{ maxWidth: '100%', borderRadius: 8, background: 'var(--bg-raised)' }} />
      ) : err ? (
        <div className="err small">{t('artifactError')}</div>
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

function ProjectSubmit({ i18n, projectID, onSubmitted }: { i18n: I18n; projectID: string; onSubmitted: () => void }) {
  const { t } = i18n
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!url.trim() || busy) return
    setBusy(true)
    setErr('')
    try {
      await submitPaper(url.trim(), projectID)
      setUrl('')
      onSubmitted()
    } catch (e) {
      setErr((e as Error).message === '409' ? t('activeSession') : t('submitError'))
    } finally {
      setBusy(false)
    }
  }
  return (
    <form className="card card-pad" onSubmit={submit} style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
      <input className="text" style={{ flex: 1 }} value={url} onChange={(e) => setUrl(e.target.value)} placeholder={t('addReplicationHint')} spellCheck={false} />
      <button className="btn ghost" disabled={busy}>
        {busy ? t('submitting') : t('addReplication')}
      </button>
      {err && <span className="err small">{err}</span>}
    </form>
  )
}

// StatusBadge shows a project's session status at a glance: how many are
// running (pulsing), else the newest session's terminal phase.
function StatusBadge({ i18n, p }: { i18n: I18n; p: ProjectView }) {
  const { t } = i18n
  if (p.running > 0)
    return (
      <span className="badge running">
        <span className="dot" />
        {t('runningN', { n: String(p.running) })}
      </span>
    )
  if (p.latest_phase === 'done') return <span className="badge done">{phaseLabel(t, 'done')}</span>
  if (p.latest_phase === 'failed') return <span className="badge failed">{phaseLabel(t, 'failed')}</span>
  return null
}

// stopLabel reads a stopped run (cancelled, or reap-closed by a restart) as
// "stopped" rather than a bare "failed".
const isStopped = (j: Job) => j.phase === 'failed' && !!j.error && /cancel|stopped|restart/i.test(j.error)

function SessionRow({ i18n, job, onOpen, onChanged }: { i18n: I18n; job: Job; onOpen: () => void; onChanged: () => void }) {
  const { t } = i18n
  const [j, setJob] = useState<Job>(job)
  const [stopping, setStopping] = useState(false)
  useEffect(() => {
    setJob(job)
    if (!terminal(job.phase)) return watchJob(job.id, setJob)
  }, [job])

  const stop = async (e: React.MouseEvent) => {
    e.stopPropagation()
    if (stopping) return
    setStopping(true)
    try {
      const updated = await cancelSession(j.id)
      setJob(updated)
      onChanged()
    } catch {
      /* leave as-is */
    } finally {
      setStopping(false)
    }
  }

  const phaseTxt = isStopped(j) ? t('stopped') : phaseLabel(t, j.phase)
  return (
    <div className="row" onClick={onOpen}>
      <span className={'badge ' + phaseClass(j.phase) + ' ' + j.phase} style={{ minWidth: 96, justifyContent: 'center' }}>
        <span className="dot" />
        {phaseTxt}
      </span>
      <span className="row-mono row-title">{j.ref}</span>
      {!terminal(j.phase) && (
        <button className="btn ghost sm" onClick={stop} disabled={stopping}>
          {stopping ? t('stopping') : t('stop')}
        </button>
      )}
      {j.repo_name && (
        <a className="muted small mono" href={`https://github.com/${j.repo_name}`} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>
          ↗ GitHub
        </a>
      )}
      {j.error && !isStopped(j) && <span className="err small">{j.error}</span>}
      <span className="muted small">{i18n.t('viewTranscript')}</span>
      <Svg d={Icon.chev} size={14} />
    </div>
  )
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
  const drop = async (target: string) => {
    await removeMember(projectID, target).catch(() => {})
    refresh()
  }
  return (
    <div className="card">
      <div className="card-head">
        <span className="eyebrow">{t('members')}</span>
      </div>
      {members.length === 0 ? (
        <div className="empty">{t('noMembers')}</div>
      ) : (
        members.map((m) => (
          <div key={m.email} className="member-row">
            <span className="user-avatar">{initials(m.email)}</span>
            <span className="member-email">{m.email}</span>
            {!m.bound && <span className="role-tag pending">{t('pending')}</span>}
            <button className="btn ghost sm" onClick={() => drop(m.email)}>
              {t('remove')}
            </button>
          </div>
        ))
      )}
      <form className="field-row" onSubmit={invite}>
        <input className="text" style={{ flex: 1 }} type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder={t('inviteEmail')} spellCheck={false} />
        <button className="btn ghost" disabled={busy}>
          {busy ? t('submitting') : t('invite')}
        </button>
      </form>
      <div style={{ padding: '0 14px 12px' }}>
        <span className="muted small">{t('inviteHint')}</span>
      </div>
      {err && <p className="err small" style={{ padding: '0 14px 12px' }}>{err}</p>}
    </div>
  )
}

function PublishPanel({ i18n, projectID, onPublished }: { i18n: I18n; projectID: string; onPublished: () => void }) {
  const { t } = i18n
  const [affirmed, setAffirmed] = useState(false)
  const [flipRepo, setFlipRepo] = useState(true)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const publish = async () => {
    if (!affirmed || busy) return
    setBusy(true)
    setErr('')
    try {
      await makeProjectPublic(projectID, flipRepo)
      onPublished()
    } catch (e) {
      setErr((e as Error).message === '409' ? t('publishNotClear') : t('publishError'))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="card publish-panel">
      <span className="eyebrow">{t('publishTitle')}</span>
      <p className="muted small" style={{ lineHeight: 1.6, margin: 0 }}>
        {t('publishBody')}
      </p>
      <label className="affirm">
        <input type="checkbox" checked={affirmed} onChange={(e) => setAffirmed(e.target.checked)} />
        <span>{t('publishAffirm')}</span>
      </label>
      <label className="affirm">
        <input type="checkbox" checked={flipRepo} onChange={(e) => setFlipRepo(e.target.checked)} />
        <span>{t('flipRepoLabel')}</span>
      </label>
      <button className="btn" disabled={!affirmed || busy} onClick={publish}>
        {busy ? t('submitting') : t('publishConfirm')}
      </button>
      <span className="attestation">{t('attestationNote')}</span>
      {err && <p className="err small" style={{ margin: 0 }}>{err}</p>}
    </div>
  )
}

function PublishedCard({ i18n, project }: { i18n: I18n; project: Project }) {
  const { t } = i18n
  return (
    <div className="card publish-panel">
      <span className="eyebrow">{t('publishTitle')}</span>
      <div className="publish-note">
        <span style={{ color: 'var(--ok)' }} className="mono">
          ✓
        </span>
        {project.signed_off_at ? t('publishedNote', { when: new Date(project.signed_off_at).toLocaleDateString() }) : t('public')}
      </div>
    </div>
  )
}

/* ─── Session / Transcript viewer ─── */

// plumbingKinds are backend lifecycle ticks, hidden by default.
const plumbingKinds = new Set(['status_idle', 'status_running', 'thread_status_idle', 'thread_status_running', 'model_request_start', 'model_request_end'])
const gradeKinds = new Set(['grade', 'define_outcome', 'outcome_evaluation_end'])

function SessionScreen({ i18n, jobId, onBack }: { i18n: I18n; jobId: string; onBack: () => void }) {
  const { t } = i18n
  const [tr, setTr] = useState<Transcript | null>(null)
  const [job, setJob] = useState<Job | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'not_ready' | 'error'>('loading')
  const [hidePlumbing, setHidePlumbing] = useState(true)
  const [off, setOff] = useState<Set<string>>(new Set())

  // Watch the job: its phase_at feeds the timeline, and its terminal phase tells
  // the transcript poll when to stop chasing a run that will never publish.
  const jobRef = useRef<Job | null>(null)
  useEffect(() => {
    jobRef.current = job
  }, [job])
  useEffect(() => {
    getJob(jobId).then(setJob).catch(() => {})
    return watchJob(jobId, setJob)
  }, [jobId])

  // Poll the transcript. While the run is live (or not yet ready but still in
  // flight) keep re-fetching so an in-flight session's trajectory streams in
  // real time — not only after it publishes.
  useEffect(() => {
    let alive = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const again = () => {
      timer = setTimeout(poll, 4000)
    }
    const poll = () => {
      getTranscript(jobId)
        .then((data) => {
          if (!alive) return
          setTr(data)
          setState('ready')
          if (data.live) again() // an in-flight run — keep streaming
        })
        .catch((e: Error) => {
          if (!alive) return
          if (e.message === '409') {
            setState('not_ready')
            // A session may observe a run id or publish shortly; keep polling
            // until it reaches a terminal phase with still no transcript.
            if (!jobRef.current || !terminal(jobRef.current.phase)) again()
          } else {
            setState('error')
          }
        })
    }
    poll()
    return () => {
      alive = false
      if (timer) clearTimeout(timer)
    }
  }, [jobId])

  const backBtn = (
    <button className="icon-btn" onClick={onBack} aria-label="back">
      <Svg d={Icon.back} size={14} w={1.8} />
    </button>
  )

  if (state !== 'ready' || !tr) {
    // A session still working (non-terminal) hasn't failed to produce a
    // transcript — it just hasn't emitted events yet. Show its live timeline and
    // wait, rather than the "not ready" dead-end reserved for terminal misses.
    const waitingLive = state === 'not_ready' && job && !terminal(job.phase)
    return (
      <div className="tx">
        <div className="tx-head">
          {backBtn}
          <span className="tx-head-ref">{t('transcript')}</span>
          {waitingLive && <span className="live-dot">{t('liveStreaming')}</span>}
        </div>
        {job && <Timeline i18n={i18n} job={job} />}
        <div className="center-load">
          {waitingLive ? t('transcriptWaitingLive') : state === 'not_ready' ? t('transcriptNotReady') : state === 'error' ? t('transcriptError') : '…'}
        </div>
      </div>
    )
  }

  // Real counts by kind, and tools used — never fabricated.
  const shown = tr.events.filter((e) => !(hidePlumbing && plumbingKinds.has(e.kind)))
  const kindCounts = new Map<string, number>()
  const toolCounts = new Map<string, number>()
  for (const e of shown) {
    kindCounts.set(displayKind(e.kind), (kindCounts.get(displayKind(e.kind)) ?? 0) + 1)
    if (e.kind === 'tool_use' && e.tool) toolCounts.set(e.tool, (toolCounts.get(e.tool) ?? 0) + 1)
  }
  const visible = shown.filter((e) => !off.has(displayKind(e.kind)))
  const usage = tr.usage

  return (
    <div className="tx">
      <div className="tx-head">
        {backBtn}
        <div className="tx-head-meta">
          <span className="tx-head-ref">
            {t('transcript')}
            {tr.live && <span className="live-dot">{t('liveStreaming')}</span>}
          </span>
          <span className="tx-head-sub">{t('eventsCounted', { n: String(tr.events.length) })}</span>
        </div>
        <div className="tx-stats">
          {usage && (
            <div className="tx-stat">
              <span className="tx-stat-label">{t('usageTotal')}</span>
              <span className="tx-stat-value">{total(usage).toLocaleString()}</span>
            </div>
          )}
          <div className="tx-stat">
            <span className="tx-stat-label">{t('events')}</span>
            <span className="tx-stat-value">{tr.events.length.toLocaleString()}</span>
          </div>
        </div>
      </div>

      {job && <Timeline i18n={i18n} job={job} />}

      <div className="tx-body">
        <aside className="tx-side">
          <div>
            <div className="eyebrow" style={{ marginBottom: 8 }}>
              {t('showEvents')}
            </div>
            {[...kindCounts.entries()].map(([kind, n]) => (
              <button key={kind} className={'kind-btn' + (off.has(kind) ? ' off' : '')} onClick={() => setOff((s) => toggle(s, kind))}>
                <span className="sq" style={{ background: kindColor(kind) }} />
                <span>{kindLabel(t, kind)}</span>
                <span className="n">{n}</span>
              </button>
            ))}
          </div>
          {toolCounts.size > 0 && (
            <div>
              <div className="eyebrow" style={{ marginBottom: 8 }}>
                {t('toolsUsed')}
              </div>
              {[...toolCounts.entries()].sort((a, b) => b[1] - a[1]).map(([name, n]) => (
                <div key={name} className="kind-btn" style={{ cursor: 'default' }}>
                  <span className="mono small">{name}</span>
                  <span className="n">{n}</span>
                </div>
              ))}
            </div>
          )}
          <label className="kind-btn" style={{ cursor: 'pointer' }}>
            <input type="checkbox" checked={hidePlumbing} onChange={(e) => setHidePlumbing(e.target.checked)} style={{ accentColor: 'var(--fg-1)' }} />
            <span>{t('hideLifecycle')}</span>
          </label>
        </aside>

        <main className="tx-stream">
          {visible.map((e, i) => (
            <EventCard key={i} i18n={i18n} e={e} />
          ))}
        </main>

        <aside className="tx-side tx-inspector">
          <div>
            <div className="eyebrow" style={{ marginBottom: 10 }}>
              {t('usage')}
            </div>
            {usage ? (
              <div className="usage-rows">
                <UsageLine label={t('usageInput')} v={usage.InputTokens} />
                <UsageLine label={t('usageOutput')} v={usage.OutputTokens} />
                <UsageLine label={t('usageCached')} v={usage.CachedInputTokens} />
                <UsageLine label={t('usageTotal')} v={total(usage)} strong />
              </div>
            ) : (
              <p className="muted small" style={{ margin: 0 }}>
                {t('usageNotRecorded')}
              </p>
            )}
          </div>
        </aside>
      </div>
    </div>
  )
}

function UsageLine({ label, v, strong }: { label: string; v: number; strong?: boolean }) {
  return (
    <div className="usage-line">
      {label}
      <span className="v" style={strong ? { color: 'var(--fg-1)', fontWeight: 600 } : undefined}>
        {v.toLocaleString()}
      </span>
    </div>
  )
}

function EventCard({ i18n, e }: { i18n: I18n; e: Event }) {
  const { t } = i18n
  const dk = displayKind(e.kind)
  const isTool = e.kind === 'tool_use' || e.kind === 'tool_result'
  const isGrade = gradeKinds.has(e.kind)
  const cls = 'event' + (e.kind === 'thinking' ? ' thinking' : isGrade ? ' grade' : isTool ? ' tool' : '')
  return (
    <div className={cls}>
      <div className="event-gutter">
        <span className="event-role">{e.role}</span>
        <span className="event-kind">{kindLabel(t, dk)}</span>
      </div>
      <div className="event-main">
        {e.kind === 'thinking' ? (
          <div className="event-body" style={{ fontStyle: 'italic' }}>
            {t('thinking')}
          </div>
        ) : e.kind === 'tool_use' ? (
          <>
            <div className="event-title">
              {t('toolCall')} {e.tool && <code>{e.tool}</code>}
            </div>
            {e.content && <pre className="event-io">{e.content}</pre>}
          </>
        ) : e.kind === 'tool_result' ? (
          <details className="event-io" open={false}>
            <summary>{t('toolResult')}</summary>
            <pre style={{ margin: '6px 0 0' }}>{e.content || t('noContent')}</pre>
          </details>
        ) : (
          <div className="event-body">{e.content || t('noContent')}</div>
        )}
      </div>
    </div>
  )
}

/* ─── helpers ─── */

function total(u: Usage) {
  return u.InputTokens + u.OutputTokens + u.CachedInputTokens
}
function toggle(s: Set<string>, k: string) {
  const n = new Set(s)
  n.has(k) ? n.delete(k) : n.add(k)
  return n
}
function initials(email?: string) {
  if (!email) return '·'
  return email.slice(0, 2).toUpperCase()
}
function tierLabel(t: I18n['t'], tier?: string) {
  return t('tier' + (tier ? tier[0].toUpperCase() + tier.slice(1) : 'None'))
}
function phaseLabel(t: I18n['t'], phase: string) {
  return t(`phase.${phase}`)
}

// PHASE_ORDER is the lifecycle spine the timeline walks. phase_at only ever
// carries phases the session actually entered, so a missing phase is simply
// skipped — the timeline never fabricates a step.
const PHASE_ORDER = ['submitted', 'resolving', 'running', 'publishing', 'done', 'failed']

// fmtDur renders a millisecond span as a compact human duration.
function fmtDur(ms: number): string {
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s`
  const h = Math.floor(m / 60)
  return `${h}h ${m % 60}m`
}

// Timeline draws the session's phase progression from the durable phase_at
// timestamps: each phase, when it started, and how long it took to reach the
// next. The final phase carries no duration; a still-running phase shows "in
// progress" rather than a fabricated elapsed count.
function Timeline({ i18n, job }: { i18n: I18n; job: Job }) {
  const { t } = i18n
  const at = job.phase_at ?? {}
  const steps = PHASE_ORDER.filter((p) => at[p])
    .map((p) => ({ phase: p, time: new Date(at[p]).getTime() }))
    .sort((a, b) => a.time - b.time)
  if (steps.length === 0) return null
  return (
    <div className="timeline">
      {steps.map((s, i) => {
        const next = steps[i + 1]
        const active = !next && !terminal(job.phase)
        return (
          <div key={s.phase} className={'tl-step' + (active ? ' active' : '')}>
            <span className={'tl-dot ' + phaseClass(s.phase)} />
            <div className="tl-body">
              <span className="tl-phase">{phaseLabel(t, s.phase)}</span>
              <span className="tl-dur">{active ? t('inProgress') : next ? fmtDur(next.time - s.time) : ''}</span>
            </div>
          </div>
        )
      })}
    </div>
  )
}
// displayKind collapses the grade family so filters read cleanly.
function displayKind(kind: string) {
  if (gradeKinds.has(kind)) return 'grade'
  return kind
}
function kindLabel(t: I18n['t'], kind: string) {
  const map: Record<string, string> = {
    message: 'message',
    thinking: 'thinking',
    tool_use: 'toolCall',
    tool_result: 'toolResult',
    grade: 'grade',
  }
  return map[kind] ? t(map[kind]) : kind
}
function kindColor(kind: string) {
  if (kind === 'grade') return 'var(--run)'
  if (kind === 'tool_use' || kind === 'tool_result') return 'var(--warn)'
  if (kind === 'thinking') return 'var(--fg-3)'
  return 'var(--fg-2)'
}
