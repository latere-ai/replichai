// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// A session as a row under its paper: its phase, how long it has been running,
// what it cost, the repository it published, and the two removal actions, stop
// while it runs and delete once it has settled.

import { useCallback, useEffect, useState } from 'react'
import { cancelSession, deleteSession, type Job } from '../api'
import { type I18n } from '../i18n'
import { deleteSessionErrLabel, failureTip, sessionFailureText } from '../errors'
import { formatUSD } from '../money'
import { Ico } from '../icons'
import { GlassModal } from 'latere-ui/react'
import { Pill, RowButton, Tip } from '../ui'
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
  // Whether this reader may remove a finished session. Only the owner may; the
  // server refuses everybody else.
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
      {/* Under a paper, the reference is already on the card above, so the
          row carries what the run ran on, which is what differs between runs.
          A session listed on its own shows the paper's reference. */}
      <span className="row-mono row-title">{nested ? runLabel(j) : j.ref}</span>
      {/* How long a running session has been going. A run costs money for as
          long as it runs, so this shows elapsed minutes rather than an age
          such as "an hour ago", which rounds them away. */}
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
      {/* The repository, named by its suffix: every reproduction of one paper
          shares a slug, so the suffix is what tells runs apart. The full name
          is in the tooltip. */}
      {j.repo_name && (
        <Tip text={j.repo_name}>
          <a className="link small mono" href={`https://github.com/${j.repo_name}`} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>
            <Ico name="github" size={13} /> {repoSuffix(j.repo_name)}
          </a>
        </Tip>
      )}
      {/* Removal, on a session that has finished. The condition is the exact
          negation of Stop's, so a row offers one removal action, never two,
          and never the one the server is about to refuse. */}
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
      {/* One error slot. A stop that did not land takes it while the session
          is still up, since the reader can act on it. Otherwise the slot shows
          the class of the worker's failure, with the raw error's first line on
          hover; the full report is in the session itself. */}
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
      {/* A session on its own shows the call to action's word. Nested rows
          show only the icon, with the word in the tooltip, since the same word
          would repeat on every row. */}
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

// The confirmation before a session leaves the project. Unlike the trash, the
// removal is not reversible; the way back is to run the paper again. So it
// states what survives: the cost stays on the ledger, and the repository the
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
    <GlassModal
      open
      title={t('deleteSessionTitle')}
      onClose={onClose}
      footer={
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
      <span className="attestation">{job.ref}</span>
      <p className="muted">{t('deleteSessionBody')}</p>
      {job.repo_name && <p className="muted small">{t('deleteSessionKeepsRepo')}</p>}
      {err && <p className="err small">{err}</p>}
    </GlassModal>
  )
}
