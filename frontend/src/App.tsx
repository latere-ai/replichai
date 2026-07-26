import { useEffect, useState } from 'react'
import { getJob, getMe, listReproductions, submitPaper, watchJob, type Job, type Me, type Reproduction } from './api'
import { useI18n, type Lang } from './i18n'

type Tracked = { id: string; ref: string }

function loadTracked(): Tracked[] {
  try {
    return JSON.parse(localStorage.getItem('runs') || '[]') as Tracked[]
  } catch {
    return []
  }
}

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
  const [runs, setRuns] = useState<Tracked[]>(loadTracked)

  const onSubmitted = (id: string, ref: string) => {
    const next = [{ id, ref }, ...runs.filter((r) => r.id !== id)].slice(0, 25)
    localStorage.setItem('runs', JSON.stringify(next))
    setRuns(next)
  }

  return (
    <>
      <Submit i18n={i18n} onSubmitted={onSubmitted} />
      <section>
        <h2>{i18n.t('yourRuns')}</h2>
        {runs.length === 0 ? (
          <p className="muted">{i18n.t('noRuns')}</p>
        ) : (
          <ul className="runs">
            {runs.map((r) => (
              <RunRow key={r.id} i18n={i18n} tracked={r} />
            ))}
          </ul>
        )}
      </section>
      <Reproductions i18n={i18n} />
    </>
  )
}

function Submit({ i18n, onSubmitted }: { i18n: I18n; onSubmitted: (id: string, ref: string) => void }) {
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
      const { job_id } = await submitPaper(url.trim())
      onSubmitted(job_id, url.trim())
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

function RunRow({ i18n, tracked }: { i18n: I18n; tracked: Tracked }) {
  const [job, setJob] = useState<Job | null>(null)

  useEffect(() => {
    getJob(tracked.id).then(setJob).catch(() => {})
    return watchJob(tracked.id, setJob)
  }, [tracked.id])

  const phase = job?.phase ?? 'submitted'
  return (
    <li className={`run ${phase}`}>
      <span className="ref">{tracked.ref}</span>
      <span className={`badge ${phase}`}>{phaseLabel(i18n.t, phase)}</span>
      {job?.error && <span className="err small">{job.error}</span>}
    </li>
  )
}

function Reproductions({ i18n }: { i18n: I18n }) {
  const [repros, setRepros] = useState<Reproduction[] | null>(null)
  useEffect(() => {
    listReproductions()
      .then((r) => setRepros(r ?? []))
      .catch(() => setRepros([]))
  }, [])

  if (repros === null) return null
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
