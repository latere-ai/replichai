// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState } from 'react'
import { LatereMark, Mark, Wordmark } from './brand'
import {
  archiveUrl,
  createProject,
  getMe,
  getProject,
  getPublicProfile,
  listProjects,
  listPublic,
  listTrash,
  type Me,
  type PaperCitation,
  type ProjectView,
  type PublicProfile,
  purgeProject,
  type ResolvedPaper,
  resolvePaper,
  restoreProject,
  trashProject,
} from './api'
import { type I18n, useI18n } from './i18n'
import { createOutcome, generic, purgeErrLabel, refusal, trashErrLabel } from './errors'
import { formatUSD } from './money'
import { Ico, type IconName } from './icons'
import { Landing } from './landing'
import { isPublicView, type Navigate, type ProjectTab, type SessionTab, useRouter, type View } from './routes'
import { useHead } from './head'
import { listCitation } from './papers'
import { Modal, Pill, RowButton, Seams, Segmented, Tip, tipAttrs } from './ui'
import { useTheme } from './theme'
import { absoluteTime, relativeTime } from './when'
import { useDrawer, useSidebar } from './sidebar'
import { isAdmin, isMine } from './account'
import { phaseLabel } from './phase'
import { Avatar, Empty, Initials, Meter, signerOf, ThemeSwitcher, TierPill, VisibilityBadge } from './screens/shared'
import { TopUpModal, WalletPill, WalletScreen } from './screens/wallet'
import { SessionScreen } from './screens/session'
import { ProjectScreen } from './screens/project'
import { AdminScreen } from './screens/admin'
import { SettingsScreen } from './screens/settings'

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

  // Each handle starts from nothing: the previous one's "not found" cleared,
  // its answer — however late — ignored. Without both, /u/a answering 404 left
  // "No such profile" standing over /u/b, and a slow 404 for a handle the reader
  // had already left could land on the page they had moved to.
  useEffect(() => {
    let live = true
    setP(null)
    setMissing(false)
    getPublicProfile(handle)
      .then((v) => live && setP(v))
      .catch(() => live && setMissing(true))
    return () => {
      live = false
    }
  }, [handle])

  if (missing) {
    return (
      <div className="auth-center">
        <div className="auth-card">
          <Mark size={40} className="auth-logo" />
          <h2>
            <Seams i18n={i18n} k="profileNotFound" />
          </h2>
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
    <div className="screen measured form">
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
        <h2>
          <Seams i18n={i18n} k="authUnavailableTitle" />
        </h2>
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
        <h2>
          <Seams i18n={i18n} k="noAccessTitle" />
        </h2>
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

        {view.name === 'notfound' && <NotFound i18n={i18n} navigate={navigate} />}
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
        <h1 className="screen-title">
          <Seams i18n={i18n} k="navProjects" />
        </h1>
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
              <RowButton key={p.id} className="row" onOpen={() => onOpen(p.id, p.title)}>
                <div className="row-main">
                  <div className="row-title">{p.title}</div>
                  {/* Which paper the project is about (043), directly under its
                      title, exactly as the registry sets it. A title is a name
                      its owner typed and two projects can carry the same one;
                      the citation is what tells them apart at a glance. */}
                  <RowCitation i18n={i18n} papers={p.papers} />
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
              </RowButton>
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

// RowCitation is a list row's half of what a landing card says: the first
// paper's citation, and how many others the project holds. Both lists of
// projects render it — the registry and the owner's own dashboard — because a
// row is scanned for which paper it is about before anything else about it.
//
// It renders nothing when no source could fill the record in. On the registry
// that is the end of it; on the dashboard the invitation to complete a record
// lives on the project screen, addressed to the owner who can act on it, rather
// than in a list row that would repeat it once per project.
function RowCitation({ i18n, papers }: { i18n: I18n; papers?: PaperCitation[] }) {
  const { t } = i18n
  const { bits, more } = listCitation(papers, t('etAl'))
  if (bits.length === 0) return null
  return (
    <div className="row-cite" data-test="row-citation">
      {bits.join(' · ')}
      {more > 0 && <span className="row-cite-more">{t('morePapers', { n: String(more) })}</span>}
    </div>
  )
}

function PublicScreen({ i18n, onOpen }: { i18n: I18n; onOpen: (id: string, title?: string) => void }) {
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
      <div className="card">
        {list === null ? (
          <div className="empty">…</div>
        ) : list.length === 0 ? (
          <Empty icon="globe" text={t('noPublic')} />
        ) : (
          <div className="rows">
            {list.map((p) => (
              <RowButton key={p.id} className="row" onOpen={() => onOpen(p.id, p.title)}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div className="row-title">{p.title}</div>
                  {/* Which paper this row is a reproduction of (043), above the
                      facts about the reproduction itself. It is the paper's own
                      line, not the project's; a project's title is a name
                      somebody typed. */}
                  <RowCitation i18n={i18n} papers={p.papers} />
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
              </RowButton>
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
    <div className="screen measured form">
      <div className="submit-form">
        <div className="screen-head">
          <h1 className="screen-title">
          <Seams i18n={i18n} k="newProject" />
        </h1>
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

/* ─── Project detail ─── */

// NotFound is an address this app does not have (042c). The server answered 404
// for it and served the app anyway, so this screen is what that status looks
// like to a person: it keeps the address they typed rather than correcting it,
// and offers the two places worth going next.
function NotFound({ i18n, navigate }: { i18n: I18n; navigate: Navigate }) {
  const { t } = i18n
  return (
    <div className="screen" data-test="not-found">
      <div className="card card-pad stack" style={{ maxWidth: 520, margin: '48px auto', textAlign: 'center', alignItems: 'center' }}>
        <Ico name="search" size={22} style={{ color: 'var(--fg-3)' }} />
        <h2 style={{ margin: 0 }}>
          <Seams i18n={i18n} k="notFoundTitle" />
        </h2>
        <p className="muted" style={{ margin: 0, lineHeight: 1.6 }}>
          {t('notFoundBody')}
        </p>
        <button className="btn" onClick={() => navigate({ name: 'projects' })}>
          {t('notFoundHome')}
        </button>
        <a className="btn ghost" href="/registry">
          <Ico name="globe" size={13} /> {t('browseRegistry')}
        </a>
      </div>
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
