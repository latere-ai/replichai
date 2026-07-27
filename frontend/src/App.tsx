import { useCallback, useEffect, useState } from 'react'
import {
  addMember,
  getArtifact,
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
  submitPaper,
  watchJob,
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
            <LangToggle i18n={i18n} />
            <button className="icon-btn" onClick={toggleTheme} title={theme === 'dark' ? 'Light' : 'Dark'} aria-label="theme">
              <Svg d={Icon.moon} />
            </button>
            <button className="btn" onClick={() => setView({ name: 'submit' })}>
              <Svg d={Icon.plus} size={13} w={2.1} /> {t('newAudit')}
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
    view.name === 'public' ? t('navPublic') : view.name === 'submit' ? t('newAudit') : view.name === 'settings' ? t('settings') : t('navProjects')
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

function LangToggle({ i18n }: { i18n: I18n }) {
  const { lang, setLang } = i18n
  return (
    <div className="lang">
      <button className={lang === 'en' ? 'on' : ''} onClick={() => setLang('en')}>
        EN
      </button>
      <button className={lang === 'zh' ? 'on' : ''} onClick={() => setLang('zh')}>
        中文
      </button>
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

function SubmitScreen({ i18n, onSubmitted }: { i18n: I18n; onSubmitted: (projectId: string) => void }) {
  const { t } = i18n
  const [url, setUrl] = useState('')
  const [project, setProject] = useState('')
  const [owned, setOwned] = useState<ProjectView[]>([])
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    listProjects().then((p) => setOwned(p ?? [])).catch(() => setOwned([]))
  }, [])

  const submit = async () => {
    if (!url.trim() || busy) return
    setBusy(true)
    setErr('')
    try {
      const { project_id } = await submitPaper(url.trim(), project)
      onSubmitted(project_id)
    } catch (e) {
      setErr((e as Error).message === '409' ? t('activeSession') : t('submitError'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="screen">
      <div className="submit-form">
        <div className="screen-head">
          <h1 className="screen-title">{t('newAudit')}</h1>
          <p className="screen-desc">{t('submitHint')}</p>
        </div>
        <div className="submit-input-row">
          <input
            className="submit-input"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="https://arxiv.org/abs/… · DOI · ACM DL"
            spellCheck={false}
            onKeyDown={(e) => e.key === 'Enter' && submit()}
          />
          <button className="btn lg" disabled={busy} onClick={submit}>
            {busy ? t('submitting') : t('submit')}
          </button>
        </div>
        <div className="card submit-options">
          <span className="eyebrow">{t('sessionOptions')}</span>
          <label className="field">
            {t('destinationProject')}
            <select className="text" value={project} onChange={(e) => setProject(e.target.value)}>
              <option value="">{t('newProjectOption')}</option>
              {owned.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.title}
                </option>
              ))}
            </select>
          </label>
          <span className="attestation">{t('submitNoSpend')}</span>
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
              <h1 className="detail-title">{project.title}</h1>
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
                    <SessionRow key={s.id} i18n={i18n} job={s} onOpen={() => onOpenSession(s.id, s.ref, project.title)} />
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
// forge pass-through that works even when the repository stays private.
function ArtifactsTab({ i18n, session }: { i18n: I18n; session: Job | null }) {
  const { t } = i18n
  const [files, setFiles] = useState<string[] | null>(null)
  const [sel, setSel] = useState<string | null>(null)
  const [content, setContent] = useState<string>('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!session) {
      setFiles([])
      return
    }
    listArtifacts(session.id).then((r) => setFiles(r.files ?? [])).catch(() => setFiles([]))
  }, [session])

  const open = (path: string) => {
    if (!session) return
    setSel(path)
    setLoading(true)
    getArtifact(session.id, path)
      .then((text) => setContent(text))
      .catch(() => setContent(t('artifactError')))
      .finally(() => setLoading(false))
  }

  if (files === null) return <div className="center-load">…</div>
  if (files.length === 0) return <div className="card"><div className="empty">{session ? t('noArtifacts') : t('noVerdict')}</div></div>

  return (
    <div className="detail-grid" style={{ gridTemplateColumns: '280px 1fr' }}>
      <div className="card">
        <div className="card-head">
          <span className="eyebrow">{t('artifacts')}</span>
        </div>
        <div className="rows">
          {files.map((f) => (
            <div key={f} className="row" style={{ padding: '9px 14px' }} onClick={() => open(f)}>
              <span className="row-mono row-title" style={{ color: sel === f ? 'var(--fg-1)' : 'var(--fg-2)' }}>
                {f}
              </span>
            </div>
          ))}
        </div>
      </div>
      <div className="card card-pad">
        {sel === null ? (
          <div className="empty">{t('selectArtifact')}</div>
        ) : loading ? (
          <div className="center-load">…</div>
        ) : (
          <>
            <div className="eyebrow" style={{ marginBottom: 10 }}>
              {sel}
            </div>
            <pre className="event-io" style={{ maxHeight: '68vh' }}>
              {content || t('noContent')}
            </pre>
          </>
        )}
      </div>
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

function SessionRow({ i18n, job, onOpen }: { i18n: I18n; job: Job; onOpen: () => void }) {
  const [j, setJob] = useState<Job>(job)
  useEffect(() => {
    setJob(job)
    if (!terminal(job.phase)) return watchJob(job.id, setJob)
  }, [job])
  return (
    <div className="row" onClick={onOpen}>
      <span className={'badge ' + phaseClass(j.phase) + ' ' + j.phase} style={{ width: 96, justifyContent: 'center' }}>
        <span className="dot" />
        {phaseLabel(i18n.t, j.phase)}
      </span>
      <span className="row-mono row-title">{j.ref}</span>
      {j.repo_name && (
        <a className="muted small mono" href={`https://github.com/${j.repo_name}`} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>
          ↗ GitHub
        </a>
      )}
      {j.error && <span className="err small">{j.error}</span>}
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
  const [state, setState] = useState<'loading' | 'ready' | 'not_ready' | 'error'>('loading')
  const [hidePlumbing, setHidePlumbing] = useState(true)
  const [off, setOff] = useState<Set<string>>(new Set())

  useEffect(() => {
    let live = true
    getTranscript(jobId)
      .then((data) => {
        if (!live) return
        setTr(data)
        setState('ready')
      })
      .catch((e: Error) => {
        if (!live) return
        setState(e.message === '409' ? 'not_ready' : 'error')
      })
    return () => {
      live = false
    }
  }, [jobId])

  const backBtn = (
    <button className="icon-btn" onClick={onBack} aria-label="back">
      <Svg d={Icon.back} size={14} w={1.8} />
    </button>
  )

  if (state !== 'ready' || !tr) {
    return (
      <div className="tx">
        <div className="tx-head">
          {backBtn}
          <span className="tx-head-ref">{t('transcript')}</span>
        </div>
        <div className="center-load">{state === 'not_ready' ? t('transcriptNotReady') : state === 'error' ? t('transcriptError') : '…'}</div>
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
          <span className="tx-head-ref">{t('transcript')}</span>
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
