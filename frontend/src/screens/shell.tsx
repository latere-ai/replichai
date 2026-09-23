// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The app's frame (013, 028, 030): the rail with its destinations, the top bar
// with its breadcrumb and wallet, the screen the address names, and the three
// full-page states outside it, namely auth unavailable, no access, and not found.

import { useCallback, useEffect, useState } from 'react'
import { LatereMark, Mark, Wordmark } from '../brand'
import { getProject, type Me } from '../api'
import { useProjects } from '../projects-store'
import { type I18n } from '../i18n'
import { Ico, type IconName } from '../icons'
import { type Navigate, type ProjectTab, type SessionTab, type View } from '../routes'
import { Seams } from '../ui'
import { useTheme } from '../theme'
import { useDrawer, useSidebar } from '../sidebar'
import { isAdmin } from '../account'
import { Avatar, ThemeSwitcher, TierPill } from './shared'
import { TopUpModal, WalletPill, WalletScreen } from './wallet'
import { SessionScreen } from './session'
import { ProjectScreen } from './project'
import { AdminScreen } from './admin'
import { SettingsScreen } from './settings'
import { SubmitScreen } from './submit'
import { ProjectsScreen, PublicScreen, TrashScreen } from './projects'

export function AuthUnavailable({ i18n, onRetry }: { i18n: I18n; onRetry: () => void }) {
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

export function NoAccess({ i18n, email }: { i18n: I18n; email: string }) {
  return (
    <div className="auth-center">
      <div className="auth-card">
        <Mark size={40} className="auth-logo" />
        <h2>
          <Seams i18n={i18n} k="noAccessTitle" />
        </h2>
        <p>{i18n.t('noAccessBody', { email })}</p>
        {/* Signing out clears a session minted before sign-in was opened, and
            re-runs the admission with a fresh identity, which is the remedy
            for every cause of this screen except an actual revocation. */}
        <a className="btn ghost" href="/logout" style={{ justifyContent: 'center' }}>
          <Ico name="logOut" size={13} /> {i18n.t('signOut')}
        </a>
      </div>
    </div>
  )
}

// NavItem is one destination in the rail. Each keeps a fixed hue on its icon so
// the rail is scannable by color; the label only takes the brand when current.
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
          hidden, because a 64px column has no room for it. */}
      {count !== undefined && <span className="count">{count}</span>}
    </button>
  )
}

export function Shell({
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

  // A session reached by its own link knows its project's id from the path but
  // not its title, because the job carries the paper, not the project. The
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
  // address (013), and a shared link opens at the width its reader last chose.
  const { folded, toggle: toggleRail } = useSidebar()
  const foldLabel = folded ? t('unfoldSidebar') : t('foldSidebar')

  // How many projects the rail's Projects row carries. It is read from the shared
  // list rather than fetched here: the rail outlives every screen, so the number
  // has to survive the list being unmounted, and it has to change when a project
  // is thrown away from the screen beside it, which a private read of the list
  // on every change of screen did not do.
  const projectCount = useProjects(signedIn)?.length

  // Below the phone breakpoint the rail is an overlay instead of a column, and
  // this is what holds it open (030). It is not the fold and never persists;
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
          has to sit under the rail and over the screen, an order a portal to
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
            {/* The wallet is a primary destination, since money is managed
                often, so it lives with the workspace rather than under
                Settings. */}
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
              {/* Sized to the meta stack of name, address and tier beside it, not
                  to a default: a 26px square against three rows of text left the
                  row bottom-heavy and the card's right half empty. Folded there is
                  no stack, and 38px would not clear the rail's own padding. */}
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
              the shell is one tree. It leads the bar rather than joining the
              controls on the right, because it is navigation rather than an
              action on this screen. */}
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
            // traveling anywhere.
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
