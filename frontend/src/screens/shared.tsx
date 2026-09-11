// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// What more than one screen draws: the small presentation pieces — meters,
// empty states, card heads, badges and pills, tabs, avatars — and the few
// components two screens share outright, such as the theme switcher, the
// profile fields and the not-public screen. Screen modules import from here;
// nothing here imports a screen.

import { useState } from 'react'
import { LatereMark } from '../brand'
import { type Me, type Profile, type Project } from '../api'
import { type I18n, type TKey } from '../i18n'
import { Ico, type IconName } from '../icons'
import { Pill, Seams, Segmented, Tip } from '../ui'
import { THEME_MODES, type ThemeMode, useTheme } from '../theme'
import { hueOf, initials, showsPicture } from '../account'

// signerOf names the person who published a project. The signer's recorded name
// is used whenever the sign-off carries one (006n); a v1 sign-off has none and
// falls back to the owner's profile name (018). `owner` itself is never shown —
// it is an opaque OIDC subject and names nobody.
export const signerOf = (p: Project & { owner_name?: string }) => p.signed_off_name || p.owner_name || '—'

// How each appearance is drawn. THEME_MODES fixes which ones exist and in what
// order; this fixes what they look like, so adding a fourth is a type error here
// until somebody answers it. Keeping the list in one place is the point: it used
// to be written out again here, and the two could drift.
const THEME_FACE: Record<ThemeMode, { icon: IconName; label: TKey; hint: TKey }> = {
  auto: { icon: 'monitor', label: 'themeAuto', hint: 'themeAutoHint' },
  light: { icon: 'sun', label: 'themeLight', hint: 'themeLight' },
  dark: { icon: 'moon', label: 'themeDark', hint: 'themeDark' },
}

// ThemeSwitcher offers the three appearances directly rather than cycling
// through them: "auto" is a real choice, not the absence of one, and a person
// who has pinned light should be able to see that they have.
export function ThemeSwitcher({ i18n, theme, compact }: { i18n: I18n; theme: ReturnType<typeof useTheme>; compact?: boolean }) {
  const { t } = i18n
  const options = THEME_MODES.map((value) => ({ value, icon: THEME_FACE[value].icon, label: t(THEME_FACE[value].label), hint: t(THEME_FACE[value].hint) }))
  return <Segmented value={theme.mode} options={options} onChange={theme.setMode} label={t('theme')} compact={compact} />
}

// Meter is a proportion drawn as a bar. It is never the only thing said about a
// number — the count and its denominator are always beside it in words, because
// a bar alone is a rate, and a rate alone is the thing this product refuses to
// hand out.
export function Meter({ pct, tone, size }: { pct: number; tone: string; size?: 'sm' }) {
  return (
    <span className={size === 'sm' ? 'meter sm' : 'meter'} aria-hidden="true">
      <span className="meter-fill" style={{ width: `${Math.max(0, Math.min(100, pct))}%`, background: tone }} />
    </span>
  )
}

// Empty states get a glyph, so "nothing yet" never reads as "failed to load".
export function Empty({ icon, text }: { icon: IconName; text: string }) {
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
export function CardHead({ label, icon, tone = 'var(--fg-3)', children }: { label: string; icon: IconName; tone?: string; children?: React.ReactNode }) {
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
export function VisibilityBadge({ i18n, visibility, icon }: { i18n: I18n; visibility: 'private' | 'public'; icon?: boolean }) {
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
export function TierPill({ i18n, tier }: { i18n: I18n; tier?: string }) {
  const admin = tier === 'admin'
  return (
    <Pill tone={admin ? 'brand' : 'neutral'} icon={admin ? 'shieldCheck' : undefined}>
      {tierLabel(i18n.t, tier)}
    </Pill>
  )
}

// ProfileFields is the one set of inputs shared by the settings form and the
// publish wizard's step 2, so the two cannot drift in labels or order.
export type TextField = 'display_name' | 'institution' | 'contact_email' | 'orcid' | 'scholar_url' | 'handle'

export function ProfileFields({
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

// Tab is one view of a project. The glyph does the work at a glance; the
// underline and the brand tint say which one is open.
export function Tab({ on, onClick, icon, label }: { on: boolean; onClick: () => void; icon: IconName; label: string }) {
  return (
    <button className={'tab' + (on ? ' on' : '')} onClick={onClick}>
      <Ico name={icon} size={13} /> {label}
    </button>
  )
}

// NotReadable is what a project nobody here may read looks like. The server
// answers one 404 for private, withdrawn, trashed, and never-existed alike (028),
// so this screen claims none of them: it says the address is not public, and — to
// a reader with no account — offers the sign-in that would settle it, carrying
// this address so they land back on it.
export function NotReadable({ i18n, me }: { i18n: I18n; me: Me }) {
  const { t } = i18n
  return (
    <div className="screen" data-test="not-readable">
      <div className="card card-pad stack" style={{ maxWidth: 520, margin: '48px auto', textAlign: 'center', alignItems: 'center' }}>
        <Ico name="globe" size={22} style={{ color: 'var(--fg-3)' }} />
        <h2 style={{ margin: 0 }}>
          <Seams i18n={i18n} k="notPublicTitle" />
        </h2>
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

// Initials is the avatar for someone we know only by address — a seeded member,
// a granted email — where there is no profile picture to fall back from.
export function Initials({ email, size = 22 }: { email: string; size?: number }) {
  return (
    <span
      className="user-avatar"
      style={{ width: size, height: size, borderRadius: Math.round(size / 3.2), fontSize: Math.round(size * 0.42), ['--av-h' as string]: hueOf(email) }}
    >
      {initials(email)}
    </span>
  )
}

// Avatar renders the IdP's picture when there is one, and falls back to
// initials — for an absent URL, and for one that fails to load (006m AC4). A
// broken-image glyph would be worse than the two letters we had before.
export function Avatar({ me, size = 22 }: { me: Me; size?: number }) {
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
// The account roles the server sends, each named by a key TypeScript can check.
// Built by concatenation this read `t('tier' + …)`, which compiled whatever it
// produced — including `tierNone`, a key that never existed and painted its own
// name in the pill.
const TIER_KEY = { user: 'tierUser', admin: 'tierAdmin' } as const satisfies Record<string, TKey>
function tierLabel(t: I18n['t'], tier?: string) {
  return t(TIER_KEY[tier as keyof typeof TIER_KEY] ?? 'tierNone')
}
