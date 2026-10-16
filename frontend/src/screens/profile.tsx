// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A researcher's public page (006r): who they say they are, how to reach them,
// and the public work signed under that name. It renders for a signed-out
// reader, so it carries its own frame rather than the app shell's.

import { useEffect, useState } from 'react'
import { Mark, Wordmark } from '../brand'
import { getPublicProfile, type PublicProfile } from '../api'
import { type I18n } from '../i18n'
import { dateOnly } from '../locale'
import { Ico } from '../icons'
import { Seams } from '../ui'
import { Empty, Initials, SignerRows } from './shared'

// PublicProfileScreen is a researcher's page: who they say they are, and the
// public work signed under that name. It renders for a signed-out reader, so it
// carries its own frame rather than the app shell's.
//
// Nothing here is verified. The platform does not check that somebody works
// where they say they work, just as a preprint server does not. It does
// guarantee that the work listed is public and was signed by this person.
export function PublicProfileScreen({ i18n, handle }: { i18n: I18n; handle: string }) {
  const { t } = i18n
  const [p, setP] = useState<PublicProfile | null>(null)
  const [missing, setMissing] = useState(false)

  // Each handle starts from nothing: the previous one's "not found" is cleared
  // and its answer, however late, is ignored. Without both, /u/a answering 404
  // left "No such profile" standing over /u/b, and a slow 404 for a handle the
  // reader had already left could land on the page they had moved to.
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
          <SignerRows i18n={i18n} contact={p.contact_email} orcid={p.orcid} scholar={p.scholar_url} />
          <span className="muted small" style={{ lineHeight: 1.6 }}>
            {t('profileUnverified')}
          </span>
        </div>

        {/* The work this researcher signed, set as a publication list is
            (047): numbered, the work's title, and the year it was signed. */}
        <section className="findings-sec">
          <p className="table-caption">
            <b>{t('profileSignedWork')}</b>
          </p>
          {p.projects.length === 0 ? (
            <Empty icon="globe" text={t('profileNoSignedWork')} />
          ) : (
            <ol className="rows ref-list">
              {p.projects.map((proj, i) => (
                <li key={proj.id}>
                  <a className="row ref-row" href={`/p/${proj.id}`}>
                    <span className="ref-n">{i + 1}</span>
                    <span className="ref-body">
                      <span className="row-title">{proj.title}</span>
                      {proj.signed_off_at && <span className="row-sub">{dateOnly(i18n.lang, proj.signed_off_at)}</span>}
                    </span>
                    <Ico name="chevronRight" size={15} className="chev" />
                  </a>
                </li>
              ))}
            </ol>
          )}
        </section>
      </div>
    </div>
  )
}
