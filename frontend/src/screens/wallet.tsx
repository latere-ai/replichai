// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// Money on screen: the wallet pill in the top bar, the top-up dialog, the
// wallet screen with its statement, and the card that moves credits between a
// person's wallet and a project's pot.

import { useEffect, useState } from 'react'
import {
  allocateCredits,
  type Credits,
  errCode,
  getWallet,
  type LedgerEntry,
  type Me,
  type ProjectCredits,
  reclaimCredits,
  startCheckout,
  type Wallet,
} from '../api'
import { type I18n } from '../i18n'
import { dateOnly } from '../locale'
import { checkoutErrLabel, creditMoveErrLabel } from '../errors'
import { creditedMicro, formatUSD, MICRO, parseUSD } from '../money'
import { Ico } from '../icons'
import { GlassModal } from 'latere-ui/react'
import { NumberField, Seams, Tip } from '../ui'
import { ledgerLabel } from '../ledger'

// WalletPill shows what this person has to spend, next to the button that
// starts spending it.
//
// A new account and one that has spent down read differently. Most people first
// see this pill at zero, and it has to say they have none yet and how to get
// some, rather than implying they lost something.
export function WalletPill({ i18n, credits, onTopUp }: { i18n: I18n; credits?: Credits; onTopUp?: () => void }) {
  const { t } = i18n
  if (!credits) return null
  const empty = credits.wallet <= 0
  // Not enough left to open one more session is a warning; nothing at all is an
  // error; the rest is just a number.
  const state = empty ? ' out' : credits.wallet < credits.start_reserve ? ' low' : ''
  const inner = (
    <>
      <Ico name={empty ? 'alert' : 'wallet'} size={12} />
      {empty ? t('walletEmpty') : formatUSD(i18n.lang, credits.wallet)}
    </>
  )
  return (
    <Tip text={onTopUp ? t('topUp') : empty ? t('walletEmptyTitle') : t('walletTitle', { reserve: formatUSD(i18n.lang, credits.start_reserve) })}>
      {onTopUp ? (
        // The balance is the way to buy more: click it to top up. A button rather
        // than a span, with the pill's own look kept; only the font, which a
        // button does not inherit, needs restoring.
        <button type="button" className={'quota-pill' + state} style={{ font: 'inherit', cursor: 'pointer' }} onClick={onTopUp} aria-label={t('topUp')}>
          {inner}
        </button>
      ) : (
        <span className={'quota-pill' + state}>{inner}</span>
      )}
    </Tip>
  )
}

// pctText renders basis points as a percentage a person reads: 500 → "5",
// 290 → "2.9". No trailing ".0", because a whole percent needs no decimal.
function pctText(bps: number): string {
  return String(Math.round((bps / 100) * 10) / 10)
}

// The preset top-up amounts, in whole dollars. A person picks one or types
// their own.
const TOPUP_PRESETS = [10, 25, 50, 100]

