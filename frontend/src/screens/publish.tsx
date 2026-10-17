// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Going public, and coming back: the three-step wizard that records the
// decision, the sessions standing in its way, the card that reports the
// recorded sign-off, and the dialog that withdraws it.

import { useEffect, useState } from 'react'
import {
  deleteSession,
  getProfile,
  makeProjectPublic,
  type Profile,
  type Project,
  type PublishBlocker,
  saveProfile,
  type SignOff,
  withdrawProject,
} from '../api'
import { type I18n } from '../i18n'
import { dateOnly } from '../locale'
import { deleteSessionErrLabel, publishBlockers, publishBlockerText, publishErrText } from '../errors'
import { Ico } from '../icons'
import { GlassCheckbox, GlassModal } from 'latere-ui/react'
import { ProfileFields, SignerRows, type TextField } from './shared'

// PublishWizard walks the three commitments of going public: what becomes
// public, who stands behind it, and the responsibility for it. Step 2 is
// prefilled from the owner's profile but recorded from what they confirm here.
// The sign-off is a snapshot, so a later profile edit cannot rewrite it.
export function PublishWizard({
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
  // Something under this dialog changed, so the screen behind it is stale:
  // deleting a blocker removes a session the project screen is listing.
  onChanged?: () => void
  // Where a named blocker is opened. Absent on a screen with no session to go
  // to; the refusal then still says what is wrong but offers no link.
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
  // Publishing puts a name on a result, and the name has to resolve to a
  // public page. The wizard sets it here rather than sending the signer to
  // settings mid-decision.
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
      // status alone cannot tell them apart. They need different remedies, so
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

  return (
    <GlassModal
      open
      title={step === 1 ? t('publishStep1') : step === 2 ? t('publishStep2') : t('publishStep3')}
      onClose={onClose}
      footer={
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
      <span className="attestation">
        <span className="step-dots">
          {t('publishStepOf', { n: String(step) })}
          <span className="step-track">
            {[1, 2, 3].map((n) => (
              <span key={n} className={'step-dot' + (n <= step ? ' on' : '')} />
            ))}
          </span>
        </span>
      </span>
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
          <GlassCheckbox value={!!p.public} onChange={(on) => setP((cur) => ({ ...cur, public: on }))} label={t('publishPublicProfile')} />
          <GlassCheckbox value={toProfile} onChange={setToProfile} label={t('publishSaveToProfile')} />
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
          <GlassCheckbox value={affirmed} onChange={setAffirmed} label={t('publishAffirm')} />
          <GlassCheckbox value={flipRepo} onChange={setFlipRepo} label={t('flipRepoLabel')} />
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
    </GlassModal>
  )
}

// Blockers lists the sessions standing between a project and the public flip:
// one row each, saying what is wrong with that one and opening it, so an owner
// sees every blocker at once. An unfinished run has no manifest to read, so its
// row carries its own remedy instead.
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
// inside the publish wizard's dialog, and a second dialog over it would hide the
// list of blockers the delete is about.
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
// alone. It also offers the owner the withdrawal.
export function PublishedCard({
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
  const when = project.signed_off_at ? dateOnly(i18n.lang, project.signed_off_at) : ''
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
                attestation reports; the link comes and goes as its owner turns
                their profile on and off. */}
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
      <SignerRows
        i18n={i18n}
        signer={project.signed_off_email}
        contact={project.signed_off_contact}
        orcid={project.signed_off_orcid}
        scholar={project.signed_off_scholar}
      />
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
// wizard's three: withdrawing is the conservative direction. It must not imply
// erasure: going private stops new access and does not retrieve copies already
// taken, and the dialog says so rather than letting the owner assume it.
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
    <GlassModal
      open
      title={t('withdrawTitle')}
      onClose={onClose}
      footer={
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
    </GlassModal>
  )
}
