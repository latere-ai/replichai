// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState } from 'react'
import { getMe, type Me, setUnauthorizedHandler } from './api'
import { useI18n } from './i18n'
import { Landing } from './landing'
import { isPublicView, useRouter } from './routes'
import { useHead } from './head'
import { useTheme } from './theme'
import { PublicProfileScreen } from './screens/profile'
import { AuthUnavailable, NoAccess, Shell } from './screens/shell'

export default function App() {
  const i18n = useI18n()
  const theme = useTheme()
  const [me, setMe] = useState<Me | null>(null)
  const [meUnavailable, setMeUnavailable] = useState(false)
  const { view, navigate } = useRouter()
  // The tab follows the screen from the second one onwards; the server writes
  // the first one's head.
  useHead(view, i18n.t, me === null ? null : me.authenticated)

  // refreshMe re-reads the identity, and with it the wallet. It is called after
  // anything that spends allowance, so the header never shows a stale balance.
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

  // A sign-in that lapsed while the app was open answers 401 to whatever is read
  // next. One banner asks the person to sign in again, and the screens do not
  // show the refusal as empty lists. Only a signed-in reader gets one; a reader
  // with no account is refused some routes by design.
  const [signedOut, setSignedOut] = useState(false)
  const signedIn = me?.authenticated === true
  useEffect(() => {
    if (!signedIn) return
    setUnauthorizedHandler(() => setSignedOut(true))
    return () => setUnauthorizedHandler(null)
  }, [signedIn])

  // A researcher's page renders before every auth gate: it is public, and a
  // reader who followed a link from a paper has no account to sign in to.
  if (view.name === 'profile') return <PublicProfileScreen i18n={i18n} handle={view.handle} />

  if (meUnavailable) return <AuthUnavailable i18n={i18n} onRetry={loadMe} />
  if (me === null) return <div className="center-load">…</div>
  // The registry and everything under it are readable signed out, in the app's
  // own frame, because a reader who came from a paper is reading the work.
  //
  // Every other screen needs an account. Each is an address somebody can arrive
  // at directly, from an invitation or a shared link, so sign-in returns to it.
  if (!me.authenticated && !isPublicView(view)) return <Landing i18n={i18n} next={window.location.pathname} theme={theme} />
  if (!me.authenticated) return <Shell i18n={i18n} me={me} theme={theme} refreshMe={refreshMe} view={view} navigate={navigate} />
  if ((me.tier ?? 'none') === 'none') return <NoAccess i18n={i18n} email={me.email ?? ''} />
  return (
    <>
      {signedOut && (
        <div className="signed-out-banner" role="alert" data-test="signed-out-banner">
          <span>{i18n.t('signedOutBanner')}</span>
          <a className="btn sm" href={`/login?next=${encodeURIComponent(window.location.pathname)}`}>
            {i18n.t('signInTitle')}
          </a>
        </div>
      )}
      <Shell i18n={i18n} me={me} theme={theme} refreshMe={refreshMe} view={view} navigate={navigate} />
    </>
  )
}
