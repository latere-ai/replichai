// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A researcher's public page (006r): who they say they are, how to reach them,
// and the public work signed under that name. It renders for a signed-out
// reader, so it carries its own frame rather than the app shell's.

import { useEffect, useState } from 'react'
import { Mark, Wordmark } from '../brand'
import { getPublicProfile, type PublicProfile } from '../api'
import { type I18n } from '../i18n'
import { Ico } from '../icons'
import { Seams } from '../ui'
import { Empty, Initials } from './shared'

// PublicProfileScreen is a researcher's page: who they say they are, and the
// public work signed under that name. It renders for a signed-out reader, so it
// carries its own frame rather than the app shell's.
//
// Nothing here is verified — the platform does not check that somebody works
// where they say they work, exactly as a preprint server does not. What it does
// guarantee is that the work listed is public and was signed by this person.
export function PublicProfileScreen({ i18n, handle }: { i18n: I18n; handle: string }) {
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
