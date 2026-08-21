import { Fragment, useCallback, useDeferredValue, useEffect, useMemo, useRef, useState } from 'react'
import { LatereMark, Mark, Wordmark } from './brand'
import { fileKind, highlightIn, isExternalHref, langOf, renderMarkdown, resolveRelative } from './artifact'
import {
  CRITERIA,
  outcomeTone,
  rate,
  releaseTone,
  scoreOf,
  unresolved,
  verdictTone,
  type ClaimOutcome,
  type Components,
  type Criterion,
  type Findings,
  type Mismatch,
  type ReleaseResult,
} from './findings'
import {
  addMember,
  archiveUrl,
  artifactUrl,
  cancelSession,
  deleteSession,
  createProject,
  getArtifact,
  getJob,
  allocateCredits,
  getFindings,
  errCode,
  getMe,
  getWallet,
  getSolvency,
  startCheckout,
  unfreezeAccount,
  refreezeAccount,
  type Wallet,
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
  listTrash,
  purgeProject,
  restoreProject,
  trashProject,
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
  movePaper,
  editPaper,
  refreshPapers,
  describeProject,
  draftDescription,
  startSession,
  listModels,
  type ModelTier,
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
  type PublishBlocker,
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
  blockerTab,
  publishBlockers,
  publishBlockerText,
  publishErrText,
  refusal,
  sessionFailureText,
  startErrLabel,
  checkoutErrLabel,
  addPaperErrLabel,
  removePaperErrLabel,
  movePaperErrLabel,
  deleteSessionErrLabel,
  trashErrLabel,
  purgeErrLabel,
} from './errors'
import { creditedMicro, formatUSD, MICRO, parseUSD, toDollars } from './money'
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
import { Landing } from './landing'
import { isPublicView, useRouter, type Navigate, type ProjectTab, type SessionTab, type SettingsTab, type View } from './routes'
import { useHead } from './head'
import { gradeable, groupByPaper, type Grouped, type PaperGroup } from './papers'
import { Check, Disclosure, Modal, NumberField, Pill, PromptDialog, Segmented, Select, Tip, tipAttrs, Toggle, type Tone } from './ui'
import { useTheme, type ThemeMode } from './theme'
import { absoluteTime, elapsed, relativeTime } from './when'
import { useDrawer, useSidebar } from './sidebar'
import { freshest, stopPending, terminal } from './session-stop'

// signerOf names the person who published a project. The signer's recorded name
// is used whenever the sign-off carries one (006n); a v1 sign-off has none and
// falls back to the owner's profile name (018). `owner` itself is never shown —
// it is an opaque OIDC subject and names nobody.
const signerOf = (p: Project & { owner_name?: string }) => p.signed_off_name || p.owner_name || '—'

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
  // The tab follows the screen from the second one onwards; the first one's head
  // was written by the server (042a).
  useHead(view, i18n.t, me === null ? null : me.authenticated)

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
  // The registry and everything under it are readable signed out (028), and they
  // render in the app's own frame rather than a preview of it — a reader who came
  // from a paper is reading the work, not being shown a sample of a product.
  //
  // Every other screen still needs an account, and every one of them is an
  // address somebody can arrive at cold — an invitation mail pointing at a
  // project, a shared link to a session. Sign-in comes back to that address
  // rather than to the dashboard.
  if (!me.authenticated && !isPublicView(view)) return <Landing i18n={i18n} next={window.location.pathname} theme={theme} />
  if (!me.authenticated) return <Shell i18n={i18n} me={me} theme={theme} refreshMe={refreshMe} view={view} navigate={navigate} />
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
          <Mark size={40} className="auth-logo" />
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
          <Ico name="globe" size={13} /> <Wordmark />
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

function AuthUnavailable({ i18n, onRetry }: { i18n: I18n; onRetry: () => void }) {
  return (
    <div className="auth-center">
      <div className="auth-card">
        <Mark size={40} className="auth-logo" />
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
        <Mark size={40} className="auth-logo" />
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
  count,
  onClick,
}: {
  label: string
  icon: IconName
  tone: string
  on: boolean
  folded?: boolean
  count?: number
  onClick: () => void
}) {
  return (
    <button className={'nav-btn' + (on ? ' active' : '')} data-tip={folded ? label : undefined} data-tip-side="right" onClick={onClick}>
      <span className="nav-ico" style={{ color: tone }}>
        <Ico name={icon} size={13} />
      </span>
      <span className="nav-label">{label}</span>
      {/* Absent until it is known, so the rail never shows a 0 that is really a
          fetch in flight. It is removed from the layout when folded, not merely
          hidden — a 64px column has no room to hold open. */}
      {count !== undefined && <span className="count">{count}</span>}
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
  // The same frame serves a reader with no account (028). What changes is what is
  // in it: the destinations that need one are absent rather than disabled, and
  // the rail's foot offers the way in where the account would be. Everything
  // conditional below reads this one flag.
  const signedIn = me.authenticated
  const signInHref = `/login?next=${encodeURIComponent(window.location.pathname)}`
  // The top-up modal's open-state lives here, so the wallet pill, the wallet
  // screen, and later the frozen banner (022f) all open the one dialog.
  const [topUp, setTopUp] = useState(false)
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

  // How many projects the rail's Projects row carries. It is fetched here rather
  // than lifted out of the screen, because the rail outlives every screen and a
  // number that only existed while the list was mounted would vanish the moment
  // the reader opened one of its rows.
  const [projectCount, setProjectCount] = useState<number | undefined>(undefined)
  useEffect(() => {
    if (!signedIn) return
    let live = true
    listProjects()
      .then((p) => live && setProjectCount((p ?? []).length))
      .catch(() => {})
    return () => {
      live = false
    }
  }, [signedIn, view.name])

  // Below the phone breakpoint the rail is an overlay instead of a column, and
  // this is what holds it open (030). It is not the fold and never persists —
  // see `useDrawer`. Above the breakpoint `.rail-open` paints nothing, so the
  // same markup serves both widths.
  const drawer = useDrawer()
  // Opening a destination dismisses the drawer, because on a phone it is
  // covering the screen being navigated to. Every rail destination goes through
  // `setView` (013), so wrapping it once covers all of them.
  const goto = useCallback(
    (v: View) => {
      drawer.hide()
      setView(v)
    },
    [drawer, setView],
  )

  return (
    <div className="shell">
      {/* The scrim is a sibling of the rail rather than a portal: it belongs to
          the shell's own stacking context, which nothing here promotes, and it
          has to sit under the rail and over the screen — an order a portal to
          <body> would have to restate. */}
      {drawer.open && <div className="rail-scrim" onClick={drawer.hide} />}
      <nav className={'rail' + (folded ? ' rail-fold' : '') + (drawer.open ? ' rail-open' : '')}>
        <div className="rail-head">
          <Mark size={22} className="rail-logo" />
          <Wordmark className="rail-name" />
          <span className="rail-beta">{t('beta')}</span>
          {/* The one control that must stay reachable in both states, so it is
              the only thing the head keeps when the rail folds. */}
          <button className="rail-fold-btn" aria-expanded={!folded} aria-label={foldLabel} data-tip={foldLabel} data-tip-side="right" onClick={toggleRail}>
            {folded ? (
              <>
                <span className="fold-mark">
                  <Mark size={22} />
                </span>
                <span className="fold-chev">
                  <Ico name="chevronRight" size={14} w={2.2} />
                </span>
              </>
            ) : (
              <Ico name="chevronLeft" size={14} w={2.2} />
            )}
          </button>
          {/* The drawer's own way out, at the same corner. It is a second
              control rather than a second meaning for the one above, because
              which one is showing has to be decided by the width alone: a
              button that read the drawer's state would go dead on a window
              resized back to desktop while the drawer was open. CSS shows
              exactly one of the two, so there is no state to go stale. */}
          <button className="rail-close-btn" aria-label={t('closeNav')} onClick={drawer.hide}>
            <Ico name="x" size={16} w={2.2} />
          </button>
        </div>
        <div className="rail-nav">
          <div className="rail-group">
            <div className="eyebrow">{signedIn ? t('workspace') : t('navPublic')}</div>
            {signedIn && (
              <NavItem
                label={t('navProjects')}
                icon="folder"
                tone="var(--brand)"
                folded={folded}
                count={projectCount}
                on={view.name === 'projects' || view.name === 'project' || view.name === 'session'}
                onClick={() => goto({ name: 'projects' })}
              />
            )}
            <NavItem
              label={t('navPublic')}
              icon="globe"
              tone="var(--pub)"
              folded={folded}
              on={view.name === 'public' || (!signedIn && (view.name === 'project' || view.name === 'session'))}
              onClick={() => goto({ name: 'public' })}
            />
            {/* The wallet is a primary destination — money you manage often — so
                it lives with the workspace rather than buried by Settings. */}
            {signedIn && (
              <NavItem
                label={t('navWallet')}
                icon="wallet"
                tone="var(--ok)"
                folded={folded}
                on={view.name === 'wallet'}
                onClick={() => goto({ name: 'wallet' })}
              />
            )}
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
                onClick={() => goto({ name: 'admin' })}
              />
            </div>
          )}
          {/* The trash sits on its own at the foot of the destinations: it is
              where work goes, not a place work is done. */}
          {signedIn && (
            <div className="rail-group rail-group-foot">
              <NavItem
                label={t('navTrash')}
                icon="trash"
                tone="var(--fg-3)"
                folded={folded}
                on={view.name === 'trash'}
                onClick={() => goto({ name: 'trash' })}
              />
              {/* Settings sits with Trash at the foot: personal chrome, not a
                  workspace destination. Active on any of its sub-tabs (024a). */}
              <NavItem
                label={t('navSettings')}
                icon="settings"
                tone="var(--fg-3)"
                folded={folded}
                on={view.name === 'settings'}
                onClick={() => goto({ name: 'settings' })}
              />
            </div>
          )}
        </div>
        <div className="rail-foot">
          {signedIn ? (
            <button className="user-btn" data-tip={folded ? me.name || me.email : undefined} data-tip-side="right" onClick={() => goto({ name: 'settings' })}>
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
          ) : (
            /* Where the account would be, for somebody who has not got one. It is
               an anchor, not a button: signing in leaves this origin and comes
               back, and `next` carries where to (013). */
            <a className="btn rail-signin" data-test="rail-signin" data-tip={folded ? t('signInTitle') : undefined} data-tip-side="right" href={signInHref}>
              <LatereMark height={11} /> <span className="nav-label">{t('signInTitle')}</span>
            </a>
          )}
        </div>
      </nav>

      <div className="main">
        <header className="topbar">
          {/* The way back to the rail once it has left the flow. It is in the
              markup at every width and hidden by CSS above the breakpoint, so
              the shell is one tree — and it leads the bar rather than joining
              the controls on the right, because it is where the reader is, not
              something to do here. */}
          <button className="rail-open-btn" aria-label={t('openNav')} aria-expanded={drawer.open} onClick={drawer.show}>
            <Ico name="menu" size={17} w={2} />
          </button>
          <Breadcrumb i18n={i18n} view={view} setView={setView} signedIn={signedIn} />
          <div className="topbar-actions">
            {signedIn ? (
              <>
                <WalletPill i18n={i18n} credits={me.credits} onTopUp={() => setTopUp(true)} />
                <ThemeSwitcher i18n={i18n} theme={theme} compact />
                <button className="btn btn-topbar" disabled={me.frozen} aria-label={t('newProject')} data-tip={me.frozen ? t('frozenControl') : undefined} onClick={() => setView({ name: 'submit' })}>
                  {/* The label is a span so a phone can drop it and keep the plus.
                      The button keeps its aria-label at every width, so what is lost
                      is the reading, not the name. */}
                  <Ico name="plus" size={13} w={2.2} /> <span className="btn-label">{t('newProject')}</span>
                </button>
              </>
            ) : (
              /* Reading is free and starting a session is not, so the bar offers
                 the one act a reader here can take. It repeats the rail's foot on
                 purpose: on a phone the rail is a drawer, and the way in must not
                 be behind it. */
              <>
                <ThemeSwitcher i18n={i18n} theme={theme} compact />
                <a className="btn btn-topbar" data-test="topbar-signin" href={signInHref}>
                  <LatereMark height={11} /> <span className="btn-label">{t('signInTitle')}</span>
                </a>
              </>
            )}
          </div>
        </header>

        {/* A clawed-back payment left the wallet negative: the account is
            view-only until it is topped back up (022d/022f). */}
        {me.frozen && (
          <div className="banner warn" data-test="frozen-banner">
            <Ico name="alert" size={14} />
            <span style={{ flex: 1 }}>{t('frozenBanner')}</span>
            <button className="btn ghost small" onClick={() => setTopUp(true)}>
              <Ico name="wallet" size={13} /> {t('topUp')}
            </button>
          </div>
        )}

        {view.name === 'projects' && <ProjectsScreen i18n={i18n} me={me} onOpen={openProject} />}
        {view.name === 'wallet' && <WalletScreen i18n={i18n} me={me} refreshMe={refreshMe} onTopUp={() => setTopUp(true)} />}
        {view.name === 'trash' && <TrashScreen i18n={i18n} me={me} />}
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
        {view.name === 'settings' && <SettingsScreen i18n={i18n} me={me} theme={theme} tab={view.tab} navigate={setView} />}
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
            onTopUp={() => setTopUp(true)}
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
            signedIn={signedIn}
          />
        )}
      </div>
      {topUp && <TopUpModal i18n={i18n} onClose={() => setTopUp(false)} />}
    </div>
  )
}

