// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

// The platform console (006k): who may sign in and at what tier, each account's
// headroom, and the deployment's policy, namely the start reserve, the overdraft
// floor, the rate card and the drafting model.

import { useCallback, useEffect, useState } from 'react'
import {
  getPlatformSettings,
  getSolvency,
  grantAccess,
  type GrantView,
  listGrants,
  type PlatformSettings,
  type RateCard,
  refreezeAccount,
  revokeAccess,
  setPlatformSettings,
  topUp,
  unfreezeAccount,
} from '../api'
import { type I18n } from '../i18n'
import { formatUSD, parseUSD, toDollars } from '../money'
import { Ico } from '../icons'
import { NumberField, Pill, PromptDialog, Seams, Select, Tip } from '../ui'
import { CardHead, Empty, Initials, TierPill } from './shared'

// AdminScreen is the console: who may sign in, at what tier, with how much
// headroom, and the platform-wide default they draw from.
export function AdminScreen({ i18n, onCreditsChanged }: { i18n: I18n; onCreditsChanged: () => void }) {
  const { t } = i18n
  const [people, setPeople] = useState<GrantView[] | null>(null)
  const [settings, setSettings] = useState<PlatformSettings | null>(null)
  const [solvency, setSolvency] = useState<number | null>(null)
  const [err, setErr] = useState('')

  const refresh = useCallback(() => {
    listGrants()
      .then((g) => setPeople(g ?? []))
      .catch(() => setPeople([]))
    getPlatformSettings()
      .then(setSettings)
      .catch(() => setSettings(null))
    getSolvency()
      .then((s) => setSolvency(s.outstanding_micro))
      .catch(() => setSolvency(null))
  }, [])
  useEffect(refresh, [refresh])

  const run = async (fn: () => Promise<unknown>) => {
    setErr('')
    try {
      await fn()
      refresh()
    } catch (e) {
      setErr((e as Error).message === '403' ? t('adminNotAdmin') : t('adminError'))
    }
  }

  return (
    <div className="screen measured">
      <div className="screen-head">
        <h1 className="screen-title">
          <Seams i18n={i18n} k="navAdmin" />
        </h1>
        <p className="screen-desc">{t('adminIntro')}</p>
      </div>
      {err && <p className="err small">{err}</p>}
      <div className="stack">
        <PlatformPolicy i18n={i18n} settings={settings} onSave={(next) => run(() => setPlatformSettings(next))} />
        {solvency !== null && (
          <div className="card card-pad" data-tip={t('solvencyTitle')}>
            <div className="usage-line">
              {t('solvency')}
              <span className="v tabular">{formatUSD(i18n.lang, solvency)}</span>
            </div>
          </div>
        )}
        <PeopleCard
          i18n={i18n}
          people={people}
          onGrant={(email, tier) => run(() => grantAccess(email, tier))}
          onRevoke={(email) => run(() => revokeAccess(email))}
          onTopup={(email, amount) =>
            run(async () => {
              await topUp(email, amount)
              onCreditsChanged()
            })
          }
          onUnfreeze={(email) => run(() => unfreezeAccount(email))}
          onRefreeze={(email) => run(() => refreezeAccount(email))}
        />
      </div>
    </div>
  )
}

