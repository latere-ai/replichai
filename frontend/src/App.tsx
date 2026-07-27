import { useEffect, useState } from 'react'
import {
  getMe,
  getProject,
  getTranscript,
  listProjects,
  listReproductions,
  submitPaper,
  watchJob,
  type Event,
  type Job,
  type Me,
  type ProjectView,
  type Reproduction,
  type Transcript,
  type Usage,
} from './api'
import { useI18n, type Lang } from './i18n'

// A view is where the user is: the projects list, one project's sessions, or one
// session's transcript. Navigation is state, not a router — the app is small.
type View =
  | { name: 'projects' }
  | { name: 'project'; id: string }
  | { name: 'session'; jobId: string; projectId: string }

export default function App() {
  const i18n = useI18n()
  const [me, setMe] = useState<Me | null>(null)

  useEffect(() => {
    getMe().then(setMe).catch(() => setMe({ authenticated: false }))
  }, [])

  return (
    <div className="app">
      <Header i18n={i18n} me={me} />
      <main>
        {me === null ? (
          <p className="muted">…</p>
        ) : !me.authenticated ? (
          <SignIn i18n={i18n} />
        ) : (me.tier ?? 'none') === 'none' ? (
          <NotInvited i18n={i18n} email={me.email ?? ''} />
        ) : (
          <Dashboard i18n={i18n} />
        )}
      </main>
      <footer className="muted">replichai · latere.ai</footer>
    </div>
  )
}

type I18n = ReturnType<typeof useI18n>

function Header({ i18n, me }: { i18n: I18n; me: Me | null }) {
  const { t, lang, setLang } = i18n
  return (
    <header>
      <div className="brand">
        <strong>replichai</strong>
        <span className="muted">{t('tagline')}</span>
      </div>
      <div className="header-right">
        <LangToggle lang={lang} setLang={setLang} />
        {me?.authenticated && (
          <>
            <span className="muted small">
              {me.email} · {t('access')}: {tierLabel(t, me.tier)}
            </span>
            <a className="btn ghost" href="/logout">
              {t('signOut')}
            </a>
          </>
        )}
      </div>
    </header>
  )
}