// TopUpModal buys credits. It shows what you pay, the fee, and the credits you
// get before sending you to the processor. The webhook, not the return from
// that page, credits the wallet; this only opens the payment, and the wallet
// screen re-reads the balance when the person comes back.
export function TopUpModal({ i18n, onClose }: { i18n: I18n; onClose: () => void }) {
  const { t } = i18n
  const [wallet, setWallet] = useState<Wallet | null>(null)
  const [choice, setChoice] = useState<number | 'other'>(25 * MICRO)
  const [custom, setCustom] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    getWallet()
      .then(setWallet)
      .catch(() => setErr(t('errOffline')))
  }, [t])

  const min = wallet?.min_topup_micro ?? 0
  const raw = choice === 'other' ? parseUSD(custom) : choice
  // The amount charged, snapped to whole cents. The fee and the credit are both
  // computed from it, so the three add up on screen.
  const gross = raw !== null ? Math.floor(raw / 10_000) * 10_000 : 0
  const belowMin = choice === 'other' && raw !== null && raw > 0 && gross < min
  const valid = gross > 0 && gross >= min
  const credited = valid && wallet ? creditedMicro(gross, wallet.spread_bps, wallet.spread_fixed_micro) : null

  const go = async () => {
    if (!valid || busy) return
    setBusy(true)
    setErr('')
    try {
      const { url } = await startCheckout(gross)
      window.location.href = url
    } catch (e) {
      setErr(checkoutErrLabel(t, e))
      setBusy(false)
    }
  }

  return (
    <GlassModal
      open
      title={t('topUpTitle')}
      onClose={onClose}
      footer={
        <>
          <button className="btn ghost" onClick={onClose}>
            {t('cancel')}
          </button>
          <button className="btn" disabled={!valid || busy} onClick={go}>
            <Ico name="lock" size={13} /> {busy ? t('topUpRedirecting') : valid ? t('topUpPayCta', { total: formatUSD(i18n.lang, gross) }) : t('topUpContinue')}
          </button>
        </>
      }
    >
      <p className="muted small" style={{ margin: '0 0 12px', lineHeight: 1.6 }}>
        {t('topUpChoose')}
      </p>
      <div className="topup-presets">
        {TOPUP_PRESETS.map((d) => {
          const p = d * MICRO
          return (
            <button key={d} type="button" className={'topup-preset' + (choice === p ? ' on' : '')} aria-pressed={choice === p} onClick={() => setChoice(p)}>
              {formatUSD(i18n.lang, p)}
            </button>
          )
        })}
        <button type="button" className={'topup-preset' + (choice === 'other' ? ' on' : '')} aria-pressed={choice === 'other'} onClick={() => setChoice('other')}>
          {t('topUpOther')}
        </button>
      </div>
      {choice === 'other' && (
        <div className="field-row" style={{ marginTop: 10 }}>
          <NumberField i18n={i18n} value={custom} onChange={setCustom} min={0} ariaLabel={t('topUpAmount')} autoFocus />
          <span className="muted small">{t('topUpMinHint', { min: formatUSD(i18n.lang, min) })}</span>
        </div>
      )}
      {credited !== null && wallet && (
        <div className="usage-rows" style={{ marginTop: 14 }} data-test="topup-quote">
          <div className="usage-line">
            {t('topUpPay')}
            <span className="v tabular">{formatUSD(i18n.lang, gross)}</span>
          </div>
          <div className="usage-line" data-tip={t('topUpFeeWhy')}>
            {t('topUpFee', { pct: pctText(wallet.spread_bps), fixed: formatUSD(i18n.lang, wallet.spread_fixed_micro) })}
            <span className="v tabular">−{formatUSD(i18n.lang, gross - credited)}</span>
          </div>
          <div className="usage-line" style={{ fontWeight: 600 }}>
            {t('topUpGet')}
            <span className="v tabular">{formatUSD(i18n.lang, credited)}</span>
          </div>
        </div>
      )}
      {belowMin && (
        <span className="err small" style={{ marginTop: 8, display: 'inline-block' }}>
          {t('topUpMin', { min: formatUSD(i18n.lang, min) })}
        </span>
      )}
      {err && (
        <span className="err small">
          <Ico name="alert" size={13} /> {err}
        </span>
      )}
      <p className="muted small" style={{ margin: '14px 0 0', lineHeight: 1.6 }}>
        {t('topUpSecure')}
      </p>
    </GlassModal>
  )
}