// PlatformPolicy edits what this deployment charges and what it will risk: two
// numbers and a price list, all micro-USD, all typed in dollars because that is
// the resolution an admin works at.
//
// The two numbers fail closed in opposite directions and the copy says so. A
// reserve that vanished would start sessions on empty projects; a floor that
// grew would stop bounding a runaway.
function PlatformPolicy({
  i18n,
  settings,
  onSave,
}: {
  i18n: I18n
  settings: PlatformSettings | null
  onSave: (next: Pick<PlatformSettings, 'start_reserve' | 'overdraft_cap' | 'rates' | 'draft'>) => void
}) {
  const { t } = i18n
  const [reserve, setReserve] = useState('')
  const [floor, setFloor] = useState('')
  // The drafting model (039): a protocol, an endpoint and a model id. There is
  // no key field; the key belongs to the deployment and this form has nowhere
  // to put one.
  const [provider, setProvider] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [model, setModel] = useState('')
  useEffect(() => {
    if (!settings) return
    setReserve(toDollars(settings.start_reserve))
    setFloor(toDollars(settings.overdraft_cap))
    setProvider(settings.draft?.provider ?? '')
    setBaseURL(settings.draft?.base_url ?? '')
    setModel(settings.draft?.model ?? '')
  }, [settings])

  const nextReserve = parseUSD(reserve)
  const nextFloor = parseUSD(floor)
  const valid = nextReserve !== null && nextReserve >= 0 && nextFloor !== null && nextFloor >= 0
  const draftCfg = { provider: provider.trim(), base_url: baseURL.trim(), model: model.trim() }
  const draftChanged =
    !!settings &&
    (draftCfg.provider !== (settings.draft?.provider ?? '') ||
      draftCfg.base_url !== (settings.draft?.base_url ?? '') ||
      draftCfg.model !== (settings.draft?.model ?? ''))
  const dirty = !!settings && valid && (nextReserve !== settings.start_reserve || nextFloor !== settings.overdraft_cap || draftChanged)
  const save = () => {
    if (settings && valid) onSave({ start_reserve: nextReserve, overdraft_cap: nextFloor, rates: settings.rates, draft: draftCfg })
  }

  return (
    <div className="card card-pad stack">
      <span className="eyebrow eyebrow-ico" style={{ color: 'var(--warn)' }}>
        <Ico name="wallet" size={12} /> {t('platformPolicy')}
      </span>
      <div className="policy-row">
        <div className="policy-copy">
          <label className="admin-label" htmlFor="start-reserve">
            {t('startReserve')}
          </label>
          <p className="policy-hint">{t('startReserveHint')}</p>
        </div>
        <NumberField i18n={i18n} id="start-reserve" value={reserve} onChange={setReserve} min={0} ariaLabel={t('startReserve')} />
      </div>
      <div className="policy-row">
        <div className="policy-copy">
          <label className="admin-label" htmlFor="overdraft-cap">
            {t('overdraftCap')}
          </label>
          <p className="policy-hint">{t('overdraftHint')}</p>
        </div>
        <NumberField i18n={i18n} id="overdraft-cap" value={floor} onChange={setFloor} min={0} ariaLabel={t('overdraftCap')} />
      </div>
      {/* Which model writes a project's description (039). Three fields and no
          key: the credential is the deployment's, read from its environment,
          because a settings row is read by every admin and lands in every
          backup. The model id must also appear in the rate card below, or the
          deployment refuses to draft rather than guessing a price. */}
      <div className="policy-row">
        <div className="policy-copy">
          <label className="admin-label" htmlFor="draft-provider">
            {t('draftProvider')}
          </label>
          <p className="policy-hint">{t('draftModelHint')}</p>
        </div>
        <Select
          value={provider}
          options={[
            { value: '', label: t('draftProviderNone') },
            { value: 'anthropic', label: 'Anthropic' },
            { value: 'openai', label: t('draftProviderOpenAI') },
          ]}
          onChange={setProvider}
          label={t('draftProvider')}
          width={260}
        />
      </div>
      {provider !== '' && (
        <>
          <div className="policy-row">
            <div className="policy-copy">
              <label className="admin-label" htmlFor="draft-model">
                {t('draftModel')}
              </label>
              {provider === 'anthropic' && <p className="policy-hint">{t('draftUsesAgentKey')}</p>}
            </div>
            <input
              id="draft-model"
              className="text policy-input"
              value={model}
              placeholder={t('draftModelPlaceholder')}
              onChange={(e) => setModel(e.target.value)}
              data-test="draft-model"
            />
          </div>
          {/* Only another vendor needs an address and a key. Anthropic is the
              one this deployment already runs its sessions on, so choosing it
              is a model id and nothing else. */}
          {provider !== 'anthropic' && (
            <div className="policy-row">
              <div className="policy-copy">
                <label className="admin-label" htmlFor="draft-base-url">
                  {t('draftBaseURL')}
                </label>
                {/* The key note belongs to the vendor's own address, not to the
                    model id: it is the row that says this is somebody else's
                    endpoint. */}
                <p className="policy-hint">{t('draftNeedsOwnKey')}</p>
              </div>
              <input
                id="draft-base-url"
                className="text policy-input"
                value={baseURL}
                placeholder={t('draftBaseURLPlaceholder')}
                onChange={(e) => setBaseURL(e.target.value)}
                data-test="draft-base-url"
              />
            </div>
          )}
        </>
      )}
      {settings && <RateCardTable i18n={i18n} card={settings.rates} />}
      {/* One Save, at the foot of the card it saves. Beside the overdraft floor
          it read as that field's control, and an admin who edited the start
          reserve had no reason to believe the button below it applied. One
          request writes both numbers, so the button sits where it reads as
          saving the whole card. */}
      <div className="card-foot">
        {settings?.updated_by && <span className="muted small">{t('policyUpdatedBy', { who: settings.updated_by })}</span>}
        <button className="btn" data-test="save-policy" disabled={!dirty} onClick={save}>
          <Ico name="check" size={13} w={2.2} /> {t('savePolicy')}
        </button>
      </div>
    </div>
  )
}

