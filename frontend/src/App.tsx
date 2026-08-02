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
  getProfile,
  getProject,
  getPlatformSettings,
  getReproduction,
  getTranscript,
  grantAccess,
  listArtifacts,
  listGrants,
  listMembers,
  listProjects,
  listPublic,
  listShared,
  makeProjectPublic,
  withdrawProject,
  removeMember,
  renameProject,
  resolvePaper,
  sponsorProject,
  revokeAccess,
  saveProfile,
  setPlatformSettings,
  setTopup,
  submitPaper,
  watchJob,
  type ResolvedPaper,
  type ChargeTo,
  type Event,
  type GrantView,
  type Job,
  type Me,
  type Member,
  type PlatformSettings,
  type Profile,
  type Project,
  type ProjectView,
  type Quota,
  type Reproduction,
  type SignOff,
  type Transcript,
  type Usage,
} from './api'
import { useI18n } from './i18n'
import { Ico, type IconName } from './icons'
import { Check, Disclosure, Modal, NumberField, Pill, PromptDialog, Radio, RadioGroup, Segmented, Select, Tip, Toggle, type Tone } from './ui'
import { useTheme, type ThemeMode } from './theme'

type I18n = ReturnType<typeof useI18n>

// signerOf names the person who published a project. `owner` is an opaque OIDC
// subject and means nothing to a reader, so the signer's recorded name is used
// whenever the sign-off carries one (006n); a v1 sign-off has none and falls
// back to the principal, as it did before.
const signerOf = (p: Project) => p.signed_off_name || p.owner

// A view is where the operator is. Navigation is state, not a router.
type View =
  | { name: 'projects' }
  | { name: 'public' }
  | { name: 'submit' }
  | { name: 'settings' }
  | { name: 'admin' }
  | { name: 'project'; id: string; title?: string }
  | { name: 'session'; jobId: string; projectId: string; ref?: string; projectTitle?: string }

const terminal = (p: string) => p === 'done' || p === 'failed'
// phaseClass tones a phase badge: any non-terminal phase reads as "running".
const phaseClass = (p: string) => (terminal(p) ? p : 'running')

// phaseTone maps a session phase onto the semantic ramp, so a phase is the same
// colour wherever it appears — a row badge, a timeline dot, a pill.
function phaseTone(phase: string): Tone {
  if (phase === 'done') return 'ok'
  if (phase === 'failed') return 'err'
  return 'run'
}

// phaseIcon gives each phase a glyph, so the lifecycle is legible before the
// label is read.
function phaseIcon(phase: string): IconName {
  if (phase === 'done') return 'check'
  if (phase === 'failed') return 'alert'
  if (phase === 'publishing') return 'package'
  if (phase === 'resolving') return 'search'
  return 'clock'
}

export default function App() {
  const i18n = useI18n()
  const theme = useTheme()
  const [me, setMe] = useState<Me | null>(null)

  // refreshMe re-reads the identity, and with it the remaining quota — called
  // after anything that spends allowance, so the header never shows a stale
  // surplus (006l).
  const refreshMe = useCallback(() => {
    getMe().then(setMe).catch(() => {})
  }, [])
  useEffect(() => {
    getMe().then(setMe).catch(() => setMe({ authenticated: false }))
  }, [])

  if (me === null) return <div className="center-load">…</div>
  if (!me.authenticated) return <SignIn i18n={i18n} />
  if ((me.tier ?? 'none') === 'none') return <NotInvited i18n={i18n} email={me.email ?? ''} />
  return <Shell i18n={i18n} me={me} theme={theme} refreshMe={refreshMe} />
}