// WalletScreen is the person's own money: the statement, a way to buy more, and
// the landing the processor returns to. On a return marked ?checkout=success the
// balance may lag the webhook, so it re-reads /me a few times over a few seconds
// and the bought credits appear on their own.
export function WalletScreen({ i18n, me, refreshMe, onTopUp }: { i18n: I18n; me: Me; refreshMe: () => void; onTopUp: () => void }) {
  const { t } = i18n
  const returned = new URLSearchParams(window.location.search).get('checkout') === 'success'
  const [polling, setPolling] = useState(returned)

  useEffect(() => {
    if (!returned) return
    let n = 0
    const id = setInterval(() => {
      refreshMe()
      if (++n >= 5) {
        clearInterval(id)
        setPolling(false)
      }
    }, 1500)
    return () => clearInterval(id)
  }, [returned, refreshMe])

  const credits = me.credits
  const balance = credits ? credits.wallet : 0
  return (
    <div className="screen measured">
      <div className="screen-head">
        <h1 className="screen-title">
          <Seams i18n={i18n} k="walletHeading" />
        </h1>
        <p className="screen-desc">{t('walletDesc')}</p>
      </div>
      <div className="stack">
        <div className="card card-pad">
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 16 }}>
            <div>
              <div className="eyebrow eyebrow-ico">
                <Ico name="wallet" size={12} /> {t('creditsBalance')}
              </div>
              <div className="tabular" style={{ fontSize: 34, fontWeight: 600, lineHeight: 1.15, marginTop: 6 }}>{formatUSD(i18n.lang, balance)}</div>
            </div>
            <button className="btn lg" onClick={onTopUp}>
              <Ico name="wallet" size={15} w={2} /> {t('topUp')}
            </button>
          </div>
          {polling && (
            <p className="muted small" style={{ margin: '12px 0 0' }}>
              {t('topUpProcessing')}
            </p>
          )}
        </div>
        {credits && <WalletActivity i18n={i18n} balance={balance} />}
      </div>
    </div>
  )
}

// WalletActivity is the wallet's history, shown rather than hidden behind a
// toggle: the place a person reads their own balance is where they read where
// it came from. It stays light however long the history grows, because the
// server returns only its most recent entries.
function WalletActivity({ i18n, balance }: { i18n: I18n; balance: number }) {
  const { t } = i18n
  const [entries, setEntries] = useState<LedgerEntry[] | null>(null)
  // Re-read when the balance changes: a list still showing the pre-move entries
  // would contradict the total it is supposed to explain.
  useEffect(() => {
    let live = true
    getWallet()
      .then((w) => live && setEntries(w.entries))
      .catch(() => live && setEntries([]))
    return () => {
      live = false
    }
  }, [balance])

  return (
    <div className="card card-pad stack">
      <span className="eyebrow eyebrow-ico">
        <Ico name="clock" size={12} /> {t('walletActivity')}
      </span>
      {entries === null ? (
        <div className="muted small">{t('loading')}</div>
      ) : entries.length === 0 ? (
        <div className="muted small">{t('statementEmpty')}</div>
      ) : (
        <>
          {/* No scroll region inside the page's own scroll: the server caps
              what it returns, so the list has an end and the page can hold it. */}
          <div className="usage-rows statement">
            {entries.map((e) => {
              const label = ledgerLabel(t, e)
              return (
                <div className="usage-line ledger" key={e.id}>
                  <span className="muted small">
                    {label.kind === 'plain' ? (
                      label.text
                    ) : (
                      <>
                        {label.prefix}{' '}
                        {label.href ? (
                          <a className="link" href={label.href}>
                            {label.name}
                          </a>
                        ) : (
                          <span style={{ color: 'var(--fg-2)' }}>{label.name}</span>
                        )}
                      </>
                    )}
                  </span>
                  <span className="muted small ledger-when">{dateOnly(i18n.lang, e.created_at)}</span>
                  <span className="v">
                    <span className={e.amount < 0 ? 'err' : ''}>{formatUSD(i18n.lang, e.amount)}</span>
                  </span>
                </div>
              )
            })}
          </div>
          {entries.length >= 200 && (
            <p className="muted small" style={{ margin: 0 }}>
              {t('walletActivityCapped')}
            </p>
          )}
        </>
      )}
    </div>
  )
}

