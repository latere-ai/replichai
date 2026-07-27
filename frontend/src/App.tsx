import { useEffect, useState } from 'react'
import { getMe, listMyJobs, listReproductions, submitPaper, watchJob, type Job, type Me, type Reproduction } from './api'
import { useI18n, type Lang } from './i18n'

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
  const [jobs, setJobs] = useState<Job[] | null>(null)
  const [repros, setRepros] = useState<Reproduction[]>([])

  const refresh = () => listMyJobs().then((j) => setJobs(j ?? [])).catch(() => setJobs([]))
  useEffect(() => {
    refresh()
    listReproductions()
      .then((r) => setRepros(r ?? []))
      .catch(() => setRepros([]))
  }, [])

  return (
    <>
      <Submit i18n={i18n} onSubmitted={refresh} />
      <section>
        <h2>{i18n.t('myProjects')}</h2>
        {jobs === null ? (
          <p className="muted">…</p>
        ) : jobs.length === 0 ? (
          <p className="muted">{i18n.t('noProjects')}</p>
        ) : (
          <ul className="runs">
            {jobs.map((j) => (
              <ProjectRow key={j.id} i18n={i18n} job={j} repros={repros} />
            ))}
          </ul>
        )}
      </section>
      <Reproductions i18n={i18n} repros={repros} />
    </>
  )
}

function Submit({ i18n, onSubmitted }: { i18n: I18n; onSubmitted: () => void }) {
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
      await submitPaper(url.trim())
      onSubmitted()
      setUrl('')
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

function ProjectRow({ i18n, job, repros }: { i18n: I18n; job: Job; repros: Reproduction[] }) {
  const [j, setJob] = useState<Job>(job)

  useEffect(() => {
    setJob(job)
    // Live-track a project whose run is still in flight; a finished one is static.
    if (job.phase !== 'done' && job.phase !== 'failed') {
      return watchJob(job.id, setJob)
    }
  }, [job])

  const repro = repros.find((r) => r.repo && r.repo === j.repo_name)
  const title = repro?.title || j.ref
  const phase = j.phase
  return (
    <li className={`run ${phase}`}>
      {repro ? (
        <a className="ref" href={`https://github.com/${repro.repo}`} target="_blank" rel="noreferrer">
          {title}
        </a>
      ) : (
        <span className="ref">{title}</span>
      )}
      <span className={`badge ${phase}`}>{phaseLabel(i18n.t, phase)}</span>
      {repro?.verdict && (
        <span className="muted small">
          {' '}
          · {i18n.t('verdict')}: {repro.verdict}
        </span>
      )}
      {j.error && <span className="err small">{j.error}</span>}
    </li>
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
