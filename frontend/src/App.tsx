import { useCallback, useDeferredValue, useEffect, useMemo, useRef, useState } from 'react'
import { fileKind, highlightIn, isExternalHref, langOf, renderMarkdown, resolveRelative } from './artifact'
import {
  addMember,
  artifactUrl,
  cancelSession,
  createProject,
  getArtifact,
  getJob,
  allocateCredits,
  getMe,
  getWallet,
  reclaimCredits,
  getProfile,
  getPublicProfile,
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
  revokeAccess,
  saveProfile,
  type PublicProfile,
  setPlatformSettings,
  topUp,
  listPapers,
  addPaper,
  removePaper,
  startSession,
  type ProjectPaper,
  watchJob,
  type ResolvedPaper,
  type Event,
  type GrantView,
  type Job,
  type Me,
  type Member,
  type LedgerEntry,
  type PlatformSettings,
  type ProjectCredits,
  type RateCard,
  type Profile,
  type Project,
  type ProjectView,
  type Credits,
  type Reproduction,
  type SignOff,
  type Transcript,
  type Usage,
} from './api'
import { type I18n, useI18n } from './i18n'
import {
  createOutcome,
  creditMoveErrLabel,
  generic,
  profileSaveErrText,
  publishErrText,
  refusal,
  sessionFailureText,
  startErrLabel,
  addPaperErrLabel,
  removePaperErrLabel,
} from './errors'
import { formatUSD, parseUSD, toDollars } from './money'
import {
  clockOf,
  compactTokens,
  seekTarget,
  freshOf,
  histogram,
  matches,
  offsetOf,
  phaseBands,
  runSpan,
  splitTitle,
  timeOf,
  cacheWrittenOf,
  totalOf,
  type Span,
} from './transcript'
import { Ico, type IconName } from './icons'
import { useRouter, type Navigate, type ProjectTab, type SessionTab, type View } from './routes'
import { gradeable, groupByPaper, type Grouped, type PaperGroup } from './papers'
import { Check, Disclosure, Modal, NumberField, Pill, PromptDialog, Segmented, Select, Tip, Toggle, type Tone } from './ui'
import { useTheme, type ThemeMode } from './theme'
import { useSidebar } from './sidebar'
import { freshest, stopPending, terminal } from './session-stop'

// signerOf names the person who published a project. `owner` is an opaque OIDC
// subject and means nothing to a reader, so the signer's recorded name is used
// whenever the sign-off carries one (006n); a v1 sign-off has none and falls
// back to the principal, as it did before.
const signerOf = (p: Project) => p.signed_off_name || p.owner

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
  const [meUnavailable, setMeUnavailable] = useState(false)
  const { view, navigate } = useRouter()

  // refreshMe re-reads the identity, and with it the wallet — called
  // after anything that spends allowance, so the header never shows a stale
  // surplus (006l).
  const refreshMe = useCallback(() => {
    getMe().then(setMe).catch(() => {})
  }, [])
  const loadMe = useCallback(() => {
    setMe(null)
    setMeUnavailable(false)
    getMe().then(setMe).catch(() => setMeUnavailable(true))
  }, [])
  useEffect(() => {
    loadMe()
  }, [loadMe])

  // A researcher's page renders before every auth gate: it is public, and a
  // reader who followed a link from a paper has no account to sign in to.
  if (view.name === 'profile') return <PublicProfileScreen i18n={i18n} handle={view.handle} />

  if (meUnavailable) return <AuthUnavailable i18n={i18n} onRetry={loadMe} />
  if (me === null) return <div className="center-load">…</div>
  // Every screen is gated, and every one of them is an address somebody can
  // arrive at cold — an invitation mail pointing at a project, a shared link to a
  // session. Sign-in comes back to that address rather than to the dashboard.
  if (!me.authenticated) return <SignIn i18n={i18n} next={window.location.pathname} />
  if ((me.tier ?? 'none') === 'none') return <NoAccess i18n={i18n} email={me.email ?? ''} />
  return <Shell i18n={i18n} me={me} theme={theme} refreshMe={refreshMe} view={view} navigate={navigate} />
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

/* ─── The public profile (006r) ─── */