// ThemeSwitcher offers the three appearances directly rather than cycling
// through them: "auto" is a real choice, not the absence of one, and a person
// who has pinned light should be able to see that they have.
function ThemeSwitcher({ i18n, theme, compact }: { i18n: I18n; theme: ReturnType<typeof useTheme>; compact?: boolean }) {
  const { t } = i18n
  const options: { value: ThemeMode; label: string; icon: IconName; hint: string }[] = [
    { value: 'auto', label: t('themeAuto'), icon: 'monitor', hint: t('themeAutoHint') },
    { value: 'light', label: t('themeLight'), icon: 'sun', hint: t('themeLight') },
    { value: 'dark', label: t('themeDark'), icon: 'moon', hint: t('themeDark') },
  ]
  return <Segmented value={theme.mode} options={options} onChange={theme.setMode} label={t('theme')} compact={compact} />
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

// NavItem is one destination in the rail. Each keeps a fixed hue on its icon so
// the rail is scannable by colour; the label only takes the brand when current.
function NavItem({ label, icon, tone, on, onClick }: { label: string; icon: IconName; tone: string; on: boolean; onClick: () => void }) {
  return (
    <button className={'nav-btn' + (on ? ' active' : '')} onClick={onClick}>
      <span className="nav-ico" style={{ color: tone }}>
        <Ico name={icon} size={13} />
      </span>
      {label}
    </button>
  )
}

function Shell({ i18n, me, theme, refreshMe }: { i18n: I18n; me: Me; theme: ReturnType<typeof useTheme>; refreshMe: () => void }) {
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
            <NavItem
              label={t('navProjects')}
              icon="folder"
              tone="var(--brand)"
              on={view.name === 'projects' || view.name === 'project' || view.name === 'session'}
              onClick={() => setView({ name: 'projects' })}
            />
            <NavItem label={t('navPublic')} icon="globe" tone="var(--pub)" on={view.name === 'public'} onClick={() => setView({ name: 'public' })} />
          </div>
          {isAdmin(me) && (
            <div className="rail-group">
              <div className="eyebrow">{t('platform')}</div>
              <NavItem label={t('navAdmin')} icon="shieldCheck" tone="var(--warn)" on={view.name === 'admin'} onClick={() => setView({ name: 'admin' })} />
            </div>
          )}
        </div>
        <div className="rail-foot">
          <button className="user-btn" onClick={() => setView({ name: 'settings' })}>
            <Avatar me={me} size={26} />
            <span className="user-meta">
              {me.name && <span className="user-name">{me.name}</span>}
              <span className={me.name ? 'user-email sub' : 'user-email'}>{me.email}</span>
              <TierPill i18n={i18n} tier={me.tier} />
            </span>
          </button>
        </div>
      </nav>

      <div className="main">
        <header className="topbar">
          <Breadcrumb i18n={i18n} view={view} setView={setView} />
          <div className="topbar-actions">
            <ThemeSwitcher i18n={i18n} theme={theme} compact />
            <QuotaPill i18n={i18n} quota={me.quota} />
            <button className="btn" onClick={() => setView({ name: 'submit' })}>
              <Ico name="plus" size={13} w={2.2} /> {t('newProject')}
            </button>
          </div>
        </header>

        {view.name === 'projects' && <ProjectsScreen i18n={i18n} onOpen={openProject} />}
        {view.name === 'public' && <PublicScreen i18n={i18n} onOpen={openProject} />}
        {view.name === 'submit' && (
          <SubmitScreen
            i18n={i18n}
            onSubmitted={(id) => {
              refreshMe()
              openProject(id)
            }}
          />
        )}
        {view.name === 'settings' && <SettingsScreen i18n={i18n} me={me} theme={theme} />}
        {view.name === 'admin' && <AdminScreen i18n={i18n} />}
        {view.name === 'project' && (
          <ProjectScreen
            i18n={i18n}
            me={me}
            id={view.id}
            onOpenSession={(jobId, ref, title) => openSession(jobId, view.id, ref, title)}
            onQuotaSpent={refreshMe}
          />
        )}
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
    view.name === 'public'
      ? t('navPublic')
      : view.name === 'submit'
        ? t('newProject')
        : view.name === 'settings'
          ? t('settings')
          : view.name === 'admin'
            ? t('navAdmin')
            : t('navProjects')
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

/* ─── Presentation helpers ─── */

// Stat is one headline number: its own icon, its own tone, and a hairline of
// that tone along the card's top edge.
function Stat({ label, value, icon, tone }: { label: string; value: React.ReactNode; icon: IconName; tone: string }) {
  return (
    <div className="stat" style={{ color: tone }}>
      <span className="stat-ico">
        <Ico name={icon} size={17} />
      </span>
      <span className="stat-body">
        <span className="eyebrow">{label}</span>
        <span className="stat-value">{value}</span>
      </span>
    </div>
  )
}

// Empty states get a glyph, so "nothing yet" never reads as "failed to load".
function Empty({ icon, text }: { icon: IconName; text: string }) {
  return (
    <div className="empty">
      <span className="empty-ico">
        <Ico name={icon} size={17} />
      </span>
      {text}
    </div>
  )
}

// CardHead is an eyebrow with its section's glyph and tone.
function CardHead({ label, icon, tone = 'var(--fg-3)', children }: { label: string; icon: IconName; tone?: string; children?: React.ReactNode }) {
  return (
    <div className="card-head">
      <span className="head-ico" style={{ color: tone }}>
        <Ico name={icon} size={12} />
      </span>
      <span className="eyebrow">{label}</span>
      {children}
    </div>
  )
}

// VisibilityBadge: private is a closed lock in ink, public is the published
// tone — the one state that means anyone can read this.
function VisibilityBadge({ i18n, visibility }: { i18n: I18n; visibility: 'private' | 'public' }) {
  return (
    <span className={'badge ' + visibility}>
      <Ico name={visibility === 'public' ? 'globe' : 'lock'} size={11} w={2} />
      {i18n.t(visibility)}
    </span>
  )
}

// TierPill is the rounded mark of what someone may do on the platform. An admin
// carries the brand and a shield; an ordinary member stays neutral, because
// "user" is the unremarkable case and should not shout.
function TierPill({ i18n, tier }: { i18n: I18n; tier?: string }) {
  const admin = tier === 'admin'
  return (
    <Pill tone={admin ? 'brand' : 'neutral'} icon={admin ? 'shieldCheck' : undefined}>
      {tierLabel(i18n.t, tier)}
    </Pill>
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
        <Stat label={t('statProjects')} value={mine === null ? '—' : mine.length} icon="folder" tone="var(--brand)" />
        <Stat label={t('statSessions')} value={mine === null ? '—' : sessionTotal} icon="beaker" tone="var(--run)" />
      </div>

      <div className="card">
        <div className="card-head">
          <Segmented
            value={tab}
            onChange={setTab}
            label={t('navProjects')}
            options={[
              { value: 'mine', label: t('myProjects'), icon: 'folder' },
              { value: 'shared', label: t('sharedWithMe'), icon: 'users' },
            ]}
          />
        </div>
        {mine === null ? (
          <div className="empty">…</div>
        ) : list.length === 0 ? (
          <Empty icon={tab === 'mine' ? 'folder' : 'users'} text={tab === 'mine' ? t('noProjects') : t('noShared')} />
        ) : (
          <div className="rows">
            {list.map((p) => (
              <div key={p.id} className="row" onClick={() => onOpen(p.id, p.title)}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div className="row-title">{p.title}</div>
                  <div className="row-sub">
                    {tab === 'shared' ? `${t('initiator')}: ${signerOf(p)} · ` : ''}
                    {t('sessionCount', { n: String(p.session_count) })}
                  </div>
                </div>
                <StatusBadge i18n={i18n} p={p} />
                <VisibilityBadge i18n={i18n} visibility={p.visibility} />
                <Ico name="chevronRight" size={15} className="chev" />
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
          <Empty icon="globe" text={t('noPublic')} />
        ) : (
          <div className="rows">
            {list.map((p) => (
              <div key={p.id} className="row" onClick={() => onOpen(p.id, p.title)}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div className="row-title">{p.title}</div>
                  <div className="row-sub">
                    {t('initiator')}: {signerOf(p)} · {t('sessionCount', { n: String(p.session_count) })}
                  </div>
                </div>
                <StatusBadge i18n={i18n} p={p} />
                <VisibilityBadge i18n={i18n} visibility="public" />
                <Ico name="chevronRight" size={15} className="chev" />
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
  // A batch that outran the quota reports this per paper (006l AC8); without
  // the case it would read as "could not start", which is not what happened.
  if (code === 'quota_exceeded') return t('errQuota')
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
                    <Ico name="search" size={12} w={2} />
                    {row.resolving ? t('resolvingPaper') : t('resolve')}
                  </button>
                  {rows.length > 1 && (
                    <Tip text={t('removePaper')}>
                      <button className="icon-btn danger" aria-label={t('removePaper')} onClick={() => removeRow(i)}>
                        <Ico name="x" size={13} w={1.9} />
                      </button>
                    </Tip>
                  )}
                </div>
                {row.preview && (
                  <div className="paper-preview">
                    <Ico name="check" size={12} w={2.2} style={{ color: 'var(--ok)' }} /> {row.preview.title || row.preview.key}
                  </div>
                )}
                {row.error && (
                  <div className="paper-preview err">
                    <Ico name="alert" size={12} /> {row.error}
                  </div>
                )}
              </div>
            ))}
          </div>
          <button className="btn ghost sm" onClick={addRow} style={{ alignSelf: 'flex-start', marginTop: 4 }}>
            <Ico name="plus" size={12} w={2.2} /> {t('addPaper')}
          </button>
        </div>

        <label className="field">
          {t('collaboratorsLabel')}
          <input className="text" value={collaborators} onChange={(e) => setCollaborators(e.target.value)} placeholder={t('collaboratorsPlaceholder')} spellCheck={false} />
          <span className="attestation">{t('collaboratorsHint')}</span>
        </label>

        <div className="submit-input-row">
          <button className="btn lg" disabled={busy} onClick={submit}>
            <Ico name="sparkles" size={14} /> {busy ? t('creatingProject') : t('createProjectBtn')}
          </button>
          <span className="attestation" style={{ alignSelf: 'center' }}>{t('submitNoSpend')}</span>
        </div>
        {err && <p className="err small">{err}</p>}
      </div>
    </div>
  )
}

/* ─── Settings ─── */

function SettingsScreen({ i18n, me, theme }: { i18n: I18n; me: Me; theme: ReturnType<typeof useTheme> }) {
  const { t } = i18n
  return (
    <div className="screen">
      <div className="screen-head">
        <h1 className="screen-title">{t('settings')}</h1>
      </div>
      <div className="card card-pad stack" style={{ maxWidth: 520 }}>
        <div className="profile-head">
          <Avatar me={me} size={44} />
          <div className="profile-id">
            <div className="profile-name">{me.name ?? me.email}</div>
            {me.name && <div className="profile-email">{me.email}</div>}
          </div>
        </div>
        <div className="usage-rows">
          <div className="usage-line">
            <Ico name="key" size={13} style={{ color: 'var(--brand)' }} />
            {t('access')}
            <span className="v">
              <TierPill i18n={i18n} tier={me.tier} />
            </span>
          </div>
          {me.quota && !me.quota.unlimited && (
            <div className="usage-line">
              <Ico name="wallet" size={13} style={{ color: 'var(--ok)' }} />
              {t('statSessions')}
              <span className="v">
                {me.quota.used} / {me.quota.limit}
              </span>
            </div>
          )}
          <div className="usage-line">
            <Ico name={theme.theme === 'dark' ? 'moon' : 'sun'} size={13} style={{ color: 'var(--warn)' }} />
            {t('theme')}
            <span className="v">
              <ThemeSwitcher i18n={i18n} theme={theme} />
            </span>
          </div>
        </div>
        <a className="btn ghost" href="/logout" style={{ justifyContent: 'center' }}>
          <Ico name="logOut" size={13} /> {t('signOut')}
        </a>
      </div>
      <ProfileCard i18n={i18n} />
    </div>
  )
}

// profileErrorText maps a server validation code to the message for the field
// that is wrong, so a refusal points somewhere rather than saying "invalid".
function profileErrorText(t: I18n['t'], code: string) {
  if (code.includes('invalid_contact')) return t('errInvalidContact')
  if (code.includes('invalid_orcid')) return t('errInvalidOrcid')
  if (code.includes('invalid_scholar')) return t('errInvalidScholar')
  return t('profileSaveError')
}

// ProfileCard edits the researcher identity a sign-off is published under
// (006n). It is a default, not the record: what a published project reports is
// the snapshot taken when it was signed, so editing here changes nothing that is
// already public.
function ProfileCard({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  const [p, setP] = useState<Profile | null>(null)
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    getProfile()
      .then((got) => setP(got ?? {}))
      .catch(() => setP({}))
  }, [])

  const set = (k: keyof Profile) => (e: React.ChangeEvent<HTMLInputElement>) => {
    setP((cur) => ({ ...(cur ?? {}), [k]: e.target.value }))
    setSaved(false)
    setErr('')
  }

  const save = async () => {
    if (!p || busy) return
    setBusy(true)
    setErr('')
    try {
      setP(await saveProfile(p))
      setSaved(true)
    } catch (e) {
      setErr(profileErrorText(t, (e as Error).message))
    } finally {
      setBusy(false)
    }
  }

  if (!p) return null
  return (
    <div className="card card-pad stack" style={{ maxWidth: 520 }}>
      <div>
        <span className="eyebrow eyebrow-ico">
          <Ico name="user" size={12} /> {t('profileTitle')}
        </span>
        <p className="muted small" style={{ lineHeight: 1.6, margin: '8px 0 0' }}>
          {t('profileIntro')}
        </p>
      </div>
      <ProfileFields i18n={i18n} p={p} set={set} />
      <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
        <button className="btn" disabled={busy} onClick={save}>
          <Ico name="check" size={13} w={2.2} /> {busy ? t('saving') : t('save')}
        </button>
        {saved && (
          <span className="attestation" style={{ color: 'var(--ok)' }}>
            {t('profileSaved')}
          </span>
        )}
      </div>
      {err && <p className="err small" style={{ margin: 0 }}>{err}</p>}
    </div>
  )
}

// ProfileFields is the one set of inputs shared by the settings form and the
// publish wizard's step 2, so the two cannot drift in labels or order.
function ProfileFields({
  i18n,
  p,
  set,
}: {
  i18n: I18n
  p: Profile
  set: (k: keyof Profile) => (e: React.ChangeEvent<HTMLInputElement>) => void
}) {
  const { t } = i18n
  const fields: { k: keyof Profile; label: string; placeholder?: string; type?: string }[] = [
    { k: 'display_name', label: t('profileName'), placeholder: t('profileNamePlaceholder') },
    { k: 'institution', label: t('profileInstitution'), placeholder: t('profileInstitutionPlaceholder') },
    { k: 'contact_email', label: t('profileContact'), placeholder: t('profileContactPlaceholder'), type: 'email' },
    { k: 'orcid', label: t('profileOrcid'), placeholder: '0000-0002-1825-0097' },
    { k: 'scholar_url', label: t('profileScholar'), placeholder: t('profileScholarPlaceholder') },
  ]
  return (
    <div className="stack" style={{ gap: 10 }}>
      {fields.map((f) => (
        <label key={f.k} className="field">
          {f.label}
          <input
            className="text"
            type={f.type ?? 'text'}
            value={p[f.k] ?? ''}
            placeholder={f.placeholder}
            onChange={set(f.k)}
          />
        </label>
      ))}
    </div>
  )
}

/* ─── Platform admin (006k) ─── */

// isAdmin decides whether the Platform group renders. It decides nothing else:
// every admin route is refused server-side by the tier guard and again by the
// store, so hiding the tab is a courtesy, not the guard.
export function isAdmin(me: Me): boolean {
  return me.tier === 'admin'
}

// AdminScreen is the console: who may sign in, at what tier, with how much
// headroom, and the platform-wide default they draw from.
function AdminScreen({ i18n }: { i18n: I18n }) {
  const { t } = i18n
  const [people, setPeople] = useState<GrantView[] | null>(null)
  const [settings, setSettings] = useState<PlatformSettings | null>(null)
  const [err, setErr] = useState('')

  const refresh = useCallback(() => {
    listGrants()
      .then((g) => setPeople(g ?? []))
      .catch(() => setPeople([]))
    getPlatformSettings()
      .then(setSettings)
      .catch(() => setSettings(null))
  }, [])
  useEffect(refresh, [refresh])

  const run = async (fn: () => Promise<unknown>) => {
    setErr('')
    try {
      await fn()
      refresh()
    } catch (e) {
      setErr((e as Error).message === '403' ? t('adminNotAdmin') : t('adminError'))
    }
  }

  return (
    <div className="screen">
      <div className="screen-head">
        <h1 className="screen-title">{t('navAdmin')}</h1>
        <p className="screen-desc">{t('adminIntro')}</p>
      </div>
      {err && <p className="err small">{err}</p>}
      <div className="stack" style={{ maxWidth: 720 }}>
        <PlatformPolicy i18n={i18n} settings={settings} onSave={(n) => run(() => setPlatformSettings({ default_session_quota: n }))} />
        <PeopleCard
          i18n={i18n}
          people={people}
          onGrant={(email, tier) => run(() => grantAccess(email, tier))}
          onRevoke={(email) => run(() => revokeAccess(email))}
          onTopup={(email, extra) => run(() => setTopup(email, extra))}
        />
      </div>
    </div>
  )
}

// PlatformPolicy edits the one platform-wide number: how many replication
// sessions an invited person may run before an admin tops them up.
function PlatformPolicy({ i18n, settings, onSave }: { i18n: I18n; settings: PlatformSettings | null; onSave: (n: number) => void }) {
  const { t } = i18n
  const [draft, setDraft] = useState('')
  useEffect(() => {
    if (settings) setDraft(String(settings.default_session_quota))
  }, [settings])
  const parsed = Number.parseInt(draft, 10)
  const valid = Number.isInteger(parsed) && parsed >= 0
  const dirty = !!settings && valid && parsed !== settings.default_session_quota

  return (
    <div className="card card-pad stack">
      <span className="eyebrow eyebrow-ico" style={{ color: 'var(--warn)' }}>
        <Ico name="wallet" size={12} /> {t('platformPolicy')}
      </span>
      <p className="muted small" style={{ lineHeight: 1.6, margin: 0 }}>
        {t('defaultQuotaHint')}
      </p>
      <div className="field-row">
        <label className="admin-label" htmlFor="default-quota">
          {t('defaultQuota')}
        </label>
        <NumberField id="default-quota" value={draft} onChange={setDraft} min={0} ariaLabel={t('defaultQuota')} />
        <button className="btn" disabled={!dirty} onClick={() => onSave(parsed)}>
          <Ico name="check" size={13} w={2.2} /> {t('save')}
        </button>
      </div>
      {settings?.updated_by && <span className="muted small">{t('policyUpdatedBy', { who: settings.updated_by })}</span>}
    </div>
  )
}

// PeopleCard is the whitelist: who may sign in, at what tier, with what
// headroom. Everything here is seeded by email — nobody needs to have signed in
// first, and a grant binds to them when they do.
function PeopleCard({
  i18n,
  people,
  onGrant,
  onRevoke,
  onTopup,
}: {
  i18n: I18n
  people: GrantView[] | null
  onGrant: (email: string, tier: 'user' | 'admin') => void
  onRevoke: (email: string) => void
  onTopup: (email: string, extra: number) => void
}) {
  const { t } = i18n
  const [email, setEmail] = useState('')
  const [tier, setTier] = useState<'user' | 'admin'>('user')
  // Which row's top-up is being edited. window.prompt would have drawn a
  // browser-owned box over the app; this is the app's own dialog.
  const [topping, setTopping] = useState<GrantView | null>(null)

  const invite = (e: React.FormEvent) => {
    e.preventDefault()
    const target = email.trim()
    if (!target) return
    onGrant(target, tier)
    setEmail('')
  }

  return (
    <div className="card">
      <CardHead label={t('people')} icon="users" tone="var(--brand)" />
      {people === null ? (
        <div className="empty">{t('loading')}</div>
      ) : people.length === 0 ? (
        <Empty icon="userPlus" text={t('noGrants')} />
      ) : (
        people.map((p) => (
          <div key={p.email} className="member-row">
            <Initials email={p.email} />
            <span className="member-email">{p.email}</span>
            <TierPill i18n={i18n} tier={p.tier} />
            {!p.bound && (
              <Pill tone="warn" icon="clock">
                {t('pending')}
              </Pill>
            )}
            {!!p.topup && <Pill tone="ok">{t('topupN', { n: String(p.topup) })}</Pill>}
            <Tip text={t('quotaTitle', { used: String(p.used), limit: String(p.limit) })}>
              <span className="muted small mono tabular">{p.unlimited ? '∞' : `${p.used}/${p.limit}`}</span>
            </Tip>
            <button className="btn ghost sm" onClick={() => setTopping(p)}>
              <Ico name="wallet" size={12} /> {t('topup')}
            </button>
            <button className="btn ghost sm danger" onClick={() => onRevoke(p.email)}>
              <Ico name="x" size={12} w={2} /> {t('revoke')}
            </button>
          </div>
        ))
      )}
      <form className="field-row" onSubmit={invite}>
        <input
          className="text"
          style={{ flex: 1 }}
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder={t('inviteEmail')}
          spellCheck={false}
        />
        <Select
          value={tier}
          onChange={setTier}
          label={t('access')}
          width={132}
          options={[
            { value: 'user', label: t('tierUser'), icon: 'user' },
            { value: 'admin', label: t('tierAdmin'), icon: 'shieldCheck' },
          ]}
        />
        <button className="btn">
          <Ico name="userPlus" size={13} /> {t('grant')}
        </button>
      </form>
      <div style={{ padding: '0 14px 12px' }}>
        <span className="muted small">{t('grantHint')}</span>
      </div>
      {topping && (
        <PromptDialog
          title={t('topup')}
          body={t('topupPrompt', { email: topping.email })}
          initial={String(topping.topup ?? 0)}
          confirmLabel={t('save')}
          cancelLabel={t('cancel')}
          onCancel={() => setTopping(null)}
          onConfirm={(n) => {
            onTopup(topping.email, n)
            setTopping(null)
          }}
        />
      )}
    </div>
  )
}

/* ─── Project detail ─── */

// Tab is one view of a project. The glyph does the work at a glance; the
// underline and the brand tint say which one is open.
function Tab({ on, onClick, icon, label }: { on: boolean; onClick: () => void; icon: IconName; label: string }) {
  return (
    <button className={'tab' + (on ? ' on' : '')} onClick={onClick}>
      <Ico name={icon} size={13} /> {label}
    </button>
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
          <Tip text={t('rename')}>
            <button className="icon-btn" aria-label={t('rename')} onClick={() => { setDraft(title); setEditing(true) }}>
              <Ico name="pencil" size={13} />
            </button>
          </Tip>
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
      <button className="btn sm" disabled={busy} onClick={save}>
        <Ico name="check" size={12} w={2.2} /> {t('save')}
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
  onQuotaSpent,
}: {
  i18n: I18n
  me: Me
  id: string
  onOpenSession: (jobId: string, ref: string, projectTitle: string) => void
  onQuotaSpent: () => void
}) {
  const { t } = i18n
  const [project, setProject] = useState<Project | null>(null)
  const [sessions, setSessions] = useState<Job[] | null>(null)
  const [maySubmit, setMaySubmit] = useState(false)
  const [tab, setTab] = useState<'sessions' | 'verdict' | 'artifacts' | 'people'>('sessions')

  const refresh = useCallback(() => {
    getProject(id)
      .then((d) => {
        setProject(d.project)
        setSessions(d.sessions ?? [])
        setMaySubmit(d.may_submit)
      })
      .catch(() => setSessions([]))
  }, [id])
  useEffect(() => {
    refresh()
  }, [refresh])

  // A submit spends allowance, so the header pill is re-read after one.
  const onSpent = useCallback(() => {
    refresh()
    onQuotaSpent()
  }, [refresh, onQuotaSpent])

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
              <VisibilityBadge i18n={i18n} visibility={project.visibility} />
            </div>
            {project.visibility === 'public' && project.signed_off_at && (
              <div className="detail-meta">
                <Ico name="check" size={12} w={2.2} style={{ color: 'var(--ok)' }} />
                {t('publishedNote', { when: new Date(project.signed_off_at).toLocaleDateString() })}
              </div>
            )}
          </div>
        </div>
        <div className="detail-tabs">
          <Tab on={tab === 'sessions'} onClick={() => setTab('sessions')} icon="layers" label={t('projectSessions')} />
          <Tab on={tab === 'verdict'} onClick={() => setTab('verdict')} icon="gauge" label={t('verdict')} />
          <Tab on={tab === 'artifacts'} onClick={() => setTab('artifacts')} icon="package" label={t('artifacts')} />
          {isOwner && <Tab on={tab === 'people'} onClick={() => setTab('people')} icon="users" label={t('members')} />}
        </div>
      </div>

      <div className="screen" style={{ animation: 'none' }}>
        {tab === 'sessions' && (
          <div className="stack">
            {maySubmit && <ProjectSubmit i18n={i18n} me={me} project={project} onSubmitted={onSpent} />}
            <div className="card">
              <CardHead label={t('projectSessions')} icon="layers" tone="var(--run)" />
              {sessions === null || sessions.length === 0 ? (
                <Empty icon="beaker" text={t('noSessions')} />
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
            <div className="stack">
              <Members i18n={i18n} projectID={id} />
              <SponsorPanel i18n={i18n} project={project} onChanged={refresh} />
            </div>
            {project.visibility === 'private' ? (
              <PublishPanel i18n={i18n} projectID={id} onPublished={refresh} />
            ) : (
              <PublishedCard i18n={i18n} project={project} onWithdrawn={refresh} />
            )}
          </div>
        )}
      </div>
    </div>
  )
}

// verdictTone maps a manifest verdict to a semantic colour, and verdictIcon to
// the matching glyph — a passed reproduction and a failed one should not need
// the word read to be told apart.
function verdictTone(v: string) {
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

  if (state === 'none')
    return (
      <div className="card">
        <Empty icon="gauge" text={t('noVerdict')} />
      </div>
    )
  if (state === 'loading' || !repro) return <div className="center-load">…</div>
  const tone = verdictTone(repro.verdict)
  return (
    <div className="card card-pad stack" style={{ maxWidth: 720 }}>
      <span className="eyebrow eyebrow-ico" style={{ color: tone }}>
        <Ico name="gauge" size={12} /> {t('verdict')}
      </span>
      <div className="verdict-line" style={{ color: tone }}>
        <span className="verdict-ico">
          <Ico name={verdictIcon(repro.verdict)} size={20} w={2.4} />
        </span>
        {repro.verdict ? repro.verdict[0].toUpperCase() + repro.verdict.slice(1) : t('noVerdict')}
      </div>
      <div className="usage-rows">
        {repro.created && (
          <div className="usage-line">
            <Ico name="clock" size={13} style={{ color: 'var(--fg-3)' }} />
            {t('graded')}
            <span className="v">{new Date(repro.created).toLocaleDateString()}</span>
          </div>
        )}
        <div className="usage-line">
          <Ico name="shieldCheck" size={13} style={{ color: repro.review === 'clear' ? 'var(--ok)' : 'var(--warn)' }} />
          {t('redistribution')}
          <span className="v">
            <Pill tone={repro.review === 'clear' ? 'ok' : 'warn'}>{repro.review || '—'}</Pill>
          </span>
        </div>
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
}

// fileIcon names a repository file by what it is, reusing the same kinds the
// viewer renders by — so the list and the pane never disagree.
function fileIcon(path: string): IconName {
  const kind = fileKind(path)
  if (kind === 'image') return 'image'
  if (kind === 'code') return 'terminal'
  return 'fileText'
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
  if (files.length === 0 || !session)
    return (
      <div className="card">
        <Empty icon="package" text={session ? t('noArtifacts') : t('noVerdict')} />
      </div>
    )

  return (
    <div className="detail-grid" style={{ gridTemplateColumns: '260px 1fr' }}>
      <div className="card" style={{ maxHeight: '78vh', overflowY: 'auto' }}>
        <CardHead label={t('artifacts')} icon="package" tone="var(--warn)" />
        <div className="rows">
          {files.map((f) => (
            <div key={f} className={'row file-row' + (sel === f ? ' on' : '')} style={{ padding: '7px 14px' }} onClick={() => setSel(f)}>
              <Ico name={fileIcon(f)} size={13} className="file-ico" />
              <span className="row-mono row-title" style={{ fontSize: 12 }}>
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
        <div className="card card-pad">
          <Empty icon="fileText" text={t('selectArtifact')} />
        </div>
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

// QuotaPill shows what is left to spend, next to the button that spends it. An
// admin (unlimited) gets no pill: there is no number to watch.
function QuotaPill({ i18n, quota }: { i18n: I18n; quota?: Quota }) {
  const { t } = i18n
  if (!quota || quota.unlimited) return null
  const left = Math.max(0, quota.limit - quota.used)
  // Empty is an error, the last one is a warning, the rest is just a number.
  const state = left === 0 ? ' out' : left <= 1 ? ' low' : ''
  // Pinned to its own right edge: the pill sits near the viewport's, where a
  // centred bubble would hang off the screen.
  return (
    <Tip align="end" text={t('quotaTitle', { used: String(quota.used), limit: String(quota.limit) })}>
      <span className={'quota-pill' + state}>
        <Ico name={left === 0 ? 'alert' : 'wallet'} size={12} />
        {t('quotaLeft', { n: String(left) })}
      </span>
    </Tip>
  )
}

// submitErrLabel names why a submit was refused, so "you are out of sessions"
// never reads as "something went wrong".
function submitErrLabel(t: I18n['t'], status: string): string {
  if (status === '409') return t('activeSession')
  if (status === '402') return t('quotaExhausted')
  if (status === '403') return t('notSponsored')
  return t('submitError')
}

// ProjectSubmit adds a session to a project the caller owns or collaborates on.
// In a sponsored project a collaborator chooses whose quota it spends; the
// server resolves the payer either way.
function ProjectSubmit({ i18n, me, project, onSubmitted }: { i18n: I18n; me: Me; project: Project; onSubmitted: () => void }) {
  const { t } = i18n
  const [url, setUrl] = useState('')
  const [chargeTo, setChargeTo] = useState<ChargeTo>('me')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const isOwner = project.owner === me.principal
  // The choice exists only where it is real: someone else's project, which its
  // owner has chosen to sponsor.
  const mayCharge = !isOwner && !!project.sponsored

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!url.trim() || busy) return
    setBusy(true)
    setErr('')
    try {
      await submitPaper(url.trim(), project.id, mayCharge ? chargeTo : 'me')
      setUrl('')
      onSubmitted()
    } catch (e) {
      setErr(submitErrLabel(t, (e as Error).message))
    } finally {
      setBusy(false)
    }
  }
  return (
    <form className="card card-pad stack" onSubmit={submit}>
      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <input className="text" style={{ flex: 1 }} value={url} onChange={(e) => setUrl(e.target.value)} placeholder={t('addReplicationHint')} spellCheck={false} />
        <button className="btn" disabled={busy}>
          <Ico name="repeat" size={13} /> {busy ? t('submitting') : t('addReplication')}
        </button>
      </div>
      {mayCharge && (
        <div className="charge-row">
          <span className="muted small">{t('chargeTo')}</span>
          <RadioGroup label={t('chargeTo')}>
            <Radio on={chargeTo === 'me'} onChange={() => setChargeTo('me')}>
              {t('chargeMine')}
            </Radio>
            <Radio on={chargeTo === 'project'} onChange={() => setChargeTo('project')}>
              {t('chargeOwner')}
            </Radio>
          </RadioGroup>
        </div>
      )}
      {err && (
        <span className="err small">
          <Ico name="alert" size={13} /> {err}
        </span>
      )}
    </form>
  )
}

// SponsorPanel is the owner's standing consent to cover their collaborators'
// sessions (006l). Off by default, and revocable at any time.
function SponsorPanel({ i18n, project, onChanged }: { i18n: I18n; project: Project; onChanged: () => void }) {
  const { t } = i18n
  const [busy, setBusy] = useState(false)
  const toggle = async (on: boolean) => {
    if (busy) return
    setBusy(true)
    try {
      await sponsorProject(project.id, on)
      onChanged()
    } catch {
      /* the checkbox simply does not move */
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="card card-pad stack">
      <span className="eyebrow eyebrow-ico" style={{ color: 'var(--ok)' }}>
        <Ico name="wallet" size={12} /> {t('sponsorTitle')}
      </span>
      <p className="muted small" style={{ lineHeight: 1.6, margin: 0 }}>
        {t('sponsorBody')}
      </p>
      <Toggle on={!!project.sponsored} disabled={busy} onChange={toggle}>
        {t('sponsorLabel')}
      </Toggle>
    </div>
  )
}

// StatusBadge shows a project's session status at a glance: how many are
// running (pulsing), else the newest session's terminal phase.
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

  // A stopped run is neither a success nor a fault of the paper: it reads in the
  // neutral tone rather than borrowing the failure red.
  const stopped = isStopped(j)
  const phaseTxt = stopped ? t('stopped') : phaseLabel(t, j.phase)
  return (
    <div className="row" onClick={onOpen}>
      <span className="phase-slot">
        <Pill
          tone={stopped ? 'neutral' : phaseTone(j.phase)}
          icon={stopped ? 'stop' : phaseIcon(j.phase)}
          dot={!terminal(j.phase) && !stopped}
          pulse={!terminal(j.phase)}
        >
          {phaseTxt}
        </Pill>
      </span>
      <span className="row-mono row-title">{j.ref}</span>
      {!terminal(j.phase) && (
        <button className="btn ghost sm danger" onClick={stop} disabled={stopping}>
          <Ico name="stop" size={11} /> {stopping ? t('stopping') : t('stop')}
        </button>
      )}
      {j.repo_name && (
        <Tip text={j.repo_name}>
          <a className="link small mono" href={`https://github.com/${j.repo_name}`} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>
            <Ico name="github" size={13} /> GitHub
          </a>
        </Tip>
      )}
      {j.error && !stopped && (
        <span className="err small">
          <Ico name="alert" size={13} /> {j.error}
        </span>
      )}
      <span className="muted small row-cta">
        <Ico name="message" size={12} /> {i18n.t('viewTranscript')}
      </span>
      <Ico name="chevronRight" size={15} className="chev" />
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

// PublishPanel is the entry to the public flip: a short summary and the button
// that opens the wizard. The decision itself is never one click from a sidebar —
// it opens the transcript and artifacts to everyone, and it puts the signer's
// name behind the result (006n). Withdrawable since 006o, but not undoable:
// copies taken while it was public stay taken.
function PublishPanel({ i18n, projectID, onPublished }: { i18n: I18n; projectID: string; onPublished: () => void }) {
  const { t } = i18n
  const [open, setOpen] = useState(false)
  return (
    <div className="card publish-panel">
      <span className="eyebrow eyebrow-ico" style={{ color: 'var(--pub)' }}>
        <Ico name="globe" size={12} /> {t('publishTitle')}
      </span>
      <p className="muted small" style={{ lineHeight: 1.6, margin: 0 }}>
        {t('publishBody')}
      </p>
      <button className="btn" onClick={() => setOpen(true)}>
        <Ico name="globe" size={13} /> {t('publishStart')}
      </button>
      <span className="attestation">{t('attestationNote')}</span>
      {open && (
        <PublishWizard
          i18n={i18n}
          projectID={projectID}
          onClose={() => setOpen(false)}
          onPublished={() => {
            setOpen(false)
            onPublished()
          }}
        />
      )}
    </div>
  )
}

// PublishWizard walks the three commitments of going public: what becomes
// public, who stands behind it, and the responsibility for it. Step 2 is
// prefilled from the owner's profile but recorded from what they confirm here —
// the sign-off is a snapshot, so a later profile edit cannot rewrite it.
function PublishWizard({
  i18n,
  projectID,
  onClose,
  onPublished,
}: {
  i18n: I18n
  projectID: string
  onClose: () => void
  onPublished: () => void
}) {
  const { t } = i18n
  const [step, setStep] = useState(1)
  const [p, setP] = useState<Profile>({})
  const [toProfile, setToProfile] = useState(true)
  const [affirmed, setAffirmed] = useState(false)
  const [flipRepo, setFlipRepo] = useState(true)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    getProfile()
      .then((got) => setP(got ?? {}))
      .catch(() => setP({}))
  }, [])

  const set = (k: keyof Profile) => (e: React.ChangeEvent<HTMLInputElement>) => {
    setP((cur) => ({ ...cur, [k]: e.target.value }))
    setErr('')
  }

  const hasContact = (p.contact_email ?? '').trim() !== ''

  const publish = async () => {
    if (!affirmed || busy) return
    setBusy(true)
    setErr('')
    const signOff: SignOff = {
      signed_off_name: p.display_name,
      signed_off_institution: p.institution,
      signed_off_contact: (p.contact_email ?? '').trim(),
      signed_off_orcid: p.orcid,
      signed_off_scholar: p.scholar_url,
    }
    try {
      // Saving to the profile is a convenience for the next publish and must not
      // decide this one, so its failure is swallowed rather than surfaced.
      if (toProfile) await saveProfile(p).catch(() => undefined)
      await makeProjectPublic(projectID, signOff, flipRepo)
      onPublished()
    } catch (e) {
      const code = (e as Error).message
      setErr(code === '409' ? t('publishNotClear') : code === '400' ? profileErrorText(t, code) : t('publishError'))
      setStep(2)
    } finally {
      setBusy(false)
    }
  }

  const stepIcon: IconName = step === 1 ? 'globe' : step === 2 ? 'user' : 'shieldCheck'
  return (
    <Modal
      title={step === 1 ? t('publishStep1') : step === 2 ? t('publishStep2') : t('publishStep3')}
      icon={stepIcon}
      eyebrow={
        <span className="step-dots">
          {t('publishStepOf', { n: String(step) })}
          <span className="step-track">
            {[1, 2, 3].map((n) => (
              <span key={n} className={'step-dot' + (n <= step ? ' on' : '')} />
            ))}
          </span>
        </span>
      }
      onClose={onClose}
      foot={
        <>
          <button className="btn ghost" onClick={step === 1 ? onClose : () => setStep(step - 1)}>
            {step > 1 && <Ico name="chevronLeft" size={12} w={2.2} />}
            {step === 1 ? t('cancel') : t('backStep')}
          </button>
          {step < 3 ? (
            <button className="btn" disabled={step === 2 && !hasContact} onClick={() => setStep(step + 1)}>
              {t('continueStep')} <Ico name="chevronRight" size={12} w={2.2} />
            </button>
          ) : (
            <button className="btn" disabled={!affirmed || busy} onClick={publish}>
              <Ico name="globe" size={13} /> {busy ? t('submitting') : t('publishConfirm')}
            </button>
          )}
        </>
      }
    >
      {step === 1 && (
        <p className="muted small" style={{ lineHeight: 1.65, margin: 0 }}>
          {t('publishBody')}
        </p>
      )}

      {step === 2 && (
        <>
          <p className="muted small" style={{ lineHeight: 1.65, margin: 0 }}>
            {t('publishStep2Body')}
          </p>
          <ProfileFields i18n={i18n} p={p} set={set} />
          <Check on={toProfile} onChange={setToProfile}>
            {t('publishSaveToProfile')}
          </Check>
          {!hasContact && (
            <p className="muted small" style={{ margin: 0 }}>
              <Ico name="info" size={12} /> {t('publishContactRequired')}
            </p>
          )}
        </>
      )}

      {step === 3 && (
        <>
          <p className="muted small" style={{ lineHeight: 1.65, margin: 0 }}>
            {t('publishAttestation')}
          </p>
          <Check on={affirmed} onChange={setAffirmed}>
            {t('publishAffirm')}
          </Check>
          <Check on={flipRepo} onChange={setFlipRepo}>
            {t('flipRepoLabel')}
          </Check>
          <span className="attestation">{t('attestationNote')}</span>
        </>
      )}

      {err && (
        <p className="err small" style={{ margin: 0 }}>
          <Ico name="alert" size={13} /> {err}
        </p>
      )}
    </Modal>
  )
}

// PublishedCard reports the recorded sign-off: who signed, where they were, and
// how to reach them. A v1 sign-off carries no snapshot and shows the date line
// alone, exactly as it did before 006n. It also carries the way back out — the
// owner may withdraw the publication (006o).
function PublishedCard({ i18n, project, onWithdrawn }: { i18n: I18n; project: Project; onWithdrawn: () => void }) {
  const { t } = i18n
  const [withdrawing, setWithdrawing] = useState(false)
  const when = project.signed_off_at ? new Date(project.signed_off_at).toLocaleDateString() : ''
  const rows: { label: string; node: React.ReactNode }[] = []
  if (project.signed_off_email) rows.push({ label: t('signedOffSigner'), node: <span className="mono">{project.signed_off_email}</span> })
  if (project.signed_off_contact)
    rows.push({ label: t('signedOffContact'), node: <a href={`mailto:${project.signed_off_contact}`}>{project.signed_off_contact}</a> })
  if (project.signed_off_orcid)
    rows.push({
      label: t('signedOffOrcid'),
      node: (
        <a href={`https://orcid.org/${project.signed_off_orcid}`} target="_blank" rel="noreferrer noopener">
          {project.signed_off_orcid}
        </a>
      ),
    })
  if (project.signed_off_scholar) {
    // The link text is the host, not the label again — a row reading
    // "Scholar · Scholar" tells a reader nothing.
    let host = project.signed_off_scholar
    try {
      host = new URL(project.signed_off_scholar).hostname
    } catch {
      /* keep the raw value if it will not parse */
    }
    rows.push({
      label: t('signedOffScholar'),
      node: (
        <a href={project.signed_off_scholar} target="_blank" rel="noreferrer noopener">
          {host}
        </a>
      ),
    })
  }

  return (
    <div className="card publish-panel">
      <span className="eyebrow eyebrow-ico" style={{ color: 'var(--pub)' }}>
        <Ico name="globe" size={12} /> {t('publishTitle')}
      </span>
      <div className="publish-note">
        <Ico name="check" size={14} w={2.4} />
        {when ? t('publishedNote', { when }) : t('public')}
      </div>
      {project.signed_off_name && (
        <div className="signer">
          <div className="signer-name">
            {t('signedOffBy')} <strong>{project.signed_off_name}</strong>
          </div>
          {project.signed_off_institution && <div className="signer-affil">{project.signed_off_institution}</div>}
        </div>
      )}
      {rows.length > 0 && (
        <div className="usage-rows">
          {rows.map((r) => (
            <div key={r.label} className="usage-line">
              {r.label}
              <span className="v">{r.node}</span>
            </div>
          ))}
        </div>
      )}
      <button className="btn ghost" onClick={() => setWithdrawing(true)}>
        <Ico name="lock" size={13} /> {t('withdrawStart')}
      </button>
      {withdrawing && (
        <WithdrawDialog
          i18n={i18n}
          projectID={project.id}
          onClose={() => setWithdrawing(false)}
          onWithdrawn={() => {
            setWithdrawing(false)
            onWithdrawn()
          }}
        />
      )}
    </div>
  )
}

// WithdrawDialog confirms ending a publication. One step, not the publish
// wizard's three: withdrawing is the conservative direction. What it must not do
// is imply erasure — going private stops new access and does not retrieve copies
// already taken, and the dialog says so rather than letting the owner assume it.
function WithdrawDialog({
  i18n,
  projectID,
  onClose,
  onWithdrawn,
}: {
  i18n: I18n
  projectID: string
  onClose: () => void
  onWithdrawn: () => void
}) {
  const { t } = i18n
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const withdraw = async () => {
    if (busy) return
    setBusy(true)
    setErr('')
    try {
      await withdrawProject(projectID, reason.trim())
      onWithdrawn()
    } catch {
      setErr(t('withdrawError'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      title={t('withdrawTitle')}
      icon="lock"
      onClose={onClose}
      foot={
        <>
          <button className="btn ghost" onClick={onClose} disabled={busy}>
            {t('cancel')}
          </button>
          {/* Withdrawing is the destructive direction, so the confirm carries
              the warning tone rather than the brand's "go ahead". */}
          <button className="btn ghost danger" onClick={withdraw} disabled={busy}>
            <Ico name="lock" size={13} /> {busy ? t('withdrawBusy') : t('withdrawConfirm')}
          </button>
        </>
      }
    >
      <p className="muted small" style={{ lineHeight: 1.65, margin: 0 }}>
        {t('withdrawBody')}
      </p>
      <p className="muted small" style={{ lineHeight: 1.65, margin: 0 }}>
        <Ico name="alert" size={12} style={{ color: 'var(--warn)' }} /> {t('withdrawNotARecall')}
      </p>
      <label className="field">
        {t('withdrawReason')}
        <input
          className="text"
          value={reason}
          onChange={(e) => {
            setReason(e.target.value)
            setErr('')
          }}
          placeholder={t('withdrawReasonHint')}
          maxLength={2000}
        />
      </label>
      {err && (
        <p className="err small" style={{ margin: 0 }}>
          <Ico name="alert" size={13} /> {err}
        </p>
      )}
    </Modal>
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
    <Tip text={t('back')}>
      <button className="icon-btn" onClick={onBack} aria-label={t('back')}>
        <Ico name="chevronLeft" size={14} w={2} />
      </button>
    </Tip>
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
              <span className="tx-stat-label">
                <Ico name="bolt" size={11} /> {t('usageTotal')}
              </span>
              <span className="tx-stat-value">{total(usage).toLocaleString()}</span>
            </div>
          )}
          <div className="tx-stat">
            <span className="tx-stat-label">
              <Ico name="layers" size={11} /> {t('events')}
            </span>
            <span className="tx-stat-value">{tr.events.length.toLocaleString()}</span>
          </div>
        </div>
      </div>

      {job && <Timeline i18n={i18n} job={job} />}

      <div className="tx-body">
        <aside className="tx-side">
          <div>
            <div className="eyebrow eyebrow-ico" style={{ marginBottom: 8 }}>
              <Ico name="filter" size={11} /> {t('showEvents')}
            </div>
            {[...kindCounts.entries()].map(([kind, n]) => (
              <button
                key={kind}
                className={'kind-btn' + (off.has(kind) ? ' off' : '')}
                aria-pressed={!off.has(kind)}
                onClick={() => setOff((s) => toggle(s, kind))}
              >
                <span className="sq" style={{ color: kindColor(kind) }} />
                <span>{kindLabel(t, kind)}</span>
                <span className="n tabular">{n}</span>
              </button>
            ))}
          </div>
          {toolCounts.size > 0 && (
            <div>
              <div className="eyebrow eyebrow-ico" style={{ marginBottom: 8, color: 'var(--warn)' }}>
                <Ico name="wrench" size={11} /> {t('toolsUsed')}
              </div>
              {[...toolCounts.entries()].sort((a, b) => b[1] - a[1]).map(([name, n]) => (
                <div key={name} className="kind-btn" style={{ cursor: 'default' }}>
                  <span className="mono small">{name}</span>
                  <span className="n tabular">{n}</span>
                </div>
              ))}
            </div>
          )}
          <Toggle on={hidePlumbing} onChange={setHidePlumbing}>
            {t('hideLifecycle')}
          </Toggle>
        </aside>

        <main className="tx-stream">
          {visible.map((e, i) => (
            <EventCard key={i} i18n={i18n} e={e} />
          ))}
        </main>

        <aside className="tx-side tx-inspector">
          <div>
            <div className="eyebrow eyebrow-ico" style={{ marginBottom: 10, color: 'var(--brand)' }}>
              <Ico name="bolt" size={11} /> {t('usage')}
            </div>
            {usage ? (
              <div className="usage-rows">
                <UsageLine label={t('usageInput')} v={usage.InputTokens} tone="var(--run)" />
                <UsageLine label={t('usageOutput')} v={usage.OutputTokens} tone="var(--pub)" />
                <UsageLine label={t('usageCached')} v={usage.CachedInputTokens} tone="var(--ok)" />
                <UsageLine label={t('usageTotal')} v={total(usage)} tone="var(--brand)" strong />
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

function UsageLine({ label, v, strong, tone }: { label: string; v: number; strong?: boolean; tone?: string }) {
  return (
    <div className="usage-line">
      <span className="usage-dot" style={{ background: tone }} />
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
  const cls = 'event ' + (e.kind === 'thinking' ? 'thinking' : isGrade ? 'grade' : isTool ? 'tool' : 'message')
  return (
    <div className={cls}>
      <div className="event-gutter">
        <span className="event-role">{e.role}</span>
        <span className="event-kind">
          <Ico name={kindIcon(dk)} size={10} w={2.2} />
          {kindLabel(t, dk)}
        </span>
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
          <Disclosure summary={t('toolResult')}>
            <pre>{e.content || t('noContent')}</pre>
          </Disclosure>
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

// hueOf turns an address into a stable hue. It is only decoration, but a stable
// one: the same person is the same colour in every list they appear in, which
// is what makes a members table scannable.
export function hueOf(email?: string): number {
  if (!email) return 240
  let h = 0
  for (const ch of email.toLowerCase()) h = (h * 31 + ch.charCodeAt(0)) % 360
  return h
}

// Initials is the avatar for someone we know only by address — a seeded member,
// a granted email — where there is no profile picture to fall back from.
function Initials({ email, size = 22 }: { email: string; size?: number }) {
  return (
    <span
      className="user-avatar"
      style={{ width: size, height: size, borderRadius: Math.round(size / 3.2), fontSize: Math.round(size * 0.42), ['--av-h' as string]: hueOf(email) }}
    >
      {initials(email)}
    </span>
  )
}

// showsPicture decides between the IdP's picture and the initials fallback. It
// is the whole rule of 006m AC4, kept pure so it is testable: no picture, a
// picture that is not absolute https (the server already filters those, and the
// client does not trust that it did), or one that failed to load, all fall back.
export function showsPicture(picture: string | undefined, broken: boolean): boolean {
  return !broken && !!picture && picture.startsWith('https://')
}

// Avatar renders the IdP's picture when there is one, and falls back to
// initials — for an absent URL, and for one that fails to load (006m AC4). A
// broken-image glyph would be worse than the two letters we had before.
function Avatar({ me, size = 22 }: { me: Me; size?: number }) {
  const [broken, setBroken] = useState(false)
  if (showsPicture(me.picture, broken)) {
    return (
      <img
        className="user-avatar"
        style={{ width: size, height: size, borderRadius: Math.round(size / 3.2) }}
        src={me.picture}
        alt=""
        referrerPolicy="no-referrer"
        onError={() => setBroken(true)}
      />
    )
  }
  return <Initials email={me.email ?? ''} size={size} />
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
// kindLabel names an event kind in the two places a name is all that fits: the
// filter list and the card's gutter. `thinking` has a separate short label
// because its body copy is a whole sentence, which wrapped the gutter into a
// column of single words.
function kindLabel(t: I18n['t'], kind: string) {
  const map: Record<string, string> = {
    message: 'message',
    thinking: 'thinkingLabel',
    tool_use: 'toolCall',
    tool_result: 'toolResult',
    grade: 'grade',
  }
  return map[kind] ? t(map[kind]) : kind
}
function kindColor(kind: string) {
  if (kind === 'grade') return 'var(--run)'
  if (kind === 'tool_use' || kind === 'tool_result') return 'var(--warn)'
  if (kind === 'thinking') return 'var(--pub)'
  if (kind === 'message') return 'var(--brand)'
  return 'var(--fg-2)'
}
// kindIcon pairs each event kind with a glyph, so a filter row and the event it
// filters are recognisable as the same thing.
function kindIcon(kind: string): IconName {
  if (kind === 'grade') return 'gauge'
  if (kind === 'tool_use') return 'wrench'
  if (kind === 'tool_result') return 'terminal'
  if (kind === 'thinking') return 'sparkles'
  if (kind === 'message') return 'message'
  return 'dot'
}