// ProjectCreditsCard is a project's money and the two moves a member can make on
// it. It sits beside the papers card, where a run is started, because it
// answers why a start is refused. The balance and what is available differ by
// the start reserve each running session holds, which is why a project with
// credits can still refuse a start.
export function ProjectCreditsCard({ i18n, projectID, credits, onMoved, onTopUp }: { i18n: I18n; projectID: string; credits: ProjectCredits; onMoved: () => void; onTopUp: () => void }) {
  const { t } = i18n
  const [amount, setAmount] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  // Whether the last refusal was a short wallet on an allocate, which is the
  // one an in-place top-up fixes. A reclaim held by a running session is not.
  const [offerTopUp, setOfferTopUp] = useState(false)

  const parsed = parseUSD(amount)
  const valid = parsed !== null && parsed > 0
  const short = credits.available < credits.start_reserve
  // What running sessions are holding: the gap between the pot's balance and
  // what is free to commit.
  const held = credits.balance - credits.available

  const move = async (back: boolean) => {
    if (!valid || busy) return
    setBusy(true)
    setErr('')
    setOfferTopUp(false)
    try {
      await (back ? reclaimCredits(projectID, parsed) : allocateCredits(projectID, parsed))
      setAmount('')
      onMoved()
    } catch (e) {
      setErr(creditMoveErrLabel(t, e, back))
      setOfferTopUp(!back && errCode(e) === 'no_credits')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card card-pad stack">
      <span className="eyebrow eyebrow-ico" style={{ color: short ? 'var(--warn)' : 'var(--ok)' }}>
        <Ico name="wallet" size={12} /> {t('projectCredits')}
      </span>
      <div className="usage-rows">
        <div className="usage-line">
          {t('creditsBalance')}
          <span className="v tabular">{formatUSD(i18n.lang, credits.balance)}</span>
        </div>
        {/* The available row appears only when a running session holds some
            of the balance; otherwise it would repeat the balance. data-tip
            rather than a <Tip> wrapper, which would collapse the block row to
            its content. */}
        {held > 0 && (
          <div className="usage-line" data-tip={t('creditsAvailableTitle')}>
            {t('creditsAvailable')}
            <span className="v tabular">{formatUSD(i18n.lang, credits.available)}</span>
          </div>
        )}
      </div>
      <p className="muted small" style={{ lineHeight: 1.6, margin: 0 }}>
        {short ? t('creditsShort', { reserve: formatUSD(i18n.lang, credits.start_reserve) }) : t('creditsEnough', { reserve: formatUSD(i18n.lang, credits.start_reserve) })}
      </p>
      {/* The amount on its own line, then the two actions side by side, so a
          narrow card does not wrap "Take back" onto a lonely second row. */}
      <div className="stack" style={{ gap: 8 }}>
        <NumberField i18n={i18n} value={amount} onChange={setAmount} min={0} ariaLabel={t('creditsAmount')} />
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="btn" style={{ flex: 1 }} disabled={!valid || busy} onClick={() => move(false)}>
            <Ico name="plus" size={13} w={2.2} /> {t('creditsAllocate')}
          </button>
          {credits.may_reclaim && (
            <button className="btn ghost" style={{ flex: 1 }} disabled={!valid || busy} onClick={() => move(true)}>
              <Ico name="minus" size={13} w={2.2} /> {t('creditsReclaim')}
            </button>
          )}
        </div>
      </div>
      {err && (
        <div className="stack" style={{ gap: 8 }}>
          <span className="err small">
            <Ico name="alert" size={13} /> {err}
          </span>
          {offerTopUp && (
            <button className="btn ghost sm" style={{ alignSelf: 'flex-start' }} onClick={onTopUp}>
              <Ico name="wallet" size={13} /> {t('topUpWallet')}
            </button>
          )}
        </div>
      )}
    </div>
  )
}