function Breadcrumb({ i18n, view, setView, signedIn }: { i18n: I18n; view: View; setView: (v: View) => void; signedIn: boolean }) {
  const { t } = i18n
  // Signed out there is one root, and it is the registry: a project reached from
  // there belongs under it, and "Projects" would name a screen this reader has
  // nothing in and cannot open.
  const root = () =>
    view.name === 'public' || !signedIn
      ? t('navPublic')
      : view.name === 'trash'
        ? t('navTrash')
        : view.name === 'submit'
          ? t('newProject')
          : view.name === 'wallet'
            ? t('navWallet')
            : view.name === 'settings'
              ? t('settings')
              : view.name === 'admin'
                ? t('navAdmin')
                : t('navProjects')
  const rootView: View =
    view.name === 'public' || !signedIn ? { name: 'public' } : view.name === 'trash' ? { name: 'trash' } : { name: 'projects' }
  return (
    <div className="crumb">
      <button onClick={() => setView(rootView)}>{root()}</button>
      {view.name === 'settings' && view.tab === 'profile' && (
        <>
          <span className="sep">/</span>
          <span className="leaf">{t('settingsProfile')}</span>
        </>
      )}
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

// Meter is a proportion drawn as a bar. It is never the only thing said about a
// number — the count and its denominator are always beside it in words, because
// a bar alone is a rate, and a rate alone is the thing this product refuses to
// hand out.
function Meter({ pct, tone, size }: { pct: number; tone: string; size?: 'sm' }) {
  return (
    <span className={size === 'sm' ? 'meter sm' : 'meter'} aria-hidden="true">
      <span className="meter-fill" style={{ width: `${Math.max(0, Math.min(100, pct))}%`, background: tone }} />
    </span>
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
function VisibilityBadge({ i18n, visibility, icon }: { i18n: I18n; visibility: 'private' | 'public'; icon?: boolean }) {
  const glyph = <Ico name={visibility === 'public' ? 'globe' : 'lock'} size={icon ? 14 : 11} w={icon ? 1.7 : 2} />
  // In a list the word is the same on nearly every row, so the glyph carries it
  // and the word moves into the tooltip — which is what buys the column its
  // width back for the figures beside it. Standing on its own, it keeps the word.
  if (icon)
    return (
      <Tip text={i18n.t(visibility)}>
        <span className={'vis-ico ' + visibility} aria-label={i18n.t(visibility)} role="img">
          {glyph}
        </span>
      </Tip>
    )
  return (
    <span className={'badge ' + visibility}>
      {glyph}
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
// shared, with the owner named on the row rather than as a tab (018). Which of
// the two a project was never a fact the reader chose, and splitting on it meant
// finding work started with remembering who had created it.
//
// 034 moved the owner and the date out of the column grid and into the title's
// meta line. That is not a demotion of what they say — the owner still links to
// their profile, the age still carries its exact moment — it is what frees the
// width for the column that was missing: what this project has actually handed
// over.
function ProjectsScreen({ i18n, me, onOpen }: { i18n: I18n; me: Me; onOpen: (id: string, title?: string) => void }) {
  const { t } = i18n
  const [list, setList] = useState<ProjectView[] | null>(null)
  // The project the reader has asked to throw away, held while they confirm.
  const [dropping, setDropping] = useState<ProjectView | null>(null)
  // The filter narrows what is already loaded and fetches nothing, so it can
  // never disagree with what the rows say.
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<ProjectFilter>('all')

  const load = useCallback(() => {
    listProjects()
      .then((p) => setList(p ?? []))
      .catch(() => setList([]))
  }, [])
  useEffect(load, [load])

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
        <h1 className="screen-title">{t('navProjects')}</h1>
        <p className="screen-desc">{t('projectsIntro')}</p>
      </div>

      {/* The boundary band. It replaces three counters, two of which restated
          the list below them. */}
      <div className="boundary-grid" data-test="boundary">
        <div className="boundary" data-test="evidence-card">
          <div className="boundary-head">
            <span className="eyebrow">{t('evidenceEyebrow')}</span>
            <span className="boundary-aside">{t('evidenceAcross', { n: String(projects.length) })}</span>
          </div>
          <div className="boundary-figure">
            <span className="boundary-n" data-test="evidence-n">
              {list === null || !pubKnown ? '—' : published}
            </span>
            <span className="boundary-of">{t('evidenceOf', { n: String(sessionTotal) })}</span>
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
            <span className="boundary-n">{me.credits ? formatUSD(me.credits.wallet) : '—'}</span>
            <span className="boundary-of">{t('creditsInWallet')}</span>
          </div>
          <p className="boundary-note">{t('creditsNote')}</p>
          {/* The windowed spend keeps its own row and keeps its window named
              (021e): a figure whose period is not stated is not a figure. */}
          <div className="boundary-foot" data-test="spend-30d">
            <span>{t('statSpend30d')}</span>
            <span className="boundary-foot-n">{list === null || !spendKnown ? '—' : formatUSD(spend30d)}</span>
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
          {/* A segmented control is back on this screen, and 018's removal of the
              mine/shared tabs still stands: that split was a fact about a project
              the reader did not choose, and this is a narrowing they pick and can
              unpick. */}
          <Segmented
            value={filter}
            options={[
              { value: 'all' as const, label: t('filterAll') },
              { value: 'running' as const, label: t('filterRunning') },
              { value: 'published' as const, label: t('filterPublished') },
            ]}
            onChange={setFilter}
            label={t('colStatus')}
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
              {/* Named so the phone can fold it away first: what a project has
                  handed over is context, and what a row *is* — its title and its
                  phase — is not (030). */}
              <span className="eyebrow row-published">{t('colPublished')}</span>
              <span className="eyebrow">{t('colStatus')}</span>
              <span className="eyebrow row-visibility">{t('colVisibility')}</span>
              <span />
              <span />
              <span />
            </div>
            {shown.map((p) => (
              <div key={p.id} className="row" onClick={() => onOpen(p.id, p.title)}>
                <div className="row-main">
                  <div className="row-title">{p.title}</div>
                  {/* One line of what the project is about, where its owner has
                      said (038). It sits above the facts rather than among them:
                      it is the only part of the row somebody wrote. */}
                  {p.description && (
                    <div className="row-desc" data-test="row-description">
                      {p.description}
                    </div>
                  )}
                  {/* Owner, count, and age on one line under the title. They are
                      the same facts the columns carried and they still behave the
                      same way — the owner links, the age holds its exact moment —
                      they simply no longer each cost a column. */}
                  <div className="row-meta">
                    <OwnerCell i18n={i18n} me={me} p={p} />
                    <span className="row-meta-dot" aria-hidden="true" />
                    <span>{t('sessionCount', { n: String(p.session_count) })}</span>
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
                {/* Each badge gets its own cell rather than sizing the column
                    itself. A badge is as wide as its word — "1 RUNNING" against
                    "DONE" — and letting that width set the column is what pushed
                    every cell to its left out of line, row by row. */}
                <span className="row-cell">
                  <StatusBadge i18n={i18n} p={p} />
                </span>
                <span className="row-cell row-visibility">
                  <VisibilityBadge i18n={i18n} visibility={p.visibility} icon />
                </span>
                {/* An anchor, not a fetch: the browser's own download is what
                    should handle a body this size. The condition is the server's
                    own — a project whose sessions all failed before publishing
                    has sessions and nothing to hand over, and the download is a
                    top-level navigation, so reaching its refusal would replace
                    the app with a JSON body (020). */}
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
            load()
          }}
        />
      )}
    </div>
  )
}

// The confirmation before a project leaves the list. It is the app's own dialog,
// not window.confirm: a chrome-owned box ignores the theme, cannot say the one
// thing worth saying here, and blocks the tab while it is up.
//
// The one thing worth saying is that this is reversible — and, for a published
// project, that it is not only a removal: the publication ends, and the
// retraction goes on the record.
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
    <Modal
      title={t('trashTitle')}
      icon="trash"
      eyebrow={p.title}
      onClose={onClose}
      foot={
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
      <p className="muted">{t('trashBody')}</p>
      {p.visibility === 'public' && <p className="warn small">{t('trashPublicNote')}</p>}
      {err && <p className="err small">{err}</p>}
    </Modal>
  )
}

/* ─── Trash ─── */

// The trash: what an owner threw away, and the two things they can do about it.
// It is a screen rather than an undo toast because the decision it holds — the
// permanent one — is not a decision to make inside a five-second window.
function TrashScreen({ i18n, me }: { i18n: I18n; me: Me }) {
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
    } catch (e) {
      setErr(generic(t, e, 'restoreFailed'))
    }
    setBusy('')
  }

  const projects = list ?? []
  return (
    <div className="screen">
      <div className="screen-head">
        <h1 className="screen-title">{t('navTrash')}</h1>
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
                  <div className="row-sub">{t('sessionCount', { n: String(p.session_count) })}</div>
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

// The permanent delete's confirmation. What it spends its words on is what does
// *not* go: the repositories close rather than being destroyed, because a
// reproduction the agent wrote is evidence and this product does not destroy
// evidence on a click.
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
    <Modal
      title={t('purgeTitle')}
      icon="alert"
      eyebrow={p.title}
      onClose={onClose}
      foot={
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
      <p className="muted">{t('purgeBody')}</p>
      <p className="muted small">{t('purgeKeepsRepos')}</p>
      {err && <p className="err small">{err}</p>}
    </Modal>
  )
}

// The Owner column. Your own projects say "You" — the common case, and one
// nobody should have to read a name to recognise. Somebody else's is their
// profile name, linking to their public page when they have one. A name the
// server could not resolve is a dash: the opaque subject it would otherwise fall
// back to named nobody either.
function OwnerCell({ i18n, me, p }: { i18n: I18n; me: Me; p: ProjectView }) {
  const { t } = i18n
  // Both sides can be absent — a reader with no stake is not sent the owning
  // principal, and a signed-out reader has none of their own (028) — and two
  // absences are not the same person.
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
  const rel = relativeTime(at)
  const tip = tipAttrs(absoluteTime(at))
  return (
    <span {...tip} className={`row-modified ${tip.className}`} data-test="modified">
      {rel || i18n.t('ownerUnknown')}
    </span>
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
    // A project may start empty (036) — it records papers and runs nothing — but
    // it may not start nameless, because the title falls back to the first
    // paper's reference and with no paper there is no fallback.
    if (papers.length === 0 && !title.trim()) {
      setErr(t('nameOrPaper'))
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
          <span className="attestation">{t('papersOptional')}</span>
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

// SettingsScreen is one screen with three sub-tabs, each a real path (024a):
// Account is what you are on the platform, Profile is what you publish as, and
// Credits is what you have to spend. Switching a tab navigates, so Back returns
// to the previous tab and a link opens on the tab it names.
function SettingsScreen({
  i18n,
  me,
  theme,
  tab,
  navigate,
}: {
  i18n: I18n
  me: Me
  theme: ReturnType<typeof useTheme>
  tab?: SettingsTab
  navigate: Navigate
}) {
  const { t } = i18n
  const cur: SettingsTab = tab ?? 'account'
  const tabs: { k: SettingsTab; label: string }[] = [
    { k: 'account', label: t('settingsAccount') },
    { k: 'profile', label: t('settingsProfile') },
  ]
  return (
    <div className="screen">
      <div className="screen-head">
        <h1 className="screen-title">{t('settings')}</h1>
      </div>
      <div className="detail-tabs" style={{ marginBottom: 18 }}>
        {tabs.map((tb) => (
          <button key={tb.k} className={'tab' + (cur === tb.k ? ' on' : '')} onClick={() => navigate({ name: 'settings', tab: tb.k })}>
            {tb.label}
          </button>
        ))}
      </div>
      <div className="stack" style={{ maxWidth: 520 }}>
        {cur === 'account' && <AccountCard i18n={i18n} me={me} theme={theme} />}
        {cur === 'profile' && <ProfileCard i18n={i18n} />}
      </div>
    </div>
  )
}

// AccountCard is identity, access, appearance, and the way out — what you are on
// the platform, kept apart from what you publish as (Profile) and what you have
// to spend (Credits).
function AccountCard({ i18n, me, theme }: { i18n: I18n; me: Me; theme: ReturnType<typeof useTheme> }) {
  const { t } = i18n
  return (
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
  )
}

// CreditsCard is the wallet balance and its statement — the one place a person
// reads their own number and can see where it came from. 024b makes each move
// name its project; the statement itself is unchanged here.
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

// isMine reports whether a project's owning principal is this reader's.
//
// Both sides are optional and both are absent at once on the screen this exists
// for: signed out, `me.principal` is undefined and the server withholds `owner`
// from a reader with no stake in the project (028) — so a bare `===` would make
// every registry row the reader's own, and every public project look editable.
// Two unknowns are not a match.
export function isMine(me: Me, owner?: string): boolean {
  return !!me.principal && !!owner && owner === me.principal
}

// AdminScreen is the console: who may sign in, at what tier, with how much
// headroom, and the platform-wide default they draw from.
function AdminScreen({ i18n, onCreditsChanged }: { i18n: I18n; onCreditsChanged: () => void }) {
  const { t } = i18n
  const [people, setPeople] = useState<GrantView[] | null>(null)
  const [settings, setSettings] = useState<PlatformSettings | null>(null)
  const [solvency, setSolvency] = useState<number | null>(null)
  const [err, setErr] = useState('')

  const refresh = useCallback(() => {
    listGrants()
      .then((g) => setPeople(g ?? []))
      .catch(() => setPeople([]))
    getPlatformSettings()
      .then(setSettings)
      .catch(() => setSettings(null))
    getSolvency()
      .then((s) => setSolvency(s.outstanding_micro))
      .catch(() => setSolvency(null))
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
        {solvency !== null && (
          <div className="card card-pad" data-tip={t('solvencyTitle')}>
            <div className="usage-line">
              {t('solvency')}
              <span className="v tabular">{formatUSD(solvency)}</span>
            </div>
          </div>
        )}
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
          onUnfreeze={(email) => run(() => unfreezeAccount(email))}
          onRefreeze={(email) => run(() => refreezeAccount(email))}
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
  onSave: (next: Pick<PlatformSettings, 'start_reserve' | 'overdraft_cap' | 'rates' | 'draft'>) => void
}) {
  const { t } = i18n
  const [reserve, setReserve] = useState('')
  const [floor, setFloor] = useState('')
  // The drafting model (039): a protocol, an endpoint and a model id. No key —
  // that is the deployment's, and this form has nowhere to put one.
  const [provider, setProvider] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [model, setModel] = useState('')
  useEffect(() => {
    if (!settings) return
    setReserve(toDollars(settings.start_reserve))
    setFloor(toDollars(settings.overdraft_cap))
    setProvider(settings.draft?.provider ?? '')
    setBaseURL(settings.draft?.base_url ?? '')
    setModel(settings.draft?.model ?? '')
  }, [settings])

  const nextReserve = parseUSD(reserve)
  const nextFloor = parseUSD(floor)
  const valid = nextReserve !== null && nextReserve >= 0 && nextFloor !== null && nextFloor >= 0
  const draftCfg = { provider: provider.trim(), base_url: baseURL.trim(), model: model.trim() }
  const draftChanged =
    !!settings &&
    (draftCfg.provider !== (settings.draft?.provider ?? '') ||
      draftCfg.base_url !== (settings.draft?.base_url ?? '') ||
      draftCfg.model !== (settings.draft?.model ?? ''))
  const dirty = !!settings && valid && (nextReserve !== settings.start_reserve || nextFloor !== settings.overdraft_cap || draftChanged)
  const save = () => {
    if (settings && valid) onSave({ start_reserve: nextReserve, overdraft_cap: nextFloor, rates: settings.rates, draft: draftCfg })
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
      </div>
      {/* Which model writes a project's description (039). Three fields and no
          key: the credential is the deployment's, read from its environment,
          because a settings row is read by every admin and lands in every
          backup. The model id must also appear in the rate card below, or the
          deployment refuses to draft rather than guessing a price. */}
      <p className="muted small" style={{ lineHeight: 1.6, margin: 0 }}>
        {t('draftModelHint')}
      </p>
      <div className="field-row">
        <label className="admin-label" htmlFor="draft-provider">
          {t('draftProvider')}
        </label>
        <Select
          value={provider}
          options={[
            { value: '', label: t('draftProviderNone') },
            { value: 'anthropic', label: 'Anthropic' },
            { value: 'openai', label: t('draftProviderOpenAI') },
          ]}
          onChange={setProvider}
          label={t('draftProvider')}
          width={260}
        />
      </div>
      {provider !== '' && (
        <>
          <div className="field-row">
            <label className="admin-label" htmlFor="draft-model">
              {t('draftModel')}
            </label>
            <input
              id="draft-model"
              className="text"
              style={{ flex: 1 }}
              value={model}
              placeholder={t('draftModelPlaceholder')}
              onChange={(e) => setModel(e.target.value)}
              data-test="draft-model"
            />
          </div>
          {/* Only another vendor needs an address and a key. Anthropic is the
              one this deployment already runs its sessions on, so choosing it
              is a model id and nothing else. */}
          {provider !== 'anthropic' && (
            <div className="field-row">
              <label className="admin-label" htmlFor="draft-base-url">
                {t('draftBaseURL')}
              </label>
              <input
                id="draft-base-url"
                className="text"
                style={{ flex: 1 }}
                value={baseURL}
                placeholder={t('draftBaseURLPlaceholder')}
                onChange={(e) => setBaseURL(e.target.value)}
                data-test="draft-base-url"
              />
            </div>
          )}
          <p className="muted small" style={{ lineHeight: 1.6, margin: 0 }}>
            {provider === 'anthropic' ? t('draftUsesAgentKey') : t('draftNeedsOwnKey')}
          </p>
        </>
      )}
      {settings && <RateCardTable i18n={i18n} card={settings.rates} />}
      {/* One Save, at the foot of the card it saves. Beside the overdraft floor
          it read as that field's control, and an admin who edited the start
          reserve had no reason to believe the button below it applied — one
          request writes both numbers, and the button has to sit where that is
          what it looks like. */}
      <div className="card-foot">
        {settings?.updated_by && <span className="muted small">{t('policyUpdatedBy', { who: settings.updated_by })}</span>}
        <button className="btn" data-test="save-policy" disabled={!dirty} onClick={save}>
          <Ico name="check" size={13} w={2.2} /> {t('savePolicy')}
        </button>
      </div>
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
  onUnfreeze,
  onRefreeze,
}: {
  i18n: I18n
  people: GrantView[] | null
  onGrant: (email: string, tier: 'user' | 'admin') => void
  onRevoke: (email: string) => void
  onTopup: (email: string, amount: number) => void
  onUnfreeze: (email: string) => void
  onRefreeze: (email: string) => void
}) {
  const { t } = i18n
  const [email, setEmail] = useState('')
  const [tier, setTier] = useState<'user' | 'admin'>('user')
  // Show only the accounts that owe — the debts an operator acts on (022d).
  const [owingOnly, setOwingOnly] = useState(false)
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

  const shown = people && owingOnly ? people.filter((p) => p.wallet < 0) : people

  return (
    <div className="card">
      <CardHead label={t('people')} icon="users" tone="var(--brand)">
        <button className={'btn ghost sm' + (owingOnly ? ' active' : '')} style={{ marginLeft: 'auto' }} aria-pressed={owingOnly} onClick={() => setOwingOnly((v) => !v)}>
          <Ico name="alert" size={12} /> {t('onlyOwing')}
        </button>
      </CardHead>
      {shown === null ? (
        <div className="empty">{t('loading')}</div>
      ) : shown.length === 0 ? (
        <Empty icon="userPlus" text={owingOnly ? t('noOwing') : t('noGrants')} />
      ) : (
        shown.map((p) => (
          <div key={p.email} className="member-row">
            <Initials email={p.email} />
            <span className="member-email">{p.email}</span>
            <TierPill i18n={i18n} tier={p.tier} />
            {!p.bound && (
              <Pill tone="warn" icon="clock">
                {t('pending')}
              </Pill>
            )}
            {p.frozen && (
              <Pill tone="err" icon="alert">
                {t('frozen')}
              </Pill>
            )}
            <Tip text={t('walletOf', { email: p.email })}>
              <span className="muted small mono tabular member-wallet">{formatUSD(p.wallet)}</span>
            </Tip>
            {/* The controls travel together. Loose in the row they were sized
                one by one, so a phone broke them across two lines and each row
                put them somewhere else; as one group they stay on one line and
                end where every other row's do. */}
            <span className="row-actions">
              {p.wallet < 0 &&
                (p.frozen ? (
                  <button className="btn ghost sm" data-tip={t('unfreezeTitle')} onClick={() => onUnfreeze(p.email)}>
                    <Ico name="check" size={12} /> {t('unfreeze')}
                  </button>
                ) : (
                  <button className="btn ghost sm" data-tip={t('refreezeTitle')} onClick={() => onRefreeze(p.email)}>
                    <Ico name="alert" size={12} /> {t('refreeze')}
                  </button>
                ))}
              <button className="btn ghost sm" onClick={() => setTopping(p)}>
                <Ico name="wallet" size={12} /> {t('topup')}
              </button>
              <button className="btn ghost sm danger" onClick={() => onRevoke(p.email)}>
                <Ico name="x" size={12} w={2} /> {t('revoke')}
              </button>
            </span>
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
      // The control belongs to the last word, not to the right-hand edge: inside
      // the heading it follows the text wherever the text ends, which is where a
      // reader's eye already is. Beside it, on a title that wrapped to two lines,
      // it floated off at the end of the row looking like somebody else's button.
      <h1 className="detail-title">
        {title}
        {canEdit && (
          <Tip text={t('rename')}>
            <button className="icon-btn" aria-label={t('rename')} onClick={() => { setDraft(title); setEditing(true) }}>
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
    </div>
  )
}

// NotReadable is what a project nobody here may read looks like. The server
// answers one 404 for private, withdrawn, trashed, and never-existed alike (028),
// so this screen claims none of them: it says the address is not public, and — to
// a reader with no account — offers the sign-in that would settle it, carrying
// this address so they land back on it.
function NotReadable({ i18n, me }: { i18n: I18n; me: Me }) {
  const { t } = i18n
  return (
    <div className="screen" data-test="not-readable">
      <div className="card card-pad stack" style={{ maxWidth: 520, margin: '48px auto', textAlign: 'center', alignItems: 'center' }}>
        <Ico name="globe" size={22} style={{ color: 'var(--fg-3)' }} />
        <h2 style={{ margin: 0 }}>{t('notPublicTitle')}</h2>
        <p className="muted" style={{ margin: 0, lineHeight: 1.6 }}>
          {me.authenticated ? t('notPublicBodyIn') : t('notPublicBody')}
        </p>
        {!me.authenticated && (
          <a className="btn" data-test="not-readable-signin" href={`/login?next=${encodeURIComponent(window.location.pathname)}`}>
            <LatereMark height={11} /> {t('signInTitle')}
          </a>
        )}
        <a className="btn ghost" href="/registry">
          <Ico name="globe" size={13} /> {t('browseRegistry')}
        </a>
      </div>
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

  // A submit spends allowance, so the header pill is re-read after one.
  const onSpent = useCallback(() => {
    refresh()
    onCreditsSpent()
  }, [refresh, onCreditsSpent])

  // What an owner may do here. A project in the trash is read-only (019): the
  // server refuses every write on one, so an owner control still on screen is a
  // button that can only fail. The banner above says as much; this is the half
  // that makes it true.
  const isOwner = project != null && isMine(me, project.owner) && !project.deleted_at
  const [publishing, setPublishing] = useState(false)
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
            refresh()
          }}
          onChanged={refresh}
          onOpenBlocker={openBlocker}
        />
      )}
      <div className="detail-head">
        <div className="detail-title-row">
          <div className="detail-title-main">
            <div className="detail-title-line">
              <EditableTitle i18n={i18n} title={project.title} canEdit={isOwner} onRename={async (next) => { await renameProject(id, next); refresh() }} />
              <VisibilityBadge i18n={i18n} visibility={project.visibility} />
            </div>
            <ProjectDescription
              i18n={i18n}
              project={project}
              canEdit={isOwner && !project.deleted_at}
              onSaved={refresh}
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
            <button
              className="btn ghost small"
              data-test="restore"
              onClick={async () => {
                await restoreProject(id)
                refresh()
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
              onRefresh={refresh}
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
              <PublishPanel i18n={i18n} projectID={id} onPublished={refresh} onOpenBlocker={openBlocker} />
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

// manifestTone maps the *manifest's* grade to a semantic colour, and verdictIcon
// to the matching glyph — a passed reproduction and a failed one should not need
// the word read to be told apart. The findings' own verdict has its own tone in
// `findings.ts`: they are different vocabularies (passed/failed against
// reproducible/partial/not_reproducible) and folding them would make one of the
// two lie.
function manifestTone(v: string) {
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
  // The findings are fetched beside the manifest and never gate it: a session
  // can have a verdict with no findings (it predates the file, or its file was
  // refused), and that pane still has something true to show. `why` keeps the
  // two absences apart, because "the run wrote nothing" and "the system refused
  // what it wrote" are different facts about the run.
  const [findings, setFindings] = useState<Findings | null>(null)
  const [why, setWhy] = useState<'none' | 'invalid' | null>(null)
  useEffect(() => {
    setState('loading')
    getReproduction(jobId)
      .then((r) => {
        setRepro(r)
        setState('ready')
      })
      .catch(() => setState('none'))
  }, [jobId])
  useEffect(() => {
    setFindings(null)
    setWhy(null)
    getFindings(jobId)
      .then(setFindings)
      .catch((e) => setWhy(e?.status === 422 ? 'invalid' : 'none'))
  }, [jobId])

  if (state === 'none')
    return (
      <div className="card">
        <Empty icon="gauge" text={t('noVerdict')} />
      </div>
    )
  if (state === 'loading' || !repro) return <div className="center-load">…</div>
  const tone = manifestTone(repro.verdict)
  // The run's record, which is not the paper's verdict and never was.
  //
  // `repro.verdict` is the grader's judgement of the *run* against the method's
  // rubric — passed, needs_revision, exhausted, failed. The findings' verdict is
  // about the *paper* — reproducible, partial, not_reproducible. Both used to be
  // headed "Verdict", which put two verdict words on one screen for one paper
  // and invited the reader to take the grader's "Passed" as a statement that the
  // paper reproduced. It is not: a well-executed audit of a paper that does not
  // reproduce passes the rubric.
  //
  // So the paper's verdict is the only headline, and this becomes what it is —
  // the record of the run that produced it, with the rubric grade as one row.
  const manifest = (
    <div className="card card-pad stack" style={{ maxWidth: 720 }} data-test="run-record">
      <span className="eyebrow eyebrow-ico">
        <Ico name="shieldCheck" size={12} /> {t('runRecord')}
      </span>
      <div className="usage-rows">
        <div className="usage-line">
          <Ico name={verdictIcon(repro.verdict)} size={13} style={{ color: tone }} />
          {t('methodCheck')}
          <span className="v" style={{ color: tone }}>
            {verdictLabel(repro.verdict) || '—'}
          </span>
        </div>
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
        {/* This row is the reason a project cannot go public, and the reader
            following a refusal here arrives at a pill with one word in it. The
            sentence says what the word means and that this is the thing in the
            way — permanently, not as a flash on arrival, because it is still
            true the next time they come. */}
        {repro.review && repro.review !== 'clear' && (
          <div className="banner warn inline" data-test="blocks-publishing">
            <Ico name="alert" size={14} />
            <span>
              <strong>{t('blocksPublishing')}</strong> —{' '}
              {t(repro.review === 'secret_in_transcript' ? 'blockerSecret' : 'blockerNotClear')}
            </span>
          </div>
        )}
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

  // Findings first when there are any: the per-component table is the result,
  // and the manifest card below it is the system's record about the run rather
  // than about the paper. Without findings the pane says which absence it is
  // instead of rendering an empty table, which would read as a paper that had
  // no boundary.
  return (
    <div className="stack" style={{ gap: 16 }}>
      {findings ? (
        <SessionFindings i18n={i18n} findings={findings} />
      ) : (
        why && (
          <div className="card card-pad small muted" style={{ maxWidth: 720 }}>
            {t(why === 'invalid' ? 'findingsInvalid' : 'noFindings')}
          </div>
        )
      )}
      {manifest}
    </div>
  )
}

// SessionFindings renders one run's verdict.json (021b): the summary first, then
// the per-component table the rubric calls the result, then what did not
// reproduce, then the decisions the paper left unwritten.
//
// The rate is never rendered alone. It appears beside its decomposition
// everywhere, because a rate depends on how the paper was sliced and the rubric
// forbids reading it as a decomposition-independent measurement.
function SessionFindings({ i18n, findings }: { i18n: I18n; findings: Findings }) {
  const { t } = i18n
  const f = findings
  const open = unresolved(f)
  const r = rate(f.components)
  const outcomeLabel = (o: ClaimOutcome) =>
    o === 'verified' ? t('outVerified') : o === 'partial' ? t('outPartial') : o === 'blocked' ? t('outBlocked') : t('outNotScored')

  return (
    <div className="stack" style={{ gap: 16 }}>
      <div className="card card-pad findings-head">
        <div className="findings-verdict">
          <span className="eyebrow">{t('verdict')}</span>
          <div className="findings-word" style={{ color: verdictTone(f.verdict) }}>
            {verdictLabel(f.verdict)}
          </div>
          <div className="mono small muted">
            {t('graded')} {new Date(f.graded_at).toLocaleDateString()}
          </div>
        </div>
        <div className="stack" style={{ gap: 12, minWidth: 0 }}>
          <p className="findings-summary">{f.summary}</p>
          {/* The decomposition and the rate, one line, never apart. */}
          <div className="findings-decomp">
            <span className="eyebrow">{t('innerLoopRate')}</span>
            {r !== null && <span className="findings-rate">{r.toFixed(2)}</span>}
            <Decomposition c={f.components} />
            <span className="small muted">
              {t('componentsOf', {
                verified: String(f.components.verified),
                partial: String(f.components.partial),
                blocked: String(f.components.blocked),
                total: String(f.components.total),
              })}
            </span>
          </div>
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <span className="eyebrow">{t('boundary')}</span>
        </div>
        {/* Four columns read across, so on a phone the box scrolls sideways
            rather than the table restacking into something that is no longer a
            comparison (030). */}
        <div className="grid-table-wrap">
        <table className="grid-table">
          <thead>
            <tr>
              <th>{t('claim')}</th>
              <th style={{ width: 92 }}>{t('loop')}</th>
              <th style={{ width: 118 }}>{t('outcome')}</th>
              <th style={{ width: '34%' }}>{t('evidenceOrBlocker')}</th>
            </tr>
          </thead>
          <tbody>
            {f.boundary.map((c, i) => (
              // An outer row is muted as well as labelled: the one rule this
              // product defends has to be visible without reading the column.
              <tr key={i} className={c.loop === 'outer' ? 'row-outer' : undefined}>
                <td>
                  <div className="claim-text">{c.claim}</div>
                  <div className="mono small muted">{c.citation}</div>
                </td>
                <td>
                  <span className="loop-pill">{c.loop === 'inner' ? t('loopInner') : t('loopOuter')}</span>
                </td>
                <td className="mono small" style={{ color: outcomeTone(c.outcome), letterSpacing: '0.08em', fontWeight: 600 }}>
                  {outcomeLabel(c.outcome)}
                </td>
                <td className="small muted">
                  {c.evidence}
                  {c.mismatch && <MismatchLine i18n={i18n} m={c.mismatch} />}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        </div>
      </div>

      <div className="card">
        <div className="card-head">
          <span className="eyebrow">{t('unresolved')}</span>
          <span className="small muted" style={{ marginLeft: 'auto' }}>{t('unresolvedHint')}</span>
        </div>
        {open.length === 0 ? (
          <div className="card-pad small muted">{t('unresolvedNone')}</div>
        ) : (
          <div className="stack" style={{ gap: 0 }}>
            {open.map((c, i) => (
              <div key={i} className="unresolved-row">
                <span className="mono small" style={{ color: outcomeTone(c.outcome), fontWeight: 600, letterSpacing: '0.08em', width: 84, flex: 'none' }}>
                  {outcomeLabel(c.outcome)}
                </span>
                <div className="stack" style={{ gap: 3, minWidth: 0 }}>
                  <span className="claim-text">{c.claim}</span>
                  <span className="small muted">{c.evidence}</span>
                  {c.mismatch && <MismatchLine i18n={i18n} m={c.mismatch} />}
                </div>
                <span className="mono small muted" style={{ marginLeft: 'auto' }}>{c.citation}</span>
              </div>
            ))}
          </div>
        )}
      </div>

      {!!f.decisions?.length && (
        <div className="card card-pad stack" style={{ gap: 14 }}>
          <span className="eyebrow">{t('decisions')}</span>
          {f.decisions.map((d, i) => (
            <div key={i} className="decision">
              <div className="claim-text">{d.question}</div>
              <div className="small muted">
                {t('decisionWhere')}: {d.where}
              </div>
              <div className="small">
                {t('decisionAssumed')}: {d.assumed}
              </div>
              <div className="small muted">
                {t('decisionSensitivity')}: {d.sensitivity}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// MismatchLine prints both numbers. An outcome alone cannot say that a run
// reproduced a claim and still found the paper's figure wrong.
function MismatchLine({ i18n, m }: { i18n: I18n; m: Mismatch }) {
  const { t } = i18n
  const kind =
    m.kind === 'numeric' ? t('mismatchNumeric')
      : m.kind === 'ordering' ? t('mismatchOrdering')
        : m.kind === 'contradiction' ? t('mismatchContradiction')
          : t('mismatchTypo')
  return (
    <div className="mismatch">
      <span className="mismatch-kind">{kind}</span>
      <span className="mono small">
        {t('reportedIs')} <b>{m.reported}</b> · {t('observedIs')} <b>{m.observed}</b>
        {m.delta ? ` · ${m.delta}` : ''}
      </span>
    </div>
  )
}

// Decomposition is the stacked bar behind the rate — verified, partial, blocked.
// A blocked share is drawn as an empty track rather than a colour, so the eye
// reads it as work not done rather than as a fourth result.
function Decomposition({ c }: { c: Components }) {
  if (!c || c.total <= 0) return null
  const pct = (n: number) => `${(n / c.total) * 100}%`
  return (
    <span className="decomp-bar" aria-hidden="true">
      <span style={{ width: pct(c.verified), background: 'var(--ok)' }} />
      <span style={{ width: pct(c.partial), background: 'var(--warn)' }} />
    </span>
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
  // What the authors released comes from the same findings file the verdict pane
  // reads (021c). A run without it renders the browser alone rather than an
  // empty "nothing released" table, which would be a claim the run never made.
  const [findings, setFindings] = useState<Findings | null>(null)

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
  useEffect(() => {
    setFindings(null)
    getFindings(jobId).then(setFindings).catch(() => setFindings(null))
  }, [jobId])

  if (files === null) return <div className="center-load">…</div>
  if (files.length === 0 && !findings)
    return (
      <div className="card">
        <Empty icon="package" text={t('noArtifacts')} />
      </div>
    )

  const browser = (
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

  // Authors first, then the reproduction. The order is the argument: the
  // reproduction exists because of what was and was not released, and reading
  // the repository first inverts that.
  return (
    <div className="stack" style={{ gap: 16 }}>
      {findings && <ReleasedByAuthors i18n={i18n} findings={findings} />}
      {files.length > 0 && browser}
    </div>
  )
}

// ReleasedByAuthors is what the run found when it went looking for the paper's
// own artifacts, and the five-criterion scorecard beside it (021c).
function ReleasedByAuthors({ i18n, findings }: { i18n: I18n; findings: Findings }) {
  const { t } = i18n
  const released = findings.released ?? []
  const scores = findings.scorecard ?? []
  if (!released.length && !scores.length) return null
  const label = (r: ReleaseResult) =>
    r === 'found' ? t('resFound') : r === 'partial' ? t('resPartial') : r === 'dead' ? t('resDead') : t('resNone')
  const critLabel: Record<Criterion, string> = {
    code: t('critCode'),
    data: t('critData'),
    license: t('critLicense'),
    preregistration: t('critPreregistration'),
    supplement: t('critSupplement'),
  }

  return (
    <div className="detail-grid" style={{ gridTemplateColumns: '1fr 300px', alignItems: 'start' }}>
      {!!released.length && (
        <div className="card">
          <div className="card-head">
            <span className="eyebrow">{t('releasedTitle')}</span>
            <span className="small muted" style={{ marginLeft: 'auto' }}>{t('releasedHint')}</span>
          </div>
          {released.map((r, i) => (
            <div key={i} className="release-row">
              <span className="release-dot" style={{ background: releaseTone(r.result) }} />
              <span className="release-where">{r.where}</span>
              <span className="small" style={{ flex: 1, minWidth: 0 }}>
                {r.url ? (
                  <a className="link" href={r.url} target="_blank" rel="noreferrer">
                    {r.what || r.url}
                  </a>
                ) : (
                  r.what
                )}
              </span>
              <span className="mono small" style={{ color: releaseTone(r.result), fontWeight: 600, letterSpacing: '0.08em' }}>
                {label(r.result)}
              </span>
            </div>
          ))}
        </div>
      )}
      {!!scores.length && (
        <div className="card card-pad stack" style={{ gap: 10 }}>
          <span className="eyebrow">{t('scorecard')}</span>
          {CRITERIA.map((c) => {
            const sc = scoreOf(findings, c)
            if (!sc) return null
            return (
              <div key={c} className="stack" style={{ gap: 2 }}>
                <div className="score-row">
                  <span className="score-mark" style={{ color: sc.found ? 'var(--ok)' : 'var(--err)' }}>{sc.found ? '✓' : '✕'}</span>
                  <span style={{ color: 'var(--fg-2)' }}>{critLabel[c]}</span>
                </div>
                {sc.found && sc.url && (
                  <a className="link mono small" style={{ paddingLeft: 21 }} href={sc.url} target="_blank" rel="noreferrer">
                    {sc.url}
                  </a>
                )}
                {/* Not found is only a finding when it says where it looked. */}
                {!sc.found && !!sc.searched?.length && (
                  <span className="small muted" style={{ paddingLeft: 21 }}>
                    {t('searchedIn')}: {sc.searched.join(', ')}
                  </span>
                )}
              </div>
            )
          })}
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

// ledgerLabel names one statement row (024b). An allocate or reclaim that
// carries a project snapshot names it — a link while the project still exists, a
// plain name once it is purged. Every other kind, and a pre-024b move that never
// captured a project, falls back to the neutral kind label so an old statement
// still reads.
export type LedgerLabel = { kind: 'plain'; text: string } | { kind: 'project'; prefix: string; name: string; href?: string }

export function ledgerLabel(t: I18n['t'], e: LedgerEntry): LedgerLabel {
  if ((e.kind === 'allocate' || e.kind === 'reclaim') && e.project_name) {
    return {
      kind: 'project',
      prefix: e.kind === 'allocate' ? t('ledgerAllocateTo') : t('ledgerReclaimFrom'),
      name: e.project_name,
      href: e.project_alive && e.project_id ? `/p/${e.project_id}` : undefined,
    }
  }
  // A card payment names its source, not "an admin": the actor distinguishes a
  // purchase (stripe:…) from an admin grant, and a refund (stripe:refund:…) from
  // a manual correction (022b/022c).
  const byCard = e.actor.startsWith('stripe:')
  if (e.kind === 'topup') return { kind: 'plain', text: byCard ? t('ledgerCardTopup') : t('ledgerTopup') }
  if (e.kind === 'adjust' && byCard) return { kind: 'plain', text: t('ledgerRefund') }
  return { kind: 'plain', text: ledgerKindLabel(t, e.kind) }
}

// WalletPill shows what this person has to spend, next to the button that
// starts spending it.
//
// A brand-new account and one that has spent down read differently. Most
// people's first sight of this pill is at zero, and it has to say they have
// none *yet* and how to get some, rather than implying they lost something.
function WalletPill({ i18n, credits, onTopUp }: { i18n: I18n; credits?: Credits; onTopUp?: () => void }) {
  const { t } = i18n
  if (!credits) return null
  const empty = credits.wallet <= 0
  // Not enough left to open one more session is a warning; nothing at all is an
  // error; the rest is just a number.
  const state = empty ? ' out' : credits.wallet < credits.start_reserve ? ' low' : ''
  const inner = (
    <>
      <Ico name={empty ? 'alert' : 'wallet'} size={12} />
      {empty ? t('walletEmpty') : formatUSD(credits.wallet)}
    </>
  )
  return (
    <Tip text={onTopUp ? t('topUp') : empty ? t('walletEmptyTitle') : t('walletTitle', { reserve: formatUSD(credits.start_reserve) })}>
      {onTopUp ? (
        // The balance is the way to buy more: click it to top up. A button rather
        // than a span, with the pill's own look kept (only the font a button does
        // not inherit needs restoring).
        <button type="button" className={'quota-pill' + state} style={{ font: 'inherit', cursor: 'pointer' }} onClick={onTopUp} aria-label={t('topUp')}>
          {inner}
        </button>
      ) : (
        <span className={'quota-pill' + state}>{inner}</span>
      )}
    </Tip>
  )
}

// pctText renders basis points as a percentage a person reads: 500 → "5",
// 290 → "2.9". No trailing ".0", because a whole percent should not wear cents.
function pctText(bps: number): string {
  return String(Math.round((bps / 100) * 10) / 10)
}

// TopUpModal buys credits. It shows exactly where the money goes — what you pay,
// the fee, and the credits you get — before sending you to the processor. The webhook, not the return from that
// page, is what actually credits (022b); this only opens the payment, and the
// wallet screen re-reads the balance when they come back.
// The preset top-up amounts, in whole dollars. A person picks one or types their
// own — a bare stepper made buying feel like filling a form, not a purchase.
const TOPUP_PRESETS = [10, 25, 50, 100]

function TopUpModal({ i18n, onClose }: { i18n: I18n; onClose: () => void }) {
  const { t } = i18n
  const [wallet, setWallet] = useState<Wallet | null>(null)
  const [choice, setChoice] = useState<number | 'other'>(25 * MICRO)
  const [custom, setCustom] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    getWallet()
      .then(setWallet)
      .catch(() => setErr(t('errOffline')))
  }, [t])

  const min = wallet?.min_topup_micro ?? 0
  const raw = choice === 'other' ? parseUSD(custom) : choice
  // The amount actually charged, snapped to whole cents — the figure the fee and
  // the credit are both computed from, so the three add up on screen.
  const gross = raw !== null ? Math.floor(raw / 10_000) * 10_000 : 0
  const belowMin = choice === 'other' && raw !== null && raw > 0 && gross < min
  const valid = gross > 0 && gross >= min
  const credited = valid && wallet ? creditedMicro(gross, wallet.spread_bps, wallet.spread_fixed_micro) : null

  const go = async () => {
    if (!valid || busy) return
    setBusy(true)
    setErr('')
    try {
      const { url } = await startCheckout(gross)
      window.location.href = url
    } catch (e) {
      setErr(checkoutErrLabel(t, e))
      setBusy(false)
    }
  }

  return (
    <Modal
      title={t('topUpTitle')}
      icon="wallet"
      onClose={onClose}
      foot={
        <>
          <button className="btn ghost" onClick={onClose}>
            {t('cancel')}
          </button>
          <button className="btn" disabled={!valid || busy} onClick={go}>
            <Ico name="lock" size={13} /> {busy ? t('topUpRedirecting') : valid ? t('topUpPayCta', { total: formatUSD(gross) }) : t('topUpContinue')}
          </button>
        </>
      }
    >
      <p className="muted small" style={{ margin: '0 0 12px', lineHeight: 1.6 }}>
        {t('topUpChoose')}
      </p>
      <div className="topup-presets">
        {TOPUP_PRESETS.map((d) => {
          const p = d * MICRO
          return (
            <button key={d} type="button" className={'topup-preset' + (choice === p ? ' on' : '')} aria-pressed={choice === p} onClick={() => setChoice(p)}>
              {formatUSD(p)}
            </button>
          )
        })}
        <button type="button" className={'topup-preset' + (choice === 'other' ? ' on' : '')} aria-pressed={choice === 'other'} onClick={() => setChoice('other')}>
          {t('topUpOther')}
        </button>
      </div>
      {choice === 'other' && (
        <div className="field-row" style={{ marginTop: 10 }}>
          <NumberField value={custom} onChange={setCustom} min={0} ariaLabel={t('topUpAmount')} autoFocus />
          <span className="muted small">{t('topUpMinHint', { min: formatUSD(min) })}</span>
        </div>
      )}
      {credited !== null && wallet && (
        <div className="usage-rows" style={{ marginTop: 14 }} data-test="topup-quote">
          <div className="usage-line">
            {t('topUpPay')}
            <span className="v tabular">{formatUSD(gross)}</span>
          </div>
          <div className="usage-line" data-tip={t('topUpFeeWhy')}>
            {t('topUpFee', { pct: pctText(wallet.spread_bps), fixed: formatUSD(wallet.spread_fixed_micro) })}
            <span className="v tabular">−{formatUSD(gross - credited)}</span>
          </div>
          <div className="usage-line" style={{ fontWeight: 600 }}>
            {t('topUpGet')}
            <span className="v tabular">{formatUSD(credited)}</span>
          </div>
        </div>
      )}
      {belowMin && (
        <span className="err small" style={{ marginTop: 8, display: 'inline-block' }}>
          {t('topUpMin', { min: formatUSD(min) })}
        </span>
      )}
      {err && (
        <span className="err small">
          <Ico name="alert" size={13} /> {err}
        </span>
      )}
      <p className="muted small" style={{ margin: '14px 0 0', lineHeight: 1.6 }}>
        {t('topUpSecure')}
      </p>
    </Modal>
  )
}

// WalletScreen is the person's own money: the statement, a way to buy more, and
// the landing the processor returns to. On a return marked ?checkout=success the
// balance may lag the webhook, so it re-reads /me a few times over a few seconds
// and the bought credits appear on their own (022e).
function WalletScreen({ i18n, me, refreshMe, onTopUp }: { i18n: I18n; me: Me; refreshMe: () => void; onTopUp: () => void }) {
  const { t } = i18n
  const returned = new URLSearchParams(window.location.search).get('checkout') === 'success'
  const [polling, setPolling] = useState(returned)

  useEffect(() => {
    if (!returned) return
    let n = 0
    const id = setInterval(() => {
      refreshMe()
      if (++n >= 5) {
        clearInterval(id)
        setPolling(false)
      }
    }, 1500)
    return () => clearInterval(id)
  }, [returned, refreshMe])

  const credits = me.credits
  const balance = credits ? credits.wallet : 0
  return (
    <div className="screen">
      <div className="screen-head">
        <h1 className="screen-title">{t('walletHeading')}</h1>
        <p className="screen-desc">{t('walletDesc')}</p>
      </div>
      <div className="stack" style={{ maxWidth: 560 }}>
        <div className="card card-pad">
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 16 }}>
            <div>
              <div className="eyebrow eyebrow-ico">
                <Ico name="wallet" size={12} /> {t('creditsBalance')}
              </div>
              <div className="tabular" style={{ fontSize: 34, fontWeight: 600, lineHeight: 1.15, marginTop: 6 }}>{formatUSD(balance)}</div>
            </div>
            <button className="btn lg" onClick={onTopUp}>
              <Ico name="wallet" size={15} w={2} /> {t('topUp')}
            </button>
          </div>
          {polling && (
            <p className="muted small" style={{ margin: '12px 0 0' }}>
              {t('topUpProcessing')}
            </p>
          )}
        </div>
        {credits && <WalletActivity i18n={i18n} balance={balance} />}
      </div>
    </div>
  )
}

// WalletActivity is the wallet's history, shown rather than hidden behind a
// toggle: the one place a person reads their own balance is where they read
// where it came from. It stays light no matter how long the history grows —
// bounded to a scroll region, and to the server's most recent entries.
function WalletActivity({ i18n, balance }: { i18n: I18n; balance: number }) {
  const { t } = i18n
  const [entries, setEntries] = useState<LedgerEntry[] | null>(null)
  // Re-read when the balance changes: a list still showing the pre-move entries
  // would contradict the total it is supposed to explain.
  useEffect(() => {
    let live = true
    getWallet()
      .then((w) => live && setEntries(w.entries))
      .catch(() => live && setEntries([]))
    return () => {
      live = false
    }
  }, [balance])

  return (
    <div className="card card-pad stack">
      <span className="eyebrow eyebrow-ico">
        <Ico name="clock" size={12} /> {t('walletActivity')}
      </span>
      {entries === null ? (
        <div className="muted small">{t('loading')}</div>
      ) : entries.length === 0 ? (
        <div className="muted small">{t('statementEmpty')}</div>
      ) : (
        <>
          <div className="usage-rows" style={{ maxHeight: 380, overflowY: 'auto' }}>
            {entries.map((e) => {
              const label = ledgerLabel(t, e)
              return (
                <div className="usage-line" key={e.id}>
                  <span className="muted small">
                    {label.kind === 'plain' ? (
                      label.text
                    ) : (
                      <>
                        {label.prefix}{' '}
                        {label.href ? (
                          <a className="link" href={label.href}>
                            {label.name}
                          </a>
                        ) : (
                          <span style={{ color: 'var(--fg-2)' }}>{label.name}</span>
                        )}
                      </>
                    )}
                  </span>
                  <span className="v">
                    <span className="muted small">{new Date(e.created_at).toLocaleDateString()}</span>
                    <span className={e.amount < 0 ? 'err' : ''}>{formatUSD(e.amount)}</span>
                  </span>
                </div>
              )
            })}
          </div>
          {entries.length >= 200 && (
            <p className="muted small" style={{ margin: 0 }}>
              {t('walletActivityCapped')}
            </p>
          )}
        </>
      )}
    </div>
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
function ProjectCreditsCard({ i18n, projectID, credits, onMoved, onTopUp }: { i18n: I18n; projectID: string; credits: ProjectCredits; onMoved: () => void; onTopUp: () => void }) {
  const { t } = i18n
  const [amount, setAmount] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  // Whether the last refusal was a short *wallet* on an allocate — the one an
  // in-place top-up fixes. A reclaim held by a running session is not.
  const [offerTopUp, setOfferTopUp] = useState(false)

  const parsed = parseUSD(amount)
  const valid = parsed !== null && parsed > 0
  const short = credits.available < credits.start_reserve
  // What a running session is holding: the gap between the pot's balance and what
  // is free to commit. Shown only when it exists, so two identical numbers do not
  // read as a duplicate when nothing is running.
  const held = credits.balance - credits.available

  const move = async (back: boolean) => {
    if (!valid || busy) return
    setBusy(true)
    setErr('')
    setOfferTopUp(false)
    try {
      await (back ? reclaimCredits(projectID, parsed) : allocateCredits(projectID, parsed))
      setAmount('')
      onMoved()
    } catch (e) {
      setErr(creditMoveErrLabel(t, e, back))
      setOfferTopUp(!back && errCode(e) === 'no_credits')
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
        {/* The available row appears only when a running session is holding some
            of the balance — otherwise it equals the balance and reads as a
            confusing duplicate. data-tip rather than a <Tip> wrapper: an inline
            wrapper around a block row collapses it to its content. */}
        {held > 0 && (
          <div className="usage-line" data-tip={t('creditsAvailableTitle')}>
            {t('creditsAvailable')}
            <span className="v tabular">{formatUSD(credits.available)}</span>
          </div>
        )}
      </div>
      <p className="muted small" style={{ lineHeight: 1.6, margin: 0 }}>
        {short ? t('creditsShort', { reserve: formatUSD(credits.start_reserve) }) : t('creditsEnough', { reserve: formatUSD(credits.start_reserve) })}
      </p>
      {/* The amount on its own line, then the two actions side by side, so a
          narrow card does not wrap "Take back" onto a lonely second row. */}
      <div className="stack" style={{ gap: 8 }}>
        <NumberField value={amount} onChange={setAmount} min={0} ariaLabel={t('creditsAmount')} />
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="btn" style={{ flex: 1 }} disabled={!valid || busy} onClick={() => move(false)}>
            <Ico name="plus" size={13} w={2.2} /> {t('creditsAllocate')}
          </button>
          {credits.may_reclaim && (
            <button className="btn ghost" style={{ flex: 1 }} disabled={!valid || busy} onClick={() => move(true)}>
              <Ico name="minus" size={13} w={2.2} /> {t('creditsReclaim')}
            </button>
          )}
        </div>
      </div>
      {err && (
        <div className="stack" style={{ gap: 8 }}>
          <span className="err small">
            <Ico name="alert" size={13} /> {err}
          </span>
          {offerTopUp && (
            <button className="btn ghost sm" style={{ alignSelf: 'flex-start' }} onClick={onTopUp}>
              <Ico name="wallet" size={13} /> {t('topUpWallet')}
            </button>
          )}
        </div>
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
  // The model tier the next session runs on (031). It is deliberately not
  // persisted: a remembered choice would quietly become the project's policy,
  // and a per-project default is a decision with a different owner.
  const [tier, setTier] = useState('')
  const [tiers, setTiers] = useState<ModelTier[]>([])
  // The session waiting on a delete confirmation (032). One dialog for the card
  // rather than one per row, the way the projects list holds its trash dialog.
  const [dropping, setDropping] = useState<Job | null>(null)
  // The paper waiting on a move (035). One dialog for the card, as above.
  const [moving, setMoving] = useState<ProjectPaper | null>(null)
  // The paper being corrected (037), and whether a fill-in is running.
  const [editing, setEditing] = useState<ProjectPaper | null>(null)
  const [filling, setFilling] = useState(false)
  const [filled, setFilled] = useState<number | null>(null)
  const refresh = onRefresh
  // Removing a session is the owner's alone — 019's rule, at session scale. The
  // trashed clause matches the server, which refuses every write on a project in
  // the trash.
  const mayDelete = isMine(me, project.owner) && !project.deleted_at

  // The tiers and their prices come from the server. Read once per mount, and
  // only where somebody can actually start a run — a reader of a public project
  // is offered nothing to choose.
  useEffect(() => {
    if (!mayWork) return
    let live = true
    listModels()
      .then((r) => live && setTiers(r.tiers))
      // A deployment that cannot list them leaves the control out rather than
      // showing an error: the Start button still works, on the default tier.
      .catch(() => {})
    return () => {
      live = false
    }
  }, [mayWork])

  // A tier reads as its name and its price. The price rides in the label rather
  // than in a new Select option field, so the shared control is reused as it is.
  const tierOptions = tiers.map((m) => {
    const name = m.tier === 'best' ? t('tierBest') : m.tier === 'balanced' ? t('tierBalanced') : t('tierFast')
    const priced = m.input && m.output ? ` — ${t('tierPrice', { in: formatUSD(m.input), out: formatUSD(m.output) })}` : ''
    return { value: m.tier, label: name + priced }
  })

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
      await startSession(project.id, key, tier || undefined)
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
      <CardHead label={t('papers')} icon="fileText" tone="var(--accent)">
        {/* The fill-in, for the papers recorded before a paper had a
            bibliography (037). Offered only when there is something to fill:
            a button that always says "nothing to do" is furniture. */}
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
            {filled > 0 ? t('fillInDone', { n: String(filled) }) : t('fillInNothing')}
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
        {tierOptions.length > 0 && (
          <div className="tier-pick" style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <span className="muted small">{t('modelTier')}</span>
            <Select
              value={tier || 'best'}
              options={tierOptions}
              onChange={setTier}
              label={t('modelTier')}
              width={280}
            />
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
                    rule (012), not a second answer to the same question. */}
                  <span className="muted small paper-state">
                    {g.sessions.length > 0 ? t('paperSessions', { n: String(g.sessions.length) }) : t('paperNeverRun')}
                  </span>
                {/* Moving is offered whether or not the paper has been run —
                    which is the point: a paper filed under the wrong project is
                    usually noticed after a replication, and that is exactly when
                    removing it stops being allowed. */}
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
// for. A title alone does not count — a title is what a paper is called, and the
// record is what it is.
function needsDetails(p: ProjectPaper): boolean {
  const r = p.record
  return !r || !r.authors?.length || !r.venue || !r.year
}

// Citation renders a paper's bibliography the way a reference list would: the
// authors, then where and when it appeared. It is one quiet line under the
// title, because it is what you scan a project by rather than what you click.
//
// Three authors, then "et al." — a fifteen-author paper would otherwise be the
// whole row, and the full list is in the correction dialog for anyone who needs
// it. A paper whose record nobody could fill in says so, which is also the
// invitation to fill it in.
function Citation({ i18n, paper }: { i18n: I18n; paper: ProjectPaper }) {
  const { t } = i18n
  const r = paper.record
  const bits: string[] = []
  if (r?.authors?.length) {
    bits.push(r.authors.length > 3 ? `${r.authors.slice(0, 3).join(', ')} ${t('etAl')}` : r.authors.join(', '))
  }
  if (r?.venue) bits.push(r.venue)
  if (r?.year) bits.push(String(r.year))
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

// Abstract is what the paper is about, in its own source's words (037).
//
// Three lines, then "more". Long enough to tell two papers apart, short enough
// that a project holding six of them still shows its sessions without a scroll —
// and expanding is per paper and per visit, because a preference about somebody
// else's abstract is not worth storing.
function Abstract({ i18n, text }: { i18n: I18n; text?: string }) {
  const { t } = i18n
  const [open, setOpen] = useState(false)
  if (!text) return null
  // The clamp is CSS, so "is it actually longer than three lines" is a layout
  // question; the control is offered from a length that cannot fit in three
  // lines at this measure rather than from a measurement that would force a
  // reflow to answer.
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
// it — and a 60-character URL was taking a third of the row.
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

// EditPaperDialog corrects a paper's record (037). What it writes is permanent:
// the server marks every corrected field, so the next lookup fills only what is
// still empty and never puts a source's wrong answer back.
//
// Authors are one field, one per line. A list of people is what somebody pastes
// from a reference manager, and a row-per-author editor would make correcting a
// twelve-author paper a chore for no gain in precision.
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
    <div className="row file-row" onClick={onOpen}>
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
    </div>
  )
}

// decompositionOf is the counts the verdict word summarises, so a row never
// carries a judgement without the slicing it came from.
function decompositionOf(f: Findings): string {
  const c = f.components
  return c?.total ? `${c.verified}/${c.total}` : ''
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
  canDelete,
  onOpen,
  onChanged,
  onDelete,
}: {
  i18n: I18n
  job: Job
  // Indented under the paper it is a run of, rather than standing on its own.
  nested?: boolean
  // Whether this reader may remove a finished session (032). The owner alone —
  // the server refuses everybody else, and a button that always fails is worse
  // than no button at all.
  canDelete?: boolean
  onOpen: () => void
  onChanged: () => void
  // Where a delete is confirmed. The dialog is the card's, not the row's: it is
  // one dialog for the list rather than one per row, and — because Modal portals
  // to <body> but a React portal still bubbles its clicks through the React
  // tree — a dialog inside this row would open the session on every click in it.
  onDelete?: (j: Job) => void
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
  // The clock the elapsed reading is drawn against. A running row re-renders on
  // its own once a second; a settled one holds no interval, so a list of
  // finished sessions costs nothing to leave open.
  const [tick, setTick] = useState(() => Date.now())
  const running = !terminal(j.phase)
  useEffect(() => {
    if (!running) return
    const h = setInterval(() => setTick(Date.now()), 1000)
    return () => clearInterval(h)
  }, [running])

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
      {/* How long it has been going, for a row that is still going (021d). A
          run costs money for as long as it runs, so the minutes are the thing
          the reader is actually watching — an age ("an hour ago") answers a
          different question and rounds the minutes away. */}
      {!terminal(j.phase) && (
        <Tip text={t('runningForTitle')}>
          <span className="muted small tabular">{elapsed(j.created_at, tick)}</span>
        </Tip>
      )}
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
      {/* Removal, on a session that has finished (032). The condition is the
          exact negation of Stop's, so a row offers one removal verb and never
          two — and never the one the server is about to refuse. */}
      {canDelete && onDelete && terminal(j.phase) && (
        <Tip text={t('deleteSession')}>
          <button
            className="icon-btn danger"
            aria-label={t('deleteSession')}
            data-test="delete-session"
            onClick={(e) => {
              e.stopPropagation()
              onDelete(j)
            }}
          >
            <Ico name="trash" size={13} />
          </button>
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

// The confirmation before a session leaves the project (032). It is the app's
// own dialog, not window.confirm, for the reasons TrashDialog gives.
//
// What it has to say is what makes this different from the trash: the removal is
// not reversible, because the way back is to run the paper again — a session is
// one run, not a container of work. So the two things that survive it are worth
// stating plainly: what it cost stays on the ledger, and the repository the
// agent wrote stays on GitHub, closed rather than destroyed.
function DeleteSessionDialog({ i18n, job, onClose, onDone }: { i18n: I18n; job: Job; onClose: () => void; onDone: () => void }) {
  const { t } = i18n
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const go = async () => {
    setBusy(true)
    setErr('')
    try {
      await deleteSession(job.id)
      onDone()
    } catch (e) {
      setErr(deleteSessionErrLabel(t, e))
      setBusy(false)
    }
  }
  return (
    <Modal
      title={t('deleteSessionTitle')}
      icon="trash"
      eyebrow={job.ref}
      onClose={onClose}
      foot={
        <>
          <button className="btn ghost" onClick={onClose} disabled={busy}>
            {t('cancel')}
          </button>
          <button className="btn danger" onClick={go} disabled={busy} data-test="delete-session-confirm">
            {busy ? t('deletingSession') : t('deleteSessionConfirm')}
          </button>
        </>
      }
    >
      <p className="muted">{t('deleteSessionBody')}</p>
      {job.repo_name && <p className="muted small">{t('deleteSessionKeepsRepo')}</p>}
      {err && <p className="err small">{err}</p>}
    </Modal>
  )
}

// MovePaperDialog refiles one paper, and its replications, under another
// project (035).
//
// The destinations are the caller's own projects, private and not in the trash
// — the plain reading of "move it to my other project", and the same three
// things the server checks, so the list offers nothing that would be refused.
// The server still decides: it authorises a lead of either end, which this
// picker does not yet surface.
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
  const [targets, setTargets] = useState<ProjectView[] | null>(null)
  const [to, setTo] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    let live = true
    listProjects()
      .then((ps) => {
        if (!live) return
        const open = ps.filter((p) => p.id !== project.id && p.visibility === 'private' && !p.deleted_at)
        setTargets(open)
        if (open.length > 0) setTo(open[0].id)
      })
      .catch(() => live && setTargets([]))
    return () => {
      live = false
    }
  }, [project.id])

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
function PublishPanel({
  i18n,
  projectID,
  onPublished,
  onChanged,
  onOpenBlocker,
}: {
  i18n: I18n
  projectID: string
  onPublished: () => void
  onChanged?: () => void
  onOpenBlocker?: (b: PublishBlocker) => void
}) {
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
          onChanged={onChanged}
          onOpenBlocker={onOpenBlocker}
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
  onChanged,
  onOpenBlocker,
}: {
  i18n: I18n
  projectID: string
  onClose: () => void
  onPublished: () => void
  // Something under this dialog changed, and the screen behind it is now stale
  // (032). Deleting a blocker removes a session the project screen is listing;
  // an owner who then closes the wizard instead of signing off again would
  // otherwise be left clicking a row that is no longer there.
  onChanged?: () => void
  // Where a named blocker is opened. Absent on a screen with no session to go to
  // — the refusal then still says what is wrong, it just cannot travel.
  onOpenBlocker?: (b: PublishBlocker) => void
}) {
  const { t } = i18n
  const [step, setStep] = useState(1)
  const [p, setP] = useState<Profile>({})
  const [toProfile, setToProfile] = useState(true)
  const [affirmed, setAffirmed] = useState(false)
  const [flipRepo, setFlipRepo] = useState(true)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [blockers, setBlockers] = useState<PublishBlocker[]>([])

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
    setBlockers([])
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
      // A 409 is any of the reasons this project is not publishable, and the
      // status alone cannot tell them apart — they need different remedies, so
      // the body's code decides. When the refusal is about the sessions rather
      // than the signer, it also names them, and the reader is offered the way
      // there instead of the sign-off form they were already past.
      const named = publishBlockers(e)
      setErr(publishErrText(t, e))
      setBlockers(named)
      if (!named.length) setStep(2)
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
      {blockers.length > 0 && (
        <Blockers
          i18n={i18n}
          blockers={blockers}
          onOpen={onOpenBlocker}
          // A deleted blocker leaves the list it was named in, and the refusal
          // above it is stale the moment it does. The sign-off is not re-sent:
          // the owner is still standing in step 3 with the affirmation checked,
          // and re-attesting on their behalf is not this dialog's decision.
          onDeleted={(b) => {
            setBlockers((cur) => cur.filter((x) => x.session_id !== b.session_id))
            setErr('')
            onChanged?.()
          }}
        />
      )}
    </Modal>
  )
}

// Blockers lists the sessions standing between a project and the public flip:
// one row each, saying what is wrong with that one and opening it.
//
// The list is the point. The refusal used to be a single sentence about "the
// reproduction", which named neither the session nor, when there were several,
// how many more were behind it — so an owner fixed one and met the same wall.
// An unfinished run is the one blocker with nothing to read and nothing to fix:
// it has no manifest, so opening it lands the reader on an absence. The row
// carries its own remedy instead (032).
function Blockers({
  i18n,
  blockers,
  onOpen,
  onDeleted,
}: {
  i18n: I18n
  blockers: PublishBlocker[]
  onOpen?: (b: PublishBlocker) => void
  onDeleted?: (b: PublishBlocker) => void
}) {
  const { t } = i18n
  return (
    <div className="stack" style={{ gap: 8 }} data-test="publish-blockers">
      <span className="eyebrow eyebrow-ico">
        <Ico name="alert" size={12} /> {t('publishBlockersTitle')}
      </span>
      {blockers.map((b) => (
        <BlockerRow key={b.session_id} i18n={i18n} b={b} onOpen={onOpen} onDeleted={onDeleted} />
      ))}
    </div>
  )
}

// One blocking session, and what the owner can do about this one.
//
// The delete confirms in the row rather than in a dialog: this list is already
// inside the publish wizard's Modal, and a second Modal over it would share the
// Escape key with the first — one press would close both and take the list of
// blockers with it.
function BlockerRow({
  i18n,
  b,
  onOpen,
  onDeleted,
}: {
  i18n: I18n
  b: PublishBlocker
  onOpen?: (b: PublishBlocker) => void
  onDeleted?: (b: PublishBlocker) => void
}) {
  const { t } = i18n
  const [asking, setAsking] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  // Only an unfinished run. A flagged tree is a decision about what the
  // reproduction contains, and deleting the evidence is not how that is made.
  const removable = !!onDeleted && b.reason === 'unfinished'
  const go = async () => {
    setBusy(true)
    setErr('')
    try {
      await deleteSession(b.session_id)
      onDeleted?.(b)
    } catch (e) {
      setErr(deleteSessionErrLabel(t, e))
      setBusy(false)
      setAsking(false)
    }
  }
  return (
    <div className="blocker-row">
      <div style={{ minWidth: 0 }}>
        <div className="small mono ellipsis">{b.ref || b.repo || b.session_id}</div>
        <div className="muted small">{publishBlockerText(t, b)}</div>
        {removable && !err && <div className="muted small">{t('blockerDeleteHint')}</div>}
        {err && <div className="err small">{err}</div>}
      </div>
      {removable &&
        (asking ? (
          <>
            <button className="btn ghost small" onClick={() => setAsking(false)} disabled={busy}>
              {t('cancel')}
            </button>
            <button className="btn danger small" data-test="delete-blocker-confirm" onClick={go} disabled={busy}>
              {busy ? t('deletingSession') : t('deleteSessionConfirm')}
            </button>
          </>
        ) : (
          <button className="btn ghost small" data-test="delete-blocker" onClick={() => setAsking(true)}>
            <Ico name="trash" size={12} /> {t('deleteSession')}
          </button>
        ))}
      {onOpen && !asking && (
        <button className="btn ghost small" data-test="open-blocker" onClick={() => onOpen(b)}>
          {t('publishOpenSession')} <Ico name="chevronRight" size={12} w={2.2} />
        </button>
      )}
    </div>
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
  signedIn,
}: {
  i18n: I18n
  jobId: string
  tab: SessionTab
  onTab: (tab: SessionTab) => void
  // The paper this session is about, reported once the job is read — a session
  // opened by its own link has nothing else to name it with.
  onLoaded: (ref: string) => void
  onBack: () => void
  // Whether there is an account behind this reader, which decides whether the
  // live stream is worth opening at all (028).
  signedIn: boolean
}) {
  const { t } = i18n
  const [job, setJob] = useState<Job | null>(null)
  // A session under a project this reader may not see reads as missing — the one
  // answer its project gives too (028).
  const [notReadable, setNotReadable] = useState(false)

  useEffect(() => {
    let live = true
    getJob(jobId)
      .then((j) => {
        if (!live) return
        setJob(j)
        setNotReadable(false)
      })
      .catch(() => live && setNotReadable(true))
    return () => {
      live = false
    }
  }, [jobId])

  // The run is watched here rather than in the transcript, because all three
  // panes are about it and only one of them is open at a time.
  //
  // It watches only what there is to watch: the live stream is an operator route
  // and stays gated (028), so for a reader with no account it is a connection
  // that is refused and reopened for as long as the page is up — and a session
  // that has finished has nothing left to send either.
  useEffect(() => {
    if (!signedIn || !job || terminal(job.phase)) return
    return watchJob(jobId, setJob)
  }, [signedIn, jobId, job])
  useEffect(() => {
    if (job?.ref) onLoaded(job.ref)
  }, [job?.ref, onLoaded])

  if (notReadable) return <NotReadable i18n={i18n} me={{ authenticated: signedIn }} />

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
