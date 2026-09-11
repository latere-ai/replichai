// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Settings (024a): one screen with a sub-tab per real path — Account is what
// you are on the platform, Profile is what you publish as. The profile fields
// themselves are shared with the publish wizard and live in shared.tsx.

import { useEffect, useState } from 'react'
import { getProfile, type Me, type Profile, saveProfile } from '../api'
import { type I18n } from '../i18n'
import { profileSaveErrText } from '../errors'
import { Ico } from '../icons'
import { type Navigate, type SettingsTab } from '../routes'
import { Seams, Toggle } from '../ui'
import { useTheme } from '../theme'
import { Avatar, ProfileFields, type TextField, ThemeSwitcher, TierPill } from './shared'

// SettingsScreen is one screen with three sub-tabs, each a real path (024a):
// Account is what you are on the platform, Profile is what you publish as, and
// Credits is what you have to spend. Switching a tab navigates, so Back returns
// to the previous tab and a link opens on the tab it names.
export function SettingsScreen({
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
    <div className="screen measured form">
      <div className="screen-head">
        <h1 className="screen-title">
          <Seams i18n={i18n} k="settings" />
        </h1>
      </div>
      <div className="detail-tabs" style={{ marginBottom: 18 }}>
        {tabs.map((tb) => (
          <button key={tb.k} className={'tab' + (cur === tb.k ? ' on' : '')} onClick={() => navigate({ name: 'settings', tab: tb.k })}>
            {tb.label}
          </button>
        ))}
      </div>
      <div className="stack">
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