// PublicProfileScreen is a researcher's page: who they say they are, and the
// public work signed under that name. It renders for a signed-out reader, so it
// carries its own frame rather than the app shell's.
//
// Nothing here is verified — the platform does not check that somebody works
// where they say they work, exactly as a preprint server does not. What it does
// guarantee is that the work listed is public and was signed by this person.
function PublicProfileScreen({ i18n, handle }: { i18n: I18n; handle: string }) {
  const { t } = i18n
  const [p, setP] = useState<PublicProfile | null>(null)
  const [missing, setMissing] = useState(false)

  useEffect(() => {
    getPublicProfile(handle)
      .then(setP)
      .catch(() => setMissing(true))
  }, [handle])

  if (missing) {
    return (
      <div className="auth-center">
        <div className="auth-card">
          <Logo big />
          <h2>{t('profileNotFound')}</h2>
          <p>{t('profileNotFoundBody')}</p>
          <a className="btn ghost" href="/" style={{ justifyContent: 'center' }}>
            {t('profileBackHome')}
          </a>
        </div>
      </div>
    )
  }
  if (!p) return <div className="center-load">…</div>

  const rows: { label: string; node: React.ReactNode }[] = []
  if (p.contact_email)
    rows.push({ label: t('signedOffContact'), node: (
        <a className="link" href={`mailto:${p.contact_email}`}>
          {p.contact_email}
        </a>
      ) })
  if (p.orcid)
    rows.push({
      label: t('signedOffOrcid'),
      node: (
        <a className="link" href={`https://orcid.org/${p.orcid}`} target="_blank" rel="noreferrer noopener">
          {p.orcid}
        </a>
      ),
    })
  if (p.scholar_url) {
    let host = p.scholar_url
    try {
      host = new URL(p.scholar_url).hostname
    } catch {
      /* keep the raw value if it will not parse */
    }
    rows.push({
      label: t('signedOffScholar'),
      node: (
        <a className="link" href={p.scholar_url} target="_blank" rel="noreferrer noopener">
          {host}
        </a>
      ),
    })
  }

  return (
    <div className="screen" style={{ maxWidth: 720, margin: '0 auto' }}>
      <div className="screen-head">
        <a className="btn ghost" href="/">
          <Ico name="globe" size={13} /> replichai
        </a>
      </div>
      <div className="stack">
        <div className="card card-pad stack" style={{ gap: 14 }}>
          <div className="profile-head">
            <Initials email={p.display_name || p.handle} />
            <div className="profile-id">
              <div className="profile-name">{p.display_name || p.handle}</div>
              {p.institution && <div className="profile-email">{p.institution}</div>}
              <div className="profile-email mono">@{p.handle}</div>
            </div>
          </div>
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
          <span className="muted small" style={{ lineHeight: 1.6 }}>
            {t('profileUnverified')}
          </span>
        </div>

        <div className="card card-pad stack">
          <span className="eyebrow eyebrow-ico" style={{ color: 'var(--pub)' }}>
            <Ico name="globe" size={12} /> {t('profileSignedWork')}
          </span>
          {p.projects.length === 0 ? (
            <Empty icon="globe" text={t('profileNoSignedWork')} />
          ) : (
            <div className="stack" style={{ gap: 8 }}>
              {p.projects.map((proj) => (
                <a key={proj.id} className="member-row" href={`/p/${proj.id}`}>
                  <span className="member-email">{proj.title}</span>
                  {proj.signed_off_at && (
                    <span className="muted small">{new Date(proj.signed_off_at).toLocaleDateString()}</span>
                  )}
                </a>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

/* ─── Auth screens ─── */

function Logo({ big }: { big?: boolean }) {
  return <div className={big ? 'auth-logo' : 'rail-logo'}>r</div>
}

function SignIn({ i18n, next }: { i18n: I18n; next?: string }) {
  const href = next ? `/login?next=${encodeURIComponent(next)}` : '/login'
  return (
    <div className="auth-center">
      <div className="auth-card">
        <Logo big />
        <h2>replichai</h2>
        <p>{i18n.t('tagline')}</p>
        <a className="btn lg" href={href} style={{ justifyContent: 'center' }}>
          {i18n.t('signInWith')}
        </a>
      </div>
    </div>
  )
}

function AuthUnavailable({ i18n, onRetry }: { i18n: I18n; onRetry: () => void }) {
  return (
    <div className="auth-center">
      <div className="auth-card">
        <Logo big />
        <h2>{i18n.t('authUnavailableTitle')}</h2>
        <p>{i18n.t('authUnavailableBody')}</p>
        <button className="btn lg" onClick={onRetry} style={{ justifyContent: 'center' }}>
          {i18n.t('retry')}
        </button>
      </div>
    </div>
  )
}

function NoAccess({ i18n, email }: { i18n: I18n; email: string }) {
  return (
    <div className="auth-center">
      <div className="auth-card">
        <Logo big />
        <h2>{i18n.t('noAccessTitle')}</h2>
        <p>{i18n.t('noAccessBody', { email })}</p>
        {/* Signing out clears a session minted before sign-in was opened, and
            re-runs the admission with a fresh identity — which is the remedy
            for every cause of this screen except an actual revocation. */}
        <a className="btn ghost" href="/logout" style={{ justifyContent: 'center' }}>
          <Ico name="logOut" size={13} /> {i18n.t('signOut')}
        </a>
      </div>
    </div>
  )
}

/* ─── Shell ─── */

// NavItem is one destination in the rail. Each keeps a fixed hue on its icon so
// the rail is scannable by colour; the label only takes the brand when current.
// A folded rail has room for the icon and nothing else, so the label moves into
// a tooltip rather than disappearing. `data-tip` goes on the button itself: it is
// all TipHost reads, and the `tip` class that usually comes with it is an
// `inline-flex` that would undo the row's own layout.
function NavItem({
  label,
  icon,
  tone,
  on,
  folded,
  onClick,
}: {
  label: string
  icon: IconName
  tone: string
  on: boolean
  folded?: boolean
  onClick: () => void
}) {
  return (
    <button className={'nav-btn' + (on ? ' active' : '')} data-tip={folded ? label : undefined} data-tip-side="right" onClick={onClick}>
      <span className="nav-ico" style={{ color: tone }}>
        <Ico name={icon} size={13} />
      </span>
      <span className="nav-label">{label}</span>
    </button>
  )
}

function Shell({
  i18n,
  me,
  theme,
  refreshMe,
  view,
  navigate,
}: {
  i18n: I18n
  me: Me
  theme: ReturnType<typeof useTheme>
  refreshMe: () => void
  view: View
  navigate: Navigate
}) {
  const { t } = i18n
  const setView = navigate
  const openProject = (id: string, title?: string, tab?: ProjectTab) => setView({ name: 'project', id, title, tab })
  const openSession = (jobId: string, projectId: string, ref?: string, projectTitle?: string, tab?: SessionTab) =>
    setView({ name: 'session', jobId, projectId, ref, projectTitle, tab })

  // A session reached by its own link knows its project's id from the path and
  // its title from nowhere — the job carries the paper, not the project. The
  // breadcrumb's middle step is read once, here, rather than by threading the
  // title through every screen that could have linked to this one.
  const untitledSessionProject = view.name === 'session' && !view.projectTitle ? view.projectId : ''
  useEffect(() => {
    if (!untitledSessionProject) return
    let live = true
    getProject(untitledSessionProject)
      .then((d) => {
        if (!live || !d.project?.title) return
        setView((now) => (now.name === 'session' ? { ...now, projectTitle: d.project.title } : now), { replace: true })
      })
      .catch(() => {})
    return () => {
      live = false
    }
  }, [untitledSessionProject, setView])

  // The rail folds to its icons and stays that way across reloads. Folding is a
  // preference about the chrome rather than a destination, so it is not in the
  // address (013) — a shared link opens at the width its reader last chose.
  const { folded, toggle: toggleRail } = useSidebar()
  const foldLabel = folded ? t('unfoldSidebar') : t('foldSidebar')

  return (
    <div className="shell">
      <nav className={folded ? 'rail rail-fold' : 'rail'}>
        <div className="rail-head">
          <Logo />
          <span className="rail-name">replichai</span>
          <span className="rail-beta">BETA</span>
          {/* The one control that must stay reachable in both states, so it is
              the only thing the head keeps when the rail folds. */}
          <button className="rail-fold-btn" aria-expanded={!folded} aria-label={foldLabel} data-tip={foldLabel} data-tip-side="right" onClick={toggleRail}>
            <Ico name={folded ? 'chevronRight' : 'chevronLeft'} size={14} w={2.2} />
          </button>
        </div>
        <div className="rail-nav">
          <div className="rail-group">
            <div className="eyebrow">{t('workspace')}</div>
            <NavItem
              label={t('navProjects')}
              icon="folder"
              tone="var(--brand)"
              folded={folded}
              on={view.name === 'projects' || view.name === 'project' || view.name === 'session'}
              onClick={() => setView({ name: 'projects' })}
            />
            <NavItem
              label={t('navPublic')}
              icon="globe"
              tone="var(--pub)"
              folded={folded}
              on={view.name === 'public'}
              onClick={() => setView({ name: 'public' })}
            />
          </div>
          {isAdmin(me) && (
            <div className="rail-group">
              <div className="eyebrow">{t('platform')}</div>
              <NavItem
                label={t('navAdmin')}
                icon="shieldCheck"
                tone="var(--warn)"
                folded={folded}
                on={view.name === 'admin'}
                onClick={() => setView({ name: 'admin' })}
              />
            </div>
          )}
        </div>
        <div className="rail-foot">
          <button className="user-btn" data-tip={folded ? me.name || me.email : undefined} data-tip-side="right" onClick={() => setView({ name: 'settings' })}>
            {/* Sized to the meta stack beside it (name + address + tier), not to
                a default: a 26px square against three rows of text left the row
                bottom-heavy and the card's right half empty. Folded there is no
                stack, and 38px would not clear the rail's own padding. */}
            <Avatar me={me} size={folded ? 30 : 38} />
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
            <WalletPill i18n={i18n} credits={me.credits} />
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
        {view.name === 'admin' && <AdminScreen i18n={i18n} onCreditsChanged={refreshMe} />}
        {view.name === 'project' && (
          <ProjectScreen
            // Remounted per project: every piece of state here belongs to one
            // project, and reusing the instance across two of them showed the
            // previous project's papers and money until the fetch landed.
            key={view.id}
            i18n={i18n}
            me={me}
            id={view.id}
            tab={view.tab ?? 'sessions'}
            // A tab is a place, so it is pushed; a tab the reader may not open is
            // a correction, so it is not.
            onTab={(tab, correct) => setView({ name: 'project', id: view.id, title: view.title, tab }, { replace: correct })}
            // The title is the breadcrumb's, and only the screen that fetched the
            // project knows it. It corrects the view in place rather than
            // travelling anywhere.
            onTitle={(title) =>
              setView((now) => (now.name === 'project' && now.title !== title ? { ...now, title } : now), { replace: true })
            }
            onOpenSession={(jobId, ref, title, tab) => openSession(jobId, view.id, ref, title, tab)}
            onCreditsSpent={refreshMe}
          />
        )}
        {view.name === 'session' && (
          <SessionScreen
            key={view.jobId}
            i18n={i18n}
            jobId={view.jobId}
            tab={view.tab ?? 'transcript'}
            // A session's tab is a place, like a project's: the verdict and the
            // artifacts of one run are each a thing worth linking to.
            onTab={(tab) => setView((now) => (now.name === 'session' ? { ...now, tab } : now))}
            // A session reached by its own link does not know its paper until the
            // job is fetched; the breadcrumb's leaf is filled in from what the
            // screen loaded.
            onLoaded={(ref) => setView((now) => (now.name === 'session' && now.ref !== ref ? { ...now, ref } : now), { replace: true })}
            onBack={() => openProject(view.projectId, view.projectTitle)}
          />
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
        <Stat label={t('statSessionsRun')} value={mine === null ? '—' : sessionTotal} icon="beaker" tone="var(--run)" />
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
                    {tab === 'shared' && (
                      <>
                        {t('initiator')}:{' '}
                        {p.signer_handle ? (
                          <a className="link" href={`/u/${p.signer_handle}`} onClick={(e) => e.stopPropagation()}>
                            {signerOf(p)}
                          </a>
                        ) : (
                          signerOf(p)
                        )}{' '}
                        ·{' '}
                      </>
                    )}
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
                    {t('initiator')}:{' '}
                    {p.signer_handle ? (
                      // The link opens the signer's page rather than the
                      // project, so the click is stopped from bubbling into the
                      // row's own handler.
                      <a className="link" href={`/u/${p.signer_handle}`} onClick={(e) => e.stopPropagation()}>
                        {signerOf(p)}
                      </a>
                    ) : (
                      signerOf(p)
                    )}{' '}
                    · {t('sessionCount', { n: String(p.session_count) })}
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

// A paper row in the New-project form: the link, and its looked-up preview
// (title) or error, filled by the Look-up button.
type PaperRow = { url: string; preview?: ResolvedPaper; error?: string; resolving?: boolean }

// Creating a project spends nothing (012), so this screen needs to know
// nothing about anybody's credits.
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

  // Look one row's link up and show the title it found (or why it could not).
  const resolveRow = async (i: number) => {
    const url = rows[i].url.trim()
    if (!url) return
    setRow(i, { resolving: true, preview: undefined, error: undefined })
    try {
      const preview = await resolvePaper(url)
      setRow(i, { preview, resolving: false })
    } catch (e) {
      const { status, offline } = refusal(e)
      const msg = offline ? t('errOffline') : status === 400 ? t('unresolvablePaper') : t('resolveFailed')
      setRow(i, { error: msg, resolving: false })
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
      const outcome = createOutcome(t, status, data)
      setRows((rs) => rs.map((r) => (outcome.rows.has(r.url.trim()) ? { ...r, error: outcome.rows.get(r.url.trim()) } : r)))
      setErr(outcome.banner)
      // The project exists either way — it records papers and runs nothing, so
      // there is no partial state to explain and nothing to keep the person here
      // for. A reference we did not recognise is reported on its row and can be
      // added again from inside the project.
      if (outcome.ok) onSubmitted(data.project_id!)
    } catch (e) {
      setErr(generic(t, e, 'createFailed'))
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
          <input
            className="text"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={t('projectNamePlaceholder')}
            spellCheck={false}
          />
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
                  <button className="btn ghost sm" disabled={!row.url.trim() || row.resolving } onClick={() => resolveRow(i)}>
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
          <input
            className="text"
            value={collaborators}
            onChange={(e) => setCollaborators(e.target.value)}
            placeholder={t('collaboratorsPlaceholder')}
            spellCheck={false}
          />
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
      <div className="stack" style={{ maxWidth: 520 }}>
        <div className="card card-pad stack">
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
            {me.credits && <WalletStatement i18n={i18n} balance={me.credits.wallet} />}
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
    </div>
  )
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

  const set = (k: TextField) => (e: React.ChangeEvent<HTMLInputElement>) => {
    setP((cur) => ({ ...(cur ?? {}), [k]: e.target.value }))
    setSaved(false)
    setErr('')
  }
  const setPublic = (on: boolean) => {
    setP((cur) => ({ ...(cur ?? {}), public: on }))
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
      setErr(profileSaveErrText(t, e))
    } finally {
      setBusy(false)
    }
  }

  if (!p) return null
  return (
    <div className="card card-pad stack">
      <div>
        <span className="eyebrow eyebrow-ico">
          <Ico name="user" size={12} /> {t('profileTitle')}
        </span>
        <p className="muted small" style={{ lineHeight: 1.6, margin: '8px 0 0' }}>
          {t('profileIntro')}
        </p>
      </div>
      <ProfileFields i18n={i18n} p={p} set={set} />

      {/* The page is a decision, not a field. What turning it on publishes is
          not new — every one of these fields already appears on any sign-off —
          it is the aggregation, and that is its owner's call to make. */}
      <div className="stack" style={{ gap: 8 }}>
        <Toggle on={!!p.public} onChange={setPublic}>
          {t('profilePublicLabel')}
        </Toggle>
        <span className="muted small" style={{ lineHeight: 1.6 }}>
          {t('profilePublicHint')}
        </span>
        {p.public && p.handle && (
          <a className="mono small" href={`/u/${p.handle}`}>
            /u/{p.handle}
          </a>
        )}
      </div>

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
type TextField = 'display_name' | 'institution' | 'contact_email' | 'orcid' | 'scholar_url' | 'handle'

function ProfileFields({
  i18n,
  p,
  set,
}: {
  i18n: I18n
  p: Profile
  set: (k: TextField) => (e: React.ChangeEvent<HTMLInputElement>) => void
}) {
  const { t } = i18n
  // Only the text fields are rendered here. `public` is a decision, not a
  // field, and it lives with the handle in the settings card where the
  // consequence can be spelled out (006r).
  const fields: { k: TextField; label: string; placeholder?: string; type?: string }[] = [
    { k: 'display_name', label: t('profileName'), placeholder: t('profileNamePlaceholder') },
    { k: 'institution', label: t('profileInstitution'), placeholder: t('profileInstitutionPlaceholder') },
    { k: 'contact_email', label: t('profileContact'), placeholder: t('profileContactPlaceholder'), type: 'email' },
    { k: 'orcid', label: t('profileOrcid'), placeholder: '0000-0002-1825-0097' },
    { k: 'scholar_url', label: t('profileScholar'), placeholder: t('profileScholarPlaceholder') },
    { k: 'handle', label: t('profileHandle'), placeholder: t('profileHandlePlaceholder') },
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
function AdminScreen({ i18n, onCreditsChanged }: { i18n: I18n; onCreditsChanged: () => void }) {
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
        <PlatformPolicy i18n={i18n} settings={settings} onSave={(next) => run(() => setPlatformSettings(next))} />
        <PeopleCard
          i18n={i18n}
          people={people}
          onGrant={(email, tier) => run(() => grantAccess(email, tier))}
          onRevoke={(email) => run(() => revokeAccess(email))}
          onTopup={(email, amount) =>
            run(async () => {
              await topUp(email, amount)
              onCreditsChanged()
            })
          }
        />
      </div>
    </div>
  )
}

// PlatformPolicy edits what this deployment charges and what it will risk: two
// numbers and a price list, all micro-USD, all typed in dollars because that is
// the resolution an admin decides anything at.
//
// The two numbers fail closed in opposite directions and the copy says so — a
// reserve that vanished would start sessions on empty projects, a floor that
// grew would stop bounding a runaway.
function PlatformPolicy({
  i18n,
  settings,
  onSave,
}: {
  i18n: I18n
  settings: PlatformSettings | null
  onSave: (next: Pick<PlatformSettings, 'start_reserve' | 'overdraft_cap' | 'rates'>) => void
}) {
  const { t } = i18n
  const [reserve, setReserve] = useState('')
  const [floor, setFloor] = useState('')
  useEffect(() => {
    if (!settings) return
    setReserve(toDollars(settings.start_reserve))
    setFloor(toDollars(settings.overdraft_cap))
  }, [settings])

  const nextReserve = parseUSD(reserve)
  const nextFloor = parseUSD(floor)
  const valid = nextReserve !== null && nextReserve >= 0 && nextFloor !== null && nextFloor >= 0
  const dirty = !!settings && valid && (nextReserve !== settings.start_reserve || nextFloor !== settings.overdraft_cap)
  const save = () => {
    if (settings && valid) onSave({ start_reserve: nextReserve, overdraft_cap: nextFloor, rates: settings.rates })
  }

  return (
    <div className="card card-pad stack">
      <span className="eyebrow eyebrow-ico" style={{ color: 'var(--warn)' }}>
        <Ico name="wallet" size={12} /> {t('platformPolicy')}
      </span>
      <p className="muted small" style={{ lineHeight: 1.6, margin: 0 }}>
        {t('startReserveHint')}
      </p>
      <div className="field-row">
        <label className="admin-label" htmlFor="start-reserve">
          {t('startReserve')}
        </label>
        <NumberField id="start-reserve" value={reserve} onChange={setReserve} min={0} ariaLabel={t('startReserve')} />
      </div>
      <p className="muted small" style={{ lineHeight: 1.6, margin: 0 }}>
        {t('overdraftHint')}
      </p>
      <div className="field-row">
        <label className="admin-label" htmlFor="overdraft-cap">
          {t('overdraftCap')}
        </label>
        <NumberField id="overdraft-cap" value={floor} onChange={setFloor} min={0} ariaLabel={t('overdraftCap')} />
        <button className="btn" disabled={!dirty} onClick={save}>
          <Ico name="check" size={13} w={2.2} /> {t('save')}
        </button>
      </div>
      {settings && <RateCardTable i18n={i18n} card={settings.rates} />}
      {settings?.updated_by && <span className="muted small">{t('policyUpdatedBy', { who: settings.updated_by })}</span>}
    </div>
  )
}

// RateCardTable shows what each model costs. It is here because a reserve and a
// floor mean nothing without the prices they bound — an admin setting a $200
// floor should be able to see what output tokens cost and work out what it buys.
function RateCardTable({ i18n, card }: { i18n: I18n; card: RateCard }) {
  const { t } = i18n
  if (!card?.models?.length) return null
  return (
    <div className="stack" style={{ gap: 6 }}>
      <span className="muted small">{t('rateCardHint')}</span>
      <div className="rate-card">
        <div className="rate-row rate-head">
          <span>{t('rateModel')}</span>
          <span>{t('rateInput')}</span>
          <span>{t('rateOutput')}</span>
          <span>{t('rateCacheRead')}</span>
          <span>{t('rateCacheWrite')}</span>
        </div>
        {card.models.map((m) => (
          <div className="rate-row" key={m.model}>
            <span className="mono">{m.model}</span>
            <span className="tabular">{formatUSD(m.input)}</span>
            <span className="tabular">{formatUSD(m.output)}</span>
            <span className="tabular">{formatUSD(m.cache_read)}</span>
            <span className="tabular">
              {formatUSD(m.cache_write_5m)} / {formatUSD(m.cache_write_1h)}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

// PeopleCard is the user list: who has signed in, at what tier, with what
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
  onTopup: (email: string, amount: number) => void
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
            <Tip text={t('walletOf', { email: p.email })}>
              <span className="muted small mono tabular">{formatUSD(p.wallet)}</span>
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
          initial="10"
          parse={parseUSD}
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
  tab,
  onTab,
  onTitle,
  onOpenSession,
  onCreditsSpent,
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
}) {
  const { t } = i18n
  const [project, setProject] = useState<Project | null>(null)
  const [sessions, setSessions] = useState<Job[] | null>(null)
  const [papers, setPapers] = useState<ProjectPaper[] | null>(null)
  const [maySubmit, setMaySubmit] = useState(false)
  const [credits, setCredits] = useState<ProjectCredits | null>(null)
  const [signerHandle, setSignerHandle] = useState('')

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
      })
      .catch(() => setSessions([]))
    listPapers(id)
      .then(setPapers)
      .catch(() => setPapers([]))
  }, [id])
  useEffect(() => {
    refresh()
  }, [refresh])

  // A submit spends allowance, so the header pill is re-read after one.
  const onSpent = useCallback(() => {
    refresh()
    onCreditsSpent()
  }, [refresh, onCreditsSpent])

  const isOwner = project != null && project.owner === me.principal
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
          <Tab on={tab === 'sessions'} onClick={() => onTab('sessions')} icon="layers" label={t('projectSessions')} />
          <Tab on={tab === 'verdict'} onClick={() => onTab('verdict')} icon="gauge" label={t('verdict')} />
          {isOwner && <Tab on={tab === 'people'} onClick={() => onTab('people')} icon="users" label={t('members')} />}
        </div>
      </div>

      <div className="screen" style={{ animation: 'none' }}>
        {tab === 'sessions' && (
          <div className="stack">
            {credits && <ProjectCreditsCard i18n={i18n} projectID={id} credits={credits} onMoved={onSpent} />}
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
              onRefresh={refresh}
            />
          </div>
        )}
        {tab === 'verdict' && <ProjectVerdicts i18n={i18n} grouped={grouped} onOpen={(s) => onOpenSession(s.id, s.ref, project.title, 'verdict')} />}
        {tab === 'people' && isOwner && (
          <div className="detail-grid">
            <div className="stack">
              <Members i18n={i18n} projectID={id} />
            </div>
            {project.visibility === 'private' ? (
              <PublishPanel i18n={i18n} projectID={id} onPublished={refresh} />
            ) : (
              <PublishedCard i18n={i18n} project={project} signerHandle={signerHandle} onWithdrawn={refresh} />
            )}
          </div>
        )}
      </div>
    </div>
  )
}

// verdictLabel reads a grade the way a person would. The grader's own names are
// snake_case identifiers (`needs_revision`, internal/provider/enum.go), and the
// manifest carries them verbatim — which is right for the record and wrong on
// screen, where "Needs_revision" reads as a leaked field name.
function verdictLabel(v: string) {
  if (!v) return ''
  const words = v.replace(/_/g, ' ')
  return words[0].toUpperCase() + words.slice(1)
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

// SessionVerdict shows one session's manifest verdict and metadata — real data
// only. Per-component scorecards are not something the backend produces, so they
// are not shown.
//
// It takes a job id rather than picking a session out of a project (015): a
// verdict is a property of the run that was graded, and there is no reading of
// "the project's verdict" that does not have to guess which run was meant.
function SessionVerdict({ i18n, jobId }: { i18n: I18n; jobId: string }) {
  const { t } = i18n
  const [repro, setRepro] = useState<Reproduction | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'none'>('loading')
  useEffect(() => {
    setState('loading')
    getReproduction(jobId)
      .then((r) => {
        setRepro(r)
        setState('ready')
      })
      .catch(() => setState('none'))
  }, [jobId])

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
        {verdictLabel(repro.verdict) || t('noVerdict')}
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

// SessionArtifacts is the in-app file browser over one session's reproduction
// repository — a forge pass-through that works even when the repository stays
// private. Files render properly: markdown formatted, code highlighted, images
// inline.
//
// The repository belongs to the session that published it. A project has as many
// as it has published sessions, which is why the project-level version of this
// browser is gone rather than taught to choose between them (015).
function SessionArtifacts({ i18n, jobId }: { i18n: I18n; jobId: string }) {
  const { t } = i18n
  const [files, setFiles] = useState<string[] | null>(null)
  const [sel, setSel] = useState<string | null>(null)

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

  if (files === null) return <div className="center-load">…</div>
  if (files.length === 0)
    return (
      <div className="card">
        <Empty icon="package" text={t('noArtifacts')} />
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

// WalletStatement is a balance and, on request, the entries that produced it.
//
// The statement is the point rather than a nicety: a balance nobody can account
// for is exactly what the ledger exists to prevent, so the one place a person
// reads their own number is the place that can show where it came from. Fetched
// lazily — most visits to settings are not about money.
function WalletStatement({ i18n, balance }: { i18n: I18n; balance: number }) {
  const { t } = i18n
  const [entries, setEntries] = useState<LedgerEntry[] | null>(null)
  const [open, setOpen] = useState(false)

  // Keyed on the balance rather than fetched once: after an allocate the number
  // above changes, and a statement still showing the pre-move entries would be
  // a list that contradicts the total it is supposed to explain.
  useEffect(() => {
    if (!open) return
    let live = true
    getWallet()
      .then((w) => live && setEntries(w.entries))
      .catch(() => live && setEntries([]))
    return () => {
      live = false
    }
  }, [open, balance])

  const reveal = () => setOpen((was) => !was)

  return (
    <>
      <div className="usage-line">
        <Ico name="wallet" size={13} style={{ color: 'var(--ok)' }} />
        {t('statWallet')}
        <span className="v tabular">
          {formatUSD(balance)}
          <button className="btn ghost sm" style={{ marginLeft: 8 }} onClick={reveal}>
            {open ? t('statementHide') : t('statementShow')}
          </button>
        </span>
      </div>
      {open &&
        (entries === null ? (
          <div className="usage-line muted small">{t('loading')}</div>
        ) : entries.length === 0 ? (
          <div className="usage-line muted small">{t('statementEmpty')}</div>
        ) : (
          entries.map((e) => (
            <div className="usage-line" key={e.id}>
              <span className="muted small">{ledgerKindLabel(t, e.kind)}</span>
              <span className="v">
                <span className="muted small">{new Date(e.created_at).toLocaleDateString()}</span>
                <span className={e.amount < 0 ? 'err' : ''}>{formatUSD(e.amount)}</span>
              </span>
            </div>
          ))
        ))}
    </>
  )
}

// ledgerKindLabel names an entry in the reader's terms. A wallet only ever sees
// three of the seven kinds — holds, releases and charges happen to a project's
// pot, never to a person — so the rest fall through to a neutral label rather
// than leaking vocabulary nobody here needs.
export function ledgerKindLabel(t: I18n['t'], kind: LedgerEntry['kind']): string {
  if (kind === 'topup') return t('ledgerTopup')
  if (kind === 'allocate') return t('ledgerAllocate')
  if (kind === 'reclaim') return t('ledgerReclaim')
  return t('ledgerOther')
}

// WalletPill shows what this person has to spend, next to the button that
// starts spending it.
//
// A brand-new account and one that has spent down read differently. Most
// people's first sight of this pill is at zero, and it has to say they have
// none *yet* and how to get some, rather than implying they lost something.
function WalletPill({ i18n, credits }: { i18n: I18n; credits?: Credits }) {
  const { t } = i18n
  if (!credits) return null
  const empty = credits.wallet <= 0
  // Not enough left to open one more session is a warning; nothing at all is an
  // error; the rest is just a number.
  const state = empty ? ' out' : credits.wallet < credits.start_reserve ? ' low' : ''
  return (
    <Tip text={empty ? t('walletEmptyTitle') : t('walletTitle', { reserve: formatUSD(credits.start_reserve) })}>
      <span className={'quota-pill' + state}>
        <Ico name={empty ? 'alert' : 'wallet'} size={12} />
        {empty ? t('walletEmpty') : formatUSD(credits.wallet)}
      </span>
    </Tip>
  )
}

// ProjectCreditsCard is a project's money and the two moves a member can make on
// it. It sits above the submit form because it is the answer to the question a
// refused submit raises.
//
// Both numbers are shown, not one. They differ by the sessions running right now
// — each holds the start reserve — and that difference is the whole reason a
// project with credits can still refuse a submit. Showing only the balance would
// make that refusal look like a bug.
function ProjectCreditsCard({ i18n, projectID, credits, onMoved }: { i18n: I18n; projectID: string; credits: ProjectCredits; onMoved: () => void }) {
  const { t } = i18n
  const [amount, setAmount] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const parsed = parseUSD(amount)
  const valid = parsed !== null && parsed > 0
  const short = credits.available < credits.start_reserve

  const move = async (back: boolean) => {
    if (!valid || busy) return
    setBusy(true)
    setErr('')
    try {
      await (back ? reclaimCredits(projectID, parsed) : allocateCredits(projectID, parsed))
      setAmount('')
      onMoved()
    } catch (e) {
      setErr(creditMoveErrLabel(t, e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card card-pad stack">
      <span className="eyebrow eyebrow-ico" style={{ color: short ? 'var(--warn)' : 'var(--ok)' }}>
        <Ico name="wallet" size={12} /> {t('projectCredits')}
      </span>
      <div className="usage-rows">
        <div className="usage-line">
          {t('creditsBalance')}
          <span className="v tabular">{formatUSD(credits.balance)}</span>
        </div>
        <Tip text={t('creditsAvailableTitle')}>
          <div className="usage-line">
            {t('creditsAvailable')}
            <span className="v tabular">{formatUSD(credits.available)}</span>
          </div>
        </Tip>
      </div>
      <p className="muted small" style={{ lineHeight: 1.6, margin: 0 }}>
        {short ? t('creditsShort', { reserve: formatUSD(credits.start_reserve) }) : t('creditsEnough', { reserve: formatUSD(credits.start_reserve) })}
      </p>
      <div className="field-row">
        <NumberField value={amount} onChange={setAmount} min={0} ariaLabel={t('creditsAmount')} />
        <button className="btn" disabled={!valid || busy} onClick={() => move(false)}>
          <Ico name="plus" size={13} w={2.2} /> {t('creditsAllocate')}
        </button>
        {credits.may_reclaim && (
          <button className="btn ghost" disabled={!valid || busy} onClick={() => move(true)}>
            <Ico name="minus" size={13} w={2.2} /> {t('creditsReclaim')}
          </button>
        )}
      </div>
      {err && (
        <span className="err small">
          <Ico name="alert" size={13} /> {err}
        </span>
      )}
    </div>
  )
}

// PapersCard is what a project is about, and where a run is started (012) —
// each paper carrying the sessions that are runs of it (015).
//
// It is the answer to two questions that used to be one. Adding a paper says
// what this project means to reproduce and costs nothing; starting a session
// runs it and spends. Fusing them is what made creating a project an act that
// could be refused for money — and made a paper whose only run failed disappear
// from the project it belonged to.
//
// The sessions were a flat list beside this card until 015, which is what let a
// run float free of the paper it was a run of. They are nested now, so the
// screen reads the way the records do.
function PapersCard({
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
  // Whether this caller may work here — owner or collaborator. Reading is not
  // working: a public project's papers are what it is *about*, and the public
  // zone exists so a visitor can check that. Adding one and starting a run are
  // the parts that need a role.
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
  const refresh = onRefresh

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
      await startSession(project.id, key)
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

  // Whether the project can afford another run. Saying so on the button beats
  // letting somebody press it and reading the refusal afterwards — and the pot
  // it is short of is on the same screen.
  const short = !!credits && credits.available < credits.start_reserve
  const shortWhy = credits ? t('startNeedsCredits', { reserve: formatUSD(credits.start_reserve) }) : ''

  return (
    <div className="card">
      <CardHead label={t('papers')} icon="fileText" tone="var(--accent)" />
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
              <div className="row">
                <span className="row-title">{g.paper.title || t('paperUntitled')}</span>
                <span className="muted small mono">{g.paper.ref}</span>
                {/* The count is the length of what this row lists. The server
                    sends one too, but it is the input to the never-remove-a-run
                    rule (012), not a second answer to the same question. */}
                <span className="muted small">
                  {g.sessions.length > 0 ? t('paperSessions', { n: String(g.sessions.length) }) : t('paperNeverRun')}
                </span>
                {mayWork && g.sessions.length === 0 && (
                  <Tip text={t('removePaper')}>
                    <button className="icon-btn danger" aria-label={t('removePaper')} onClick={() => remove(g.paper.key)}>
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
                    <button className="btn sm" disabled={!!busyKey} onClick={() => start(g.paper.key)}>
                      <Ico name="sparkles" size={12} /> {busyKey === g.paper.key ? t('startingSession') : t('startSession')}
                    </button>
                  ))}
              </div>
              {g.sessions.map((s) => (
                <SessionRow key={s.id} i18n={i18n} job={s} nested onOpen={() => onOpenSession(s)} onChanged={refresh} />
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
                <SessionRow key={s.id} i18n={i18n} job={s} nested onOpen={() => onOpenSession(s)} onChanged={refresh} />
              ))}
            </div>
          )}
        </div>
      )}
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
  useEffect(() => {
    let live = true
    getReproduction(job.id)
      .then((r) => live && setRepro(r))
      .catch(() => live && setMissing(true))
    return () => {
      live = false
    }
  }, [job.id])
  const tone = repro ? verdictTone(repro.verdict) : 'var(--fg-3)'
  return (
    <div className="row file-row" onClick={onOpen}>
      <span className="row-title">{paper.title || paper.ref}</span>
      <span className="muted small">{job.created_at ? new Date(job.created_at).toLocaleDateString() : ''}</span>
      {repro?.review && <Pill tone={repro.review === 'clear' ? 'ok' : 'warn'}>{repro.review}</Pill>}
      <span className="verdict-cell" style={{ color: tone }}>
        {repro ? (
          <>
            <Ico name={verdictIcon(repro.verdict)} size={14} w={2.2} />
            {verdictLabel(repro.verdict) || t('noVerdict')}
          </>
        ) : missing ? (
          <>
            <Ico name="alert" size={13} /> {t('verdictUnread')}
          </>
        ) : (
          '…'
        )}
      </span>
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

function SessionRow({
  i18n,
  job,
  nested,
  onOpen,
  onChanged,
}: {
  i18n: I18n
  job: Job
  // Indented under the paper it is a run of, rather than standing on its own.
  nested?: boolean
  onOpen: () => void
  onChanged: () => void
}) {
  const { t } = i18n
  const [j, setJob] = useState<Job>(job)
  // Asking to stop is remembered for as long as the session takes to settle, so
  // the control cannot re-arm mid-stop and take a second click.
  const [stopAsked, setStopAsked] = useState(false)
  const [stopErr, setStopErr] = useState(false)
  // Every snapshot of this session — the list's, the cancel's answer, the
  // stream's — arrives through the same guard, so none of them can walk a
  // settled session back to running.
  const show = useCallback((next: Job) => setJob((shown) => freshest(shown, next)), [])
  useEffect(() => {
    show(job)
    if (!terminal(job.phase)) return watchJob(job.id, show)
  }, [job, show])
  useEffect(() => setStopAsked(false), [job.id])

  const stopping = stopPending(j.phase, stopAsked)
  const stop = async (e: React.MouseEvent) => {
    e.stopPropagation()
    if (stopping) return
    setStopAsked(true)
    setStopErr(false)
    try {
      show(await cancelSession(j.id))
      onChanged()
    } catch {
      // The stop did not land — the run is still up. Hand the control back, and
      // say why, rather than leaving a dead button on a running session.
      setStopAsked(false)
      setStopErr(true)
    }
  }

  // A stopped run is neither a success nor a fault of the paper: it reads in the
  // neutral tone rather than borrowing the failure red.
  const stopped = isStopped(j)
  const phaseTxt = stopped ? t('stopped') : phaseLabel(t, j.phase)
  return (
    <div className={nested ? 'row row-nested' : 'row'} onClick={onOpen}>
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
      {!!j.cost && (
        <Tip text={terminal(j.phase) ? t('sessionCostTitle') : t('sessionCostRunningTitle')}>
          <span className="muted small tabular">{formatUSD(j.cost)}</span>
        </Tip>
      )}
      {!terminal(j.phase) && (
        <button className={'btn ghost sm' + (stopping ? ' pending' : ' danger')} onClick={stop} disabled={stopping}>
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
      {/* One error slot. A stop that did not land is the newer news and the one
          the reader can act on, so it takes the slot while the session is still
          up; it is gone the moment the session settles, whoever settled it.
          Otherwise: what the worker recorded is a Go error string. It says what
          broke to somebody who wrote the worker, and nothing to anybody else —
          so the row states the class of failure, and keeps the original on hover
          for the person who does want it. */}
      {stopErr && !terminal(j.phase) ? (
        <span className="err small">
          <Ico name="alert" size={13} /> {t('stopError')}
        </span>
      ) : (
        j.error &&
        !stopped && (
          <Tip text={t('failDetail', { cause: j.error })}>
            <span className="err small">
              <Ico name="alert" size={13} /> {sessionFailureText(t, j.error)}
            </span>
          </Tip>
        )
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

  const set = (k: TextField) => (e: React.ChangeEvent<HTMLInputElement>) => {
    setP((cur) => ({ ...cur, [k]: e.target.value }))
    setErr('')
  }

  const hasContact = (p.contact_email ?? '').trim() !== ''
  // 006r: publishing puts a name on a result, and the name has to resolve to
  // somewhere. The wizard sets it here rather than sending the signer off to
  // settings and back — they are mid-decision, and the requirement is part of
  // the decision.
  const hasPage = !!p.public && (p.handle ?? '').trim() !== ''
  const canContinueFromStep2 = hasContact && hasPage

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
      // The handle and the public flag are not a convenience: the flip is
      // refused without them, so this save has to succeed and its failure has
      // to be reported. The rest of the profile stays a convenience for the
      // next publish, which is why one save carries both.
      await saveProfile(toProfile ? p : { handle: p.handle, public: p.public })
      await makeProjectPublic(projectID, signOff, flipRepo)
      onPublished()
    } catch (e) {
      // A 409 is either an unclear tree or a profile that is not public, and
      // the status alone cannot tell them apart — they need different remedies,
      // so the body's code decides.
      setErr(publishErrText(t, e))
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
            <button className="btn" disabled={step === 2 && !canContinueFromStep2} onClick={() => setStep(step + 1)}>
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
          <Check on={!!p.public} onChange={(on) => setP((cur) => ({ ...cur, public: on }))}>
            {t('publishPublicProfile')}
          </Check>
          <Check on={toProfile} onChange={setToProfile}>
            {t('publishSaveToProfile')}
          </Check>
          {!hasContact && (
            <p className="muted small" style={{ margin: 0 }}>
              <Ico name="info" size={12} /> {t('publishContactRequired')}
            </p>
          )}
          {hasContact && !hasPage && (
            <p className="muted small" style={{ margin: 0 }}>
              <Ico name="info" size={12} /> {t('publishProfileRequired')}
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
function PublishedCard({
  i18n,
  project,
  signerHandle,
  onWithdrawn,
}: {
  i18n: I18n
  project: Project
  signerHandle?: string
  onWithdrawn: () => void
}) {
  const { t } = i18n
  const [withdrawing, setWithdrawing] = useState(false)
  const when = project.signed_off_at ? new Date(project.signed_off_at).toLocaleDateString() : ''
  const rows: { label: string; node: React.ReactNode }[] = []
  if (project.signed_off_email) rows.push({ label: t('signedOffSigner'), node: <span className="mono">{project.signed_off_email}</span> })
  if (project.signed_off_contact)
    rows.push({ label: t('signedOffContact'), node: (
        <a className="link" href={`mailto:${project.signed_off_contact}`}>
          {project.signed_off_contact}
        </a>
      ) })
  if (project.signed_off_orcid)
    rows.push({
      label: t('signedOffOrcid'),
      node: (
        <a className="link" href={`https://orcid.org/${project.signed_off_orcid}`} target="_blank" rel="noreferrer noopener">
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
        <a className="link" href={project.signed_off_scholar} target="_blank" rel="noreferrer noopener">
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
            {/* The name links to the signer's page when they have one, and is
                plain text when they do not. The snapshot below is what the
                attestation reports; this is a convenience that may come and go
                as its owner turns their profile on and off (006r). */}
            {t('signedOffBy')}{' '}
            {signerHandle ? (
              <a className="link" href={`/u/${signerHandle}`}>
                <strong>{project.signed_off_name}</strong>
              </a>
            ) : (
              <strong>{project.signed_off_name}</strong>
            )}
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
// gradeKinds is the whole outcome family, which the filter rail folds into one
// row: a reader wants "the grading" as a thing, not four of the backend's
// internal steps (006s).
const gradeKinds = new Set([
  'grade',
  'define_outcome',
  'outcome_evaluation_start',
  'outcome_evaluation_ongoing',
  'outcome_evaluation_end',
])

// SessionScreen is one replication session, and everything it produced: the
// transcript of the run, the verdict it was graded to, and the repository it
// published. All three are the session's, which is why all three are addressed
// under it (015) — a project holds papers and a paper has runs, so there is no
// level above this one at which "the verdict" or "the repository" names one
// thing.
function SessionScreen({
  i18n,
  jobId,
  tab,
  onTab,
  onLoaded,
  onBack,
}: {
  i18n: I18n
  jobId: string
  tab: SessionTab
  onTab: (tab: SessionTab) => void
  // The paper this session is about, reported once the job is read — a session
  // opened by its own link has nothing else to name it with.
  onLoaded: (ref: string) => void
  onBack: () => void
}) {
  const { t } = i18n
  const [job, setJob] = useState<Job | null>(null)

  // The job is watched here rather than in the transcript, because all three
  // panes are about it and only one of them is open at a time.
  useEffect(() => {
    getJob(jobId).then(setJob).catch(() => {})
    return watchJob(jobId, setJob)
  }, [jobId])
  useEffect(() => {
    if (job?.ref) onLoaded(job.ref)
  }, [job?.ref, onLoaded])

  return (
    <div className="tx">
      <div className="detail-head session-head">
        <div className="detail-title-row" style={{ alignItems: 'center', gap: 10 }}>
          <Tip text={t('back')}>
            <button className="icon-btn" onClick={onBack} aria-label={t('back')}>
              <Ico name="chevronLeft" size={14} w={2} />
            </button>
          </Tip>
          <span className="row-mono row-title">{job?.ref ?? '…'}</span>
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

// SessionTranscript is the run's trajectory in its own time (006s).
function SessionTranscript({ i18n, jobId, job }: { i18n: I18n; jobId: string; job: Job | null }) {
  const { t } = i18n
  const [tr, setTr] = useState<Transcript | null>(null)
  const [state, setState] = useState<'loading' | 'ready' | 'not_ready' | 'error'>('loading')
  const [hidePlumbing, setHidePlumbing] = useState(true)
  const [off, setOff] = useState<Set<string>>(new Set())
  const [q, setQ] = useState('')
  // The query is deferred so that typing stays responsive on a transcript of
  // several hundred events: the input updates now, the list re-filters when
  // React has room.
  const dq = useDeferredValue(q)
  // Which card the inspector is showing, as an index into the *filtered* list —
  // so it is cleared whenever the filters move under it.
  const [sel, setSel] = useState<number | null>(null)
  const streamRef = useRef<HTMLElement>(null)

  // The job's phase_at feeds the timeline, and its terminal phase tells the
  // transcript poll when to stop chasing a run that will never publish. Read
  // through a ref so the poll does not restart on every observation.
  const jobRef = useRef<Job | null>(job)
  useEffect(() => {
    jobRef.current = job
  }, [job])

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

  // The span the scrubber draws: the session's extent, widened to cover its
  // events. A transcript whose events carry no timestamps and a session with no
  // recorded phases leave no clock at all, and the lifecycle strip stands in.
  const span = useMemo(() => (tr ? runSpan(tr.events, job?.phase_at) : null), [tr, job?.phase_at])

  // seek scrolls the stream to the first *rendered* card at or after an instant
  // — the list it searches is what the filters left standing, so scrubbing never
  // aims at an event that is not on screen. Positions come off the DOM rather
  // than from tracked heights, because a card is as tall as its content and
  // nothing here knows that in advance.
  const seek = useCallback((at: number, list: Event[]) => {
    const stream = streamRef.current
    if (!stream) return
    const i = list.findIndex((e) => {
      const t = timeOf(e)
      return !Number.isNaN(t) && t >= at
    })
    if (i < 0) return
    const card = stream.querySelector<HTMLElement>(`.event[data-i="${i}"]`)
    if (card) stream.scrollTo({ top: card.offsetTop - stream.offsetTop - 8, behavior: 'smooth' })
  }, [])

  if (state !== 'ready' || !tr) {
    // A session still working (non-terminal) hasn't failed to produce a
    // transcript — it just hasn't emitted events yet. Show its live timeline and
    // wait, rather than the "not ready" dead-end reserved for terminal misses.
    const waitingLive = state === 'not_ready' && job && !terminal(job.phase)
    return (
      <>
        {waitingLive && (
          <div className="tx-head">
            <span className="live-dot">{t('liveStreaming')}</span>
          </div>
        )}
        {job && <Timeline i18n={i18n} job={job} />}
        <div className="center-load">
          {waitingLive ? t('transcriptWaitingLive') : state === 'not_ready' ? t('transcriptNotReady') : state === 'error' ? t('transcriptError') : '…'}
        </div>
      </>
    )
  }

  // Real counts by kind, and tools used — never fabricated. The counts describe
  // what the search left standing, so the rail always adds up to the stream.
  const shown = tr.events.filter((e) => !(hidePlumbing && plumbingKinds.has(e.kind)) && matches(e, dq))
  const kindCounts = new Map<string, number>()
  const toolCounts = new Map<string, number>()
  for (const e of shown) {
    kindCounts.set(displayKind(e.kind), (kindCounts.get(displayKind(e.kind)) ?? 0) + 1)
    if (e.kind === 'tool_use' && e.tool) toolCounts.set(e.tool, (toolCounts.get(e.tool) ?? 0) + 1)
  }
  const visible = shown.filter((e) => !off.has(displayKind(e.kind)))
  const usage = tr.usage
  const picked = sel !== null ? visible[sel] : undefined

  return (
    <>
      <div className="tx-head">
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
              <span className="tx-stat-value">{totalOf(usage).toLocaleString()}</span>
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

      {/* The run's own clock when the events carry one; the lifecycle strip when
          they do not, so a transcript without timestamps still says where the
          session got to. */}
      {span ? (
        <Scrubber i18n={i18n} span={span} events={tr.events} job={job} seekList={visible} onSeek={seek} />
      ) : (
        job && <Timeline i18n={i18n} job={job} />
      )}

      <div className="tx-body">
        <aside className="tx-side">
          <div>
            <div className="eyebrow eyebrow-ico" style={{ marginBottom: 8 }}>
              <Ico name="search" size={11} /> {t('search')}
            </div>
            <input
              className="text tx-search"
              type="search"
              value={q}
              placeholder={t('searchTranscript')}
              aria-label={t('searchTranscript')}
              onChange={(ev) => {
                setQ(ev.target.value)
                setSel(null) // the selection indexes the filtered list
              }}
            />
          </div>
          <div>
            <div className="eyebrow eyebrow-ico" style={{ marginBottom: 8 }}>
              <Ico name="filter" size={11} /> {t('showEvents')}
            </div>
            {[...kindCounts.entries()].map(([kind, n]) => (
              <button
                key={kind}
                className={'kind-btn' + (off.has(kind) ? ' off' : '')}
                aria-pressed={!off.has(kind)}
                onClick={() => {
                  setOff((s) => toggle(s, kind))
                  setSel(null)
                }}
              >
                <span className="sq" style={{ color: kindColor(kind) }} />
                <span>{kindLabel(t, kind)}</span>
                <span className="n tabular">{n}</span>
              </button>
            ))}
            {kindCounts.size === 0 && (
              <p className="muted small" style={{ margin: '2px 8px' }}>
                {t('searchNoMatch')}
              </p>
            )}
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
          <Toggle
            on={hidePlumbing}
            onChange={(v) => {
              setHidePlumbing(v)
              setSel(null)
            }}
          >
            {t('hideLifecycle')}
          </Toggle>
        </aside>

        <main className="tx-stream" ref={streamRef}>
          {visible.map((e, i) => (
            <EventCard key={i} i18n={i18n} e={e} i={i} span={span} on={i === sel} onPick={() => setSel(i === sel ? null : i)} />
          ))}
        </main>

        <aside className="tx-side tx-inspector">
          {picked && <EventDetail i18n={i18n} e={picked} span={span} onClose={() => setSel(null)} />}
          <div>
            <div className="eyebrow eyebrow-ico" style={{ marginBottom: 10, color: 'var(--brand)' }}>
              <Ico name="bolt" size={11} /> {t('usage')}
            </div>
            {usage ? (
              <div className="usage-rows">
                <UsageLine label={t('usageInput')} v={usage.InputTokens} tone="var(--run)" />
                <UsageLine label={t('usageOutput')} v={usage.OutputTokens} tone="var(--pub)" />
                <UsageLine label={t('usageCached')} v={usage.CachedInputTokens} tone="var(--ok)" />
                <UsageLine label={t('usageCacheWritten')} v={cacheWrittenOf(usage)} tone="var(--warn)" />
                <UsageLine label={t('usageTotal')} v={totalOf(usage)} tone="var(--brand)" strong />
              </div>
            ) : (
              <p className="muted small" style={{ margin: 0 }}>
                {t('usageNotRecorded')}
              </p>
            )}
          </div>
        </aside>
      </div>
    </>
  )
}

// Scrubber draws the run against its own clock: the lifecycle phases at the
// width they earned, a histogram of where the run spent itself, and a cursor
// that scrolls the stream to a moment.
//
// It never filters. Dimming the events outside the cursor would mean a style
// recomputed on every one of several hundred cards per pointer frame — and
// scrolling to the moment is what a reader wanted anyway.
function Scrubber({
  i18n,
  span,
  events,
  job,
  seekList,
  onSeek,
}: {
  i18n: I18n
  span: Span
  events: Event[]
  job: Job | null
  // seekList is what the filters left standing — the same list the seek
  // resolves against, so the cursor never points at a card that is not on
  // screen.
  seekList: Event[]
  onSeek: (at: number, list: Event[]) => void
}) {
  const { t } = i18n
  const barRef = useRef<HTMLDivElement>(null)
  const [cursor, setCursor] = useState<number | null>(null)
  const bands = useMemo(() => phaseBands(job?.phase_at, span), [job?.phase_at, span])
  const hist = useMemo(() => histogram(events, span, 64), [events, span])

  // A pointer anywhere on the bar seeks; holding and dragging keeps seeking.
  // Pointer capture is what makes a drag survive leaving the element.
  // The cursor follows the event the scrub landed on, not the pointer. A long
  // session that finished its work early leaves a stretch of bar with no events
  // under it; every position in it resolves to the last event, so a cursor left
  // where the pointer was would report a time the transcript is nowhere near.
  const seekTo = (clientX: number) => {
    const box = barRef.current?.getBoundingClientRect()
    if (!box) return
    const f = Math.min(1, Math.max(0, (clientX - box.left) / box.width))
    const target = seekTarget(seekList, span, f)
    if (!target) {
      setCursor(f)
      return
    }
    setCursor(target.cursor)
    onSeek(target.at, seekList)
  }

  return (
    <div className="scrub">
      <span className="scrub-clock">{fmtClock(span.from)}</span>
      <div
        ref={barRef}
        className="scrub-bar"
        role="slider"
        tabIndex={0}
        aria-label={t('scrubLabel')}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round((cursor ?? 0) * 100)}
        onPointerDown={(e) => {
          e.currentTarget.setPointerCapture(e.pointerId)
          seekTo(e.clientX)
        }}
        onPointerMove={(e) => {
          if (e.currentTarget.hasPointerCapture(e.pointerId)) seekTo(e.clientX)
        }}
        onKeyDown={(e) => {
          const step = e.key === 'ArrowLeft' ? -0.02 : e.key === 'ArrowRight' ? 0.02 : 0
          if (!step) return
          e.preventDefault()
          const box = barRef.current?.getBoundingClientRect()
          if (box) seekTo(box.left + Math.min(1, Math.max(0, (cursor ?? 0) + step)) * box.width)
        }}
      >
        {bands.map((b) => (
          <div
            key={b.phase}
            className={'scrub-band ' + phaseClass(b.phase)}
            data-phase={b.phase}
            style={{ left: pct(b.left), width: pct(b.width) }}
          >
            <span>{phaseLabel(t, b.phase)}</span>
          </div>
        ))}
        <div className="scrub-hist">
          {hist.bars.map((h, i) => (
            <i key={i} style={{ height: pct(h) }} />
          ))}
        </div>
        {cursor !== null && (
          <>
            <div className="scrub-past" style={{ width: pct(cursor) }} />
            <div className="scrub-cursor" style={{ left: pct(cursor) }} />
          </>
        )}
      </div>
      <span className="scrub-clock right">{fmtClock(span.to)}</span>
      <span className="scrub-legend">{hist.weighted ? t('scrubByTokens') : t('scrubByEvents')}</span>
    </div>
  )
}

// EventDetail is the selected event in full — the reason the cards can stay
// scannable. Nothing here is truncated or folded: this is where a reader goes
// when the card was not enough.
function EventDetail({ i18n, e, span, onClose }: { i18n: I18n; e: Event; span: Span | null; onClose: () => void }) {
  const { t } = i18n
  const dk = displayKind(e.kind)
  return (
    <div className="ev-detail">
      <div className="eyebrow eyebrow-ico" style={{ marginBottom: 10, color: kindColor(dk) }}>
        <Ico name={kindIcon(dk)} size={11} /> {kindLabel(t, dk)}
        <button className="icon-btn tiny" onClick={onClose} aria-label={t('close')} style={{ marginLeft: 'auto' }}>
          <Ico name="x" size={12} w={2} />
        </button>
      </div>
      <div className="ev-detail-meta">
        <span>{e.role}</span>
        {e.tool && <code>{e.tool}</code>}
        {clockOf(e) && <span className="tabular">{clockOf(e)}</span>}
        {span && offsetOf(e, span) && <span className="tabular">{offsetOf(e, span)}</span>}
      </div>
      {e.usage && (
        <div className="usage-rows" style={{ marginTop: 10 }}>
          <UsageLine label={t('usageInput')} v={e.usage.InputTokens} tone="var(--run)" />
          <UsageLine label={t('usageOutput')} v={e.usage.OutputTokens} tone="var(--pub)" />
          <UsageLine label={t('usageCached')} v={e.usage.CachedInputTokens} tone="var(--ok)" />
          <UsageLine label={t('usageCacheWritten')} v={cacheWrittenOf(e.usage)} tone="var(--warn)" />
          <UsageLine label={t('usageTotal')} v={totalOf(e.usage)} tone="var(--brand)" strong />
        </div>
      )}
      <pre className="ev-detail-body">{e.content || t('noContent')}</pre>
    </div>
  )
}

// pct formats a 0..1 fraction as a CSS percentage.
function pct(f: number) {
  return `${(f * 100).toFixed(3)}%`
}

// fmtClock is a wall-clock label for the ends of the scrubber.
function fmtClock(ms: number) {
  return new Date(ms).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: false })
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

function EventCard({
  i18n,
  e,
  i,
  span,
  on,
  onPick,
}: {
  i18n: I18n
  e: Event
  i: number
  span: Span | null
  on: boolean
  onPick: () => void
}) {
  const { t } = i18n
  const dk = displayKind(e.kind)
  const isTool = e.kind === 'tool_use' || e.kind === 'tool_result'
  const isGrade = gradeKinds.has(e.kind)
  const cls =
    'event ' + (e.kind === 'thinking' ? 'thinking' : isGrade ? 'grade' : isTool ? 'tool' : 'message') + (on ? ' on' : '')
  // A title is the event's own first line and nothing more — see splitTitle.
  const { title, body } = splitTitle(e.content)
  const at = timeOf(e)
  return (
    <div
      className={cls}
      data-i={i}
      data-at={Number.isNaN(at) ? '' : at}
      onClick={onPick}
      role="button"
      tabIndex={0}
      aria-pressed={on}
      onKeyDown={(ev) => {
        if (ev.key === 'Enter' || ev.key === ' ') {
          ev.preventDefault()
          onPick()
        }
      }}
    >
      <div className="event-gutter">
        <span className="event-clock tabular">{clockOf(e) || e.role}</span>
        <span className="event-kind">
          <Ico name={kindIcon(dk)} size={10} w={2.2} />
          {kindLabel(t, dk)}
        </span>
        {span && <span className="event-offset tabular">{offsetOf(e, span)}</span>}
      </div>
      <div className="event-main">
        {e.kind === 'thinking' ? (
          <div className="event-head">
            <span className="event-title dim">{t('thinking')}</span>
            <EventCost u={e.usage} />
          </div>
        ) : e.kind === 'tool_use' ? (
          <>
            <div className="event-head">
              <span className="event-title">{t('toolCall')}</span>
              {e.tool && <code>{e.tool}</code>}
              <EventCost u={e.usage} />
            </div>
            {e.content && <pre className="event-io">{e.content}</pre>}
          </>
        ) : e.kind === 'tool_result' ? (
          <>
            <div className="event-head">
              <span className="event-title">{t('toolResult')}</span>
              <EventCost u={e.usage} />
            </div>
            <Disclosure summary={title || t('noContent')}>
              <pre>{e.content || t('noContent')}</pre>
            </Disclosure>
          </>
        ) : (
          <>
            <div className="event-head">
              <span className="event-title">{title || t('noContent')}</span>
              <EventCost u={e.usage} />
            </div>
            {body && <div className="event-body">{body}</div>}
          </>
        )}
      </div>
    </div>
  )
}

// EventCost is what this one request pushed through the model — see freshOf for
// why it is not the total. An event that was charged nothing renders nothing: a
// zero here would claim a free request happened, which is not what an absent
// figure means (006s).
// What the figure means is said once, in the scrubber's legend above the
// stream, rather than in a tooltip on each card: a card clips its own overflow,
// so an overlay anchored inside one is cut off (the reason overlays portal to
// the body elsewhere in this app).
function EventCost({ u }: { u?: Usage }) {
  if (!u) return null
  return <span className="event-cost tabular">{compactTokens(freshOf(u))}</span>
}

/* ─── helpers ─── */

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
//
// A kind with no name of its own is humanised rather than printed raw: the
// backend adds event types faster than the client learns them, and
// `outcome_evaluation_ongoing` sitting in a filter row is an identifier leaking
// onto the screen, not a label (006s).
function kindLabel(t: I18n['t'], kind: string) {
  const map: Record<string, string> = {
    message: 'message',
    thinking: 'thinkingLabel',
    tool_use: 'toolCall',
    tool_result: 'toolResult',
    grade: 'grade',
  }
  return map[kind] ? t(map[kind]) : kind.replace(/_/g, ' ')
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