// RateCardTable shows what each model costs. It is here because a reserve and a
// floor mean nothing without the prices they bound: an admin setting a $200
// floor should be able to see what output tokens cost and work out what it buys.
function RateCardTable({ i18n, card }: { i18n: I18n; card: RateCard }) {
  const { t } = i18n
  if (!card?.models?.length) return null
  return (
    <div className="stack" style={{ gap: 6 }}>
      <span className="muted small">{t('rateCardHint')}</span>
      <div className="rate-card">
        <div className="rate-row rate-head">
          <span>{t('rateModel')}</span>
          <span>{t('rateInput')}</span>
          <span>{t('rateOutput')}</span>
          <span>{t('rateCacheRead')}</span>
          <span>{t('rateCacheWrite')}</span>
        </div>
        {card.models.map((m) => (
          <div className="rate-row" key={m.model}>
            <span className="mono">{m.model}</span>
            <span className="tabular">{formatUSD(i18n.lang, m.input)}</span>
            <span className="tabular">{formatUSD(i18n.lang, m.output)}</span>
            <span className="tabular">{formatUSD(i18n.lang, m.cache_read)}</span>
            <span className="tabular">
              {formatUSD(i18n.lang, m.cache_write_5m)} / {formatUSD(i18n.lang, m.cache_write_1h)}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

// PeopleCard is the user list: who has signed in, at what tier, with what
// headroom. Everything here is seeded by email. Nobody needs to have signed in
// first, and a grant binds to them when they do.
function PeopleCard({
  i18n,
  people,
  onGrant,
  onRevoke,
  onTopup,
  onUnfreeze,
  onRefreeze,
}: {
  i18n: I18n
  people: GrantView[] | null
  onGrant: (email: string, tier: 'user' | 'admin') => void
  onRevoke: (email: string) => void
  onTopup: (email: string, amount: number) => void
  onUnfreeze: (email: string) => void
  onRefreeze: (email: string) => void
}) {
  const { t } = i18n
  const [email, setEmail] = useState('')
  const [tier, setTier] = useState<'user' | 'admin'>('user')
  // Show only the accounts that owe, which are the debts an operator acts on (022d).
  const [owingOnly, setOwingOnly] = useState(false)
  // Which row's top-up is being edited. window.prompt would have drawn a
  // browser-owned box over the app; this is the app's own dialog.
  const [topping, setTopping] = useState<GrantView | null>(null)

  const invite = (e: React.FormEvent) => {
    e.preventDefault()
    const target = email.trim()
    if (!target) return
    onGrant(target, tier)
    setEmail('')
  }

  const shown = people && owingOnly ? people.filter((p) => p.wallet < 0) : people

  return (
    <div className="card">
      <CardHead label={t('people')} icon="users" tone="var(--brand)">
        <button className={'btn ghost sm' + (owingOnly ? ' active' : '')} style={{ marginLeft: 'auto' }} aria-pressed={owingOnly} onClick={() => setOwingOnly((v) => !v)}>
          <Ico name="alert" size={12} /> {t('onlyOwing')}
        </button>
      </CardHead>
      {shown === null ? (
        <div className="empty">{t('loading')}</div>
      ) : shown.length === 0 ? (
        <Empty icon="userPlus" text={owingOnly ? t('noOwing') : t('noGrants')} />
      ) : (
        shown.map((p) => (
          <div key={p.email} className="member-row">
            <Initials email={p.email} />
            <span className="member-email">{p.email}</span>
            <TierPill i18n={i18n} tier={p.tier} />
            {!p.bound && (
              <Pill tone="warn" icon="clock">
                {t('pending')}
              </Pill>
            )}
            {p.frozen && (
              <Pill tone="err" icon="alert">
                {t('frozen')}
              </Pill>
            )}
            <Tip text={t('walletOf', { email: p.email })}>
              <span className="muted small mono tabular member-wallet">{formatUSD(i18n.lang, p.wallet)}</span>
            </Tip>
            {/* The controls are grouped. Loose in the row they were sized one by
                one, so a phone broke them across two lines and each row put them
                somewhere else. As one group they stay on one line and end where
                every other row's do. */}
            <span className="row-actions">
              {p.wallet < 0 &&
                (p.frozen ? (
                  <button className="btn ghost sm" data-tip={t('unfreezeTitle')} onClick={() => onUnfreeze(p.email)}>
                    <Ico name="check" size={12} /> {t('unfreeze')}
                  </button>
                ) : (
                  <button className="btn ghost sm" data-tip={t('refreezeTitle')} onClick={() => onRefreeze(p.email)}>
                    <Ico name="alert" size={12} /> {t('refreeze')}
                  </button>
                ))}
              <button className="btn ghost sm" onClick={() => setTopping(p)}>
                <Ico name="wallet" size={12} /> {t('topup')}
              </button>
              <button className="btn ghost sm danger" onClick={() => onRevoke(p.email)}>
                <Ico name="x" size={12} w={2} /> {t('revoke')}
              </button>
            </span>
          </div>
        ))
      )}
      <form className="field-row" onSubmit={invite}>
        <input
          className="text"
          style={{ flex: 1 }}
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          placeholder={t('inviteEmail')}
          spellCheck={false}
        />
        <Select
          value={tier}
          onChange={setTier}
          label={t('access')}
          width={132}
          options={[
            { value: 'user', label: t('tierUser'), icon: 'user' },
            { value: 'admin', label: t('tierAdmin'), icon: 'shieldCheck' },
          ]}
        />
        <button className="btn">
          <Ico name="userPlus" size={13} /> {t('grant')}
        </button>
      </form>
      <div style={{ padding: '0 14px 12px' }}>
        <span className="muted small">{t('grantHint')}</span>
      </div>
      {topping && (
        <PromptDialog
          i18n={i18n}
          title={t('topup')}
          body={t('topupPrompt', { email: topping.email })}
          initial="10"
          parse={parseUSD}
          confirmLabel={t('save')}
          cancelLabel={t('cancel')}
          onCancel={() => setTopping(null)}
          onConfirm={(n) => {
            onTopup(topping.email, n)
            setTopping(null)
          }}
        />
      )}
    </div>
  )
}