function LangToggle({ lang, setLang }: { lang: Lang; setLang: (l: Lang) => void }) {
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

function SignIn({ i18n }: { i18n: I18n }) {
  return (
    <div className="card center">
      <a className="btn" href="/login">
        {i18n.t('signInWith')}
      </a>
    </div>
  )
}

function NotInvited({ i18n, email }: { i18n: I18n; email: string }) {
  return (
    <div className="card">
      <h2>{i18n.t('notInvitedTitle')}</h2>
      <p>{i18n.t('notInvitedBody', { email })}</p>
    </div>
  )
}

function Dashboard({ i18n }: { i18n: I18n }) {
  const [view, setView] = useState<View>({ name: 'projects' })
  const [repros, setRepros] = useState<Reproduction[]>([])

  useEffect(() => {
    listReproductions()
      .then((r) => setRepros(r ?? []))
      .catch(() => setRepros([]))
  }, [])

  if (view.name === 'project') {
    return <ProjectPage i18n={i18n} id={view.id} onBack={() => setView({ name: 'projects' })} onOpenSession={(jobId) => setView({ name: 'session', jobId, projectId: view.id })} />
  }
  if (view.name === 'session') {
    return <SessionPage i18n={i18n} jobId={view.jobId} onBack={() => setView({ name: 'project', id: view.projectId })} />
  }
  return (
    <>
      <Submit i18n={i18n} onSubmitted={(projectID) => setView({ name: 'project', id: projectID })} />
      <ProjectsList i18n={i18n} onOpen={(id) => setView({ name: 'project', id })} />
      <Reproductions i18n={i18n} repros={repros} />
    </>
  )
}

// Submit opens a fresh project for a paper and navigates into it, so a user goes
// straight from a link to the project that now holds its first session.
function Submit({ i18n, onSubmitted }: { i18n: I18n; onSubmitted: (projectID: string) => void }) {
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
      const { project_id } = await submitPaper(url.trim())
      setUrl('')
      onSubmitted(project_id)
    } catch {
      setErr(t('submitError'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="card" onSubmit={submit}>
      <h2>{t('submitTitle')}</h2>
      <p className="muted small">{t('submitHint')}</p>
      <div className="row">
        <input
          type="text"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          placeholder="https://arxiv.org/abs/…"
          spellCheck={false}
        />
        <button className="btn" disabled={busy}>
          {busy ? t('submitting') : t('submit')}
        </button>
      </div>
      {err && <p className="err">{err}</p>}
    </form>
  )
}

function ProjectsList({ i18n, onOpen }: { i18n: I18n; onOpen: (id: string) => void }) {
  const { t } = i18n
  const [projects, setProjects] = useState<ProjectView[] | null>(null)

  useEffect(() => {
    listProjects().then((p) => setProjects(p ?? [])).catch(() => setProjects([]))
  }, [])

  return (
    <section>
      <h2>{t('myProjects')}</h2>
      {projects === null ? (
        <p className="muted">…</p>
      ) : projects.length === 0 ? (
        <p className="muted">{t('noProjects')}</p>
      ) : (
        <ul className="projects">
          {projects.map((p) => (
            <li key={p.id} className="project-card" onClick={() => onOpen(p.id)}>
              <div className="project-title">{p.title}</div>
              <div className="project-meta muted small">
                <span className={`badge ${p.visibility}`}>{t(p.visibility)}</span>
                {' · '}
                {t('sessionCount', { n: String(p.session_count) })}
              </div>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function ProjectPage({
  i18n,
  id,
  onBack,
  onOpenSession,
}: {
  i18n: I18n
  id: string
  onBack: () => void
  onOpenSession: (jobId: string) => void
}) {
  const { t } = i18n
  const [title, setTitle] = useState('')
  const [sessions, setSessions] = useState<Job[] | null>(null)

  const refresh = () =>
    getProject(id)
      .then((d) => {
        setTitle(d.project.title)
        setSessions(d.sessions ?? [])
      })
      .catch(() => setSessions([]))
  useEffect(() => {
    refresh()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  return (
    <>
      <div className="card">
        <button className="btn ghost small" onClick={onBack}>
          {t('backToProjects')}
        </button>
        <h2 className="project-heading">{title}</h2>
        <ProjectSubmit i18n={i18n} projectID={id} onSubmitted={refresh} />
      </div>
      <section>
        <h3>{t('projectSessions')}</h3>
        {sessions === null ? (
          <p className="muted">…</p>
        ) : (
          <ul className="runs">
            {sessions.map((s) => (
              <SessionRow key={s.id} i18n={i18n} job={s} onOpen={() => onOpenSession(s.id)} />
            ))}
          </ul>
        )}
      </section>
    </>
  )
}

// ProjectSubmit adds another replication of a paper to this project, so a user
// can deliberately re-run — the guard blocks only a concurrent double-fire.
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
    } catch {
      setErr(t('submitError'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="row add-replication" onSubmit={submit}>
      <input
        type="text"
        value={url}
        onChange={(e) => setUrl(e.target.value)}
        placeholder={t('addReplicationHint')}
        spellCheck={false}
      />
      <button className="btn ghost" disabled={busy}>
        {busy ? t('submitting') : t('addReplication')}
      </button>
      {err && <p className="err small">{err}</p>}
    </form>
  )
}

function SessionRow({ i18n, job, onOpen }: { i18n: I18n; job: Job; onOpen: () => void }) {
  const [j, setJob] = useState<Job>(job)

  useEffect(() => {
    setJob(job)
    if (job.phase !== 'done' && job.phase !== 'failed') {
      return watchJob(job.id, setJob)
    }
  }, [job])

  return (
    <li className={`run ${j.phase}`} onClick={onOpen}>
      <span className="ref">{j.ref}</span>
      <span className={`badge ${j.phase}`}>{phaseLabel(i18n.t, j.phase)}</span>
      {j.repo_name && (
        <a
          className="muted small"
          href={`https://github.com/${j.repo_name}`}
          target="_blank"
          rel="noreferrer"
          onClick={(e) => e.stopPropagation()}
        >
          {' '}
          · {i18n.t('openOnGitHub')}
        </a>
      )}
      {j.error && <span className="err small">{j.error}</span>}
      <span className="muted small chev"> · {i18n.t('viewTranscript')} →</span>
    </li>
  )
}

function SessionPage({ i18n, jobId, onBack }: { i18n: I18n; jobId: string; onBack: () => void }) {
  const { t } = i18n
  const [tr, setTr] = useState<Transcript | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'not_ready' | 'error'>('loading')

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

  return (
    <>
      <div className="card">
        <button className="btn ghost small" onClick={onBack}>
          {t('back')}
        </button>
        <h2>{t('transcript')}</h2>
      </div>
      {state === 'loading' && <p className="muted">…</p>}
      {state === 'not_ready' && <p className="muted">{t('transcriptNotReady')}</p>}
      {state === 'error' && <p className="err">{t('transcriptError')}</p>}
      {state === 'ready' && tr && (
        <>
          <UsageSummary i18n={i18n} usage={tr.usage} />
          <section>
            <h3>{t('trajectory')}</h3>
            <Trajectory i18n={i18n} events={tr.events} />
          </section>
        </>
      )}
    </>
  )
}

function UsageSummary({ i18n, usage }: { i18n: I18n; usage: Usage | null }) {
  const { t } = i18n
  if (!usage) {
    return (
      <div className="card usage">
        <h3>{t('usage')}</h3>
        <p className="muted small">{t('usageNotRecorded')}</p>
      </div>
    )
  }
  const total = usage.InputTokens + usage.OutputTokens + usage.CachedInputTokens
  return (
    <div className="card usage">
      <h3>{t('usage')}</h3>
      <div className="usage-grid">
        <Stat label={t('usageInput')} value={usage.InputTokens} />
        <Stat label={t('usageOutput')} value={usage.OutputTokens} />
        <Stat label={t('usageCached')} value={usage.CachedInputTokens} />
        <Stat label={t('usageTotal')} value={total} strong />
      </div>
    </div>
  )
}

function Stat({ label, value, strong }: { label: string; value: number; strong?: boolean }) {
  return (
    <div className={`stat ${strong ? 'strong' : ''}`}>
      <div className="stat-value">{value.toLocaleString()}</div>
      <div className="stat-label muted small">{label}</div>
    </div>
  )
}

// plumbingKinds are the backend's lifecycle ticks — not trajectory content, so
// the viewer hides them to keep the agent's actual work legible.
const plumbingKinds = new Set([
  'status_idle',
  'status_running',
  'thread_status_idle',
  'thread_status_running',
  'model_request_start',
  'model_request_end',
])

// gradeKinds carry the grader's outcome — called out like the grade.
const gradeKinds = new Set(['grade', 'define_outcome', 'outcome_evaluation_end'])

// Trajectory renders the agent's events in time order, pairing each tool call
// with the result that follows it so the reader sees a call and its output
// together, hiding the backend's lifecycle ticks, and calling out the outcome.
function Trajectory({ i18n, events: all }: { i18n: I18n; events: Event[] }) {
  const { t } = i18n
  const events = all.filter((e) => !plumbingKinds.has(e.kind))
  const blocks: React.ReactNode[] = []
  for (let i = 0; i < events.length; i++) {
    const e = events[i]
    if (e.kind === 'tool_use') {
      const result = events[i + 1]?.kind === 'tool_result' ? events[i + 1] : undefined
      if (result) i++
      blocks.push(<ToolBlock key={i} i18n={i18n} call={e} result={result} />)
      continue
    }
    if (e.kind === 'tool_result') {
      blocks.push(<ToolBlock key={i} i18n={i18n} result={e} />)
      continue
    }
    if (e.kind === 'thinking') {
      blocks.push(
        <div key={i} className="event thinking muted small">
          {e.role} · {t('thinking')}
        </div>,
      )
      continue
    }
    if (gradeKinds.has(e.kind)) {
      blocks.push(
        <div key={i} className="event grade">
          <div className="event-head">
            <span className="badge grade">{t('grade')}</span> <span className="muted small">{e.kind}</span>
          </div>
          <div className="event-body">{e.content || t('noContent')}</div>
        </div>,
      )
      continue
    }
    blocks.push(
      <div key={i} className={`event ${e.kind}`}>
        <div className="event-head muted small">
          {e.role} · {e.kind}
        </div>
        <div className="event-body">{e.content || t('noContent')}</div>
      </div>,
    )
  }
  return <div className="trajectory">{blocks}</div>
}

function ToolBlock({ i18n, call, result }: { i18n: I18n; call?: Event; result?: Event }) {
  const { t } = i18n
  return (
    <div className="event tool">
      {call && (
        <>
          <div className="event-head muted small">
            {t('toolCall')} · <code>{call.tool}</code>
          </div>
          {call.content && <pre className="tool-io">{call.content}</pre>}
        </>
      )}
      {result && (
        <details className="tool-result">
          <summary className="muted small">{t('toolResult')}</summary>
          <pre className="tool-io">{result.content || t('noContent')}</pre>
        </details>
      )}
    </div>
  )
}

function Reproductions({ i18n, repros }: { i18n: I18n; repros: Reproduction[] }) {
  return (
    <section>
      <h2>{i18n.t('reproductions')}</h2>
      {repros.length === 0 ? (
        <p className="muted">{i18n.t('noReproductions')}</p>
      ) : (
        <ul className="repros">
          {repros.map((r) => (
            <li key={r.id}>
              <a href={`https://github.com/${r.repo}`} target="_blank" rel="noreferrer">
                {r.title || r.repo}
              </a>
              {r.verdict && (
                <span className="muted small">
                  {' '}
                  · {i18n.t('verdict')}: {r.verdict}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function tierLabel(t: I18n['t'], tier?: string) {
  return t('tier' + (tier ? tier[0].toUpperCase() + tier.slice(1) : 'None'))
}

function phaseLabel(t: I18n['t'], phase: string) {
  return t(`phase.${phase}`)
}
