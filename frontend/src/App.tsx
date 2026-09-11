// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useEffect, useState } from 'react'
import { getMe, type Me } from './api'
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
