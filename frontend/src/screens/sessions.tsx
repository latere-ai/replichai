// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A session as a row under its paper: its phase, how long it has been running,
// what it cost, the repository it published, and the two removal actions, stop
// while it runs and delete once it has settled (021d, 032).

import { useCallback, useEffect, useState } from 'react'
import { cancelSession, deleteSession, type Job } from '../api'
import { type I18n } from '../i18n'
import { deleteSessionErrLabel, failureTip, sessionFailureText } from '../errors'
import { formatUSD } from '../money'
import { Ico } from '../icons'
import { Modal, Pill, RowButton, Tip } from '../ui'
import { elapsed } from '../when'
import { freshest, stopPending, stoppedByHand, terminal } from '../session-stop'
import { useJobStream } from '../job-stream'
import { repoSuffix, runLabel } from '../repo'
import { phaseIcon, phaseLabel, phaseTone } from '../phase'

export function SessionRow({
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
  // Whether this reader may remove a finished session (032). Only the owner
  // may: the server refuses everybody else, and a button that always fails is
  // worse than no button.
  canDelete?: boolean
  onOpen: () => void
  onChanged: () => void
  // Where a delete is confirmed. The dialog is the card's, not the row's: it is
  // one dialog for the list rather than one per row, and because Modal portals
  // to <body> but a React portal still bubbles its clicks through the React
  // tree, a dialog inside this row would open the session on every click in it.
  onDelete?: (j: Job) => void
}) {
  const { t } = i18n
  const [j, setJob] = useState<Job>(job)
  // Asking to stop is remembered for as long as the session takes to settle, so
  // the control cannot re-arm mid-stop and take a second click.
  const [stopAsked, setStopAsked] = useState(false)
  const [stopErr, setStopErr] = useState(false)
  // Every snapshot of this session, from the list, the cancel's answer or the
  // stream, arrives through the same guard, so none of them can walk a settled
  // session back to running.
  const show = useCallback((next: Job) => setJob((shown) => freshest(shown, next)), [])
  useEffect(() => show(job), [job, show])
  // The stream is keyed on the session and on whether it is still running, not
  // on the snapshot the list handed over, which a refresh replaces every time.
  useJobStream(job.id, !terminal(j.phase), show)
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
      // The stop did not land and the run is still up. Re-enable the control and
      // say why, rather than leaving a dead button on a running session.
      setStopAsked(false)
      setStopErr(true)
    }
  }

  // A stopped run is neither a success nor a fault of the paper: it reads in the
  // neutral tone rather than borrowing the failure red.
  const stopped = stoppedByHand(j.phase, j.error)
  const phaseTxt = stopped ? t('stopped') : phaseLabel(t, j.phase)
  return (
    <RowButton className={nested ? 'row row-nested' : 'row'} onOpen={onOpen}>
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
      {/* Under a paper, the paper's own reference is already on the card above
          and repeating it on every row says nothing: five runs of one paper
          render five copies of one string. What differs between them is what
          they ran on, so that is what the row carries. A session listed on its
          own, on the dashboard, still needs the reference to say which paper it
          is about. */}
      <span className="row-mono row-title">{nested ? runLabel(j) : j.ref}</span>
      {/* How long it has been going, for a row that is still going (021d). A
          run costs money for as long as it runs, so the minutes are what the
          reader is watching. An age such as "an hour ago" answers a different
          question and rounds the minutes away. */}
      {!terminal(j.phase) && (
        <Tip text={t('runningForTitle')}>
          <span className="muted small tabular">{elapsed(t, j.created_at, tick)}</span>
        </Tip>
      )}
      {!!j.cost && (
        <Tip text={terminal(j.phase) ? t('sessionCostTitle') : t('sessionCostRunningTitle')}>
          <span className="muted small tabular">{formatUSD(i18n.lang, j.cost)}</span>
        </Tip>
      )}
      {!terminal(j.phase) && (
        <button className={'btn ghost sm' + (stopping ? ' pending' : ' danger')} onClick={stop} disabled={stopping}>
          <Ico name="stop" size={11} /> {stopping ? t('stopping') : t('stop')}
        </button>
      )}
      {/* The repository, named by the part that differs. Every reproduction of
          one paper gets the same slug and a different suffix, so a row labeled
          "GitHub" is indistinguishable from the nine beside it once a paper is
          reproduced more than once, which a repeat study does by design. The
          full name stays in the tooltip. */}
      {j.repo_name && (
        <Tip text={j.repo_name}>
          <a className="link small mono" href={`https://github.com/${j.repo_name}`} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>
            <Ico name="github" size={13} /> {repoSuffix(j.repo_name)}
          </a>
        </Tip>
      )}
      {/* Removal, on a session that has finished (032). The condition is the
          exact negation of Stop's, so a row offers one removal action, never
          two, and never the one the server is about to refuse. */}
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
      {/* One error slot. A stop that did not land is the more recent error and
          the one the reader can act on, so it takes the slot while the session
          is still up; it is gone the moment the session settles, whoever settled
          it. Otherwise the slot shows what the worker recorded, which is a Go
          error string. That says what broke to somebody who wrote the worker
          and nothing to anybody else, so the row states the class of failure
          and keeps the original's first line on hover; the whole of it, a
          stack trace when the runner panicked, is in the session itself. */}
      {stopErr && !terminal(j.phase) ? (
        <span className="err small">
          <Ico name="alert" size={13} /> {t('stopError')}
        </span>
      ) : (
        j.error &&
        !stopped && (
          <Tip text={failureTip(t, j.error)}>
            <span className="err small">
              <Ico name="alert" size={13} /> {sessionFailureText(t, j.error)}
            </span>
          </Tip>
        )
      )}
      {/* The call to action names itself once, on a session standing on its
          own. Under a paper it is the same word on every row of the column and
          the chevron beside it already says the row opens, so the nested rows
          carry the icon and keep the word in the tooltip. */}
      {nested ? (
        <Tip text={i18n.t('viewTranscript')}>
          <span className="muted small row-cta">
            <Ico name="message" size={12} />
          </span>
        </Tip>
      ) : (
        <span className="muted small row-cta">
          <Ico name="message" size={12} /> {i18n.t('viewTranscript')}
        </span>
      )}
      <Ico name="chevronRight" size={15} className="chev" />
    </RowButton>
  )
}

// The confirmation before a session leaves the project (032). It is the app's
// own dialog, not window.confirm, for the reasons TrashDialog gives.
//
// It differs from the trash dialog in what it says: the removal is not
// reversible, because the way back is to run the paper again, since a session
// is one run rather than a container of work. So it states the two things that
// survive the removal: what it cost stays on the ledger, and the repository the
// agent wrote stays on GitHub, closed rather than destroyed.
export function DeleteSessionDialog({ i18n, job, onClose, onDone }: { i18n: I18n; job: Job; onClose: () => void; onDone: () => void }) {
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
